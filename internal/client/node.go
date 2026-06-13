package client

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/Jingqi0327/GopherHole/pkg/tun"
	"github.com/Jingqi0327/GopherHole/proto/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Node struct {
	hostname   string
	serverAddr string
	virtualIP  string
	peerTable  *PeerTable
	udpEngine  *UDPEngine
	tunDevice  tun.Tunnel
}

func NewNode(serverAddr, requestedIP, hostname string) *Node {
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	return &Node{
		hostname:   hostname,
		serverAddr: serverAddr,
		virtualIP:  requestedIP,
		peerTable:  NewPeerTable(),
	}
}

// Run 启动客户端的核心生命周期
func (a *Node) Run() error {
	log.Printf("Connecting to Signaling Server at %s...", a.serverAddr)

	// 连接 Server
	conn, err := grpc.NewClient(a.serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer conn.Close()

	grpcClient := pb.NewSignalingServiceClient(conn)

	// 第一步：注册节点并获取 Virtual IP
	if err := a.registerNode(grpcClient); err != nil {
		return err
	}

	// 第二步：初始化 TUN 虚拟网卡
	if err := a.initTunDevice(); err != nil {
		return err
	}

	// 第三步：启动 UDP 引擎
	if err := a.startUDPEngine(grpcClient); err != nil {
		return err
	}

	// 第四步：开启 Data Pump 出站协程
	a.startDataPumpOutbound()

	// 第五步：开启心跳与重连守护协程
	go a.keepaliveLoop(grpcClient)

	// 阻塞当前主线程，处理交互式终端输入
	a.handleTerminalInput()

	return nil
}

func (a *Node) startDataPumpOutbound() {
	go func() {
		buf := make([]byte, 2000) // MTU 1420，2000足够容纳
		for {
			n, err := a.tunDevice.Read(buf)
			if err != nil {
				log.Printf("TUN Read error: %v", err)
				return
			}

			// 检查包长是否至少包含一个基础的 IPv4 头部 (20字节)
			if n < 20 {
				continue
			}

			// 检查是否为 IPv4 数据包 (IP 版本号位于第 1 个字节的高 4 位)
			if buf[0]>>4 != 4 {
				continue
			}

			// 解析目的 IP (IPv4 头部的第 16 到 19 字节是目的 IP)
			destIP := net.IPv4(buf[16], buf[17], buf[18], buf[19]).String()

			// 查找对方节点
			peer := a.peerTable.GetPeer(destIP)
			if peer != nil {
				if peer.State == StateConnected {
					// 已连通，直接通过 UDP 发送原生 IP 数据包
					a.udpEngine.SendRaw(buf[:n], peer.PublicAddr)
				} else if peer.State != StatePunching {
					// 发现发往该 IP 的流量，但尚未连通，触发打洞
					log.Printf("🚦 Traffic detected for %s, but not connected. Triggering hole punch...", destIP)
					a.udpEngine.Punch(destIP)
				}
			}
		}
	}()
}

func (a *Node) registerNode(grpcClient pb.SignalingServiceClient) error {
	regReq := &pb.RegisterRequest{
		RequestedHostname: a.hostname,
		RequestedIp:       a.virtualIP,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	regResp, err := grpcClient.Register(ctx, regReq)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	a.hostname = regResp.Hostname
	a.virtualIP = regResp.VirtualIp
	log.Printf("✅ Registration successful!")
	log.Printf("🌐 Assigned Hostname: %s", a.hostname)
	log.Printf("🌐 Assigned Virtual IP: %s", a.virtualIP)
	log.Printf("🌍 Server sees our Public IP as: %s", regResp.PublicIp)
	return nil
}

func (a *Node) initTunDevice() error {
	tunDevice, err := tun.NewTunnel("gh0", a.virtualIP)
	if err != nil {
		return fmt.Errorf("failed to initialize TUN device: %w", err)
	}
	a.tunDevice = tunDevice
	log.Printf("🚀 TUN interface [%s] initialized with IP %s", tunDevice.Name(), a.virtualIP)

	return nil
}

func (a *Node) startUDPEngine(grpcClient pb.SignalingServiceClient) error {
	engine, err := NewUDPEngine(a.virtualIP, a.peerTable, grpcClient, func(data []byte) {
		if a.tunDevice != nil {
			n, err := a.tunDevice.Write(data)
			if err != nil {
				log.Printf("TUN Write error: %v", err)
			} else {
				// log.Printf("📦 [UDP -> TUN] Injected %d bytes to network stack", n)
				_ = n
			}
		}
	})
	if err != nil {
		return fmt.Errorf("failed to start UDP engine: %w", err)
	}
	a.udpEngine = engine
	a.udpEngine.Start()
	return nil
}

func (a *Node) runHeartbeatStream(grpcClient pb.SignalingServiceClient) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := grpcClient.Heartbeat(ctx)
	if err != nil {
		return fmt.Errorf("failed to start heartbeat stream: %w", err)
	}

	errCh := make(chan error, 1)

	// 开启协程，定时发送心跳
	go func() {
		for {
			err := stream.Send(&pb.HeartbeatRequest{
				Hostname:   a.hostname,
				VirtualIp:  a.virtualIP,
				PublicPort: int32(a.udpEngine.GetPublicPort()),
			})
			if err != nil {
				errCh <- err
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):

			}
		}
	}()

	// 开启协程，接收服务端的推送
	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}

			switch payload := resp.Payload.(type) {
			case *pb.HeartbeatResponse_PeerList:
				a.peerTable.SyncPeers(payload.PeerList.Peers)
				a.printPeers(payload.PeerList.Peers)
			case *pb.HeartbeatResponse_Signal:
				log.Printf("📥 Received signal from %s (Type: %v)", payload.Signal.FromVirtualIp, payload.Signal.Type)
				a.udpEngine.HandleSignal(payload.Signal)
			}
		}
	}()

	return <-errCh
}

func (a *Node) keepaliveLoop(grpcClient pb.SignalingServiceClient) {
	for {
		// 阻塞执行心跳流，直到流异常断开（比如网络断开、服务端重启）
		err := a.runHeartbeatStream(grpcClient)
		log.Printf("\n⚠️ Disconnected from server: %v. Existing P2P connections remain active.", err)

		// 进入断线重连循环
		for {
			time.Sleep(5 * time.Second) // 退避等待
			log.Printf("🔄 Attempting to reconnect and re-register with server...")

			// a.virtualIP 此时保存的是我们断线前的 IP
			// 这里会带着这个旧 IP 请求重新注册
			err := a.registerNode(grpcClient)
			if err == nil {
				log.Printf("✅ Re-registration successful. Resuming heartbeat.")
				break // 注册成功，跳出重试，回到外层重新执行 runHeartbeatStream
			}

			if strings.Contains(err.Error(), "AlreadyExists") || strings.Contains(err.Error(), "IP conflict") {
				log.Fatalf("❌ Critical Error: The IP %s has been occupied by another node. Connection cannot be restored. Please restart the client to obtain a new IP!", a.virtualIP)
			}
			log.Printf("❌ Re-registration failed: %v. Retrying in 5 seconds...", err)
		}
	}
}

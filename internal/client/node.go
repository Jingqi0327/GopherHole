package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Jingqi0327/GopherHole/internal/config"
	"github.com/Jingqi0327/GopherHole/pkg/auth"
	"github.com/Jingqi0327/GopherHole/pkg/crypto"
	"github.com/Jingqi0327/GopherHole/pkg/stun"
	"github.com/Jingqi0327/GopherHole/pkg/terminal"

	"github.com/Jingqi0327/GopherHole/pkg/tun"
	"github.com/Jingqi0327/GopherHole/proto/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// NatType 定义 NAT 类型
type NatType string

const (
	NatTypeUnknown NatType = "Unknown"
	NatTypeEasy    NatType = "EasyNAT" // Cone NAT (NAT 1,2,3)
	NatTypeHard    NatType = "HardNAT" // Symmetric NAT (NAT 4)
)

type Node struct {
	cfg             *config.ClientConfig // Client端配置文件
	hostname        string               // Client端主机名
	virtualIP       string               // Client端虚拟IP
	peerTable       *PeerTable           // 节点表
	udpEngine       *UDPEngine           // UDP引擎
	tunDevice       tun.Tunnel           // TUN虚拟网卡
	serverStunPorts []int32              // 服务端开放的STUN探测端口列表
	publicIP        string               // Client端公网IP
	publicPort      int                  // Client端公网端口
	natType         NatType              // Client端所在NAT环境的类型
	privateKey      [32]byte             // Client端私钥
	publicKey       [32]byte             // Client端公钥
}

// NewNode 创建一个新的Node
func NewNode(cfg *config.ClientConfig) (*Node, error) {
	hostname := cfg.Hostname
	if hostname == "" {
		hostname, _ = os.Hostname()
	}

	privateKey, pubKey, err := crypto.GenerateCurve25519Keypair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate Curve25519 keypair: %w", err)
	}

	node := &Node{
		cfg:        cfg,
		hostname:   hostname,
		virtualIP:  cfg.IP,
		peerTable:  NewPeerTable(privateKey),
		natType:    NatTypeUnknown,
		privateKey: privateKey,
		publicKey:  pubKey,
	}
	return node, nil
}

// Run 运行Node
func (node *Node) Run() error {
	stopAnim := terminal.StartSpinner(fmt.Sprintf("%sConnecting to Signaling Server at %s%s", terminal.ColorCyan, node.cfg.Server, terminal.ColorReset))

	opts := node.initgRPCOpts()

	// 连接 Server
	conn, err := grpc.NewClient(node.cfg.Server, opts...)
	if err != nil {
		stopAnim(true)
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer conn.Close()

	grpcClient := pb.NewSignalingServiceClient(conn)

	// 第一步：注册节点并获取 Virtual IP
	err = node.registerNode(grpcClient)
	stopAnim(true)
	if err != nil {
		if status.Code(err) == codes.Unauthenticated {
			return fmt.Errorf("authentication failed (invalid token): %w", err)
		}
		return fmt.Errorf("registration failed: %w", err)
	}
	terminal.Success("Registration successful!")
	node.printNodeInfo()

	// 第二步：初始化 TUN 虚拟网卡
	if err := node.initTunDevice(); err != nil {
		return err
	}
	defer node.tunDevice.Close()

	// 第三步：初始化网络 Socket 并探测 NAT 环境
	addr, err := net.ResolveUDPAddr("udp", ":0")
	if err != nil {
		return fmt.Errorf("failed to resolve UDP address: %w", err)
	}
	connUDP, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on UDP: %w", err)
	}
	defer connUDP.Close()

	if err := node.detectNAT(connUDP); err != nil {
		return fmt.Errorf("NAT detection failed: %w", err)
	}

	// 第四步：启动 UDP 引擎
	if err := node.startUDPEngine(connUDP, grpcClient); err != nil {
		return err
	}

	// 第五步：启动外网数据泵出协程
	node.startDataPumpOutbound()

	g, _ := errgroup.WithContext(context.Background())

	g.Go(func() error {
		return node.keepaliveLoop(grpcClient)
	})

	// 阻塞当前主线程，处理交互式终端输入
	g.Go(func() error {
		node.handleTerminalInput()
		return nil
	})

	return g.Wait()
}

func (node *Node) startDataPumpOutbound() {
	go func() {
		// Headroom 预留了 13 个字节：5 字节协议头 + 8 字节加密 Nonce
		const Headroom = 13
		buf := make([]byte, 2000)

		for {
			// 直接从偏移 Headroom 的位置开始读取 TUN 数据
			// 这样底层操作系统的网络栈会把真实的 IPv4 报文写在 buf[13:] 的位置
			n, err := node.tunDevice.Read(buf[Headroom:])
			if err != nil {
				terminal.Error(fmt.Sprintf("Failed to read from TUN: %v", err))
				time.Sleep(time.Second)
				continue
			}

			// IPv4 报文实际上在 buf[Headroom : Headroom+n]
			packetData := buf[Headroom : Headroom+n]

			// 检查是否为 IPv4 数据包 (IP 版本号位于第一个字节的高 4 位)
			if packetData[0]>>4 != 4 {
				continue
			}

			// 解析目的 IP (IPv4 头部的第 16 到 19 字节是目的 IP)
			destVitrualIP := net.IPv4(packetData[16], packetData[17], packetData[18], packetData[19]).String()

			// 查找对方节点
			peer := node.peerTable.GetPeer(destVitrualIP)
			if peer != nil {
				if peer.State == StateConnected {
					// 已连通，直接通过 UDP 发送零拷贝封装的 IP 数据包
					node.udpEngine.SendDataPacket(buf[:Headroom+n], n, peer.PublicAddr, destVitrualIP)
				} else {
					// 未连通（StateDisconnected 或 StatePunching），暂存数据包以防丢失首包
					node.peerTable.EnqueuePacket(destVitrualIP, buf[:Headroom+n], n)
					
					if peer.State != StatePunching {
						// 发现发往该 IP 的流量，但尚未连通，触发打洞
						terminal.Info(fmt.Sprintf("Traffic detected for %s, but not connected. Triggering hole punch...", destVitrualIP))
						node.udpEngine.Punch(destVitrualIP)
					}
				}
			}
		}
	}()
}

func (node *Node) registerNode(grpcClient pb.SignalingServiceClient) error {
	regReq := &pb.RegisterRequest{
		RequestedHostname: node.hostname,
		RequestedIp:       node.virtualIP,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	regRsp, err := grpcClient.Register(ctx, regReq)
	if err != nil {
		return err
	}

	node.hostname = regRsp.Hostname
	node.virtualIP = regRsp.VirtualIp
	node.serverStunPorts = regRsp.StunPorts
	return nil
}

func (node *Node) initTunDevice() error {
	tunDevice, err := tun.NewTunnel("gh0", node.virtualIP)
	if err != nil {
		return fmt.Errorf("failed to initialize TUN device: %w", err)
	}
	node.tunDevice = tunDevice
	terminal.Success(fmt.Sprintf("TUN interface [%s] initialized with IP %s", tunDevice.Name(), node.virtualIP))

	return nil
}

func (node *Node) startUDPEngine(conn *net.UDPConn, grpcClient pb.SignalingServiceClient) error {
	// 启动引擎，将干净的 conn 和探测到的公网端点传给它
	engine, err := NewUDPEngine(
		conn,
		node.virtualIP,
		node.peerTable,
		grpcClient,
		node.cfg.Server, // 直接使用 gRPC 的服务器地址作为主 STUN 和保活地址
		node.publicIP,
		node.publicPort,
		node.cfg.KeepaliveInt,
		func(data []byte) {
			if node.tunDevice != nil {
				_, err := node.tunDevice.Write(data)
				if err != nil {
					terminal.Error(fmt.Sprintf("TUN Write error: %v", err))
				}
			}
		})
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to start UDP engine: %w", err)
	}
	node.udpEngine = engine
	node.udpEngine.Start()
	return nil
}

// detectNAT 执行 STUN 探测，确定公网端点以及 NAT 类型
func (node *Node) detectNAT(conn *net.UDPConn) error {
	if len(node.serverStunPorts) == 0 {
		return fmt.Errorf("server did not provide any STUN ports")
	}

	serverHost, _, err := net.SplitHostPort(node.cfg.Server)
	if err != nil {
		serverHost = node.cfg.Server // fallback
	}

	var stunServers []string
	for _, port := range node.serverStunPorts {
		stunServers = append(stunServers, fmt.Sprintf("%s:%d", serverHost, port))
	}

	terminal.Info(fmt.Sprintf("Discovering public endpoint via STUN servers: %v...", stunServers))

	pubIP, pubPort, endpoints, err := stun.DetectNAT(conn, stunServers)
	if err != nil {
		return fmt.Errorf("all STUN discovery attempts failed: %w", err)
	}

	node.publicIP = pubIP
	node.publicPort = pubPort

	terminal.Success(fmt.Sprintf("STUN discovery successful! Public Endpoint: %s:%d", node.publicIP, node.publicPort))

	if len(endpoints) > 1 {
		node.natType = NatTypeHard
		terminal.Warning("Different public endpoints detected across STUN servers:")
		for ep, srv := range endpoints {
			terminal.Warning(fmt.Sprintf("  - %s returned: %s", srv, ep))
		}
		terminal.Warning("This indicates you are behind a Symmetric NAT (NAT4).")
		terminal.Warning("Standard UDP Hole Punching may FAIL in this network environment.")
	} else {
		node.natType = NatTypeEasy
		terminal.Success("NAT Type detected as Easy (Cone NAT).")
	}

	return nil
}

func (node *Node) runHeartbeatStream(grpcClient pb.SignalingServiceClient) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := grpcClient.Heartbeat(ctx)
	if err != nil {
		return fmt.Errorf("failed to start heartbeat stream: %w", err)
	}

	errGroup, ctx := errgroup.WithContext(ctx)

	// 开启协程，定时发送心跳
	errGroup.Go(func() error {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			err := stream.Send(&pb.HeartbeatRequest{
				Hostname:   node.hostname,
				VirtualIp:  node.virtualIP,
				PublicPort: int32(node.udpEngine.GetPublicPort()),
				PublicKey:  node.publicKey[:],
			})
			if err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	})

	// 开启协程，接收服务端的推送
	errGroup.Go(func() error {
		for {
			resp, err := stream.Recv()
			if err != nil {
				return err
			}

			switch payload := resp.Payload.(type) {
			case *pb.HeartbeatResponse_PeerList:
				node.peerTable.SyncPeers(payload.PeerList.Peers)
				node.printPeers(payload.PeerList.Peers)
			case *pb.HeartbeatResponse_Signal:
				terminal.Info(fmt.Sprintf("Received signal from %s (Type: %v)", payload.Signal.FromVirtualIp, payload.Signal.Type))
				node.udpEngine.HandleSignal(payload.Signal)
			}
		}
	})

	return errGroup.Wait()
}

func (node *Node) keepaliveLoop(grpcClient pb.SignalingServiceClient) error {
	for {
		// 阻塞执行心跳流，直到流异常断开（比如网络断开、服务端重启）
		err := node.runHeartbeatStream(grpcClient)
		terminal.Error(fmt.Sprintf("\nDisconnected from server: %v. Existing P2P connections remain active.", err))

		// 进入断线重连循环
		var lastErrMsg string
		for {
			stopAnim := terminal.StartSpinner("Waiting to reconnect...")
			time.Sleep(4000 * time.Millisecond)

			// node.virtualIP 此时保存的是我们断线前的 IP
			// 这里会带着这个旧 IP 请求重新注册
			err = node.registerNode(grpcClient)
			stopAnim(true) // 清除动画
			if err == nil {
				terminal.Success("Re-registration successful. Resuming heartbeat.")
				node.printNodeInfo()
				break // 注册成功，跳出重试，回到外层重新执行 runHeartbeatStream
			}

			if status.Code(err) == codes.AlreadyExists {
				terminal.Error(fmt.Sprintf("Critical Error: The IP %s has been occupied by another node. Connection cannot be restored. Please restart the client to obtain a new IP!", node.virtualIP))
				return err
			}

			errMsg := err.Error()
			if errMsg != lastErrMsg {
				terminal.Error(fmt.Sprintf("Re-registration failed: %v. Retrying...", err))
				lastErrMsg = errMsg
			}
		}
	}
}

// initgRPCOpts 初始化与 Signaling Server 通信的 gRPC 选项
func (node *Node) initgRPCOpts() []grpc.DialOption {
	var tlsConfig *tls.Config

	// 1. 安全性配置：基于公钥的 TLS Pinning (防 MITM 攻击)
	if node.cfg.ServerPubKey == "" {
		// 未配置服务端公钥时，直接跳过所有 TLS 校验（不安全）
		tlsConfig = &tls.Config{InsecureSkipVerify: true}
		terminal.Warning("WARNING: Server Public Key not provided. Connection is NOT secure against MITM attacks.")
	} else {
		// 配置了公钥时，跳过传统 CA 校验，使用手动提取和比对公钥的方式进行强验证
		tlsConfig = &tls.Config{
			InsecureSkipVerify:    true,
			VerifyPeerCertificate: crypto.VerifyPeerPublicKey(node.cfg.ServerPubKey),
		}
	}

	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))

	// 2. 鉴权配置：将 Token 附加到每个 gRPC 请求的 Metadata 中
	if node.cfg.Token != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(auth.NewTokenAuth(node.cfg.Token)))
	}
	return opts
}

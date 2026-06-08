package client

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/Jingqi0327/GopherHole/proto/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type App struct {
	serverAddr  string
	requestedIP string
	virtualIP   string
	peerTable   *PeerTable
	udpEngine   *UDPEngine
}

func NewApp(serverAddr, requestedIP string) *App {
	return &App{
		serverAddr:  serverAddr,
		requestedIP: requestedIP,
		peerTable:   NewPeerTable(),
	}
}

// Run 启动客户端的核心生命周期
func (a *App) Run() error {
	log.Printf("Connecting to Signaling Server at %s...", a.serverAddr)

	// 连接 Server
	conn, err := grpc.NewClient(a.serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer conn.Close()

	grpcClient := pb.NewSignalingServiceClient(conn)

	// 第一步：注册节点并获取 Virtual IP
	hostname, _ := os.Hostname()
	regReq := &pb.RegisterRequest{
		Hostname:    hostname,
		RequestedIp: a.requestedIP,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	regResp, err := grpcClient.Register(ctx, regReq)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	a.virtualIP = regResp.VirtualIp
	log.Printf("✅ Registration successful!")
	log.Printf("🌐 Assigned Virtual IP: %s", a.virtualIP)
	log.Printf("🌍 Server sees our Public IP as: %s", regResp.PublicIp)

	// 启动 UDP 引擎
	a.udpEngine, err = NewUDPEngine(a.virtualIP, a.peerTable, grpcClient)
	if err != nil {
		return fmt.Errorf("failed to start UDP engine: %w", err)
	}
	a.udpEngine.Start()

	// 第二步：开启心跳双向流
	stream, err := grpcClient.Heartbeat(context.Background())
	if err != nil {
		return fmt.Errorf("failed to start heartbeat stream: %w", err)
	}

	// 开启协程，定时发送心跳
	go func() {
		for {
			err := stream.Send(&pb.HeartbeatRequest{
				VirtualIp:  a.virtualIP,
				PublicPort: int32(a.udpEngine.GetLocalPort()),
			})
			if err != nil {
				log.Printf("Failed to send heartbeat: %v", err)
				return
			}
			time.Sleep(10 * time.Second)
		}
	}()

	// 开启协程，接收服务端的推送
	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				log.Printf("\n⚠️ Stream disconnected from server: %v", err)
				log.Printf("👉 Existing P2P connections remain active, but no new peers can be discovered.")
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

	// 阻塞当前主线程，处理交互式终端输入
	a.handleTerminalInput()

	return nil
}

func (a *App) handleTerminalInput() {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		// fmt.Print("> ") // 省略 prompt 以免干扰日志
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}

		parts := strings.SplitN(text, " ", 3)
		cmd := strings.ToLower(parts[0])

		switch cmd {
		case "punch":
			if len(parts) < 2 {
				fmt.Println("Usage: punch <virtual_ip>")
				continue
			}
			a.udpEngine.Punch(parts[1])
		case "msg":
			if len(parts) < 3 {
				fmt.Println("Usage: msg <virtual_ip> <text>")
				continue
			}
			a.udpEngine.SendMessage(parts[1], parts[2])
		case "list":
			// 可以增加一个打印本地 PeerTable 的命令
			fmt.Println("Run 'list' to be implemented")
		default:
			fmt.Println("Unknown command. Supported: punch, msg")
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Terminal input error: %v", err)
	}
}

// printPeers 在终端格式化打印当前所有的在线节点
func (a *App) printPeers(peers []*pb.RemotePeer) {
	fmt.Println("\n================= 🟢 ONLINE PEERS =================")
	for _, p := range peers {
		marker := ""
		if p.VirtualIp == a.virtualIP {
			marker = "👈 (本节点/This Node)"
		} else {
			state := "Disconnected"
			if peer := a.peerTable.GetPeer(p.VirtualIp); peer != nil {
				switch peer.State {
				case StatePunching:
					state = "Punching..."
				case StateConnected:
					state = "Connected!"
				}
			}
			marker = fmt.Sprintf("[%s]", state)
		}
		publicAddr := fmt.Sprintf("%s:%d", p.PublicIp, p.PublicPort)
		fmt.Printf(" - Virtual IP: %-15s | Public: %-20s %s\n", p.VirtualIp, publicAddr, marker)
	}
	fmt.Println("===================================================")
}

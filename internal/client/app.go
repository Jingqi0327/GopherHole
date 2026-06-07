package client

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Jingqi0327/GopherHole/proto/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type App struct {
	serverAddr  string
	requestedIP string
	virtualIP   string
}

func NewApp(serverAddr, requestedIP string) *App {
	return &App{
		serverAddr:  serverAddr,
		requestedIP: requestedIP,
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

	// 第二步：开启心跳双向流
	stream, err := grpcClient.Heartbeat(context.Background())
	if err != nil {
		return fmt.Errorf("failed to start heartbeat stream: %w", err)
	}

	// 开启协程，定时发送心跳
	go func() {
		// 先发一个立刻报到
		for {
			err := stream.Send(&pb.HeartbeatRequest{
				VirtualIp:  a.virtualIP,
				PublicPort: 0, // MVP-1 阶段还不涉及 P2P 实际通信，端口填 0
			})
			if err != nil {
				log.Printf("Failed to send heartbeat: %v", err)
				return
			}
			time.Sleep(10 * time.Second)
		}
	}()

	// 第三步：主线程阻塞接收服务端的节点变动推送
	for {
		resp, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("stream disconnected: %w", err)
		}
		a.printPeers(resp.Peers)
	}
}

// printPeers 在终端格式化打印当前所有的在线节点
func (a *App) printPeers(peers []*pb.RemotePeer) {
	fmt.Println("\n================= 🟢 ONLINE PEERS =================")
	for _, p := range peers {
		marker := ""
		if p.VirtualIp == a.virtualIP {
			marker = "👈 (本节点/This Node)"
		}
		publicAddr := fmt.Sprintf("%s:%d", p.PublicIp, p.PublicPort)
		fmt.Printf(" - VIP: %-15s | Public: %-20s %s\n", p.VirtualIp, publicAddr, marker)
	}
	fmt.Println("===================================================")
}

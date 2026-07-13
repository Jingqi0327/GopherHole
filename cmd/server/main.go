package main

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/Jingqi0327/GopherHole/internal/config"
	"github.com/Jingqi0327/GopherHole/internal/server"
	"github.com/Jingqi0327/GopherHole/pkg/auth"
	"github.com/Jingqi0327/GopherHole/pkg/crypto"
	"github.com/Jingqi0327/GopherHole/pkg/stun"
	"github.com/Jingqi0327/GopherHole/pkg/terminal"
	"github.com/Jingqi0327/GopherHole/proto/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg, err := config.LoadServerConfig()
	if err != nil {
		terminal.Fatalf("Failed to load config: %v", err)
	}

	// 初始化与校验密钥对
	privB64, _, err := crypto.EnsureServerKeypair(cfg.PrivateKey, cfg.PublicKey)
	if err != nil {
		terminal.Fatalf("Keypair initialization failed: %v", err)
	}

	// 动态生成内存 TLS 证书
	cert, pubB64, err := crypto.GenerateMemTLSCertFromKey(privB64)
	if err != nil {
		terminal.Fatalf("Failed to generate TLS certificate: %v", err)
	}

	terminal.Info("GopherHole Signaling Server is starting...")
	terminal.Info("=====================================================")
	terminal.Warning("[CONFIG GUIDE] Please set the following Public Key in Client config (SERVER_PUBLIC_KEY):")
	terminal.Success(pubB64)
	terminal.Info("=====================================================")

	registry := server.NewRegistry(cfg.VirtualSubnet)
	manager := server.NewPeerManager(func(virtualIP string) {
		registry.Release(virtualIP)
	})

	// 开启后台巡检 Goroutine，定期清理超时的离线节点 (超时时间设为 30s)
	go func() {
		for {
			time.Sleep(10 * time.Second)
			manager.CleanExpired(30 * time.Second)
		}
	}()

	// 提取绑定端口
	_, portStr, err := net.SplitHostPort(cfg.BindAddr)
	if err != nil {
		terminal.Fatalf("Invalid BindAddr %s: %v", cfg.BindAddr, err)
	}
	basePort, err := strconv.Atoi(portStr)
	if err != nil {
		terminal.Fatalf("Invalid port %s: %v", portStr, err)
	}


	// 启动主端口的 UDP 监听
	go func(port int) {
		addr := fmt.Sprintf(":%d", port)
		udpAddr, _ := net.ResolveUDPAddr("udp", addr)
		conn, err := net.ListenUDP("udp", udpAddr)
		if err != nil {
			terminal.Fatalf("Failed to listen on primary UDP port %s: %v", addr, err)
		}
		terminal.Infof("Primary STUN Responder listening on UDP %s", addr)
		buf := make([]byte, 2048)
		for {
			n, remoteAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				continue
			}
			stun.HandleSTUNRequest(conn, remoteAddr, buf[:n])
		}
	}(basePort)

	svc := server.NewSignalingService(registry, manager)

	// 监听 TCP 端口
	lis, err := net.Listen("tcp", cfg.BindAddr)
	if err != nil {
		terminal.Fatalf("failed to listen on %s: %v", cfg.BindAddr, err)
	}

	// 组装 gRPC Options
	var opts []grpc.ServerOption
	opts = append(opts, grpc.Creds(credentials.NewServerTLSFromCert(&cert)))
	if cfg.Token != "" {
		opts = append(opts, grpc.UnaryInterceptor(auth.NewAuthUnaryInterceptor(cfg.Token)))
		opts = append(opts, grpc.StreamInterceptor(auth.NewAuthStreamInterceptor(cfg.Token)))
		terminal.Success("Token Authentication: ENABLED")
	} else {
		terminal.Warning("Token Authentication: DISABLED (No token configured)")
	}

	grpcServer := grpc.NewServer(opts...)
	pb.RegisterSignalingServiceServer(grpcServer, svc)

	reflection.Register(grpcServer)

	terminal.Successf("Signaling Server listening on %s (Subnet: %s.x)", cfg.BindAddr, cfg.VirtualSubnet)
	if err := grpcServer.Serve(lis); err != nil {
		terminal.Fatalf("failed to serve: %v", err)
	}
}

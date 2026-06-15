package main

import (
	"log"
	"net"
	"time"

	"github.com/Jingqi0327/GopherHole/internal/config"
	"github.com/Jingqi0327/GopherHole/internal/server"
	"github.com/Jingqi0327/GopherHole/pkg/auth"
	"github.com/Jingqi0327/GopherHole/pkg/crypto"
	"github.com/Jingqi0327/GopherHole/proto/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg, err := config.LoadServerConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 初始化与校验密钥对
	privB64, _, err := crypto.EnsureServerKeypair(cfg.PrivateKey, cfg.PublicKey)
	if err != nil {
		log.Fatalf("Keypair initialization failed: %v", err)
	}

	// 动态生成内存 TLS 证书
	cert, pubB64, err := crypto.GenerateMemTLSCertFromKey(privB64)
	if err != nil {
		log.Fatalf("Failed to generate TLS certificate: %v", err)
	}

	log.Println("GopherHole Signaling Server is starting...")
	log.Printf("=====================================================")
	log.Printf("[CONFIG GUIDE] Please set the following Public Key in Client config (SERVER_PUBLIC_KEY):")
	log.Printf("%s", pubB64)
	log.Printf("=====================================================")

	ipam := server.NewIPAM(cfg.VirtualSubnet)
	manager := server.NewPeerManager(func(virtualIP string) {
		ipam.Release(virtualIP)
	})

	// 开启后台巡检 Goroutine，定期清理超时的离线节点 (超时时间设为 30s)
	go func() {
		for {
			time.Sleep(10 * time.Second)
			manager.CleanExpired(30 * time.Second)
		}
	}()

	svc := server.NewSignalingService(ipam, manager)

	// 监听 TCP 端口
	lis, err := net.Listen("tcp", cfg.BindAddr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", cfg.BindAddr, err)
	}

	// 组装 gRPC Options
	var opts []grpc.ServerOption
	opts = append(opts, grpc.Creds(credentials.NewServerTLSFromCert(&cert)))
	if cfg.Token != "" {
		opts = append(opts, grpc.UnaryInterceptor(auth.NewAuthUnaryInterceptor(cfg.Token)))
		opts = append(opts, grpc.StreamInterceptor(auth.NewAuthStreamInterceptor(cfg.Token)))
		log.Println("Token Authentication: ENABLED")
	} else {
		log.Println("Token Authentication: DISABLED (No token configured)")
	}

	grpcServer := grpc.NewServer(opts...)
	pb.RegisterSignalingServiceServer(grpcServer, svc)

	reflection.Register(grpcServer)

	log.Printf("Signaling Server listening on %s (Subnet: %s.x)\n", cfg.BindAddr, cfg.VirtualSubnet)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

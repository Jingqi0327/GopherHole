package main

import (
	"log"
	"net"
	"time"

	"github.com/Jingqi0327/GopherHole/internal/config"
	"github.com/Jingqi0327/GopherHole/internal/server"
	"github.com/Jingqi0327/GopherHole/proto/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg, err := config.LoadServerConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Println("GopherHole Signaling Server is starting...")

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

	grpcServer := grpc.NewServer()
	pb.RegisterSignalingServiceServer(grpcServer, svc)

	reflection.Register(grpcServer)

	log.Printf("Signaling Server listening on %s (Subnet: %s.x)\n", cfg.BindAddr, cfg.VirtualSubnet)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

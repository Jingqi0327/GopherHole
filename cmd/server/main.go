package main

import (
	"log"
	"net"
	"time"

	"github.com/Jingqi0327/GopherHole/internal/server"
	"github.com/Jingqi0327/GopherHole/proto/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	log.Println("GopherHole Signaling Server is starting...")

	ipam := server.NewIPAM("10.8.0")
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
	lis, err := net.Listen("tcp", ":8086")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterSignalingServiceServer(grpcServer, svc)

	reflection.Register(grpcServer)

	log.Println("Signaling Server listening on :8086")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

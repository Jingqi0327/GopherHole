package main

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/Jingqi0327/GopherHole/internal/config"
	"github.com/Jingqi0327/GopherHole/internal/server"
	"github.com/Jingqi0327/GopherHole/pkg/auth"
	"github.com/Jingqi0327/GopherHole/pkg/crypto"
	"github.com/Jingqi0327/GopherHole/pkg/stun"
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

	// 提取绑定端口
	_, portStr, err := net.SplitHostPort(cfg.BindAddr)
	if err != nil {
		log.Fatalf("Invalid BindAddr %s: %v", cfg.BindAddr, err)
	}
	basePort, err := strconv.Atoi(portStr)
	if err != nil {
		log.Fatalf("Invalid port %s: %v", portStr, err)
	}

	stunPorts := []int32{int32(basePort)}

	secondaryPort := cfg.SecondarySTUNPort
	if secondaryPort <= 0 {
		secondaryPort = basePort + 1
	}

	addr := fmt.Sprintf(":%d", secondaryPort)
	udpAddr, _ := net.ResolveUDPAddr("udp", addr)
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Fatalf("Failed to listen on secondary STUN port %s: %v", addr, err)
	}

	stunPorts = append(stunPorts, int32(secondaryPort))
	go func(c *net.UDPConn, p string) {
		log.Printf("Secondary STUN Responder listening on UDP %s", p)
		buf := make([]byte, 2048)
		for {
			n, remoteAddr, err := c.ReadFromUDP(buf)
			if err != nil {
				continue
			}
			stun.HandleSTUNRequest(c, remoteAddr, buf[:n])
		}
	}(conn, addr)

	// 启动主端口的 UDP 监听
	go func(port int) {
		addr := fmt.Sprintf(":%d", port)
		udpAddr, _ := net.ResolveUDPAddr("udp", addr)
		conn, err := net.ListenUDP("udp", udpAddr)
		if err != nil {
			log.Fatalf("Failed to listen on primary UDP port %s: %v", addr, err)
		}
		log.Printf("Primary STUN Responder listening on UDP %s", addr)
		buf := make([]byte, 2048)
		for {
			n, remoteAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				continue
			}
			stun.HandleSTUNRequest(conn, remoteAddr, buf[:n])
		}
	}(basePort)

	svc := server.NewSignalingService(ipam, manager, stunPorts)

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

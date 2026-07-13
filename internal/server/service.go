package server

import (
	"context"
	"errors"
	"log"
	"net"
	"time"

	"github.com/Jingqi0327/GopherHole/proto/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// SignalingService 实现了 pb.SignalingServiceServer
type SignalingService struct {
	pb.UnimplementedSignalingServiceServer
	registry  *Registry
	manager   *PeerManager
}

func NewSignalingService(registry *Registry, manager *PeerManager) *SignalingService {
	return &SignalingService{
		registry: registry,
		manager:  manager,
	}
}

// extractClientIP 从 gRPC context 中提取客户端真实的公网 IP
func extractClientIP(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok {
		if host, _, err := net.SplitHostPort(p.Addr.String()); err == nil {
			return host
		}
		return p.Addr.String()
	}
	return ""
}

// Register 处理节点注册
func (s *SignalingService) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	publicIP := extractClientIP(ctx)

	hostname, virtualIP, err := s.registry.Allocate(req.GetRequestedHostname(), req.GetRequestedIp())
	if err != nil {
		if errors.Is(err, ErrIPConflict) {
			return nil, status.Errorf(codes.AlreadyExists, "IP allocation failed: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "Registration failed: %v", err)
	}

	// 启动一个定时器，如果 30 秒内没有建立心跳（即未被加入到 PeerManager），则视为注册后客户端异常退出，主动释放 IP
	go func(ip string) {
		time.Sleep(30 * time.Second)
		if !s.manager.HasPeer(ip) {
			log.Printf("Cleaning up potentially orphaned IP %s (no heartbeat established within 30s of registration)", ip)
			s.registry.Release(ip)
		}
	}(virtualIP)

	log.Printf("Node registered: %s (Public IP: %s), Assigned VirtualIP: %s", hostname, publicIP, virtualIP)

	return &pb.RegisterResponse{
		Hostname:  hostname,
		VirtualIp: virtualIP,
	}, nil
}

// Heartbeat 处理节点心跳与状态同步，同时作为 P2P 信令下发通道
func (s *SignalingService) Heartbeat(stream pb.SignalingService_HeartbeatServer) error {
	// 等待第一次心跳包，建立连接
	req, err := stream.Recv()
	if err != nil {
		return err
	}

	publicIP := extractClientIP(stream.Context())

	s.manager.AddOrUpdatePeer(req.GetHostname(), req.GetVirtualIp(), publicIP, req.GetPublicPort(), req.GetPublicKey())
	log.Printf("Node %s(%s) started heartbeat (Public Port: %d)", req.GetHostname(), req.GetVirtualIp(), req.GetPublicPort())

	// 注册信令通道
	signalCh := s.manager.Router.Register(req.GetVirtualIp())

	// 断开时移除节点
	defer func() {
		log.Printf("Node %s(%s) disconnected", req.GetHostname(), req.GetVirtualIp())
		s.manager.RemovePeer(req.GetVirtualIp())
	}()

	// 订阅节点列表变动
	updateCh := s.manager.Events.Subscribe()
	defer s.manager.Events.Unsubscribe(updateCh)

	// 连接成功后，立即下发一次当前的完整节点列表
	initResp := &pb.HeartbeatResponse{
		Payload: &pb.HeartbeatResponse_PeerList{
			PeerList: &pb.PeerList{Peers: s.manager.GetAllPeers()},
		},
	}
	if err := stream.Send(initResp); err != nil {
		return err
	}

	// 开启协程接收后续的心跳包
	errCh := make(chan error, 1)
	go func() {
		for {
			req, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			s.manager.AddOrUpdatePeer(req.GetHostname(), req.GetVirtualIp(), publicIP, req.GetPublicPort(), req.GetPublicKey())
		}
	}()

	// 主循环：等待错误、节点列表更新事件或定向信令推送
	for {
		select {
		case err := <-errCh:
			return err
		case <-updateCh:
			// 节点列表有变动，下发给当前客户端
			updateResp := &pb.HeartbeatResponse{
				Payload: &pb.HeartbeatResponse_PeerList{
					PeerList: &pb.PeerList{Peers: s.manager.GetAllPeers()},
				},
			}
			if err := stream.Send(updateResp); err != nil {
				return err
			}
		case sig, ok := <-signalCh:
			if !ok {
				// channel closed, exit
				return nil
			}
			// 有针对该客户端的信令需要下发
			sigResp := &pb.HeartbeatResponse{
				Payload: &pb.HeartbeatResponse_Signal{
					Signal: sig,
				},
			}
			if err := stream.Send(sigResp); err != nil {
				return err
			}
		}
	}
}

// SignalRoute 处理客户端发送的信令路由请求
func (s *SignalingService) SignalRoute(ctx context.Context, req *pb.SignalMessage) (*pb.SignalMessageAck, error) {
	log.Printf("Routing signal [%v] from %s to %s", req.Type, req.FromVirtualIp, req.ToVirtualIp)

	success := s.manager.Router.Route(req.ToVirtualIp, req)
	if !success {
		log.Printf("Failed to route signal to %s (Offline or channel full)", req.ToVirtualIp)
		return &pb.SignalMessageAck{
			Success: false,
			ErrMsg:  "Target node offline or busy",
		}, nil
	}

	return &pb.SignalMessageAck{
		Success: true,
	}, nil
}

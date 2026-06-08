package server

import (
	"context"
	"log"
	"net"

	"github.com/Jingqi0327/GopherHole/proto/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// SignalingService 实现了 pb.SignalingServiceServer
type SignalingService struct {
	pb.UnimplementedSignalingServiceServer
	ipam    *IPAM
	manager *PeerManager
}

func NewSignalingService(ipam *IPAM, manager *PeerManager) *SignalingService {
	return &SignalingService{
		ipam:    ipam,
		manager: manager,
	}
}

// Register 处理节点注册
func (s *SignalingService) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	var publicIP string
	if p, ok := peer.FromContext(ctx); ok {
		if host, _, err := net.SplitHostPort(p.Addr.String()); err == nil {
			publicIP = host
		} else {
			publicIP = p.Addr.String()
		}
	}

	virtualIP, err := s.ipam.Allocate(req.GetRequestedIp())
	if err != nil {
		return nil, status.Errorf(codes.AlreadyExists, "IP allocation failed: %v", err)
	}
	log.Printf("Node registered: %s (Public IP: %s), Assigned VirtualIP: %s", req.Hostname, publicIP, virtualIP)

	return &pb.RegisterResponse{
		VirtualIp: virtualIP,
		PublicIp:  publicIP,
	}, nil
}

// Heartbeat 处理节点心跳与状态同步，同时作为 P2P 信令下发通道
func (s *SignalingService) Heartbeat(stream pb.SignalingService_HeartbeatServer) error {
	// 等待第一次心跳包，建立连接
	req, err := stream.Recv()
	if err != nil {
		return err
	}

	var publicIP string
	if p, ok := peer.FromContext(stream.Context()); ok {
		if host, _, err := net.SplitHostPort(p.Addr.String()); err == nil {
			publicIP = host
		} else {
			publicIP = p.Addr.String()
		}
	}

	s.manager.AddOrUpdatePeer(req.VirtualIp, publicIP, req.PublicPort)
	log.Printf("Node %s started heartbeat (Public Port: %d)", req.VirtualIp, req.PublicPort)

	// 注册信令通道
	signalCh := s.manager.RegisterSignalChannel(req.VirtualIp)

	// 断开时移除节点
	defer func() {
		log.Printf("Node %s disconnected", req.VirtualIp)
		s.manager.UnregisterSignalChannel(req.VirtualIp)
		s.manager.RemovePeer(req.VirtualIp)
	}()

	// 订阅节点列表变动
	updateCh := s.manager.Subscribe()
	defer s.manager.Unsubscribe(updateCh)

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
			s.manager.AddOrUpdatePeer(req.VirtualIp, publicIP, req.PublicPort)
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
	
	success := s.manager.RouteSignal(req.ToVirtualIp, req)
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

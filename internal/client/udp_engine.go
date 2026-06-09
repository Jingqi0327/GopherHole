package client

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/Jingqi0327/GopherHole/proto/pb"
)

type UDPEngine struct {
	conn      *net.UDPConn
	localPort int
	peerTable *PeerTable
	virtualIP string
	grpcCli   pb.SignalingServiceClient
}

func NewUDPEngine(virtualIP string, pt *PeerTable, grpcCli pb.SignalingServiceClient) (*UDPEngine, error) {
	addr, err := net.ResolveUDPAddr("udp", ":0") // Bind to any available port
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	log.Printf("🚀 UDP Engine started on local port %d", localAddr.Port)

	return &UDPEngine{
		conn:      conn,
		localPort: localAddr.Port,
		peerTable: pt,
		virtualIP: virtualIP,
		grpcCli:   grpcCli,
	}, nil
}

func (e *UDPEngine) GetLocalPort() int {
	return e.localPort
}

func (e *UDPEngine) Start() {
	go e.readLoop()
}

func (e *UDPEngine) readLoop() {
	buf := make([]byte, 2048)
	for {
		n, addr, err := e.conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("UDP read error: %v", err)
			continue
		}
		e.handlePacket(buf[:n], addr)
	}
}

func (e *UDPEngine) handlePacket(data []byte, addr *net.UDPAddr) {
	msg := string(data)
	parts := strings.SplitN(msg, ":", 3) //msg格式 cmd:virtualIP:port
	if len(parts) < 2 {
		return
	}

	cmd := parts[0]
	fromVirtualIP := parts[1]
	fromPort := parts[2]

	// 无论收到什么包，更新该节点的实际公网端点（这对于穿越 Symmetric NAT 很关键，因为信令服务器看到的端口可能和双方互打的端口不同）
	e.peerTable.UpdateAddr(fromVirtualIP, addr)

	switch cmd {
	case "PUNCH":
		// 收到对方的探测包
		peer := e.peerTable.GetPeer(fromVirtualIP)
		if peer != nil {
			if peer.State != StateConnected {
				e.peerTable.UpdateState(fromVirtualIP, StateConnected)
				fmt.Printf("\n🎉 [Hole Punched] %s <-> %s\n", e.virtualIP, fromVirtualIP)
			}
			// 回复 ACK
			e.sendUDPStr(addr, fmt.Sprintf("PUNCH_ACK:%s", e.virtualIP))
		}
	case "PUNCH_ACK":
		// 收到对方的探测响应
		peer := e.peerTable.GetPeer(fromVirtualIP)
		if peer != nil && peer.State != StateConnected {
			e.peerTable.UpdateState(fromVirtualIP, StateConnected)
			fmt.Printf("\n🎉 [Hole Punched] %s <-> %s\n", e.virtualIP, fromVirtualIP)
		}
	case "MSG":
		if len(parts) == 3 {
			fmt.Printf("\n💬 [Msg from %s]: %s\n", fromVirtualIP, fromPort)
		}
	}
}

func (e *UDPEngine) sendUDPStr(addr *net.UDPAddr, msg string) {
	_, _ = e.conn.WriteToUDP([]byte(msg), addr)
}

// Punch 向目标节点发起打洞
func (e *UDPEngine) Punch(targetVirtualIP string) {
	peer := e.peerTable.GetPeer(targetVirtualIP)
	if peer == nil {
		log.Printf("❌ Target %s not found in peer table", targetVirtualIP)
		return
	}

	log.Printf("⏳ Starting hole punch to %s...", targetVirtualIP)
	e.peerTable.UpdateState(targetVirtualIP, StatePunching)

	// 1. 发送探测包
	e.sendUDPStr(peer.PublicAddr, fmt.Sprintf("PUNCH:%s", e.virtualIP))

	// 2. 通过信令服务器下发打洞请求
	req := &pb.SignalMessage{
		FromVirtualIp: e.virtualIP,
		ToVirtualIp:   targetVirtualIP,
		Type:          pb.SignalMessage_REQUEST_PUNCH,
	}
	_, err := e.grpcCli.SignalRoute(context.Background(), req)
	if err != nil {
		log.Printf("⚠️ Failed to route signal: %v", err)
	}
}

// HandleSignal 处理从 gRPC 收到的信令
func (e *UDPEngine) HandleSignal(sig *pb.SignalMessage) {
	if sig.Type == pb.SignalMessage_REQUEST_PUNCH {
		peer := e.peerTable.GetPeer(sig.FromVirtualIp)
		if peer != nil {
			// 收到对方通过服务器转来的打洞请求，立刻向对方的公网地址发包协助打洞
			log.Printf("🔔 Received punch request from %s, assisting...", sig.FromVirtualIp)
			e.peerTable.UpdateState(sig.FromVirtualIp, StatePunching)
			e.sendUDPStr(peer.PublicAddr, fmt.Sprintf("PUNCH:%s", e.virtualIP))
		}
	}
}

// SendMessage 发送测试文本消息
func (e *UDPEngine) SendMessage(targetVirtualIP, text string) {
	peer := e.peerTable.GetPeer(targetVirtualIP)
	if peer == nil || peer.State != StateConnected {
		log.Printf("❌ Cannot send message: not connected to %s", targetVirtualIP)
		return
	}
	e.sendUDPStr(peer.PublicAddr, fmt.Sprintf("MSG:%s:%s", e.virtualIP, text))
}

package client

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/Jingqi0327/GopherHole/proto/pb"
)

const (
	PacketTypeData    byte = 0x01
	PacketTypeControl byte = 0x02
)

type UDPEngine struct {
	conn       *net.UDPConn
	localPort  int
	peerTable  *PeerTable
	virtualIP  string
	grpcCli    pb.SignalingServiceClient
	onIPPacket func(data []byte)
}

func NewUDPEngine(virtualIP string, pt *PeerTable, grpcCli pb.SignalingServiceClient, onIP func([]byte)) (*UDPEngine, error) {
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
		conn:       conn,
		localPort:  localAddr.Port,
		peerTable:  pt,
		virtualIP:  virtualIP,
		grpcCli:    grpcCli,
		onIPPacket: onIP,
	}, nil
}

func (e *UDPEngine) GetLocalPort() int {
	return e.localPort
}

func (e *UDPEngine) Start() {
	go e.readLoop()
	go e.startKeepAlive()
}

func (e *UDPEngine) startKeepAlive() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		<-ticker.C
		peers := e.peerTable.GetAllPeers()
		for _, peer := range peers {
			// 对于正在打洞或者已经连通的节点，定时发送探测包维持 NAT 映射不超时
			if peer.State == StateConnected || peer.State == StatePunching {
				// 复用 PUNCH 信令作为 Keep-Alive，对端收到后会回复 PUNCH_ACK 并更新活跃时间
				e.sendUDPStr(peer.PublicAddr, fmt.Sprintf("PUNCH:%s", e.virtualIP))
			}
		}
	}
}

func (e *UDPEngine) readLoop() {
	buf := make([]byte, 2048)
	for {
		n, addr, err := e.conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("UDP read error: %v", err)
			continue
		}
		if n < 1 {
			continue
		}

		packetType := buf[0]
		payload := buf[1:n]

		switch packetType {
		case PacketTypeData:
			if e.onIPPacket != nil {
				e.onIPPacket(payload)
			}
		case PacketTypeControl:
			e.handlePacket(payload, addr)
		default:
			log.Printf("⚠️ Unknown packet type received: %d", packetType)
		}
	}
}

func (e *UDPEngine) handlePacket(data []byte, addr *net.UDPAddr) {
	msg := string(data)
	parts := strings.SplitN(msg, ":", 3) //msg格式 PUNCH:virtualIP、PUNCH_ACK:virtualIP、MSG:virtualIP:text
	if len(parts) < 2 {
		return
	}

	cmd := parts[0]
	fromVirtualIP := parts[1]


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
			fmt.Printf("\n💬 [Msg from %s]: %s\n", fromVirtualIP, parts[2])
		}
	}
}

func (e *UDPEngine) sendUDPStr(addr *net.UDPAddr, msg string) {
	buf := make([]byte, len(msg)+1)
	buf[0] = PacketTypeControl
	copy(buf[1:], msg)
	_, _ = e.conn.WriteToUDP(buf, addr)
}

// SendRaw 暴露给 Data Pump，用于发送原生的 IPv4 数据包
func (e *UDPEngine) SendRaw(data []byte, addr *net.UDPAddr) {
	buf := make([]byte, len(data)+1)
	buf[0] = PacketTypeData
	copy(buf[1:], data)
	_, _ = e.conn.WriteToUDP(buf, addr)
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

	// 1. 启动一个高频重试协程，快速发送多次探测包（提升穿透成功率，对抗丢包和时序问题）
	go func() {
		for i := 0; i < 5; i++ {
			p := e.peerTable.GetPeer(targetVirtualIP)
			if p == nil || p.State == StateConnected {
				return // 如果已经连通，直接停止发送探测
			}
			e.sendUDPStr(p.PublicAddr, fmt.Sprintf("PUNCH:%s", e.virtualIP))
			time.Sleep(300 * time.Millisecond)
		}
	}()

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
			
			go func() {
				for i := 0; i < 5; i++ {
					p := e.peerTable.GetPeer(sig.FromVirtualIp)
					if p == nil || p.State == StateConnected {
						return
					}
					e.sendUDPStr(p.PublicAddr, fmt.Sprintf("PUNCH:%s", e.virtualIP))
					time.Sleep(300 * time.Millisecond)
				}
			}()
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

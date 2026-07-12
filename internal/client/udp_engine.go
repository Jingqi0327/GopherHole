package client

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Jingqi0327/GopherHole/pkg/packet"
	"github.com/Jingqi0327/GopherHole/pkg/stun"
	"github.com/Jingqi0327/GopherHole/pkg/terminal"
	"github.com/Jingqi0327/GopherHole/proto/pb"
)



type UDPEngine struct {
	conn              *net.UDPConn              // 本地绑定的 UDP Socket，供所有 P2P 流量共用
	peerTable         *PeerTable                // 节点表，存储其他节点的信息、状态、公网地址及加密密钥
	virtualIP         string                    // 本节点的虚拟 IP
	publicPort        int                       // 经 STUN 探测后获得的公网映射端口
	publicIP          string                    // 经 STUN 探测后获得的公网 IP
	serverAddr        string                    // 远端信令服务器地址，用于发送探测包进行 NAT 映射保活
	grpcClient        pb.SignalingServiceClient // gRPC 客户端，用于通过服务器中转打洞信令
	keepaliveInterval int                       // NAT保活心跳间隔(秒)
	onIPPacket        func(data []byte)         // 回调函数：将收到的、解密后的底层 IP 报文注入 TUN 虚拟网卡
}

func NewUDPEngine(conn *net.UDPConn, virtualIP string, pt *PeerTable, grpcClient pb.SignalingServiceClient, serverAddr string, publicIP string, publicPort int, keepaliveInterval int, onIP func([]byte)) (*UDPEngine, error) {
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	terminal.Success(fmt.Sprintf("🚀 UDP Engine started on local port %d", localAddr.Port))

	return &UDPEngine{
		conn:              conn,
		publicPort:        publicPort,
		publicIP:          publicIP,
		serverAddr:        serverAddr,
		peerTable:         pt,
		virtualIP:         virtualIP,
		grpcClient:        grpcClient,
		keepaliveInterval: keepaliveInterval,
		onIPPacket:        onIP,
	}, nil
}

// GetPublicPort 返回 STUN 探测到的公网端口
func (e *UDPEngine) GetPublicPort() int {
	return e.publicPort
}

func (e *UDPEngine) Start() {
	go e.readLoop()
	go e.startKeepAlive()
}

func (e *UDPEngine) startKeepAlive() {
	if e.keepaliveInterval <= 0 {
		e.keepaliveInterval = 15 // Fallback to 15s if invalid
	}
	ticker := time.NewTicker(time.Duration(e.keepaliveInterval) * time.Second)
	defer ticker.Stop()

	// 使用 pkg/stun 提供的标准 STUN Binding Request 用于保活
	stunReq := stun.BuildSTUNRequest()

	var stunAddr *net.UDPAddr
	if e.serverAddr != "" {
		stunAddr, _ = net.ResolveUDPAddr("udp", e.serverAddr)
	}

	for {
		<-ticker.C

		// 1. 向 STUN 服务器发送保活包，维持 NAT 映射的公网端口不被回收
		if stunAddr != nil {
			// 我们不需要读取响应，仅仅是为了让 NAT 路由器看到有从本地 UDP 端口发往外网的活跃流量
			_, _ = e.conn.WriteToUDP(stunReq, stunAddr)
		}

		// 2. 对于正在打洞或者已经连通的节点，定时发送探测包维持 P2P 隧道不超时
		peers := e.peerTable.GetAllPeers()
		for _, peer := range peers {
			if peer.State == StateConnected || peer.State == StatePunching {
				// 复用 PUNCH 信令作为 Keep-Alive
				e.sendControlPacket(peer.PublicAddr, peer.VirtualIP, fmt.Sprintf("PUNCH:%s", e.virtualIP))
			}
		}
	}
}

func (e *UDPEngine) readLoop() {
	buf := make([]byte, 2048)
	for {
		n, addr, err := e.conn.ReadFromUDP(buf)
		if err != nil {
			terminal.Error(fmt.Sprintf("UDP read error: %v", err))
			continue
		}
		if n < 1 {
			continue
		}

		// 检测是否为 STUN 响应（处理 NAT 映射端口变动）
		// 心跳协程会在下一个心跳周期内将新的端点同步给 Signaling Server
		if stun.IsSTUNResponse(buf[:n]) {
			ip, port, err := stun.ParseSTUNResponse(buf[:n])
			if err == nil {
				if e.publicIP != ip || e.publicPort != port {
					terminal.Info(fmt.Sprintf("🌐 NAT Mapping changed! New public endpoint: %s:%d", ip, port))
					e.publicIP = ip
					e.publicPort = port
					if e.peerTable != nil {
						e.peerTable.ResetAllConnections()
					}
				}
			}
			continue
		}

		packetType, srcIP, ciphertext, err := packet.Parse(buf[:n])
		if err != nil {
			continue
		}

		peer := e.peerTable.GetPeer(srcIP)
		if peer == nil || peer.Cipher == nil {
			// 未知来源或未协商好密钥，丢弃
			continue
		}

		plaintext, err := peer.Cipher.Decrypt(ciphertext)
		if err != nil {
			terminal.Warning(fmt.Sprintf("Failed to decrypt packet from %s: %v", srcIP, err))
			continue
		}

		switch packetType {
		case packet.TypeData:
			if e.onIPPacket != nil {
				e.onIPPacket(plaintext)
			}
		case packet.TypeControl:
			e.handleControlPacket(plaintext, addr)
		default:
			terminal.Warning(fmt.Sprintf("Unknown packet type received: %d", packetType))
		}
	}
}

// handleControlPacket 处理控制包
func (e *UDPEngine) handleControlPacket(data []byte, addr *net.UDPAddr) {
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
				terminal.Success(fmt.Sprintf("[Hole Punched] %s <-> %s", e.virtualIP, fromVirtualIP))
				
				// 取出并发送由于尚未连通而积压在队列中的数据包 (例如 TCP SYN 首包)
				pending := e.peerTable.FlushPendingPackets(fromVirtualIP)
				for _, pkt := range pending {
					e.SendDataPacket(pkt.Buffer, pkt.PayloadLen, addr, fromVirtualIP)
				}
			}
			// 回复 ACK
			e.sendControlPacket(addr, fromVirtualIP, fmt.Sprintf("PUNCH_ACK:%s", e.virtualIP))
		}
	case "PUNCH_ACK":
		// 收到对方的探测响应
		peer := e.peerTable.GetPeer(fromVirtualIP)
		if peer != nil && peer.State != StateConnected {
			e.peerTable.UpdateState(fromVirtualIP, StateConnected)
			terminal.Success(fmt.Sprintf("[Hole Punched] %s <-> %s", e.virtualIP, fromVirtualIP))

			// 取出并发送由于尚未连通而积压在队列中的数据包 (例如 TCP SYN 首包)
			pending := e.peerTable.FlushPendingPackets(fromVirtualIP)
			for _, pkt := range pending {
				e.SendDataPacket(pkt.Buffer, pkt.PayloadLen, addr, fromVirtualIP)
			}
		}
	case "MSG":
		if len(parts) == 3 {
			terminal.Info(fmt.Sprintf("[Msg from %s]: %s", fromVirtualIP, parts[2]))
		}
	}
}

// sendControlPacket 发送一个带有 PUNCH、PUNCH_ACK 或 MSG 命令的 UDP 报文
func (e *UDPEngine) sendControlPacket(addr *net.UDPAddr, destVirtualIP string, msg string) error {
	peer := e.peerTable.GetPeer(destVirtualIP)
	if peer == nil || peer.Cipher == nil {
		return fmt.Errorf("peer not found or no cipher")
	}
	ciphertext := peer.Cipher.Encrypt([]byte(msg))
	buf := packet.Build(packet.TypeControl, e.virtualIP, ciphertext)
	if buf == nil {
		return fmt.Errorf("failed to build packet for %s", destVirtualIP)
	}

	_, err := e.conn.WriteToUDP(buf, addr)
	return err
}

// SendDataPacket 暴露给 Data Pump，用于发送零拷贝封装的 IPv4 数据包
// buffer 是预留了 Headroom 的完整切片，payloadLen 是真实的 IP 报文长度
func (e *UDPEngine) SendDataPacket(buffer []byte, payloadLen int, addr *net.UDPAddr, destVirtualIP string) {
	peer := e.peerTable.GetPeer(destVirtualIP)
	if peer == nil || peer.Cipher == nil {
		return
	}

	packet.InjectHeader(buffer, packet.TypeData, e.virtualIP)
	finalBuf := peer.Cipher.EncryptInPlace(buffer, payloadLen)

	_, _ = e.conn.WriteToUDP(finalBuf, addr)
}

// Punch 向目标节点发起打洞
func (e *UDPEngine) Punch(targetVirtualIP string) {
	peer := e.peerTable.GetPeer(targetVirtualIP)
	if peer == nil {
		terminal.Error(fmt.Sprintf("Target %s not found in peer table", targetVirtualIP))
		return
	}

	terminal.Info(fmt.Sprintf("Starting hole punch to %s...", targetVirtualIP))
	e.peerTable.UpdateState(targetVirtualIP, StatePunching)

	// 1. 启动一个高频重试协程，快速发送多次探测包（提升穿透成功率，对抗丢包和时序问题）
	go func() {
		for i := 0; i < 5; i++ {
			p := e.peerTable.GetPeer(targetVirtualIP)
			if p == nil || p.State == StateConnected {
				return // 如果已经连通，直接停止发送探测
			}
			e.sendControlPacket(p.PublicAddr, targetVirtualIP, fmt.Sprintf("PUNCH:%s", e.virtualIP))
			time.Sleep(300 * time.Millisecond)
		}

		// 打不通的话恢复未连接状态
		p := e.peerTable.GetPeer(targetVirtualIP)
		if p != nil && p.State == StatePunching {
			terminal.Warning(fmt.Sprintf("Hole punch to %s timed out.", targetVirtualIP))
			e.peerTable.UpdateState(targetVirtualIP, StateDisconnected)
			e.peerTable.FlushPendingPackets(targetVirtualIP)
		}
	}()

	// 2. 通过信令服务器下发打洞请求
	req := &pb.SignalMessage{
		FromVirtualIp: e.virtualIP,
		ToVirtualIp:   targetVirtualIP,
		Type:          pb.SignalMessage_REQUEST_PUNCH,
	}
	_, err := e.grpcClient.SignalRoute(context.Background(), req)
	if err != nil {
		terminal.Error(fmt.Sprintf("Failed to route signal: %v", err))
	}
}

// HandleSignal 处理从 gRPC 收到的信令
func (e *UDPEngine) HandleSignal(sig *pb.SignalMessage) {
	if sig.Type == pb.SignalMessage_REQUEST_PUNCH {
		targetPeer := e.peerTable.GetPeer(sig.FromVirtualIp)
		if targetPeer != nil {
			if targetPeer.State == StateDisconnected {
				terminal.Info(fmt.Sprintf("Received punch request from %s, assisting...", sig.FromVirtualIp))
				e.Punch(sig.FromVirtualIp)
			}
		}
	}
}

// SendMessage 发送测试文本消息
func (e *UDPEngine) SendMessage(targetVirtualIP, text string) {
	peer := e.peerTable.GetPeer(targetVirtualIP)
	if peer == nil {
		terminal.Error(fmt.Sprintf("Cannot send message: peer %s not found", targetVirtualIP))
		return
	}
    // 如果没有连通，顺手帮他触发打洞
	if peer.State != StateConnected {
		terminal.Info(fmt.Sprintf("Not connected to %s. Triggering hole punch, please try sending again in a moment...", targetVirtualIP))
		if peer.State != StatePunching {
			e.Punch(targetVirtualIP)
		}
		return
	}
	e.sendControlPacket(peer.PublicAddr, targetVirtualIP, fmt.Sprintf("MSG:%s:%s", e.virtualIP, text))
}


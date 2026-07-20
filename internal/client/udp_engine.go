package client

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
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
	localNatType      string                    // 本端 NAT 类型: "EasyNAT" 或 "HardNAT"
	onIPPacket        func(data []byte)         // 回调函数：将收到的、解密后的底层 IP 报文注入 TUN 虚拟网卡
	punchCancelsMu    sync.Mutex
	punchCancels      map[string]context.CancelFunc
}

func NewUDPEngine(conn *net.UDPConn, virtualIP string, pt *PeerTable, grpcClient pb.SignalingServiceClient, serverAddr string, publicIP string, publicPort int, keepaliveInterval int, natType string, onIP func([]byte)) (*UDPEngine, error) {
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	terminal.Success(fmt.Sprintf("UDP Engine started on local port %d", localAddr.Port))

	return &UDPEngine{
		conn:              conn,
		publicPort:        publicPort,
		publicIP:          publicIP,
		serverAddr:        serverAddr,
		peerTable:         pt,
		virtualIP:         virtualIP,
		grpcClient:        grpcClient,
		keepaliveInterval: keepaliveInterval,
		localNatType:      natType,
		onIPPacket:        onIP,
		punchCancels:      make(map[string]context.CancelFunc),
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
			if peer.GetState() == StateConnected || peer.GetState() == StatePunching {
				// 复用 PUNCH 信令作为 Keep-Alive，选择正确的 socket 发送
				e.sendControlPacketAuto(peer.VirtualIP, fmt.Sprintf("PUNCH:%s", e.virtualIP))
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
					terminal.Info(fmt.Sprintf("NAT Mapping changed! New public endpoint: %s:%d", ip, port))
					e.publicIP = ip
					e.publicPort = port
					if e.peerTable != nil {
						e.peerTable.ResetAllConnections()
					}
				}
			}
			continue
		}

		e.processIncomingPacket(buf[:n], addr, e.conn)
	}
}

// processIncomingPacket 解析并处理收到的 UDP 数据包
func (e *UDPEngine) processIncomingPacket(data []byte, addr *net.UDPAddr, rxConn *net.UDPConn) {
	packetType, srcIP, ciphertext, err := packet.Parse(data)
	if err != nil {
		return
	}

	peer := e.peerTable.GetPeer(srcIP)
	if peer == nil || peer.GetCipher() == nil {
		// 未知来源或未协商好密钥，丢弃
		return
	}

	plaintext, err := peer.GetCipher().Decrypt(ciphertext)
	if err != nil {
		terminal.Warning(fmt.Sprintf("Failed to decrypt packet from %s: %v", srcIP, err))
		return
	}

	switch packetType {
	case packet.TypeData:
		if e.onIPPacket != nil {
			e.onIPPacket(plaintext)
		}
	case packet.TypeControl:
		e.handleControlPacket(plaintext, addr, rxConn)
	default:
		terminal.Warning(fmt.Sprintf("Unknown packet type received: %d", packetType))
	}
}

// handleControlPacket 处理控制包
func (e *UDPEngine) handleControlPacket(data []byte, addr *net.UDPAddr, rxConn *net.UDPConn) {
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
			if peer.GetState() != StateConnected {
				e.peerTable.UpdateState(fromVirtualIP, StateConnected)
				e.CancelPunch(fromVirtualIP)
				terminal.Success(fmt.Sprintf("[Hole Punched] %s <-> %s", e.virtualIP, fromVirtualIP))

				// 取出并发送由于尚未连通而积压在队列中的数据包 (例如 TCP SYN 首包)
				pending := e.peerTable.FlushPendingPackets(fromVirtualIP)
				for _, pkt := range pending {
					e.SendDataPacket(pkt.Buffer, pkt.PayloadLen, addr, fromVirtualIP)
				}
			}
			// 回复 ACK，优先使用接收到该包的连接（如辅助 socket），以确保能被对端（NAT3）的防火墙放行
			conn := rxConn
			if conn == nil {
				conn = e.conn
			}
			e.sendControlPacket(conn, addr, fromVirtualIP, fmt.Sprintf("PUNCH_ACK:%s", e.virtualIP))
		}
	case "PUNCH_ACK":
		// 收到对方的探测响应
		peer := e.peerTable.GetPeer(fromVirtualIP)
		if peer != nil && peer.GetState() != StateConnected {
			e.peerTable.UpdateState(fromVirtualIP, StateConnected)
			e.CancelPunch(fromVirtualIP)
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

// sendControlPacket 通过指定的 conn 和 addr 发送控制包
func (e *UDPEngine) sendControlPacket(conn *net.UDPConn, addr *net.UDPAddr, destVirtualIP string, msg string) error {
	peer := e.peerTable.GetPeer(destVirtualIP)
	if peer == nil || peer.GetCipher() == nil {
		return fmt.Errorf("peer not found or no cipher")
	}
	ciphertext := peer.GetCipher().Encrypt([]byte(msg))
	buf := packet.Build(packet.TypeControl, e.virtualIP, ciphertext)
	if buf == nil {
		return fmt.Errorf("failed to build packet for %s", destVirtualIP)
	}

	_, err := conn.WriteToUDP(buf, addr)
	return err
}

// sendControlPacketAuto 自动选择正确的 socket 发送控制包
// 如果 peer 有 DirectConn（NAT4 打洞成功后的专用 socket），使用 DirectConn；否则使用主 socket
func (e *UDPEngine) sendControlPacketAuto(destVirtualIP string, msg string) error {
	peer := e.peerTable.GetPeer(destVirtualIP)
	if peer == nil {
		return fmt.Errorf("peer not found")
	}

	conn := e.conn
	if dc := peer.GetDirectConn(); dc != nil {
		conn = dc
	}

	return e.sendControlPacket(conn, peer.GetObservedAddr(), destVirtualIP, msg)
}

// SendDataPacket 暴露给 Data Pump，用于发送零拷贝封装的 IPv4 数据包
// buffer 是预留了 Headroom 的完整切片，payloadLen 是真实的 IP 报文长度
func (e *UDPEngine) SendDataPacket(buffer []byte, payloadLen int, addr *net.UDPAddr, destVirtualIP string) {
	peer := e.peerTable.GetPeer(destVirtualIP)
	if peer == nil || peer.GetCipher() == nil {
		return
	}

	packet.InjectHeader(buffer, packet.TypeData, e.virtualIP)
	finalBuf := peer.GetCipher().EncryptInPlace(buffer, payloadLen)

	// 选择正确的 socket：如果有 DirectConn 则使用它
	conn := e.conn
	if dc := peer.GetDirectConn(); dc != nil {
		conn = dc
	}

	_, _ = conn.WriteToUDP(finalBuf, addr)
}


// HandleSignal 处理从 gRPC 收到的信令
func (e *UDPEngine) HandleSignal(sig *pb.SignalMessage) {
	if sig.Type == pb.SignalMessage_REQUEST_PUNCH {
		targetPeer := e.peerTable.GetPeer(sig.FromVirtualIp)
		if targetPeer != nil {
			if targetPeer.GetState() == StateDisconnected {
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
	if peer.GetState() != StateConnected {
		terminal.Info(fmt.Sprintf("Not connected to %s. Triggering hole punch, please try sending again in a moment...", targetVirtualIP))
		if peer.GetState() != StatePunching {
			e.Punch(targetVirtualIP)
		}
		return
	}
	e.sendControlPacketAuto(targetVirtualIP, fmt.Sprintf("MSG:%s:%s", e.virtualIP, text))
}

package client

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/Jingqi0327/GopherHole/pkg/terminal"
	"github.com/Jingqi0327/GopherHole/proto/pb"
)

// registerPunchContext 为目标节点创建并注册一个 Context
func (e *UDPEngine) registerPunchContext(targetVirtualIP string) context.Context {
	e.punchCancelsMu.Lock()
	defer e.punchCancelsMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())

	// 如果该节点之前已经有正在运行的打洞协程，先将其取消，防止重复协程泄漏
	if oldCancel, ok := e.punchCancels[targetVirtualIP]; ok {
		oldCancel()
	}

	e.punchCancels[targetVirtualIP] = cancel
	return ctx
}

// CancelPunch 触发并移除指定节点的打洞取消信号
func (e *UDPEngine) CancelPunch(targetVirtualIP string) {
	e.punchCancelsMu.Lock()
	defer e.punchCancelsMu.Unlock()

	if cancel, ok := e.punchCancels[targetVirtualIP]; ok {
		cancel()
		delete(e.punchCancels, targetVirtualIP)
	}
}

// Punch 向目标节点发起打洞，根据双方 NAT 类型选择不同策略
func (e *UDPEngine) Punch(targetVirtualIP string) {
	peer := e.peerTable.GetPeer(targetVirtualIP)
	if peer == nil {
		terminal.Error(fmt.Sprintf("Target %s not found in peer table", targetVirtualIP))
		return
	}

	localNAT := e.localNatType
	remoteNAT := peer.GetNatType()

	terminal.Info(fmt.Sprintf("Starting hole punch to %s (local=%s, remote=%s)...", targetVirtualIP, localNAT, remoteNAT))
	e.peerTable.UpdateState(targetVirtualIP, StatePunching)

	ctx := e.registerPunchContext(targetVirtualIP)

	// 根据 NAT 类型组合选择打洞策略
	if localNAT == "EasyNAT" && remoteNAT == "HardNAT" {
		// 本端是 NAT3（EasyNAT），对端是 NAT4（HardNAT）→ 端口扫描打洞（两阶段扫射）
		go e.portScanningPunch(ctx, targetVirtualIP)
	} else if localNAT == "HardNAT" && remoteNAT == "EasyNAT" {
		// 本端是 NAT4（HardNAT），对端是 NAT3（EasyNAT）→ 多 socket 打洞
		go e.multiSocketPunch(ctx, targetVirtualIP)
	} else if localNAT == "HardNAT" && remoteNAT == "HardNAT" {
		// NAT4 × NAT4 → 目前不支持直连
		terminal.Warning(fmt.Sprintf("NAT4×NAT4 (%s): Direct P2P not supported. Relay needed (v1.4).", targetVirtualIP))
		e.peerTable.UpdateState(targetVirtualIP, StateDisconnected)
		e.peerTable.FlushPendingPackets(targetVirtualIP)
		e.CancelPunch(targetVirtualIP)
		return
	} else {
		// EasyNAT × EasyNAT 或类型未知 → 走标准打洞逻辑
		go e.standardPunch(ctx, targetVirtualIP)
	}

	// 通过信令服务器下发打洞请求，通知对端也开始打洞
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

// isPeerConnected 检查与目标节点的连接是否已打通
func (e *UDPEngine) isPeerConnected(p *PeerEntry) bool {
	return p != nil && p.GetState() == StateConnected
}

// standardPunch 标准打洞逻辑（EasyNAT × EasyNAT 或类型未知时使用，保留原有行为）
func (e *UDPEngine) standardPunch(ctx context.Context, targetVirtualIP string) {
	p := e.peerTable.GetPeer(targetVirtualIP)
	if p == nil {
		return
	}

	for i := 0; i < 5; i++ {
		e.sendControlPacket(e.conn, p.GetObservedAddr(), p.VirtualIP, fmt.Sprintf("PUNCH:%s", e.virtualIP))
		
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}

	// 打不通的话恢复未连接状态
	if p.GetState() == StatePunching {
		terminal.Warning(fmt.Sprintf("Hole punch to %s timed out.", p.VirtualIP))
		e.peerTable.UpdateState(p.VirtualIP, StateDisconnected)
		e.peerTable.FlushPendingPackets(p.VirtualIP)
	}
	e.CancelPunch(p.VirtualIP)
}

// portScanningPunch NAT3 端端口扫描打洞
func (e *UDPEngine) portScanningPunch(ctx context.Context, targetVirtualIP string) {
	peer := e.peerTable.GetPeer(targetVirtualIP)
	if peer == nil {
		return
	}

	// 无论以何种原因退出（打通、掉线或超时），都确保在该打洞任务结束时清理其 Context 关联
	defer e.CancelPunch(targetVirtualIP)

	targetIP := peer.GetObservedAddr().IP
	portY := peer.GetObservedAddr().Port

	terminal.Info(fmt.Sprintf("[PortScanning] Starting two-phase scan for %s (portY=%d)", targetVirtualIP, portY))

	// 给 NAT4 端一秒钟的抢跑时间，确保它的多 socket 映射表先建立完毕
	select {
	case <-ctx.Done():
		terminal.Warning(fmt.Sprintf("[PortScanning] Cancelled for %s before starting (peer offline).", targetVirtualIP))
		return
	case <-time.After(1 * time.Second):
	}

	// === 阶段1: 定向扫射 ===
	e.directedScanPunch(ctx, peer, targetIP, portY)

	// 阶段一完成后 Double-check 连通状态，防止因网络延迟更新造成抢跑
	if e.isPeerConnected(peer) {
		terminal.Success("[PortScanning] Succeeded in Phase 1!")
		return
	}
	if ctx.Err() != nil {
		terminal.Warning(fmt.Sprintf("[PortScanning] Cancelled for %s during Phase 1 (peer offline).", targetVirtualIP))
		return // 掉线直接退出
	}

	// === 阶段2: 全端口随机扫射 ===
	e.randomScanPunch(ctx, peer, targetIP)

	// 阶段二完成后 Double-check 连通状态
	if e.isPeerConnected(peer) {
		terminal.Success("[PortScanning] Succeeded in Phase 2!")
	} else {
		// 只有在打洞整体超时且没有取消、且依然是打洞状态时，才恢复为未连接状态
		if ctx.Err() == nil && peer.GetState() == StatePunching {
			terminal.Warning(fmt.Sprintf("[PortScanning] Timed out for %s.", targetVirtualIP))
			e.peerTable.UpdateState(targetVirtualIP, StateDisconnected)
			e.peerTable.FlushPendingPackets(targetVirtualIP)
		} else if ctx.Err() != nil {
			terminal.Warning(fmt.Sprintf("[PortScanning] Cancelled for %s (peer offline).", targetVirtualIP))
		}
	}
}

// directedScanPunch 端口扫描第一阶段：定向扫射对端 portY 递增的端口范围
func (e *UDPEngine) directedScanPunch(ctx context.Context, peer *PeerEntry, targetIP net.IP, portY int) {
	terminal.Info(fmt.Sprintf("[PortScanning] Phase 1: Directed scan [%d, %d]", portY, min(65535, portY+200)))

	// 生成候选端口并打乱顺序（向递增方向扫描 200 个端口）
	candidates := make([]int, 0, 201)
	for p := portY; p <= portY+200; p++ {
		if p >= 1024 && p <= 65535 {
			candidates = append(candidates, p)
		}
	}
	rand.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	for _, port := range candidates {
		addr := &net.UDPAddr{IP: targetIP, Port: port}
		e.sendControlPacket(e.conn, addr, peer.VirtualIP, fmt.Sprintf("PUNCH:%s", e.virtualIP))
		
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// randomScanPunch 端口扫描第二阶段：全端口随机扫射
func (e *UDPEngine) randomScanPunch(ctx context.Context, peer *PeerEntry, targetIP net.IP) {
	terminal.Info("[PortScanning] Phase 1 missed. Entering Phase 2: Random full-port scan ...")

	timeout := time.After(59 * time.Second) // 阶段1 约耗时 0.4 秒，总计给 60 秒
	const maxRandomPackets = 1500
	sentCount := 0
	currentBatchSize := 300

	for sentCount < maxRandomPackets {
		select {
		case <-ctx.Done():
			return
		case <-timeout:
			return
		default:
			// 确定本批次发送数量
			toSend := currentBatchSize
			if sentCount+toSend > maxRandomPackets {
				toSend = maxRandomPackets - sentCount
			}

			terminal.Info(fmt.Sprintf("[PortScanning] Phase 2: sending batch of %d packets (total sent: %d)...", toSend, sentCount))

			for i := 0; i < toSend; i++ {
				randomPort := 1024 + rand.Intn(65535-1024+1)
				addr := &net.UDPAddr{IP: targetIP, Port: randomPort}
				e.sendControlPacket(e.conn, addr, peer.VirtualIP, fmt.Sprintf("PUNCH:%s", e.virtualIP))
				
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Millisecond):
				}
			}

			sentCount += toSend

			// 批次大小逐次递减，最低降至 150
			if currentBatchSize > 150 {
				currentBatchSize -= 50
			}

			// 强制 Sleep 2 秒供路由器缓冲并给 RTT 留出极宽裕的状态回传检测时间，同时检测是否超时
			select {
			case <-ctx.Done():
				return
			case <-timeout:
				return
			case <-time.After(2 * time.Second):
			}
		}
	}

	// 1500 个包发完后，进入静默监听状态，直到超时
	terminal.Info("[PortScanning] Phase 2 packets sent. Entering silent listening mode...")
	for {
		select {
		case <-ctx.Done():
			return
		case <-timeout:
			return
		}
	}
}

// multiSocketPunch NAT4 端多 socket 打散端口逻辑
// 开 N 个辅助 socket，每个 socket 向 NAT3 端已知地址发包，
// 制造尽可能多的公网端口映射，等待 NAT3 端扫射命中
func (e *UDPEngine) multiSocketPunch(ctx context.Context, targetVirtualIP string) {
	peer := e.peerTable.GetPeer(targetVirtualIP)
	if peer == nil {
		return
	}

	nat3Addr := peer.GetObservedAddr() // NAT3 的公网地址（固定映射，可信）
	const socketCount = 128

	terminal.Info(fmt.Sprintf("[MultiSocket] Opening %d sockets toward %s", socketCount, nat3Addr.String()))

	var successConn *net.UDPConn
	var successOnce sync.Once

	// 共用的读取处理函数
	handleRead := func(c *net.UDPConn, id string) {
		buf := make([]byte, 2048)
		for {
			n, _, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}

			// 复用主 readLoop 的解析逻辑
			e.processIncomingPacket(buf[:n], nat3Addr, c)

			// 检查是否打通了
			p := e.peerTable.GetPeer(targetVirtualIP)
			if p != nil && p.GetState() == StateConnected {
				successOnce.Do(func() {
					successConn = c
					terminal.Success(fmt.Sprintf("[MultiSocket] %s punched through!", id))
				})
				return
			}
		}
	}

	// 批量创建辅助 sockets
	sockets := make([]*net.UDPConn, socketCount)
	for i := 0; i < socketCount; i++ {
		s, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			continue
		}
		sockets[i] = s
		go handleRead(s, fmt.Sprintf("Socket-%d", i+1))
	}

	// 循环发包
	go func() {
		round := 0
		for {
			select {
			case <-ctx.Done():
				return
			default:
				round++
				terminal.Info(fmt.Sprintf("[MultiSocket] Round %d: sending from %d sockets...", round, socketCount))

				// 每 10 轮（约 30 秒）换一批新 socket，映射到新的公网端口
				if round > 1 && round%10 == 0 {
					terminal.Info("[MultiSocket] Renewing sockets for fresh port mappings...")
					for i := 0; i < socketCount; i++ {
						select {
						case <-ctx.Done():
							return
						default:
						}
						if sockets[i] != nil {
							sockets[i].Close()
						}
						s, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
						if err != nil {
							continue
						}
						sockets[i] = s
						go handleRead(s, fmt.Sprintf("Socket-New-%d", i+1))
					}
				}

				// 所有 socket 同时向 NAT3 地址发包
				for i := 0; i < socketCount; i++ {
					if sockets[i] != nil {
						e.sendControlPacket(sockets[i], nat3Addr, targetVirtualIP, fmt.Sprintf("PUNCH:%s", e.virtualIP))
					}
				}

				select {
				case <-ctx.Done():
					return
				case <-time.After(3 * time.Second):
				}
			}
		}
	}()

	// 等待结果
	select {
	case <-ctx.Done():
	case <-time.After(60 * time.Second):
		if successConn == nil {
			terminal.Warning(fmt.Sprintf("[HardPunch-Hard] Timed out for %s (60s).", targetVirtualIP))
			e.peerTable.UpdateState(targetVirtualIP, StateDisconnected)
			e.peerTable.FlushPendingPackets(targetVirtualIP)
		}
	}

	// 判定结果与清理：保留打通的 socket 作为 DirectConn，关闭其余的
	if successConn != nil {
		terminal.Success(fmt.Sprintf("[HardPunch-Hard] Hole punch to %s succeeded!", targetVirtualIP))
		// 将打通的辅助 socket 升级为该 peer 的专用通道
		peer := e.peerTable.GetPeer(targetVirtualIP)
		if peer != nil {
			peer.SetDirectConn(successConn)
			terminal.Info(fmt.Sprintf("[HardPunch-Hard] Socket promoted to DirectConn for %s", targetVirtualIP))

			// 为 DirectConn 启动持久的读取循环
			go e.directConnReadLoop(successConn, targetVirtualIP)
		}
	} else if ctx.Err() != nil {
		terminal.Info(fmt.Sprintf("[HardPunch-Hard] Cancelled for %s.", targetVirtualIP))
	}

	// 关闭其余未打通的辅助 socket
	for i := 0; i < socketCount; i++ {
		if sockets[i] != nil && sockets[i] != successConn {
			sockets[i].Close()
		}
	}
	e.CancelPunch(targetVirtualIP)
}

// directConnReadLoop 为 NAT4 打洞成功后的专用 socket 启动持久读取循环
func (e *UDPEngine) directConnReadLoop(conn *net.UDPConn, peerVirtualIP string) {
	buf := make([]byte, 2048)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			// socket 被关闭，退出
			terminal.Info(fmt.Sprintf("[DirectConn] Read loop ended for %s: %v", peerVirtualIP, err))
			return
		}
		if n < 1 {
			continue
		}
		e.processIncomingPacket(buf[:n], addr, conn)
	}
}

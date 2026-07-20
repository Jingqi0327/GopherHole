package client

import (
	"bytes"
	"log"
	"net"
	"sync"
	"time"

	"github.com/Jingqi0327/GopherHole/pkg/crypto"
	"github.com/Jingqi0327/GopherHole/pkg/utils"
	"github.com/Jingqi0327/GopherHole/proto/pb"
)

type PeerState int

const (
	StateDisconnected PeerState = iota
	StatePunching
	StateConnected
)

type PendingPacket struct {
	Buffer     []byte
	PayloadLen int
}

type PeerEntry struct {
	mu             sync.RWMutex
	Hostname       string              // 对端hostname
	VirtualIP      string              // 对端虚拟IP
	ReportedAddr   *net.UDPAddr        // Server 下发的参考地址
	ObservedAddr   *net.UDPAddr        // 实际打通的公网地址
	PublicKey      []byte              // Client端的公钥,用于p2p隧道的加密
	Cipher         *crypto.SymmetricCipher
	CipherFailed   bool                // 标记Cipher是否初始化失败过
	State          PeerState           // 和对端的状态
	NatType        string              // 对端的 NAT 类型: "EasyNAT" 或 "HardNAT"
	DirectConn     *net.UDPConn        // NAT4 打洞成功后保留的专用 socket（仅 HardNAT 本端使用）
	LastActive     time.Time           // 上次活跃时间
	PendingPackets []PendingPacket     // 用于暂存打洞期间的数据包
}

func (p *PeerEntry) GetCipher() *crypto.SymmetricCipher {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Cipher
}

func (p *PeerEntry) GetObservedAddr() *net.UDPAddr {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ObservedAddr
}

func (p *PeerEntry) GetState() PeerState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.State
}

func (p *PeerEntry) GetNatType() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.NatType
}

func (p *PeerEntry) GetDirectConn() *net.UDPConn {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.DirectConn
}

func (p *PeerEntry) SetDirectConn(conn *net.UDPConn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.DirectConn = conn
}

type PeerTable struct {
	mu         sync.RWMutex
	peers      map[string]*PeerEntry
	privateKey [32]byte
}

func NewPeerTable(privateKey [32]byte) *PeerTable {
	return &PeerTable{
		peers:      make(map[string]*PeerEntry),
		privateKey: privateKey,
	}
}

// SyncPeers 全量同步 Server 下发的在线节点列表，返回被下线剔除的虚拟 IP 列表
func (pt *PeerTable) SyncPeers(onlinePeers []*pb.RemotePeer) []string {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	onlineMap := make(map[string]*pb.RemotePeer)
	for _, p := range onlinePeers {
		onlineMap[p.VirtualIp] = p
	}

	var deletedPeers []string
	// 1. 踢掉已经下线的节点
	for virtualIP := range pt.peers {
		if _, ok := onlineMap[virtualIP]; !ok {
			deletedPeers = append(deletedPeers, virtualIP)
			delete(pt.peers, virtualIP)
		}
	}

	// 2. 更新或添加在线节点
	for virtualIP, p := range onlineMap {
		sigAddr := &net.UDPAddr{
			IP:   net.ParseIP(p.PublicIp),
			Port: int(p.PublicPort),
		}

		existing, ok := pt.peers[virtualIP]
		if !ok {
			// 新上线的节点
			pt.peers[virtualIP] = &PeerEntry{
				Hostname:       p.Hostname,
				VirtualIP:      virtualIP,
				ReportedAddr:   sigAddr,
				ObservedAddr:   sigAddr, // 初始时，将信令地址作为预测的打洞地址
				PublicKey:      p.PublicKey,
				Cipher:         nil, // 懒加载，在首次通信时通过 GetPeer 触发生成
				State:          StateDisconnected,
				NatType:        p.NatType,
				PendingPackets: nil,
			}
		} else { // 整理并重构 PeerEntry 的更新逻辑
			existing.mu.Lock()
			existing.Hostname = p.Hostname
			existing.NatType = p.NatType

			pubKeyChanged := !bytes.Equal(existing.PublicKey, p.PublicKey)
			addrChanged := existing.ReportedAddr.String() != sigAddr.String()

			if pubKeyChanged || addrChanged {
				existing.PublicKey = p.PublicKey
				existing.ReportedAddr = sigAddr
				existing.ObservedAddr = sigAddr
				if pubKeyChanged {
					existing.Cipher = nil
					existing.CipherFailed = false
				}
				existing.State = StateDisconnected
				existing.PendingPackets = nil
				// 地址变了，旧的 DirectConn 也要清理
				if existing.DirectConn != nil {
					existing.DirectConn.Close()
					existing.DirectConn = nil
				}
			}
			existing.mu.Unlock()
		}
	}

	// 更新本地 hosts 文件
	hostMap := make(map[string]string)
	for _, p := range pt.peers {
		hostMap[p.Hostname] = p.VirtualIP
	}
	// 异步更新防止阻塞
	go utils.UpdateHostsFile(hostMap)

	return deletedPeers
}

func (pt *PeerTable) createCipher(peerPubKey []byte) *crypto.SymmetricCipher {
	if len(peerPubKey) != 32 {
		return nil
	}
	var pubKey32 [32]byte
	copy(pubKey32[:], peerPubKey)

	sharedSecret, err := crypto.ComputeSharedSecret(pt.privateKey, pubKey32)
	if err != nil {
		log.Printf("Failed to compute shared secret: %v", err)
		return nil
	}
	sessionKey := crypto.DeriveSessionKey(sharedSecret)
	cipher, err := crypto.NewSymmetricCipher(sessionKey)
	if err != nil {
		log.Printf("Failed to create symmetric cipher: %v", err)
		return nil
	}
	return cipher
}

func (pt *PeerTable) GetPeer(virtualIP string) *PeerEntry {
	pt.mu.RLock()
	p, ok := pt.peers[virtualIP]
	pt.mu.RUnlock()

	if !ok {
		return nil
	}

	p.mu.RLock()
	cipherReady := p.Cipher != nil
	cipherFailed := p.CipherFailed
	pubKey := p.PublicKey
	p.mu.RUnlock()

	// 并发安全的懒加载，计算过程不持有任何锁
	if !cipherReady && !cipherFailed {
		cipher := pt.createCipher(pubKey)
		
		p.mu.Lock()
		// Double check
		if p.Cipher == nil && !p.CipherFailed {
			if cipher != nil {
				p.Cipher = cipher
			} else {
				p.CipherFailed = true // 防止非法公钥导致无限重试
			}
		}
		p.mu.Unlock()
	}

	return p
}

// UpdateState 更新对端的连接状态
func (pt *PeerTable) UpdateState(virtualIP string, state PeerState) {
	pt.mu.RLock()
	p, ok := pt.peers[virtualIP]
	pt.mu.RUnlock()
	
	if ok {
		p.mu.Lock()
		p.State = state
		p.LastActive = time.Now()
		p.mu.Unlock()
	}
}

// ResetAllConnections 将所有节点的连接状态重置为断开
func (pt *PeerTable) ResetAllConnections() {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	for _, p := range pt.peers {
		p.mu.Lock()
		p.State = StateDisconnected
		p.mu.Unlock()
	}
}

// UpdateAddr 更新对端的公网端点
func (pt *PeerTable) UpdateAddr(virtualIP string, addr *net.UDPAddr) {
	pt.mu.RLock()
	p, ok := pt.peers[virtualIP]
	pt.mu.RUnlock()
	
	if ok {
		p.mu.Lock()
		p.ObservedAddr = addr
		p.LastActive = time.Now()
		p.mu.Unlock()
	}
}

// GetAllPeers 返回当前 PeerTable 的所有节点快照
func (pt *PeerTable) GetAllPeers() []*PeerEntry {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	peers := make([]*PeerEntry, 0, len(pt.peers))
	for _, p := range pt.peers {
		p.mu.RLock()
		peers = append(peers, &PeerEntry{
			Hostname:     p.Hostname,
			VirtualIP:    p.VirtualIP,
			ReportedAddr: p.ReportedAddr,
			ObservedAddr: p.ObservedAddr,
			PublicKey:    p.PublicKey,
			Cipher:       p.Cipher,
			State:        p.State,
			NatType:      p.NatType,
			LastActive:   p.LastActive,
		})
		p.mu.RUnlock()
	}
	return peers
}

// ResolveVirtualIP 根据目标主机名或虚拟IP，返回对应的虚拟IP
func (pt *PeerTable) ResolveVirtualIP(target string) string {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	if _, ok := pt.peers[target]; ok {
		return target
	}

	for virtualIP, p := range pt.peers {
		p.mu.RLock()
		hostname := p.Hostname
		p.mu.RUnlock()
		
		if hostname == target {
			return virtualIP
		}
	}
	return ""
}

// EnqueuePacket 将打洞期间未能发送的数据包进行深拷贝并暂存
func (pt *PeerTable) EnqueuePacket(virtualIP string, buffer []byte, payloadLen int) {
	pt.mu.RLock()
	p, ok := pt.peers[virtualIP]
	pt.mu.RUnlock()

	if !ok {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// 限制最大积压包数，防止内存泄漏或被恶意流量打爆
	if len(p.PendingPackets) > 100 {
		return
	}

	// 必须做深拷贝，因为上层调用者（Data Pump）会复用 buffer
	copiedBuf := make([]byte, len(buffer))
	copy(copiedBuf, buffer)

	p.PendingPackets = append(p.PendingPackets, PendingPacket{
		Buffer:     copiedBuf,
		PayloadLen: payloadLen,
	})
}

// FlushPendingPackets 获取所有暂存的数据包并清空队列
func (pt *PeerTable) FlushPendingPackets(virtualIP string) []PendingPacket {
	pt.mu.RLock()
	p, ok := pt.peers[virtualIP]
	pt.mu.RUnlock()

	if !ok {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.PendingPackets) == 0 {
		return nil
	}

	packets := p.PendingPackets
	p.PendingPackets = nil // 清空队列

	return packets
}

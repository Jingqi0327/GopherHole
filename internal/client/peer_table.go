package client

import (
	"bytes"
	"log"
	"net"
	"sync"
	"time"

	"github.com/Jingqi0327/GopherHole/pkg/crypto"
	"github.com/Jingqi0327/GopherHole/proto/pb"
)

type PeerState int

const (
	StateDisconnected PeerState = iota
	StatePunching
	StateConnected
)

type PeerConnection struct {
	Hostname      string
	VirtualIP     string
	SignalingAddr *net.UDPAddr // Server 下发的参考地址（用于感知节点重启或网络切换）
	PublicAddr    *net.UDPAddr // 实际打通的公网地址（NAT 穿透后的真实端点）
	PublicKey     []byte       // Client端的公钥,用于p2p隧道的加密
	Cipher        *crypto.SymmetricCipher
	State         PeerState
	LastActive    time.Time
}

type PeerTable struct {
	mu         sync.RWMutex
	peers      map[string]*PeerConnection
	privateKey [32]byte
}

func (pt *PeerTable) createCipher(peerPubKey []byte) *crypto.SymmetricCipher {
	if len(peerPubKey) != 32 {
		return nil
	}
	var pubKey32 [32]byte
	copy(pubKey32[:], peerPubKey)
	
	sharedSecret, err := crypto.ComputeSharedSecret(pt.privateKey, pubKey32)
	if err != nil {
		log.Printf("⚠️ Failed to compute shared secret: %v", err)
		return nil
	}
	sessionKey := crypto.DeriveSessionKey(sharedSecret)
	cipher, err := crypto.NewSymmetricCipher(sessionKey)
	if err != nil {
		log.Printf("⚠️ Failed to create symmetric cipher: %v", err)
		return nil
	}
	return cipher
}

func NewPeerTable(privateKey [32]byte) *PeerTable {
	return &PeerTable{
		peers:      make(map[string]*PeerConnection),
		privateKey: privateKey,
	}
}

// SyncPeers 全量同步 Server 下发的在线节点列表
func (pt *PeerTable) SyncPeers(onlinePeers []*pb.RemotePeer) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	onlineMap := make(map[string]*pb.RemotePeer)
	for _, p := range onlinePeers {
		onlineMap[p.VirtualIp] = p
	}

	// 1. 踢掉已经下线的节点
	for virtualIP := range pt.peers {
		if _, ok := onlineMap[virtualIP]; !ok {
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
			pt.peers[virtualIP] = &PeerConnection{
				Hostname:      p.Hostname,
				VirtualIP:     virtualIP,
				SignalingAddr: sigAddr,
				PublicAddr:    sigAddr, // 初始时，将信令地址作为预测的打洞地址
				PublicKey:     p.PublicKey,
				Cipher:        nil, // 懒加载，在首次通信时通过 GetPeer 触发生成
				State:         StateDisconnected,
			}
		} else { // TODO: 重新处理下PeerConnection的逻辑================
			// 检查公钥是否变更 (意味着对方重启了进程)
			if !bytes.Equal(existing.PublicKey, p.PublicKey) {
				existing.PublicKey = p.PublicKey
				existing.Cipher = nil // 懒加载
				existing.State = StateDisconnected
			}
			// 节点仍在运行，但如果信令服务器下发的端点发生了变化（IP变了或者绑定的本地UDP端口变了）
			// 这意味着对方客户端重启了，或者网络环境切换了。之前的打洞状态完全失效！
			if existing.SignalingAddr.String() != sigAddr.String() {
				existing.Hostname = p.Hostname
				existing.SignalingAddr = sigAddr
				existing.PublicAddr = sigAddr // 重置预测地址
				existing.State = StateDisconnected
			}
		}
	}
}

func (pt *PeerTable) GetPeer(virtualIP string) *PeerConnection {
	pt.mu.RLock()
	p, ok := pt.peers[virtualIP]
	if !ok {
		pt.mu.RUnlock()
		return nil
	}
	
	// 如果 Cipher 已经就绪，直接拷贝返回
	if p.Cipher != nil {
		copyPeer := &PeerConnection{
			Hostname:      p.Hostname,
			VirtualIP:     p.VirtualIP,
			SignalingAddr: p.SignalingAddr,
			PublicAddr:    p.PublicAddr,
			PublicKey:     p.PublicKey,
			Cipher:        p.Cipher,
			State:         p.State,
			LastActive:    p.LastActive,
		}
		pt.mu.RUnlock()
		return copyPeer
	}
	pt.mu.RUnlock()

	// Cipher 为空，升级为写锁进行懒加载初始化
	pt.mu.Lock()
	defer pt.mu.Unlock()
	
	// Double-check under write lock
	p, ok = pt.peers[virtualIP]
	if !ok {
		return nil
	}
	if p.Cipher == nil {
		p.Cipher = pt.createCipher(p.PublicKey)
	}

	return &PeerConnection{
		Hostname:      p.Hostname,
		VirtualIP:     p.VirtualIP,
		SignalingAddr: p.SignalingAddr,
		PublicAddr:    p.PublicAddr,
		PublicKey:     p.PublicKey,
		Cipher:        p.Cipher,
		State:         p.State,
		LastActive:    p.LastActive,
	}
}

func (pt *PeerTable) UpdateState(virtualIP string, state PeerState) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if p, ok := pt.peers[virtualIP]; ok {
		p.State = state
		p.LastActive = time.Now()
	}
}

// UpdateAddr updates the UDP address learned from actual UDP packets (NAT behavior)
func (pt *PeerTable) UpdateAddr(virtualIP string, addr *net.UDPAddr) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if p, ok := pt.peers[virtualIP]; ok {
		p.PublicAddr = addr
		p.LastActive = time.Now()
	}
}

// GetAllPeers 返回当前 PeerTable 的所有节点快照
func (pt *PeerTable) GetAllPeers() []*PeerConnection {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	peers := make([]*PeerConnection, 0, len(pt.peers))
	for _, p := range pt.peers {
		peers = append(peers, &PeerConnection{
			Hostname:      p.Hostname,
			VirtualIP:     p.VirtualIP,
			SignalingAddr: p.SignalingAddr,
			PublicAddr:    p.PublicAddr,
			PublicKey:     p.PublicKey,
			Cipher:        p.Cipher,
			State:         p.State,
			LastActive:    p.LastActive,
		})
	}
	return peers
}

// ResolveVirtualIP tries to find the VirtualIP by the given target string.
// If target is already a VirtualIP in the table, it returns it.
// If target matches a Hostname in the table, it returns the corresponding VirtualIP.
func (pt *PeerTable) ResolveVirtualIP(target string) string {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	if _, ok := pt.peers[target]; ok {
		return target
	}

	for virtualIP, p := range pt.peers {
		if p.Hostname == target {
			return virtualIP
		}
	}
	return ""
}

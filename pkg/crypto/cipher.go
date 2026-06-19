package crypto

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// GenerateCurve25519Keypair 生成临时的 Curve25519 密钥对，用于数据面的 ECDH 协商。
func GenerateCurve25519Keypair() (privateKey [32]byte, publicKey [32]byte, err error) {
	_, err = io.ReadFull(rand.Reader, privateKey[:])
	if err != nil {
		return [32]byte{}, [32]byte{}, err
	}
	// 根据 RFC7748 要求，对私钥进行 bit clamping
	privateKey[0] &= 248
	privateKey[31] &= 127
	privateKey[31] |= 64

	curve25519.ScalarBaseMult(&publicKey, &privateKey)
	return privateKey, publicKey, nil
}

// ComputeSharedSecret 根据我方的私钥和对方的公钥进行 ECDH 协商，计算出共享密钥。
func ComputeSharedSecret(privateKey [32]byte, peerPublicKey [32]byte) ([]byte, error) {
	return curve25519.X25519(privateKey[:], peerPublicKey[:])
}

// DeriveSessionKey 利用共享密钥，通过 HKDF-SHA256 派生出会话对称密钥 (32 Bytes)。
func DeriveSessionKey(sharedSecret []byte) []byte {
	hkdfHash := hkdf.New(sha256.New, sharedSecret, nil, []byte("GopherHole-P2P-Tunnel"))
	sessionKey := make([]byte, chacha20poly1305.KeySize) // 32 bytes
	_, err := io.ReadFull(hkdfHash, sessionKey)
	if err != nil {
		panic(fmt.Sprintf("hkdf read failed: %v", err))
	}
	return sessionKey
}

// SymmetricCipher P2P 数据面加密引擎，维护 AEAD 状态、发送计数器以及接收防重放滑动窗口。
type SymmetricCipher struct {
	aead       cipher.AEAD
	txNonce    atomic.Uint64
	rxMu       sync.Mutex
	maxRxNonce uint64
	rxBitmap   uint64 // 64位滑动窗口，位 n 表示 (maxRxNonce - n) 是否已收到
}

// NewSymmetricCipher 根据派生出的会话密钥初始化加密引擎。
func NewSymmetricCipher(sessionKey []byte) (*SymmetricCipher, error) {
	if len(sessionKey) != chacha20poly1305.KeySize {
		return nil, errors.New("invalid session key size")
	}
	aead, err := chacha20poly1305.New(sessionKey)
	if err != nil {
		return nil, err
	}
	return &SymmetricCipher{
		aead: aead,
	}, nil
}

// Encrypt 对明文进行 ChaCha20-Poly1305 加密，返回格式: [Nonce (8 bytes)] + [Encrypted Payload + Tag (16 bytes)]
func (c *SymmetricCipher) Encrypt(plaintext []byte) []byte {
	// 发送端 Nonce 自增 (保证绝对不会重复使用相同的 Nonce)
	nonceVal := c.txNonce.Add(1)

	// 构造 12 字节 nonce (ChaCha20Poly1305 标准 nonce 长度)
	nonce12 := make([]byte, c.aead.NonceSize())
	binary.LittleEndian.PutUint64(nonce12[0:8], nonceVal)

	// AEAD 加密，直接附着认证标签
	ciphertext := c.aead.Seal(nil, nonce12, plaintext, nil)

	// 拼装最终的数据包 Header (8 byte nonce) + Ciphertext
	out := make([]byte, 8+len(ciphertext))
	binary.LittleEndian.PutUint64(out[0:8], nonceVal)
	copy(out[8:], ciphertext)

	return out
}

// Decrypt 检查重放攻击、剥离 Nonce 并解密密文，返回真实载荷。
func (c *SymmetricCipher) Decrypt(data []byte) ([]byte, error) {
	if len(data) < 8+c.aead.Overhead() {
		return nil, errors.New("ciphertext too short")
	}

	nonceVal := binary.LittleEndian.Uint64(data[0:8])

	// 1. 防重放攻击检查 (仅检查，不更新状态)
	if !c.checkRx(nonceVal) {
		return nil, fmt.Errorf("packet dropped: replayed or too old (nonce: %d)", nonceVal)
	}

	// 2. 构造 12 字节 nonce 用于解密
	nonce12 := make([]byte, c.aead.NonceSize())
	binary.LittleEndian.PutUint64(nonce12[0:8], nonceVal)

	// 3. 验证并解密
	plaintext, err := c.aead.Open(nil, nonce12, data[8:], nil)
	if err != nil {
		return nil, fmt.Errorf("aead open failed: %w", err)
	}

	// 4. 解密和认证成功后，正式更新滑动窗口状态
	c.markRx(nonceVal)

	return plaintext, nil
}

// checkRx 仅判断 Nonce 是否合法 (不修改状态)
func (c *SymmetricCipher) checkRx(nonce uint64) bool {
	c.rxMu.Lock()
	defer c.rxMu.Unlock()

	if nonce == 0 {
		return false // Nonce 从 1 开始
	}

	if nonce > c.maxRxNonce {
		return true
	}

	diff := c.maxRxNonce - nonce
	if diff >= 64 {
		return false // 丢弃：包太老，已经滑出窗口
	}

	if (c.rxBitmap & (1 << diff)) != 0 {
		return false // 丢弃：收到过了
	}

	return true
}

// markRx 确认接收，更新滑动窗口状态
func (c *SymmetricCipher) markRx(nonce uint64) {
	c.rxMu.Lock()
	defer c.rxMu.Unlock()

	if nonce > c.maxRxNonce {
		diff := nonce - c.maxRxNonce
		if diff >= 64 {
			c.rxBitmap = 1
		} else {
			c.rxBitmap <<= diff
			c.rxBitmap |= 1
		}
		c.maxRxNonce = nonce
		return
	}

	diff := c.maxRxNonce - nonce
	if diff < 64 {
		c.rxBitmap |= (1 << diff)
	}
}

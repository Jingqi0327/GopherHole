package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"os"
	"time"
)

// EnsureServerKeypair 检查配置中的私钥，缺失则生成新的密钥对并尝试写入当前目录的 .env 中。
// 若配置中同时存在公私钥，则进行一致性校验，不一致则触发致命错误。
func EnsureServerKeypair(privB64Config, pubB64Config string) (privB64, pubB64 string, err error) {
	if privB64Config == "" {
		// 生成新的密钥对
		pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return "", "", fmt.Errorf("failed to generate ed25519 keypair: %w", err)
		}

		// WireGuard style: Base64 encoded 32-byte keys.
		// For private key, we only save the 32-byte seed.
		privB64 = base64.StdEncoding.EncodeToString(privKey.Seed())
		pubB64 = base64.StdEncoding.EncodeToString(pubKey)

		// 尝试追加写入到 .env 文件
		envContent := fmt.Sprintf("\nPRIVATE_KEY=%s\nPUBLIC_KEY=%s\n", privB64, pubB64)

		f, openErr := os.OpenFile(".env", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if openErr == nil {
			defer f.Close()
			if _, writeErr := f.WriteString(envContent); writeErr != nil {
				fmt.Printf("Warning: Failed to write to .env: %v\n", writeErr)
			}
		} else {
			fmt.Printf("Warning: Failed to open .env for writing: %v\n", openErr)
		}

		fmt.Println("==========================================================================================")
		fmt.Println("⚠ No Server Private Key detected. A new Ed25519 keypair has been generated.")
		fmt.Println("⚠ We attempted to save it to the .env file in the current directory.")
		fmt.Println("⚠ If you are using Docker or environment variables, PLEASE SAVE THESE KEYS to avoid loss:")
		fmt.Printf("PRIVATE_KEY=%s\n", privB64)
		fmt.Printf("PUBLIC_KEY=%s\n", pubB64)
		fmt.Println("==========================================================================================")

		return privB64, pubB64, nil
	}

	// 配置中有私钥
	privSeed, err := base64.StdEncoding.DecodeString(privB64Config)
	if err != nil {
		return "", "", fmt.Errorf("invalid base64 format for PRIVATE_KEY: %w", err)
	}
	if len(privSeed) != ed25519.SeedSize {
		return "", "", fmt.Errorf("invalid PRIVATE_KEY size: expected %d bytes (seed), got %d", ed25519.SeedSize, len(privSeed))
	}

	// Reconstruct the full private key from the seed
	privKey := ed25519.NewKeyFromSeed(privSeed)
	derivedPubKey := privKey.Public().(ed25519.PublicKey)
	derivedPubB64 := base64.StdEncoding.EncodeToString(derivedPubKey)

	// 如果配置中也提供了公钥，则进行交叉校验
	if pubB64Config != "" {
		if pubB64Config != derivedPubB64 {
			return "", "", fmt.Errorf("FATAL: PUBLIC_KEY in config does not match the derived public key from PRIVATE_KEY")
		}
	}

	return privB64Config, derivedPubB64, nil
}

// GenerateMemTLSCertFromKey 利用传入的私钥，在内存中动态生成一张自签名 TLS 证书。
func GenerateMemTLSCertFromKey(b64PrivateKey string) (tls.Certificate, string, error) {
	privSeed, err := base64.StdEncoding.DecodeString(b64PrivateKey)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("invalid base64 private key: %w", err)
	}
	if len(privSeed) != ed25519.SeedSize {
		return tls.Certificate{}, "", fmt.Errorf("invalid ed25519 seed size")
	}

	privKey := ed25519.NewKeyFromSeed(privSeed)
	pubKey := privKey.Public().(ed25519.PublicKey)
	pubB64 := base64.StdEncoding.EncodeToString(pubKey)

	// 创建自签名证书的模板
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"GopherHole Node"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(100 * 365 * 24 * time.Hour), // 100 年有效期
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// 签发证书 (自签名)
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, pubKey, privKey)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to create certificate: %w", err)
	}

	// 构建 tls.Certificate
	cert := tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  privKey,
		Leaf:        nil, // 可选，如果在握手后需要解析的话可以放这里，一般会自动解析
	}

	return cert, pubB64, nil
}

// VerifyPeerPublicKey 供客户端使用的 TLS 自定义校验回调，用于精准比对公钥，拦截不匹配的连接。
func VerifyPeerPublicKey(expectedPubKeyB64 string) func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("no certificates provided by peer")
		}

		// 解析对方发来的叶子证书
		cert, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("failed to parse peer certificate: %w", err)
		}

		// 提取证书中的公钥并判断是否为 ed25519
		edPubKey, ok := cert.PublicKey.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("peer certificate public key is not ed25519")
		}

		// 将提取到的公钥编码为 Base64
		actualPubKeyB64 := base64.StdEncoding.EncodeToString(edPubKey)

		// 强校验公钥指纹 (Base64 是大小写敏感的)
		if actualPubKeyB64 != expectedPubKeyB64 {
			return fmt.Errorf("public key mismatch! expected: %s, got: %s", expectedPubKeyB64, actualPubKeyB64)
		}

		return nil
	}
}

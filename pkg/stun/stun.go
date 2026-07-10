package stun

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// NATDetectionResult 存放 NAT 探测结果
type NATDetectionResult struct {
	PublicIP    string
	PublicPort  int
	ActiveStun  string
	Endpoints   map[string]string
	IsSymmetric bool
}

// DetectNAT 对指定的多个 STUN 服务器进行探测，返回公网地址信息并判断是否为对称型 NAT
func DetectNAT(conn *net.UDPConn, stunServers []string) (*NATDetectionResult, error) {
	var pubIP string
	var pubPort int
	var activeStun string
	endpoints := make(map[string]string)

	for _, server := range stunServers {
		ip, port, err := DiscoverPublicEndpoint(conn, server)
		if err == nil {
			if activeStun == "" {
				pubIP = ip
				pubPort = port
				activeStun = server
			}
			endpoint := fmt.Sprintf("%s:%d", ip, port)
			endpoints[endpoint] = server
		}
	}

	if len(endpoints) == 0 {
		return nil, fmt.Errorf("all STUN servers failed")
	}

	isSymmetric := len(endpoints) > 1

	return &NATDetectionResult{
		PublicIP:    pubIP,
		PublicPort:  pubPort,
		ActiveStun:  activeStun,
		Endpoints:   endpoints,
		IsSymmetric: isSymmetric,
	}, nil
}

// DiscoverPublicEndpoint 向指定的 STUN 服务器发送请求，获取自身的公网 IP 和端口
// 为了保持项目轻量化，这里手写了一个极简的 STUN 协议客户端，不引入外部依赖
func DiscoverPublicEndpoint(conn *net.UDPConn, stunServer string) (string, int, error) {
	addr, err := net.ResolveUDPAddr("udp", stunServer)
	if err != nil {
		return "", 0, err
	}

	// 构造 STUN Binding Request (20 字节头部)
	req := make([]byte, 20)
	binary.BigEndian.PutUint16(req[0:2], 0x0001)     // Message Type: Binding Request
	binary.BigEndian.PutUint16(req[2:4], 0x0000)     // Message Length: 0
	binary.BigEndian.PutUint32(req[4:8], 0x2112A442) // Magic Cookie (固定值)
	rand.Read(req[8:20])                             // Transaction ID (12字节随机数)

	// 设置超时，避免因为网络问题永远阻塞
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	defer conn.SetReadDeadline(time.Time{}) // 恢复默认，不阻塞后续流程

	_, err = conn.WriteToUDP(req, addr)
	if err != nil {
		return "", 0, err
	}

	resp := make([]byte, 1024)
	n, _, err := conn.ReadFromUDP(resp)
	if err != nil {
		return "", 0, err
	}

	if n < 20 {
		return "", 0, fmt.Errorf("response too short")
	}

	return ParseSTUNResponse(resp[:n])
}

// ParseSTUNResponse 解析 STUN 响应，提取公网 IP 和端口
func ParseSTUNResponse(resp []byte) (string, int, error) {
	n := len(resp)
	if n < 20 {
		return "", 0, fmt.Errorf("response too short")
	}

	// 验证 Message Type: Binding Response (0x0101)
	if binary.BigEndian.Uint16(resp[0:2]) != 0x0101 {
		return "", 0, fmt.Errorf("invalid response type")
	}

	// STUN Magic Cookie
	magicCookie := binary.BigEndian.Uint32(resp[4:8])
	if magicCookie != 0x2112A442 {
		return "", 0, fmt.Errorf("invalid magic cookie")
	}

	// 解析 Attribute
	offset := 20
	var publicIP string
	var publicPort int

	for offset < n {
		if offset+4 > n {
			break
		}
		attrType := binary.BigEndian.Uint16(resp[offset : offset+2])
		attrLen := int(binary.BigEndian.Uint16(resp[offset+2 : offset+4]))
		offset += 4

		if offset+attrLen > n {
			break
		}

		// 处理 XOR-MAPPED-ADDRESS (0x0020) 或 MAPPED-ADDRESS (0x0001)
		if attrType == 0x0020 {
			// XOR-MAPPED-ADDRESS
			family := resp[offset+1]
			if family == 0x01 { // IPv4
				port := binary.BigEndian.Uint16(resp[offset+2 : offset+4])
				publicPort = int(port ^ 0x2112)
				ip := make(net.IP, 4)
				ipData := binary.BigEndian.Uint32(resp[offset+4 : offset+8])
				binary.BigEndian.PutUint32(ip, ipData^magicCookie)
				publicIP = ip.String()
				return publicIP, publicPort, nil
			}
		} else if attrType == 0x0001 {
			// MAPPED-ADDRESS
			family := resp[offset+1]
			if family == 0x01 { // IPv4
				publicPort = int(binary.BigEndian.Uint16(resp[offset+2 : offset+4]))
				ip := make(net.IP, 4)
				copy(ip, resp[offset+4:offset+8])
				publicIP = ip.String()
				return publicIP, publicPort, nil
			}
		}

		offset += attrLen
	}

	return "", 0, fmt.Errorf("public endpoint not found in STUN response")
}

// HandleSTUNRequest 解析并响应标准的 STUN Binding Request
// 这使得 GopherHole Server 可以直接作为 STUN 服务器供客户端探测 NAT 类型
func HandleSTUNRequest(conn *net.UDPConn, addr *net.UDPAddr, req []byte) {
// 1. 基本格式校验
if len(req) < 20 {
return
}

// 验证 Message Type: Binding Request (0x0001)
if binary.BigEndian.Uint16(req[0:2]) != 0x0001 {
return
}

// 验证 Magic Cookie (0x2112A442)
magicCookie := binary.BigEndian.Uint32(req[4:8])
if magicCookie != 0x2112A442 {
return
}

// 提取 Transaction ID (12 bytes)
transactionID := req[8:20]

// 仅支持 IPv4 (考虑到 GopherHole 客户端目前只支持 IPv4)
ip4 := addr.IP.To4()
if ip4 == nil {
return
}

// 2. 构造 STUN Binding Response (0x0101)
// 头20字节 + XOR-MAPPED-ADDRESS 属性 (4字节头 + 8字节值) = 32字节
resp := make([]byte, 32)

// Message Type: Binding Response (0x0101)
binary.BigEndian.PutUint16(resp[0:2], 0x0101)
// Message Length: 属性的总长度 (12 bytes)
binary.BigEndian.PutUint16(resp[2:4], 12)
// Magic Cookie
binary.BigEndian.PutUint32(resp[4:8], magicCookie)
// Transaction ID
copy(resp[8:20], transactionID)

// 3. 添加 XOR-MAPPED-ADDRESS (0x0020) 属性
binary.BigEndian.PutUint16(resp[20:22], 0x0020) // Attribute Type: XOR-MAPPED-ADDRESS
binary.BigEndian.PutUint16(resp[22:24], 8)      // Attribute Length: 8 bytes
resp[24] = 0x00                                  // Reserved
resp[25] = 0x01                                  // Family: IPv4

// 端口进行 XOR (XOR 魔法常数的高 16 位 0x2112)
xorPort := uint16(addr.Port) ^ uint16(magicCookie>>16)
binary.BigEndian.PutUint16(resp[26:28], xorPort)

// IP 进行 XOR (XOR 魔法常数 0x2112A442)
ipUint32 := binary.BigEndian.Uint32(ip4)
xorIP := ipUint32 ^ magicCookie
binary.BigEndian.PutUint32(resp[28:32], xorIP)

// 发送响应
_, _ = conn.WriteToUDP(resp, addr)
}

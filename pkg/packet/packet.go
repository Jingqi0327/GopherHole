package packet

import (
	"fmt"
	"net"
)

const (
	TypeData    byte = 0x01
	TypeControl byte = 0x02
)

const HeaderLen = 5

// Parse 从原始的 UDP 报文字节中提取出包类型、源虚拟 IP 以及加密载荷。
// 如果报文长度太短，则返回错误。
func Parse(buf []byte) (packetType byte, srcIP string, payload []byte, err error) {
	if len(buf) < 5 {
		return 0, "", nil, fmt.Errorf("packet too short")
	}
	packetType = buf[0]
	srcIP = net.IPv4(buf[1], buf[2], buf[3], buf[4]).String()
	payload = buf[5:]
	return
}

// Build 构造一个准备发送的完整 UDP 报文。
// 它会将包类型和发送方的虚拟 IP 追加到载荷的最前面。
func Build(packetType byte, srcIP string, payload []byte) []byte {
	ipBytes := net.ParseIP(srcIP).To4()
	if ipBytes == nil {
		return nil
	}
	buf := make([]byte, 1+4+len(payload))
	buf[0] = packetType
	copy(buf[1:5], ipBytes)
	copy(buf[5:], payload)
	return buf
}

// InjectHeader 原地向 buffer[0:5] 注入协议头（包类型和源虚拟 IP）。
// buffer 的长度必须大于等于 HeaderLen (5)。
func InjectHeader(buffer []byte, packetType byte, srcIP string) {
	if len(buffer) < HeaderLen {
		return
	}
	ipBytes := net.ParseIP(srcIP).To4()
	if ipBytes == nil {
		return
	}
	buffer[0] = packetType
	copy(buffer[1:5], ipBytes)
}

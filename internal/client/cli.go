package client

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Jingqi0327/GopherHole/pkg/terminal"
	"github.com/Jingqi0327/GopherHole/proto/pb"
)



func (node *Node) handleTerminalInput() {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		// fmt.Print("> ") // 省略 prompt 以免干扰日志
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}

		parts := strings.SplitN(text, " ", 3)
		cmd := strings.ToLower(parts[0])

		switch cmd {
		case "punch":
			if len(parts) < 2 {
				fmt.Println("Usage: punch <virtual_ip_or_hostname>")
				continue
			}
			target := node.peerTable.ResolveVirtualIP(parts[1])
			if target == "" {
				target = parts[1] // fallback
			}
			node.udpEngine.Punch(target)
		case "msg":
			if len(parts) < 3 {
				fmt.Println("Usage: msg <virtual_ip_or_hostname> <text>")
				continue
			}
			target := node.peerTable.ResolveVirtualIP(parts[1])
			if target == "" {
				target = parts[1]
			}
			node.udpEngine.SendMessage(target, parts[2])
		case "list":
			localPeers := node.peerTable.GetAllPeers()
			var displayPeers []peerDisplayInfo
			for _, p := range localPeers {
				info := peerDisplayInfo{
					Hostname:   p.Hostname,
					VirtualIP:  p.VirtualIP,
					PublicAddr: p.PublicAddr.String(),
				}
				if p.VirtualIP == node.virtualIP {
					info.StateMsg = fmt.Sprintf("%s<This Node>%s", terminal.ColorCyan, terminal.ColorReset)
				} else {
					info.StateMsg = formatState(p.State)
				}
				displayPeers = append(displayPeers, info)
			}
			printPeerTable(displayPeers)
		default:
			fmt.Println("Unknown command. Supported: punch, msg, list")
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Terminal input error: %v", err)
	}
}

type peerDisplayInfo struct {
	Hostname   string
	VirtualIP  string
	PublicAddr string
	StateMsg   string
}

func printPeerTable(peers []peerDisplayInfo) {
	fmt.Printf("\n%s============================================= ONLINE PEERS =============================================%s\n", terminal.ColorYellow, terminal.ColorReset)

	// 初始化 tabwriter：按制表符 '\t' 自动对齐列，padding 设为 3 个空格
	// 注意：由于 ANSI 颜色代码的存在，tabwriter 计算宽度会把颜色代码的长度也算进去。
	// 但只要同一列的每一行写入的颜色代码长度完全一致，相对对齐就不会遭到破坏！
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

	for _, p := range peers {
		fmt.Fprintf(w, "  %s%s%s\t| %sVirtual IP:%s %s\t| %sPublic IP:%s %s\t| %s\n",
			terminal.ColorCyan, p.Hostname, terminal.ColorReset,
			terminal.ColorCyan, terminal.ColorReset, p.VirtualIP,
			terminal.ColorGreen, terminal.ColorReset, p.PublicAddr,
			p.StateMsg,
		)
	}
	w.Flush() // 触发排版输出

	fmt.Printf("%s=========================================================================================================%s\n", terminal.ColorYellow, terminal.ColorReset)
}

// 被 Server 心跳推送触发
func (node *Node) printPeers(remotePeers []*pb.RemotePeer) {
	var displayPeers []peerDisplayInfo

	for _, p := range remotePeers {
		info := peerDisplayInfo{
			Hostname:   p.Hostname,
			VirtualIP:  p.VirtualIp,
			PublicAddr: fmt.Sprintf("%s:%d", p.PublicIp, p.PublicPort),
		}

		if p.VirtualIp == node.virtualIP {
			info.StateMsg = fmt.Sprintf("%s<This Node>%s", terminal.ColorCyan, terminal.ColorReset)
		} else {
			info.StateMsg = fmt.Sprintf("%s[Disconnected]%s", terminal.ColorRed, terminal.ColorReset)
			if localPeer := node.peerTable.GetPeer(p.VirtualIp); localPeer != nil {
				info.StateMsg = formatState(localPeer.State)
			}
		}
		displayPeers = append(displayPeers, info)
	}

	printPeerTable(displayPeers)
}

func formatState(state PeerState) string {
	switch state {
	case StatePunching:
		return fmt.Sprintf("%s[Punching...]%s", terminal.ColorYellow, terminal.ColorReset)
	case StateConnected:
		return fmt.Sprintf("%s[Connected!]%s", terminal.ColorGreen, terminal.ColorReset)
	default:
		return fmt.Sprintf("%s[Disconnected]%s", terminal.ColorRed, terminal.ColorReset)
	}
}

func (node *Node) printNodeInfo() {
	fmt.Printf("\n%s--- GopherHole Node Info ---%s\n", terminal.ColorYellow, terminal.ColorReset)
	fmt.Printf("  %sHostname%s   : %s\n", terminal.ColorCyan, terminal.ColorReset, node.hostname)
	fmt.Printf("  %sVirtual IP%s : %s\n", terminal.ColorCyan, terminal.ColorReset, node.virtualIP)
	fmt.Printf("  %sPublic IP%s  : %s\n", terminal.ColorGreen, terminal.ColorReset, node.publicIP)
	fmt.Printf("%s----------------------------%s\n\n", terminal.ColorYellow, terminal.ColorReset)
}




package client

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Jingqi0327/GopherHole/proto/pb"
)

func (a *Node) handleTerminalInput() {
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
			target := a.peerTable.ResolveVirtualIP(parts[1])
			if target == "" {
				target = parts[1] // fallback
			}
			a.udpEngine.Punch(target)
		case "msg":
			if len(parts) < 3 {
				fmt.Println("Usage: msg <virtual_ip_or_hostname> <text>")
				continue
			}
			target := a.peerTable.ResolveVirtualIP(parts[1])
			if target == "" {
				target = parts[1]
			}
			a.udpEngine.SendMessage(target, parts[2])
		case "list":
			a.listPeers()
		default:
			fmt.Println("Unknown command. Supported: punch, msg, list")
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Terminal input error: %v", err)
	}
}

// printPeers 在终端格式化打印当前所有的在线节点
func (a *Node) printPeers(peers []*pb.RemotePeer) {
	fmt.Println("\n============================================ 🟢 ONLINE PEERS ============================================")
	for _, p := range peers {
		marker := ""
		if p.VirtualIp == a.virtualIP {
			marker = "👈 (This Node)"
		} else {
			state := "Disconnected"
			if peer := a.peerTable.GetPeer(p.VirtualIp); peer != nil {
				switch peer.State {
				case StatePunching:
					state = "Punching..."
				case StateConnected:
					state = "Connected!"
				}
			}
			marker = fmt.Sprintf("[%s]", state)
		}
		publicAddr := fmt.Sprintf("%s:%d", p.PublicIp, p.PublicPort)
		fmt.Printf(" - | %-15s | Virtual IP: %-15s | Public IP: %-20s %s\n", p.Hostname, p.VirtualIp, publicAddr, marker)
	}
	fmt.Println("=========================================================================================================")
}

func (a *Node) listPeers() {
	peers := a.peerTable.GetAllPeers()

	fmt.Println("\n============================================ 🟢 ONLINE PEERS ============================================")
	for _, p := range peers {
		marker := ""
		if p.VirtualIP == a.virtualIP {
			marker = "👈 (This Node)"
		} else {
			state := "Disconnected"
			if peer := a.peerTable.GetPeer(p.VirtualIP); peer != nil {
				switch peer.State {
				case StatePunching:
					state = "Punching..."
				case StateConnected:
					state = "Connected!"
				}
			}
			marker = fmt.Sprintf("[%s]", state)
		}
		fmt.Printf(" - | %-15s | Virtual IP: %-15s | Public IP: %-20s %s\n", p.Hostname, p.VirtualIP, p.PublicAddr.String(), marker)
	}
	fmt.Println("=========================================================================================================")
}

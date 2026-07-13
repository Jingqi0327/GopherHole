package utils

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
)

const (
	hostsBlockStart = "# --- GOPHERHOLE PEERS START ---"
	hostsBlockEnd   = "# --- GOPHERHOLE PEERS END ---"
)

func getHostsFilePath() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}

// UpdateHostsFile 接收一个映射为 hostname->virtualIP 的 map。
// 读取 hosts 文件，移除旧的 GopherHole 块（如果存在），并追加新的块
func UpdateHostsFile(hostMap map[string]string) {
	hostsPath := getHostsFilePath()
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		log.Printf("Failed to read %s: %v", hostsPath, err)
		return
	}

	var newContent bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(content))
	inBlock := false

	// Copy everything except our block
	for scanner.Scan() {
		line := scanner.Text()
		if line == hostsBlockStart {
			inBlock = true
			continue
		}
		if line == hostsBlockEnd {
			inBlock = false
			continue
		}
		if !inBlock {
			newContent.WriteString(line)
			newContent.WriteString("\n")
		}
	}

	// Remove trailing newline if it exists to keep things clean
	cleaned := bytes.TrimRight(newContent.Bytes(), "\n")
	newContent.Reset()
	newContent.Write(cleaned)
	newContent.WriteString("\n")

	// If we have peers, append the new block
	if len(hostMap) > 0 {
		newContent.WriteString(hostsBlockStart + "\n")
		for hostname, virtualIP := range hostMap {
			// Write both .local and exact hostname
			lineLocal := fmt.Sprintf("%s\t%s.local\n", virtualIP, strings.ToLower(hostname))
			lineExact := fmt.Sprintf("%s\t%s\n", virtualIP, hostname)
			newContent.WriteString(lineLocal)
			if strings.ToLower(hostname)+".local" != strings.ToLower(hostname) {
				newContent.WriteString(lineExact)
			}
		}
		newContent.WriteString(hostsBlockEnd + "\n")
	}

	// Write back to the file
	err = os.WriteFile(hostsPath, newContent.Bytes(), 0644)
	if err != nil {
		log.Printf("Failed to write to %s: %v. Please ensure you are running as Administrator/root.", hostsPath, err)
	} else {
		log.Printf("Local hosts file updated successfully.")
	}
}

// CleanHostsFile 清除 hosts 文件中的 GopherHole 块
func CleanHostsFile() {
	UpdateHostsFile(nil)
}

package client

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

// UpdateHostsFile reads the hosts file, removes the old GopherHole block if it exists,
// and appends a new block with the current peers.
func UpdateHostsFile(peers []*PeerConnection) {
	hostsPath := getHostsFilePath()
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		log.Printf("⚠️ Failed to read %s: %v", hostsPath, err)
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
	if len(peers) > 0 {
		newContent.WriteString(hostsBlockStart + "\n")
		for _, p := range peers {
			// Write both .local and exact hostname
			lineLocal := fmt.Sprintf("%s\t%s.local\n", p.VirtualIP, strings.ToLower(p.Hostname))
			lineExact := fmt.Sprintf("%s\t%s\n", p.VirtualIP, p.Hostname)
			newContent.WriteString(lineLocal)
			if strings.ToLower(p.Hostname)+".local" != strings.ToLower(p.Hostname) {
				newContent.WriteString(lineExact)
			}
		}
		newContent.WriteString(hostsBlockEnd + "\n")
	}

	// Write back to the file
	err = os.WriteFile(hostsPath, newContent.Bytes(), 0644)
	if err != nil {
		log.Printf("⚠️ Failed to write to %s: %v. Please ensure you are running as Administrator/root.", hostsPath, err)
	} else {
		log.Printf("✅ Local hosts file updated successfully.")
	}
}

// CleanHostsFile removes the GopherHole block from the hosts file.
func CleanHostsFile() {
	UpdateHostsFile(nil)
}

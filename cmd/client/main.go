package main

import (
	"log"

	"github.com/Jingqi0327/GopherHole/internal/client"
	"github.com/Jingqi0327/GopherHole/internal/config"
	"github.com/Jingqi0327/GopherHole/pkg/utils"
)

func main() {
	if !utils.IsAdmin() {
		log.Fatalf("Error: GopherHole requires Root/Administrator privileges to manage virtual network interfaces. Please run with sudo or as Administrator.")
	}

	cfg, err := config.LoadClientConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Println("GopherHole Client is starting...")

	node := client.NewNode(cfg.Server, cfg.IP)
	if err := node.Run(); err != nil {
		log.Fatalf("Client error: %v", err)
	}
}

package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

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

	node := client.NewNode(cfg)

	// Catch SIGINT and SIGTERM to clean up hosts file
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Println("\n🛑 Shutting down GopherHole client...")
		client.CleanHostsFile()
		os.Exit(0)
	}()

	// Also clean up if Run() returns normally
	defer client.CleanHostsFile()

	if err := node.Run(); err != nil {
		log.Fatalf("Client error: %v", err)
	}
}

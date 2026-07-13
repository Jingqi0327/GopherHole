package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Jingqi0327/GopherHole/internal/client"
	"github.com/Jingqi0327/GopherHole/internal/config"
	"github.com/Jingqi0327/GopherHole/pkg/terminal"
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

	node, err := client.NewNode(cfg)
	if err != nil {
		fmt.Printf("\n%sFATAL: Failed to initialize node: %v. Exiting.%s\n", terminal.ColorRed, err, terminal.ColorReset)
		os.Exit(1)
	}

	// Catch SIGINT and SIGTERM to clean up hosts file
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		fmt.Printf("\n%sShutting down GopherHole client...%s\n", terminal.ColorYellow, terminal.ColorReset)
		utils.CleanHostsFile()
		os.Exit(0)
	}()

	// Also clean up if Run() returns normally
	defer utils.CleanHostsFile()

	if err := node.Run(); err != nil {
		fmt.Printf("\n%sFATAL: %v. Exiting.%s\n", terminal.ColorRed, err, terminal.ColorReset)
		os.Exit(1)
	}
}

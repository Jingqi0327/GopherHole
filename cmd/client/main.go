package main

import (
	"log"

	"github.com/Jingqi0327/GopherHole/internal/client"
	"github.com/Jingqi0327/GopherHole/internal/config"
)

func main() {
	cfg, err := config.LoadClientConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Println("GopherHole Client is starting...")

	app := client.NewApp(cfg.Server, cfg.IP)
	if err := app.Run(); err != nil {
		log.Fatalf("Client error: %v", err)
	}
}

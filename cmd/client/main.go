package main

import (
	"flag"
	"log"

	"github.com/Jingqi0327/GopherHole/internal/client"
)

func main() {
	serverAddr := flag.String("server", "127.0.0.1:8086", "Signaling Server address")
	requestedIP := flag.String("ip", "", "Requested static Virtual IP (e.g. 10.8.0.5)")
	flag.Parse()

	log.Println("GopherHole Client is starting...")

	app := client.NewApp(*serverAddr, *requestedIP)
	if err := app.Run(); err != nil {
		log.Fatalf("Client error: %v", err)
	}
}

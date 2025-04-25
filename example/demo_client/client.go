package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/net/websocket"
)

var (
	addr = flag.String("addr", "localhost:8080", "server address")
)

func main() {
	flag.Parse()

	// Create WebSocket config
	config, err := websocket.NewConfig("ws://"+*addr+"/ws", "http://localhost")
	if err != nil {
		log.Fatal("config:", err)
	}

	// Add some headers
	config.Header.Set("User-Agent", "regulaway-client")
	config.Header.Set("Origin", "http://localhost")

	// Connect to WebSocket server
	conn, err := websocket.DialConfig(config)
	if err != nil {
		log.Fatalf("dial error: %v\nMake sure the server is running and supports WebSocket connections", err)
	}
	defer conn.Close()

	log.Printf("Connected to %s", *addr)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start reading messages
	go func() {
		for {
			var message string
			err := websocket.Message.Receive(conn, &message)
			if err != nil {
				log.Println("read error:", err)
				return
			}
			log.Printf("received: %s", message)
		}
	}()

	// Send time every second
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ticker.C:
				currentTime := time.Now().Format("2006-01-02 15:04:05")
				if err := websocket.Message.Send(conn, currentTime); err != nil {
					log.Println("write error:", err)
					return
				}
			case <-sigChan:
				return
			}
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutting down...")
}

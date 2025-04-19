package main

import (
	"flag"
	"log"
	"net/http"

	"golang.org/x/net/websocket"
)

func main() {
	port := flag.String("port", ":8080", "Port to listen on")
	flag.Parse()

	// WebSocket handler
	http.Handle("/ws", websocket.Handler(func(ws *websocket.Conn) {
		log.Printf("New WebSocket connection from %s", ws.RemoteAddr().String())

		// Echo server
		for {
			var msg string
			if err := websocket.Message.Receive(ws, &msg); err != nil {
				log.Printf("Error receiving message: %v", err)
				break
			}
			log.Printf("Received: %s", msg)

			if err := websocket.Message.Send(ws, "Echo: "+msg); err != nil {
				log.Printf("Error sending message: %v", err)
				break
			}
		}
	}))

	// Start server
	log.Printf("Starting WebSocket server on %s", *port)
	if err := http.ListenAndServe(*port, nil); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

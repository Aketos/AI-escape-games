package main

import (
	"context"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	url := "ws://67.185.96.105:50168/api/chat"
	log.Printf("Connecting to %s", url)
	conn, _, err := websocket.DefaultDialer.DialContext(context.Background(), url, nil)
	if err != nil {
		log.Fatalf("Dial error: %v", err)
	}
	defer conn.Close()

	log.Println("Connected!")

	// Send setup
	err = conn.WriteJSON(map[string]interface{}{
		"type": "setup",
		"text": "Hello",
	})
	if err != nil {
		log.Fatalf("Write error: %v", err)
	}

	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		mt, p, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Read error: %v", err)
			break
		}
		log.Printf("Received msg type %d: %s", mt, string(p))
	}
}

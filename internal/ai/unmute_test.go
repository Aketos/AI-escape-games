package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestCloseBeforeConnectDropsConnection: a player who leaves while Unmute is
// still connecting must not leave an Unmute session open.
func TestCloseBeforeConnectDropsConnection(t *testing.T) {
	serverClosed := make(chan struct{})
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				close(serverClosed)
				return
			}
		}
	}))
	defer srv.Close()

	c := NewUnmuteClient("ws"+strings.TrimPrefix(srv.URL, "http"), "")
	c.Close() // the player left before Connect finished
	if err := c.Connect(context.Background()); err == nil {
		t.Fatal("Connect after Close must fail")
	}
	select {
	case <-serverClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("Unmute connection left open after the player left")
	}
}

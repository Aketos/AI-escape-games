package network

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"escape-game/internal/ai"
	"escape-game/internal/game"

	"github.com/gorilla/websocket"
)

// TestOnePlayerFreshGame: a second visitor is turned away instead of sharing
// the game, and the next connection starts a fresh game of the same scenario.
func TestOnePlayerFreshGame(t *testing.T) {
	p, url := startServer(t, fakeUnmute(t))

	// dial connects and returns the first status message.
	dial := func() (*websocket.Conn, string) {
		t.Helper()
		ws, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatal(err)
		}
		var msg ServerMessage
		if err := ws.ReadJSON(&msg); err != nil {
			t.Fatal(err)
		}
		return ws, msg.Payload
	}
	solInspected := func() bool {
		for _, room := range p.Engine.GameStateForClient().Rooms {
			for _, it := range room.Items {
				if it.ID == "zone_sol" {
					return it.Inspected
				}
			}
		}
		t.Fatal("zone_sol not found")
		return false
	}

	first, msg := dial()
	if msg != "AI Engine Ready" {
		t.Fatalf("first player: %q", msg)
	}
	p.Engine.ProcessLLMFunctionCall(game.FunctionCall{Name: "inspect_item", Arguments: map[string]interface{}{"target_item": "zone_sol"}})

	second, msg := dial()
	second.Close()
	if !strings.Contains(msg, "already in progress") {
		t.Fatalf("second player must be turned away, got %q", msg)
	}
	if !solInspected() {
		t.Fatal("second visitor reset the first player's game")
	}

	first.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		next, msg := dial()
		next.Close()
		if msg == "AI Engine Ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot never freed after the first player left: %q", msg)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if solInspected() {
		t.Fatal("new connection kept the previous player's game")
	}
}

// TestUnmuteDownEndsGame: without Unmute there is no voice, so the player is
// told and disconnected instead of waiting in silence while holding the slot.
func TestUnmuteDownEndsGame(t *testing.T) {
	_, url := startServer(t, "ws://127.0.0.1:1") // nothing listens there
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var statuses []string
	for {
		var msg ServerMessage
		if err := ws.ReadJSON(&msg); err != nil {
			break // the server closed the game
		}
		if msg.Type == "status" {
			statuses = append(statuses, msg.Payload)
		}
	}
	if len(statuses) == 0 || !strings.Contains(statuses[len(statuses)-1], "Failed to connect") {
		t.Fatalf("player not told that the AI engine is down: %q", statuses)
	}
}

// startServer runs the WebSocket server on the French scenario, bridged to
// unmuteURL, and returns its proxy and the player URL.
func startServer(t *testing.T, unmuteURL string) (*ai.LLMProxy, string) {
	t.Setenv("CONFIG_DIR", "../../config")
	t.Setenv("GAME_LANGUAGE", "fr")
	t.Setenv("UNMUTE_WS_URL", unmuteURL)
	t.Setenv("LLM_PROXY_TOKEN", "")

	s := NewWSServer()
	p, err := ai.NewLLMProxyFromEnv("projet_longevite", s.BroadcastSFX, s.BroadcastGameState)
	if err != nil {
		t.Fatal(err)
	}
	s.SetLLMProxy(p)
	srv := httptest.NewServer(http.HandlerFunc(s.HandleConnections))
	t.Cleanup(srv.Close)
	return p, "ws" + strings.TrimPrefix(srv.URL, "http") + "?scenario=projet_longevite"
}

// fakeUnmute accepts Unmute connections and ignores everything sent to it.
func fakeUnmute(t *testing.T) string {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

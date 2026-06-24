package ws

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"mobiledev/server/internal/auth"
	"mobiledev/server/internal/hub"
)

func TestAgentRejectsInvalidToken(t *testing.T) {
	server, _, _ := newTestServer(t)
	defer server.Close()

	_, resp, err := websocket.DefaultDialer.Dial(wsURL(server.URL, "/ws/agent?token=wrong&name=test-agent"), nil)
	if err == nil {
		t.Fatal("expected websocket dial to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 response, got %#v", resp)
	}
}

func TestAgentRegisterAndUnregister(t *testing.T) {
	server, connectionHub, token := newTestServer(t)
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(server.URL, "/ws/agent?token="+token+"&name=test-agent"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}

	waitFor(t, func() bool {
		agents := connectionHub.Agents()
		return len(agents) == 1 && agents[0].Name == "test-agent"
	})

	if err := conn.Close(); err != nil {
		t.Fatalf("close websocket: %v", err)
	}
	waitFor(t, func() bool {
		return len(connectionHub.Agents()) == 0
	})
}

func newTestServer(t *testing.T) (*httptest.Server, *hub.Hub, string) {
	t.Helper()

	password := "test123"
	connectionHub := hub.New()
	wsServer := New(auth.New(password), connectionHub)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/agent", wsServer.HandleAgent)

	return httptest.NewServer(mux), connectionHub, hash(password)
}

func wsURL(serverURL, path string) string {
	return "ws" + serverURL[len("http"):] + path
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

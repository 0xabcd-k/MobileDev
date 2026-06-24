package ws

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	"mobiledev/server/internal/auth"
	"mobiledev/server/internal/hub"
	"mobiledev/server/protocol"
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

func TestRouteMessageBetweenClientAndAgent(t *testing.T) {
	server, connectionHub, token := newTestServer(t)
	defer server.Close()

	agent := dialWebSocket(t, server.URL, "/ws/agent?token="+token+"&name=test-agent")
	defer agent.Close()
	client := dialWebSocket(t, server.URL, "/ws/client?token="+token)
	defer client.Close()

	agentID := waitForAgentID(t, connectionHub)
	sendProto(t, client, &protocol.Message{
		From:    "spoofed-client",
		To:      agentID,
		Action:  "plugin.list",
		Payload: []byte("request"),
	})

	clientToAgent := readProto(t, agent)
	if clientToAgent.From == "spoofed-client" || !strings.HasPrefix(clientToAgent.From, "client-") {
		t.Fatalf("expected server-assigned client from, got %q", clientToAgent.From)
	}
	if clientToAgent.To != agentID {
		t.Fatalf("expected message to %q, got %q", agentID, clientToAgent.To)
	}
	if clientToAgent.Type != "client2agent" {
		t.Fatalf("expected client2agent type, got %q", clientToAgent.Type)
	}
	if string(clientToAgent.Payload) != "request" {
		t.Fatalf("expected payload to be routed")
	}

	sendProto(t, agent, &protocol.Message{
		To:      clientToAgent.From,
		Action:  "plugin.list.resp",
		Payload: []byte("response"),
	})

	agentToClient := readProto(t, client)
	if agentToClient.From != agentID {
		t.Fatalf("expected server-assigned agent from %q, got %q", agentID, agentToClient.From)
	}
	if agentToClient.To != clientToAgent.From {
		t.Fatalf("expected response to client %q, got %q", clientToAgent.From, agentToClient.To)
	}
	if agentToClient.Type != "agent2client" {
		t.Fatalf("expected agent2client type, got %q", agentToClient.Type)
	}
	if string(agentToClient.Payload) != "response" {
		t.Fatalf("expected response payload to be routed")
	}
}

func TestRouteMessageToMissingTargetReturnsError(t *testing.T) {
	server, _, token := newTestServer(t)
	defer server.Close()

	client := dialWebSocket(t, server.URL, "/ws/client?token="+token)
	defer client.Close()

	sendProto(t, client, &protocol.Message{
		To:     "agent-missing",
		Action: "plugin.list",
	})

	msg := readProto(t, client)
	if msg.From != "server" || msg.Action != "error" || msg.Type != "error" {
		t.Fatalf("expected server error message, got from=%q type=%q action=%q", msg.From, msg.Type, msg.Action)
	}
	if !strings.Contains(string(msg.Payload), "target not found: agent-missing") {
		t.Fatalf("expected missing target error, got %q", string(msg.Payload))
	}
}

func TestTwoClientsRouteToSameAgentWithDifferentFromIDs(t *testing.T) {
	server, connectionHub, token := newTestServer(t)
	defer server.Close()

	agent := dialWebSocket(t, server.URL, "/ws/agent?token="+token+"&name=test-agent")
	defer agent.Close()
	clientA := dialWebSocket(t, server.URL, "/ws/client?token="+token)
	defer clientA.Close()
	clientB := dialWebSocket(t, server.URL, "/ws/client?token="+token)
	defer clientB.Close()

	agentID := waitForAgentID(t, connectionHub)
	sendProto(t, clientA, &protocol.Message{To: agentID, Action: "plugin.do", Payload: []byte("a")})
	sendProto(t, clientB, &protocol.Message{To: agentID, Action: "plugin.do", Payload: []byte("b")})

	first := readProto(t, agent)
	second := readProto(t, agent)
	if first.From == second.From {
		t.Fatalf("expected different client from ids, got %q and %q", first.From, second.From)
	}
	if string(first.Payload) == string(second.Payload) {
		t.Fatalf("expected two distinct routed payloads")
	}
}

func newTestServer(t *testing.T) (*httptest.Server, *hub.Hub, string) {
	t.Helper()

	password := "test123"
	connectionHub := hub.New()
	wsServer := New(auth.New(password), connectionHub)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/agent", wsServer.HandleAgent)
	mux.HandleFunc("/ws/client", wsServer.HandleClient)

	return httptest.NewServer(mux), connectionHub, hash(password)
}

func dialWebSocket(t *testing.T, serverURL, path string) *websocket.Conn {
	t.Helper()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(serverURL, path), nil)
	if err != nil {
		t.Fatalf("dial websocket %s: %v", path, err)
	}
	return conn
}

func waitForAgentID(t *testing.T, connectionHub *hub.Hub) string {
	t.Helper()

	var id string
	waitFor(t, func() bool {
		agents := connectionHub.Agents()
		if len(agents) != 1 {
			return false
		}
		id = agents[0].ID
		return true
	})
	return id
}

func sendProto(t *testing.T, conn *websocket.Conn, msg *protocol.Message) {
	t.Helper()

	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal protobuf: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		t.Fatalf("write protobuf websocket message: %v", err)
	}
}

func readProto(t *testing.T, conn *websocket.Conn) *protocol.Message {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	messageType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("expected binary websocket message, got %d", messageType)
	}
	var msg protocol.Message
	if err := proto.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal protobuf: %v", err)
	}
	return &msg
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

package main

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	"mobiledev/server/protocol"
)

func TestAgent(t *testing.T) {
	serverURL := "wss://127.0.0.1:8443/ws/agent"
	password := "114514"
	agentName := "just-test-agent"

	listenFor := 30 * time.Second

	wsURL, err := agentWebSocketURL(serverURL, password, agentName)
	if err != nil {
		t.Fatalf("build websocket url: %v", err)
	}

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // manual test helper for self-signed local server certs.
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("connect agent websocket: %v", err)
	}
	defer conn.Close()

	t.Logf("connected to %s as %s, listening for %s", wsURL, agentName, listenFor)
	deadline := time.Now().Add(listenFor)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) ||
				strings.Contains(err.Error(), "i/o timeout") {
				t.Log("listen finished")
				return
			}
			t.Fatalf("read websocket message: %v", err)
		}

		if messageType != websocket.BinaryMessage {
			t.Logf("received websocket message type=%d payload=%q", messageType, string(data))
			continue
		}

		var msg protocol.Message
		if err := proto.Unmarshal(data, &msg); err != nil {
			t.Logf("received binary message len=%d protobuf_error=%v", len(data), err)
			continue
		}
		t.Logf("received message from=%q to=%q type=%q action=%q sid=%q payload=%q",
			msg.From, msg.To, msg.Type, msg.Action, msg.Sid, string(msg.Payload))
	}
}

func agentWebSocketURL(serverURL, password, agentName string) (string, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "wss", "ws":
	default:
		parsed.Scheme = "wss"
	}
	parsed.Path = "/ws/agent"
	query := parsed.Query()
	query.Set("token", sha256Hex(password))
	query.Set("name", agentName)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TestClient(t *testing.T) {
	serverURL := "wss://127.0.0.1:8443/ws/client"
	password := "114514"
	agentName := "just-test-agent"

	agentID, err := findAgentID(serverURL, password, agentName)
	if err != nil {
		t.Fatalf("find agent id: %v", err)
	}

	wsURL, err := clientWebSocketURL(serverURL, password)
	if err != nil {
		t.Fatalf("build client websocket url: %v", err)
	}

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("connect client websocket: %v", err)
	}
	defer conn.Close()

	msg := &protocol.Message{
		To:      agentID,
		Action:  "plugin.list",
		Sid:     "just-test-sid",
		Payload: []byte("hello from ws client"),
	}
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		t.Fatalf("send message: %v", err)
	}
	t.Logf("sent message to agent id=%s action=%s payload=%q", agentID, msg.Action, string(msg.Payload))
}

func clientWebSocketURL(serverURL, password string) (string, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "wss", "ws":
	default:
		parsed.Scheme = "wss"
	}
	parsed.Path = "/ws/client"
	query := parsed.Query()
	query.Set("token", sha256Hex(password))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func findAgentID(serverURL, password, agentName string) (string, error) {
	apiURL, err := agentsAPIURL(serverURL)
	if err != nil {
		return "", err
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 5 * time.Second,
	}
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+sha256Hex(password))

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s returned %d: %s", apiURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var agents []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &agents); err != nil {
		return "", err
	}
	for _, agent := range agents {
		if agent.Name == agentName {
			return agent.ID, nil
		}
	}
	return "", fmt.Errorf("agent %q not found in %v", agentName, agents)
}

func agentsAPIURL(serverURL string) (string, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "wss":
		parsed.Scheme = "https"
	case "ws":
		parsed.Scheme = "http"
	case "https", "http":
	default:
		parsed.Scheme = "https"
	}
	parsed.Path = "/api/agents"
	parsed.RawQuery = ""
	return parsed.String(), nil
}

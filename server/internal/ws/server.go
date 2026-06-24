package ws

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	"mobiledev/server/internal/auth"
	"mobiledev/server/internal/hub"
	"mobiledev/server/protocol"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 54 * time.Second
)

type Server struct {
	auth     auth.Authenticator
	hub      *hub.Hub
	upgrader websocket.Upgrader
}

func New(authenticator auth.Authenticator, connectionHub *hub.Hub) *Server {
	return &Server{
		auth: authenticator,
		hub:  connectionHub,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool {
				return true
			},
		},
	}
}

func (s *Server) HandleAgent(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeToken(w, r) {
		return
	}

	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = "unnamed-agent"
	}
	s.handleConnection(w, r, hub.ConnectionTypeAgent, name)
}

func (s *Server) HandleClient(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeToken(w, r) {
		return
	}

	s.handleConnection(w, r, hub.ConnectionTypeClient, "")
}

func (s *Server) authorizeToken(w http.ResponseWriter, r *http.Request) bool {
	if !s.auth.ValidToken(r.URL.Query().Get("token")) {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *Server) handleConnection(w http.ResponseWriter, r *http.Request, connectionType, name string) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: type=%s err=%v", connectionType, err)
		return
	}

	connection := &hub.Connection{
		ID:   newConnectionID(connectionType),
		Type: connectionType,
		Name: name,
		Conn: conn,
	}
	s.hub.Register(connection)
	log.Printf("%s registered: id=%s name=%s remote=%s", connection.Type, connection.ID, connection.Name, r.RemoteAddr)

	s.serve(connection)
}

func (s *Server) serve(connection *hub.Connection) {
	defer func() {
		s.hub.Unregister(connection)
		_ = connection.Conn.Close()
		log.Printf("%s unregistered: id=%s name=%s", connection.Type, connection.ID, connection.Name)
	}()

	connection.Conn.SetReadLimit(1 << 20)
	_ = connection.Conn.SetReadDeadline(time.Now().Add(pongWait))
	connection.Conn.SetPongHandler(func(string) error {
		return connection.Conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	done := make(chan struct{})
	go heartbeat(connection, done)
	defer close(done)

	for {
		messageType, data, err := connection.Conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.BinaryMessage {
			s.sendError(connection, "only binary protobuf websocket messages are supported")
			continue
		}
		s.routeMessage(connection, data)
	}
}

func (s *Server) routeMessage(sender *hub.Connection, data []byte) {
	var msg protocol.Message
	if err := proto.Unmarshal(data, &msg); err != nil {
		s.sendError(sender, "invalid protobuf message")
		return
	}

	msg.From = sender.ID
	if msg.Type == "" {
		msg.Type = messageTypeFor(sender.Type)
	}
	if strings.TrimSpace(msg.To) == "" {
		s.sendError(sender, "message target is required")
		return
	}

	target, ok := s.hub.Get(msg.To)
	if !ok {
		s.sendError(sender, fmt.Sprintf("target not found: %s", msg.To))
		return
	}

	payload, err := proto.Marshal(&msg)
	if err != nil {
		s.sendError(sender, "failed to serialize routed message")
		return
	}

	if err := target.WriteMessage(websocket.BinaryMessage, payload, time.Now().Add(writeWait)); err != nil {
		s.sendError(sender, fmt.Sprintf("failed to route message to %s", msg.To))
	}
}

func (s *Server) sendError(target *hub.Connection, message string) {
	msg := &protocol.Message{
		From:    "server",
		To:      target.ID,
		Type:    "error",
		Action:  "error",
		Payload: []byte(message),
	}
	payload, err := proto.Marshal(msg)
	if err != nil {
		log.Printf("failed to serialize websocket error response: target=%s err=%v", target.ID, err)
		return
	}
	if err := target.WriteMessage(websocket.BinaryMessage, payload, time.Now().Add(writeWait)); err != nil {
		log.Printf("failed to send websocket error response: target=%s err=%v", target.ID, err)
	}
}

func messageTypeFor(senderType string) string {
	switch senderType {
	case hub.ConnectionTypeAgent:
		return "agent2client"
	case hub.ConnectionTypeClient:
		return "client2agent"
	default:
		return ""
	}
}

func heartbeat(connection *hub.Connection, done <-chan struct{}) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := connection.WriteMessage(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
				_ = connection.Conn.Close()
				return
			}
		case <-done:
			return
		}
	}
}

func newConnectionID(connectionType string) string {
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		return connectionType + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return connectionType + "-" + hex.EncodeToString(randomBytes)
}

package ws

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"mobiledev/server/internal/auth"
	"mobiledev/server/internal/hub"
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
		if _, _, err := connection.Conn.NextReader(); err != nil {
			return
		}
	}
}

func heartbeat(connection *hub.Connection, done <-chan struct{}) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_ = connection.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := connection.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
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

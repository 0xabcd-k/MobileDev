package hub

import (
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	ConnectionTypeAgent  = "agent"
	ConnectionTypeClient = "client"
)

type Connection struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name,omitempty"`
	Conn    *websocket.Conn
	writeMu sync.Mutex
}

type AgentInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Hub struct {
	agents  map[string]*Connection
	clients map[string]*Connection
	mu      sync.RWMutex
}

func New() *Hub {
	return &Hub{
		agents:  make(map[string]*Connection),
		clients: make(map[string]*Connection),
	}
}

func (h *Hub) Register(conn *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()

	switch conn.Type {
	case ConnectionTypeAgent:
		h.agents[conn.ID] = conn
	case ConnectionTypeClient:
		h.clients[conn.ID] = conn
	}
}

func (h *Hub) Unregister(conn *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()

	switch conn.Type {
	case ConnectionTypeAgent:
		delete(h.agents, conn.ID)
	case ConnectionTypeClient:
		delete(h.clients, conn.ID)
	}
}

func (h *Hub) Get(id string) (*Connection, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if conn, ok := h.agents[id]; ok {
		return conn, true
	}
	if conn, ok := h.clients[id]; ok {
		return conn, true
	}
	return nil, false
}

func (h *Hub) Agents() []AgentInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()

	agents := make([]AgentInfo, 0, len(h.agents))
	for _, conn := range h.agents {
		agents = append(agents, AgentInfo{
			ID:   conn.ID,
			Name: conn.Name,
		})
	}
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Name == agents[j].Name {
			return agents[i].ID < agents[j].ID
		}
		return agents[i].Name < agents[j].Name
	})
	return agents
}

func (c *Connection) WriteMessage(messageType int, data []byte, deadline time.Time) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if !deadline.IsZero() {
		if err := c.Conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
	}
	return c.Conn.WriteMessage(messageType, data)
}

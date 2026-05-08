package realtime

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const websocketSendBuffer = 256

type Event struct {
	Type      string      `json:"type"`
	ProjectID int64       `json:"projectId"`
	User      string      `json:"user"`
	Color     string      `json:"color"`
	Payload   interface{} `json:"payload"`
}

type Hub struct {
	mu       sync.RWMutex
	projects map[int64]map[*Client]bool
}

func NewHub() *Hub { return &Hub{projects: make(map[int64]map[*Client]bool)} }

func (h *Hub) Broadcast(projectID int64, ev Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	h.mu.RLock()
	clients := h.projects[projectID]
	var dead []*Client
	for c := range clients {
		select {
		case c.send <- b:
			if len(c.send) > websocketSendBuffer*3/4 {
				slog.Warn("realtime: client send buffer high", "project_id", projectID, "username", c.username, "queued", len(c.send), "capacity", cap(c.send))
			}
		default:
			slog.Warn("realtime: client send buffer full; dropping client", "project_id", projectID, "username", c.username, "capacity", cap(c.send))
			dead = append(dead, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range dead {
		c.Close()
	}
}

func (h *Hub) register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.projects[c.projectID] == nil {
		h.projects[c.projectID] = make(map[*Client]bool)
	}
	h.projects[c.projectID][c] = true
}

func (h *Hub) unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.projects[c.projectID] != nil {
		delete(h.projects[c.projectID], c)
		if len(h.projects[c.projectID]) == 0 {
			delete(h.projects, c.projectID)
		}
	}
}

func (h *Hub) presenceSnapshot(projectID int64) []map[string]string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	seen := map[string]bool{}
	users := []map[string]string{}
	for c := range h.projects[projectID] {
		if seen[c.username] {
			continue
		}
		seen[c.username] = true
		users = append(users, map[string]string{"username": c.username, "color": c.color})
	}
	return users
}

type Client struct {
	hub       *Hub
	conn      *websocket.Conn
	send      chan []byte
	projectID int64
	username  string
	color     string
	closed    sync.Once
}

var upgrader = websocket.Upgrader{CheckOrigin: sameOrigin}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func ServeWS(hub *Hub, w http.ResponseWriter, r *http.Request, projectID int64, username, color string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &Client{hub: hub, conn: conn, send: make(chan []byte, websocketSendBuffer), projectID: projectID, username: username, color: color}
	hub.register(c)
	c.sendEvent(Event{Type: "presence.snapshot", ProjectID: projectID, User: "system", Payload: map[string]any{"users": hub.presenceSnapshot(projectID)}})
	hub.Broadcast(projectID, Event{Type: "user.joined", ProjectID: projectID, User: username, Color: color, Payload: map[string]string{"username": username, "color": color}})
	go c.writePump()
	go c.readPump()
}

func (c *Client) sendEvent(ev Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	select {
	case c.send <- b:
	default:
		slog.Warn("realtime: client send buffer full during direct send", "project_id", c.projectID, "username", c.username, "capacity", cap(c.send))
		c.Close()
	}
}

func (c *Client) Close() {
	c.closed.Do(func() {
		c.hub.unregister(c)
		close(c.send)
		_ = c.conn.Close()
		c.hub.Broadcast(c.projectID, Event{Type: "user.left", ProjectID: c.projectID, User: c.username, Color: c.color, Payload: map[string]string{"username": c.username}})
	})
}

func (c *Client) readPump() {
	defer c.Close()
	c.conn.SetReadLimit(4096)
	_ = c.conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	c.conn.SetPongHandler(func(string) error { _ = c.conn.SetReadDeadline(time.Now().Add(70 * time.Second)); return nil })
	for {
		messageType, msg, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		if messageType == websocket.TextMessage && len(msg) > 0 {
			slog.Debug("realtime: unsupported client message", "project_id", c.projectID, "username", c.username, "bytes", len(msg))
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() { ticker.Stop(); c.Close() }()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				slog.Debug("websocket write failed", "project_id", c.projectID, "username", c.username, "err", err)
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type wsMessage struct {
	Action string          `json:"action,omitempty"`
	Topic  string          `json:"topic"`
	Data   json.RawMessage `json:"data,omitempty"`
}

type client struct {
	conn   *websocket.Conn
	topics map[string]bool
	send   chan []byte
	mu     sync.Mutex
}

type Hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}
	log     *slog.Logger
}

func NewHub() *Hub {
	return &Hub{
		clients: make(map[*client]struct{}),
		log:     slog.Default().With("component", "ws-hub"),
	}
}

func (h *Hub) Broadcast(topic string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		h.log.Error("marshal broadcast", "error", err)
		return
	}

	msg, _ := json.Marshal(wsMessage{Topic: topic, Data: payload})

	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		c.mu.Lock()
		subscribed := c.topics[topic]
		c.mu.Unlock()
		if !subscribed {
			continue
		}
		select {
		case c.send <- msg:
		default:
		}
	}
}

func (h *Hub) register(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	close(c.send)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Error("ws upgrade", "error", err)
		return
	}

	c := &client{
		conn:   conn,
		topics: make(map[string]bool),
		send:   make(chan []byte, 64),
	}

	s.hub.register(c)
	go s.wsWritePump(c)
	go s.wsReadPump(c)
}

func (s *Server) wsReadPump(c *client) {
	defer func() {
		s.hub.unregister(c)
		c.conn.Close()
	}()

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			return
		}

		var msg wsMessage
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}

		switch msg.Action {
		case "subscribe":
			c.mu.Lock()
			c.topics[msg.Topic] = true
			c.mu.Unlock()
		case "unsubscribe":
			c.mu.Lock()
			delete(c.topics, msg.Topic)
			c.mu.Unlock()
		}
	}
}

func (s *Server) wsWritePump(c *client) {
	defer c.conn.Close()
	for msg := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

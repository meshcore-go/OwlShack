package api

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

var upgrader = websocket.Upgrader{
	CheckOrigin: sameHostOrigin,
}

// sameHostOrigin requires Origin's host to equal the request Host; non-browser clients send no Origin.
func sameHostOrigin(r *http.Request) bool {
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

// Keepalive: a browser's automatic pong extends the read deadline, and a silent client is reaped after wsPongWait.
const (
	wsWriteWait  = 10 * time.Second
	wsPongWait   = 75 * time.Second
	wsPingPeriod = 50 * time.Second // must be < wsPongWait
)

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

	c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		c.conn.SetReadDeadline(time.Now().Add(wsPongWait))

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
		case "ping":
			// App-level probe: protocol pongs aren't visible to JS, so the SPA checks a half-open socket this way.
			pong, _ := json.Marshal(wsMessage{Topic: "pong"})
			select {
			case c.send <- pong:
			default:
			}
		}
	}
}

func (s *Server) wsWritePump(c *client) {
	ticker := time.NewTicker(wsPingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

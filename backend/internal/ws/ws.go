package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"groupflow/backend/internal/config"
	"groupflow/backend/pkg/id"
)

type Envelope struct {
	Type      string          `json:"type"`
	Version   string          `json:"version,omitempty"`
	RequestID string          `json:"requestId,omitempty"`
	Timestamp int64           `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

type Client struct {
	ID         string
	UserID     int64
	Hub        *Hub
	Conn       *websocket.Conn
	Send       chan []byte
	DeviceID   string
	ClientType string
}

type HandlerFunc func(c *Client, env Envelope)

type Hub struct {
	cfg         config.Config
	redis       *redis.Client
	log         *zap.Logger
	mu          sync.RWMutex
	clients     map[string]*Client
	userClients map[int64]map[string]*Client
	OnMessage   HandlerFunc
}

func NewHub(cfg config.Config, redis *redis.Client, log *zap.Logger) *Hub {
	return &Hub{cfg: cfg, redis: redis, log: log, clients: map[string]*Client{}, userClients: map[int64]map[string]*Client{}}
}

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }, ReadBufferSize: 4096, WriteBufferSize: 4096}

func (h *Hub) Upgrade(w http.ResponseWriter, r *http.Request, userID int64) error {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	c := &Client{ID: id.New("conn"), UserID: userID, Hub: h, Conn: conn, Send: make(chan []byte, 256), DeviceID: r.URL.Query().Get("deviceId"), ClientType: r.URL.Query().Get("clientType")}
	h.register(c)
	go c.writePump()
	go c.readPump()
	c.SendJSON("connection_connected", "", map[string]any{"connectionId": c.ID, "userId": c.UserID, "serverId": h.cfg.ServerID, "heartbeatIntervalSeconds": 20, "heartbeatTimeoutSeconds": 60, "protocolVersion": "v1"})
	return nil
}

func (h *Hub) register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c.ID] = c
	if h.userClients[c.UserID] == nil {
		h.userClients[c.UserID] = map[string]*Client{}
	}
	h.userClients[c.UserID][c.ID] = c
	h.renewRedis(context.Background(), c)
	h.log.Info("ws connected", zap.Int64("userId", c.UserID), zap.String("connectionId", c.ID), zap.String("serverId", h.cfg.ServerID))
}

func (h *Hub) unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c.ID]; ok {
		delete(h.clients, c.ID)
		close(c.Send)
		if h.userClients[c.UserID] != nil {
			delete(h.userClients[c.UserID], c.ID)
			if len(h.userClients[c.UserID]) == 0 {
				delete(h.userClients, c.UserID)
			}
		}
	}
	if h.redis != nil {
		ctx := context.Background()
		_ = h.redis.SRem(ctx, fmt.Sprintf("online:user:%d:connections", c.UserID), c.ID).Err()
		_ = h.redis.Del(ctx, fmt.Sprintf("connection:%s:user", c.ID), fmt.Sprintf("connection:%s:server", c.ID)).Err()
	}
	h.log.Info("ws disconnected", zap.Int64("userId", c.UserID), zap.String("connectionId", c.ID))
}

func (h *Hub) renewRedis(ctx context.Context, c *Client) {
	if h.redis == nil {
		return
	}
	pipe := h.redis.Pipeline()
	pipe.SAdd(ctx, fmt.Sprintf("online:user:%d:connections", c.UserID), c.ID)
	pipe.Expire(ctx, fmt.Sprintf("online:user:%d:connections", c.UserID), 90*time.Second)
	pipe.Set(ctx, fmt.Sprintf("online:user:%d", c.UserID), h.cfg.ServerID, 90*time.Second)
	pipe.Set(ctx, fmt.Sprintf("connection:%s:user", c.ID), c.UserID, 90*time.Second)
	pipe.Set(ctx, fmt.Sprintf("connection:%s:server", c.ID), h.cfg.ServerID, 90*time.Second)
	pipe.SAdd(ctx, fmt.Sprintf("server:%s:connections", h.cfg.ServerID), c.ID)
	pipe.Expire(ctx, fmt.Sprintf("server:%s:connections", h.cfg.ServerID), 90*time.Second)
	_, _ = pipe.Exec(ctx)
}

func (h *Hub) SendToUsers(userIDs []int64, msgType string, data any) int {
	payload := Build(msgType, "", data)
	b, _ := json.Marshal(payload)
	count := 0
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, uid := range userIDs {
		for _, c := range h.userClients[uid] {
			select {
			case c.Send <- b:
				count++
			default:
				h.log.Warn("ws send channel full", zap.Int64("userId", uid))
			}
		}
	}
	return count
}

func (h *Hub) OnlineUserIDs() []int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]int64, 0, len(h.userClients))
	for uid := range h.userClients {
		ids = append(ids, uid)
	}
	return ids
}

func (c *Client) SendJSON(msgType, requestID string, data any) {
	b, _ := json.Marshal(Build(msgType, requestID, data))
	select {
	case c.Send <- b:
	default:
		c.Hub.log.Warn("send channel full", zap.Int64("userId", c.UserID))
	}
}

func Build(msgType, requestID string, data any) map[string]any {
	return map[string]any{"type": msgType, "version": "v1", "requestId": requestID, "timestamp": time.Now().UnixMilli(), "data": data}
}

func (c *Client) readPump() {
	defer func() { c.Hub.unregister(c); _ = c.Conn.Close() }()
	c.Conn.SetReadLimit(65536)
	_ = c.Conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		_ = c.Conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		c.Hub.renewRedis(context.Background(), c)
		return nil
	})
	for {
		_, b, err := c.Conn.ReadMessage()
		if err != nil {
			return
		}
		var env Envelope
		if err := json.Unmarshal(b, &env); err != nil {
			c.SendJSON("error", "", map[string]any{"code": "BAD_REQUEST", "message": "invalid json", "retryable": false})
			continue
		}
		if env.Type == "ping" {
			c.Hub.renewRedis(context.Background(), c)
			c.SendJSON("pong", env.RequestID, map[string]any{"serverTime": time.Now().UnixMilli()})
			continue
		}
		if c.Hub.OnMessage != nil {
			c.Hub.OnMessage(c, env)
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() { ticker.Stop(); _ = c.Conn.Close() }()
	for {
		select {
		case msg, ok := <-c.Send:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

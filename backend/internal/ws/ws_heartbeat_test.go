package ws

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"groupflow/backend/internal/config"
)

func TestHeartbeatKeyValueAndTTL(t *testing.T) {
	hub := NewHub(config.Config{ServerID: "ws-server-01"}, nil, zap.NewNop())
	key, value, ttl := hub.heartbeatPayloadForTest()
	if key != "server:ws-server-01:heartbeat" {
		t.Fatalf("unexpected heartbeat key %q", key)
	}
	if value == "" {
		t.Fatalf("expected non-empty heartbeat value")
	}
	if ttl != 30*time.Second {
		t.Fatalf("expected heartbeat ttl 30s, got %s", ttl)
	}
}

func TestHeartbeatLoopReturnsWhenRedisMissing(t *testing.T) {
	hub := NewHub(config.Config{ServerID: "ws-server-01"}, nil, zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := hub.writeHeartbeat(ctx); err != nil {
		t.Fatalf("expected nil error without redis, got %v", err)
	}
}

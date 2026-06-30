package service

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"groupflow/backend/internal/config"
	"groupflow/backend/internal/domain"
)

func TestGlobalSendRateLimitUsesDocumentedKey(t *testing.T) {
	keys := make([]string, 0, 2)
	svc := &Service{Cfg: config.Config{}, Log: zap.NewNop()}
	svc.globalSendRateLimit = func(ctx context.Context, key string, window time.Duration, limit int) error {
		keys = append(keys, key)
		if window != time.Second {
			t.Fatalf("unexpected send limit window %s", window)
		}
		if limit != 5 {
			t.Fatalf("unexpected send limit %d", limit)
		}
		return nil
	}

	if err := svc.checkGlobalSendRateLimit(context.Background(), 1001); err != nil {
		t.Fatalf("checkGlobalSendRateLimit returned error: %v", err)
	}
	if len(keys) != 1 || keys[0] != "rate_limit:user:1001:send_message" {
		t.Fatalf("unexpected rate limit keys: %#v", keys)
	}
}

func TestMentionAllChecksGroupAndUserDocumentedKeys(t *testing.T) {
	keys := make([]string, 0, 4)
	svc := &Service{Cfg: config.Config{}, Log: zap.NewNop()}
	svc.setNXRateLimit = func(ctx context.Context, key string, ttl time.Duration) (bool, error) {
		keys = append(keys, key)
		return true, nil
	}
	group := &domain.Group{ID: 10001, GroupType: domain.GroupLarge, MentionAllRole: domain.RoleAdmin}
	member := &domain.Member{UserID: 1001, Role: domain.RoleAdmin}

	if err := svc.checkMentionAll(context.Background(), group, member); err != nil {
		t.Fatalf("checkMentionAll returned error: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 rate limit keys, got %#v", keys)
	}
	if keys[0] != "rate_limit:group:10001:mention_all" || keys[1] != "rate_limit:user:1001:mention_all" {
		t.Fatalf("unexpected mention_all keys: %#v", keys)
	}
}

func TestMentionAllReturnsRateLimitedWhenUserWindowBlocked(t *testing.T) {
	svc := &Service{Cfg: config.Config{}, Log: zap.NewNop()}
	call := 0
	svc.setNXRateLimit = func(ctx context.Context, key string, ttl time.Duration) (bool, error) {
		call++
		if call == 2 {
			return false, nil
		}
		return true, nil
	}
	group := &domain.Group{ID: 10001, GroupType: domain.GroupNormal, MentionAllRole: domain.RoleAdmin}
	member := &domain.Member{UserID: 1001, Role: domain.RoleAdmin}

	if err := svc.checkMentionAll(context.Background(), group, member); err != ErrRateLimited {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
}

func TestSetNXRateLimitFallsBackToRedisWhenHookUnset(t *testing.T) {
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0", DialTimeout: 20 * time.Millisecond, ReadTimeout: 20 * time.Millisecond, WriteTimeout: 20 * time.Millisecond})
	defer redisClient.Close()
	svc := &Service{Redis: redisClient, Log: zap.NewNop()}
	if _, err := svc.useSetNXRateLimit(context.Background(), "rate_limit:test", time.Second); err == nil {
		t.Fatalf("expected redis error from useSetNXRateLimit")
	}
}

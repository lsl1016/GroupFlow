package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"groupflow/backend/internal/config"
	"groupflow/backend/internal/domain"
)

func TestGetGroupConfigReturnsCachedValueWithoutRepoFetch(t *testing.T) {
	ctx := context.Background()
	cachedGroup := &domain.Group{ID: 10001, Name: "cached", GroupType: domain.GroupLarge, SlowModeSeconds: 5}
	payload, err := marshalGroupConfigCacheValue(cachedGroup)
	if err != nil {
		t.Fatalf("marshalGroupConfigCacheValue returned error: %v", err)
	}
	fetchCalls := 0
	svc := &Service{Log: zap.NewNop()}
	svc.groupConfigGet = func(ctx context.Context, key string) ([]byte, error) {
		if key != groupConfigCacheKey(10001) {
			t.Fatalf("unexpected cache key %q", key)
		}
		return payload, nil
	}
	svc.groupConfigFetch = func(ctx context.Context, groupID int64) (*domain.Group, error) {
		fetchCalls++
		return nil, errors.New("repo fetch should not be called")
	}

	group, err := svc.getGroupConfig(ctx, 10001)
	if err != nil {
		t.Fatalf("getGroupConfig returned error: %v", err)
	}
	if group.Name != "cached" || group.GroupType != domain.GroupLarge {
		t.Fatalf("unexpected cached group: %#v", group)
	}
	if fetchCalls != 0 {
		t.Fatalf("expected 0 repo fetches, got %d", fetchCalls)
	}
}

func TestGetGroupConfigFetchesAndCachesOnMiss(t *testing.T) {
	ctx := context.Background()
	fetchCalls := 0
	cacheSets := 0
	freshGroup := &domain.Group{ID: 10001, Name: "fresh", GroupType: domain.GroupNormal, SlowModeSeconds: 3}
	svc := &Service{Log: zap.NewNop()}
	svc.groupConfigGet = func(ctx context.Context, key string) ([]byte, error) {
		return nil, redis.Nil
	}
	svc.groupConfigFetch = func(ctx context.Context, groupID int64) (*domain.Group, error) {
		fetchCalls++
		return freshGroup, nil
	}
	svc.groupConfigSet = func(ctx context.Context, key string, value []byte, ttl time.Duration) error {
		cacheSets++
		if key != groupConfigCacheKey(10001) {
			t.Fatalf("unexpected cache key %q", key)
		}
		if ttl != 10*time.Minute {
			t.Fatalf("unexpected cache ttl %s", ttl)
		}
		var cached domain.Group
		if err := json.Unmarshal(value, &cached); err != nil {
			t.Fatalf("unmarshal cached payload: %v", err)
		}
		if cached.Name != "fresh" || cached.SlowModeSeconds != 3 {
			t.Fatalf("unexpected cached group payload: %#v", cached)
		}
		return nil
	}

	group, err := svc.getGroupConfig(ctx, 10001)
	if err != nil {
		t.Fatalf("getGroupConfig returned error: %v", err)
	}
	if group.Name != "fresh" {
		t.Fatalf("unexpected fetched group: %#v", group)
	}
	if fetchCalls != 1 {
		t.Fatalf("expected 1 repo fetch, got %d", fetchCalls)
	}
	if cacheSets != 1 {
		t.Fatalf("expected 1 cache set, got %d", cacheSets)
	}
}

func TestInvalidateGroupConfigCacheUsesDeleteHook(t *testing.T) {
	ctx := context.Background()
	deletes := 0
	svc := &Service{Cfg: config.Config{}, Log: zap.NewNop()}
	svc.groupConfigDel = func(ctx context.Context, key string) error {
		deletes++
		if key != groupConfigCacheKey(10001) {
			t.Fatalf("unexpected cache key %q", key)
		}
		return nil
	}

	if err := svc.invalidateGroupConfigCache(ctx, 10001); err != nil {
		t.Fatalf("invalidateGroupConfigCache returned error: %v", err)
	}
	if deletes != 1 {
		t.Fatalf("expected 1 cache delete, got %d", deletes)
	}
}

func TestMarshalGroupConfigCacheValue(t *testing.T) {
	group := &domain.Group{ID: 10001, Name: "test", GroupType: domain.GroupLarge, Status: domain.StatusNormal, MuteAll: true, SlowModeSeconds: 5, MentionAllRole: domain.RoleAdmin, MaxMemberCount: 1000}
	payload, err := marshalGroupConfigCacheValue(group)
	if err != nil {
		t.Fatalf("marshalGroupConfigCacheValue returned error: %v", err)
	}
	var cached domain.Group
	if err := json.Unmarshal(payload, &cached); err != nil {
		t.Fatalf("unmarshal cached payload: %v", err)
	}
	if cached.ID != group.ID || cached.GroupType != group.GroupType || cached.SlowModeSeconds != group.SlowModeSeconds {
		t.Fatalf("cached group mismatch: %#v", cached)
	}
}

package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"groupflow/backend/internal/config"
)

func TestProcessMessageCommitsAfterSuccessfulRetry(t *testing.T) {
	attempts := 0
	commits := 0
	consumer := &Consumer{cfg: config.Config{}, log: zap.NewNop()}
	consumer.handleMessage = func(ctx context.Context, payload []byte) error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary push failure")
		}
		return nil
	}
	consumer.commitMessage = func(ctx context.Context, msg kafka.Message) error {
		commits++
		return nil
	}

	err := consumer.processMessage(context.Background(), kafka.Message{Value: []byte(`{"eventType":"group_message_created"}`)})
	if err != nil {
		t.Fatalf("expected processMessage success, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if commits != 1 {
		t.Fatalf("expected 1 commit, got %d", commits)
	}
}

func TestProcessMessageDoesNotCommitAfterExhaustedRetries(t *testing.T) {
	attempts := 0
	commits := 0
	consumer := &Consumer{cfg: config.Config{}, log: zap.NewNop()}
	consumer.handleMessage = func(ctx context.Context, payload []byte) error {
		attempts++
		return errors.New("redis unavailable")
	}
	consumer.commitMessage = func(ctx context.Context, msg kafka.Message) error {
		commits++
		return nil
	}

	err := consumer.processMessage(context.Background(), kafka.Message{Value: []byte(`{"eventType":"group_message_created"}`)})
	if err == nil {
		t.Fatalf("expected processMessage failure")
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if commits != 0 {
		t.Fatalf("expected 0 commits, got %d", commits)
	}
}

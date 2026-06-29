package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"groupflow/backend/internal/config"
	"groupflow/backend/internal/domain"
	"groupflow/backend/internal/repo"
)

type Consumer struct {
	cfg    config.Config
	repo   *repo.Repository
	redis  *redis.Client
	log    *zap.Logger
	reader *kafka.Reader
	http   *http.Client
}

type messageEvent struct {
	EventID   string         `json:"eventId"`
	EventType string         `json:"eventType"`
	GroupID   int64          `json:"groupId"`
	GroupType string         `json:"groupType"`
	MessageID string         `json:"messageId"`
	Sequence  int64          `json:"sequence"`
	Payload   domain.Message `json:"payload"`
}

func New(cfg config.Config, repo *repo.Repository, redis *redis.Client, log *zap.Logger) *Consumer {
	return &Consumer{cfg: cfg, repo: repo, redis: redis, log: log, http: &http.Client{Timeout: 5 * time.Second}, reader: kafka.NewReader(kafka.ReaderConfig{Brokers: cfg.KafkaBrokers, Topic: cfg.KafkaTopic, GroupID: cfg.KafkaGroup, MinBytes: 1, MaxBytes: 10e6, CommitInterval: time.Second})}
}

func (c *Consumer) Run(ctx context.Context) error {
	if !c.cfg.KafkaEnabled {
		c.log.Info("delivery disabled: kafka disabled")
		<-ctx.Done()
		return nil
	}
	c.log.Info("delivery consumer started", zap.Strings("brokers", c.cfg.KafkaBrokers), zap.String("topic", c.cfg.KafkaTopic))
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.log.Warn("fetch message failed", zap.Error(err))
			continue
		}
		if err := c.handle(ctx, m.Value); err != nil {
			c.log.Error("delivery handle failed", zap.Error(err))
		}
		_ = c.reader.CommitMessages(ctx, m)
	}
}

func (c *Consumer) Close() error {
	if c.reader != nil {
		return c.reader.Close()
	}
	return nil
}

func (c *Consumer) handle(ctx context.Context, b []byte) error {
	var evt messageEvent
	if err := json.Unmarshal(b, &evt); err != nil {
		return err
	}
	msg := &evt.Payload
	if msg.MessageID == "" {
		m, err := c.repo.FindMessageByID(ctx, evt.MessageID)
		if err != nil {
			return err
		}
		msg = m
	}
	batchSize := 500
	var cursor int64
	for {
		memberIDs, next, err := c.repo.ListActiveMemberIDs(ctx, evt.GroupID, cursor, 1000)
		if err != nil {
			return err
		}
		online := c.filterOnline(ctx, memberIDs)
		for i := 0; i < len(online); i += batchSize {
			end := i + batchSize
			if end > len(online) {
				end = len(online)
			}
			if err := c.push(ctx, online[i:end], msg); err != nil {
				c.log.Warn("push batch failed", zap.Error(err), zap.Int("size", end-i), zap.Int64("groupId", evt.GroupID))
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return nil
}

// filterOnline filters the userIDs and returns only those who are online.
func (c *Consumer) filterOnline(ctx context.Context, userIDs []int64) []int64 {
	if c.redis == nil {
		return userIDs
	}
	pipe := c.redis.Pipeline()
	cmds := make([]*redis.StringCmd, 0, len(userIDs))
	for _, uid := range userIDs {
		cmds = append(cmds, pipe.Get(ctx, fmt.Sprintf("online:user:%d", uid)))
	}
	_, _ = pipe.Exec(ctx)
	online := make([]int64, 0, len(userIDs))
	for i, cmd := range cmds {
		if cmd.Err() == nil && cmd.Val() != "" {
			online = append(online, userIDs[i])
		}
	}
	return online
}

func (c *Consumer) push(ctx context.Context, userIDs []int64, msg *domain.Message) error {
	if len(userIDs) == 0 {
		return nil
	}
	data, _ := json.Marshal(msg)
	body, _ := json.Marshal(map[string]any{"userIds": userIDs, "type": "group_message_receive", "data": json.RawMessage(data)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.InternalPushURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("push status %s for users=%s", resp.Status, strconv.Itoa(len(userIDs)))
	}
	return nil
}

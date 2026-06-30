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
	"groupflow/backend/pkg/logx"
)

type Consumer struct {
	cfg    config.Config
	repo   *repo.Repository
	redis  *redis.Client
	log    *zap.Logger
	reader *kafka.Reader
	http   *http.Client
}

const (
	GROUP_MESSAGE_CREATED  = "group_message_created"
	GROUP_MESSAGE_RECALLED = "group_message_recalled"
)

type groupEvent struct {
	EventID   string          `json:"eventId"`
	EventType string          `json:"eventType"`
	TraceID   string          `json:"traceId"`
	GroupID   int64           `json:"groupId"`
	GroupType string          `json:"groupType"`
	MessageID string          `json:"messageId"`
	Sequence  int64           `json:"sequence"`
	Payload   json.RawMessage `json:"payload"`
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
	var evt groupEvent
	if err := json.Unmarshal(b, &evt); err != nil {
		return err
	}
	// 还原生产端写入事件的 traceId，使消费/投递日志与上游消息发送归并到同一链路。
	ctx = logx.WithTrace(ctx, logx.Trace{TraceID: evt.TraceID})
	logx.From(ctx).Info("kafka_consume_success",
		zap.String("event", "kafka_consume_success"), zap.String("topic", c.cfg.KafkaTopic),
		zap.String("consumerGroup", c.cfg.KafkaGroup), zap.String("eventId", evt.EventID),
		zap.String("eventType", evt.EventType), zap.Int64("groupId", evt.GroupID), zap.String("messageId", evt.MessageID))
	switch evt.EventType {
	case GROUP_MESSAGE_CREATED:
		var msg domain.Message
		if len(evt.Payload) > 0 {
			_ = json.Unmarshal(evt.Payload, &msg)
		}
		if msg.MessageID == "" {
			m, err := c.repo.FindMessageByID(ctx, evt.GroupID, evt.MessageID)
			if err != nil {
				return err
			}
			msg = *m
		}
		return c.fanout(ctx, evt, "group_message_receive", &msg)
	case GROUP_MESSAGE_RECALLED:
		var payload any
		_ = json.Unmarshal(evt.Payload, &payload)
		return c.fanout(ctx, evt, "group_message_recalled", payload)
	default:
		logx.From(ctx).Info("ignore_unknown_event", zap.String("event", "ignore_unknown_event"), zap.String("eventType", evt.EventType), zap.Int64("groupId", evt.GroupID))
		return nil
	}
}

// fanout 将 payload 投递给群内全部在线成员，并聚合 fanout/在线/成功/失败/路由缺失等计数用于大群投递排查。
func (c *Consumer) fanout(ctx context.Context, evt groupEvent, wsType string, payload any) error {
	groupID := evt.GroupID
	start := time.Now()
	batchSize := 500
	var cursor int64
	var fanoutCount, onlineCount, successCount, failedCount, notFoundCount int
	urlCache := map[string]string{}
	for {
		memberIDs, next, err := c.repo.ListActiveMemberIDs(ctx, groupID, cursor, 1000)
		if err != nil {
			return err
		}
		fanoutCount += len(memberIDs)
		// 按用户所在 WS 节点（online:user→serverId）分组，分别投递到各节点的内部推送地址，支持多节点部署。
		routes := c.resolveOnlineRoutes(ctx, memberIDs)
		pageOnline := 0
		for serverID, users := range routes {
			pageOnline += len(users)
			url, ok := urlCache[serverID]
			if !ok {
				url = c.pushURL(ctx, serverID)
				urlCache[serverID] = url
			}
			for i := 0; i < len(users); i += batchSize {
				end := i + batchSize
				if end > len(users) {
					end = len(users)
				}
				target := users[i:end]
				pushed, err := c.push(ctx, url, target, wsType, payload)
				if err != nil {
					failedCount += len(target)
					logx.From(ctx).Warn("delivery_push_failed",
						zap.String("event", "delivery_push_failed"), zap.Int64("groupId", groupID),
						zap.String("messageId", evt.MessageID), zap.Int64("sequence", evt.Sequence),
						zap.String("serverId", serverID), zap.String("pushUrl", url),
						zap.Int("targetUserCount", len(target)),
						zap.String("reason", err.Error()), zap.Bool("retryable", true))
					continue
				}
				successCount += pushed
				failedCount += len(target) - pushed
				logx.From(ctx).Info("delivery_push_task",
					zap.String("event", "delivery_push_task"), zap.Int64("groupId", groupID),
					zap.String("messageId", evt.MessageID), zap.Int64("sequence", evt.Sequence),
					zap.String("serverId", serverID), zap.Int("targetUserCount", len(target)),
					zap.Int("successCount", pushed), zap.Int("failedCount", len(target)-pushed))
			}
		}
		onlineCount += pageOnline
		notFoundCount += len(memberIDs) - pageOnline
		if next == 0 {
			break
		}
		cursor = next
	}
	logx.From(ctx).Info("group_delivery_completed",
		zap.String("event", "group_delivery_completed"), zap.Int64("groupId", groupID),
		zap.String("groupType", evt.GroupType), zap.String("messageId", evt.MessageID),
		zap.Int64("sequence", evt.Sequence), zap.Int("fanoutCount", fanoutCount),
		zap.Int("onlineCount", onlineCount), zap.Int("successCount", successCount),
		zap.Int("failedCount", failedCount), zap.Int("notFoundCount", notFoundCount),
		zap.Int64("durationMs", time.Since(start).Milliseconds()))
	return nil
}

// resolveOnlineRoutes 按 online:user:%d 记录的 serverId 把在线用户分组到各自所在的 WS 节点；
// 离线用户（无记录）被跳过。无 Redis 时退化为单节点（key ""，使用默认内部推送地址）。
func (c *Consumer) resolveOnlineRoutes(ctx context.Context, userIDs []int64) map[string][]int64 {
	routes := map[string][]int64{}
	if c.redis == nil {
		routes[""] = append([]int64(nil), userIDs...)
		return routes
	}
	pipe := c.redis.Pipeline()
	cmds := make([]*redis.StringCmd, 0, len(userIDs))
	for _, uid := range userIDs {
		cmds = append(cmds, pipe.Get(ctx, fmt.Sprintf("online:user:%d", uid)))
	}
	_, _ = pipe.Exec(ctx)
	for i, cmd := range cmds {
		serverID, err := cmd.Result()
		if err != nil || serverID == "" {
			continue
		}
		routes[serverID] = append(routes[serverID], userIDs[i])
	}
	return routes
}

// pushURL 解析某 WS 节点的内部推送地址（server:%s:push_url）；缺失时回落到配置的默认地址（单节点）。
func (c *Consumer) pushURL(ctx context.Context, serverID string) string {
	if c.redis != nil && serverID != "" {
		if url, err := c.redis.Get(ctx, fmt.Sprintf("server:%s:push_url", serverID)).Result(); err == nil && url != "" {
			return url
		}
	}
	return c.cfg.InternalPushURL
}

// push 调用指定 WebSocket 节点的内部推送接口，返回实际推送成功的连接数。透传 X-Trace-Id 维持链路。
func (c *Consumer) push(ctx context.Context, url string, userIDs []int64, wsType string, payload any) (int, error) {
	if len(userIDs) == 0 {
		return 0, nil
	}
	data, _ := json.Marshal(payload)
	body, _ := json.Marshal(map[string]any{"userIds": userIDs, "type": wsType, "data": json.RawMessage(data)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if tid := logx.TraceIDFrom(ctx); tid != "" {
		req.Header.Set("X-Trace-Id", tid)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return 0, fmt.Errorf("push status %s for users=%s", resp.Status, strconv.Itoa(len(userIDs)))
	}
	var out struct {
		Data struct {
			Pushed int `json:"pushed"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return len(userIDs), nil
	}
	return out.Data.Pushed, nil
}

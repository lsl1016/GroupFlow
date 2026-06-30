package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
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
	cfg             config.Config
	repo            *repo.Repository
	redis           *redis.Client
	log             *zap.Logger
	reader          *kafka.Reader
	http            *http.Client
	routeResolver   func(ctx context.Context, userIDs []int64) (map[string][]string, int, error)
	pushURLResolver func(ctx context.Context, serverID string) (string, error)
	handleMessage   func(ctx context.Context, payload []byte) error
	commitMessage   func(ctx context.Context, msg kafka.Message) error
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

type cleanupCommand struct {
	kind   string
	key    string
	member string
}

func (c *Consumer) RunCleanup(ctx context.Context) {
	if c.redis == nil {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.cleanupDeadServers(ctx)
		}
	}
}

// cleanupDeadServers 扫描已注册的 WS 节点，对 heartbeat 已过期（死节点）的 server
// 清理其遗留的连接路由 key，避免脏数据无限堆积。
func (c *Consumer) cleanupDeadServers(ctx context.Context) {
	serverIDs, err := c.scanActiveServerIDs(ctx)
	if err != nil {
		c.log.Warn("cleanup_scan_servers_failed", zap.String("event", "cleanup_scan_servers_failed"), zap.Error(err))
		return
	}
	for _, serverID := range serverIDs {
		alive, err := c.isServerAlive(ctx, serverID)
		if err != nil {
			continue
		}
		if alive {
			continue
		}
		if err := c.cleanupServer(ctx, serverID); err != nil {
			c.log.Warn("cleanup_server_failed", zap.String("event", "cleanup_server_failed"), zap.String("serverId", serverID), zap.Error(err))
			continue
		}
		c.log.Info("cleanup_dead_server", zap.String("event", "cleanup_dead_server"), zap.String("serverId", serverID))
	}
}

// scanActiveServerIDs 通过 server:*:connections 推导出当前注册过的 WS 节点 ID。
func (c *Consumer) scanActiveServerIDs(ctx context.Context) ([]string, error) {
	var serverIDs []string
	seen := map[string]struct{}{}
	var cursor uint64
	for {
		keys, next, err := c.redis.Scan(ctx, cursor, "server:*:connections", 100).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			serverID := strings.TrimSuffix(strings.TrimPrefix(key, "server:"), ":connections")
			if serverID == "" {
				continue
			}
			if _, ok := seen[serverID]; ok {
				continue
			}
			seen[serverID] = struct{}{}
			serverIDs = append(serverIDs, serverID)
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return serverIDs, nil
}

func (c *Consumer) isServerAlive(ctx context.Context, serverID string) (bool, error) {
	n, err := c.redis.Exists(ctx, fmt.Sprintf("server:%s:heartbeat", serverID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// cleanupServer 读取死节点遗留的 connection 集合，删除每个连接的路由 key，并从用户连接集合中移除。
func (c *Consumer) cleanupServer(ctx context.Context, serverID string) error {
	connectionIDs, err := c.redis.SMembers(ctx, fmt.Sprintf("server:%s:connections", serverID)).Result()
	if err != nil {
		return err
	}
	connections := make(map[string]int64, len(connectionIDs))
	for _, connectionID := range connectionIDs {
		if connectionID == "" {
			continue
		}
		userID, err := c.redis.Get(ctx, fmt.Sprintf("connection:%s:user", connectionID)).Int64()
		if err != nil {
			userID = 0
		}
		connections[connectionID] = userID
	}
	return c.applyCleanupCommands(ctx, buildCleanupCommands(serverID, connections))
}

func (c *Consumer) applyCleanupCommands(ctx context.Context, commands []cleanupCommand) error {
	pipe := c.redis.Pipeline()
	for _, cmd := range commands {
		switch cmd.kind {
		case "del":
			pipe.Del(ctx, cmd.key)
		case "srem":
			pipe.SRem(ctx, cmd.key, cmd.member)
		}
	}
	_, err := pipe.Exec(ctx)
	return err
}

func New(cfg config.Config, repo *repo.Repository, redis *redis.Client, log *zap.Logger) *Consumer {
	c := &Consumer{cfg: cfg, repo: repo, redis: redis, log: log, http: &http.Client{Timeout: 5 * time.Second}, reader: kafka.NewReader(kafka.ReaderConfig{Brokers: cfg.KafkaBrokers, Topic: cfg.KafkaTopic, GroupID: cfg.KafkaGroup, MinBytes: 1, MaxBytes: 10e6, CommitInterval: time.Second})}
	c.routeResolver = c.resolveOnlineRoutes
	c.pushURLResolver = c.pushURL
	c.handleMessage = c.handle
	c.commitMessage = func(ctx context.Context, msg kafka.Message) error { return c.reader.CommitMessages(ctx, msg) }
	return c
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
		if err := c.processMessage(ctx, m); err != nil {
			c.log.Error("delivery handle failed", zap.Error(err))
		}
	}
}

func (c *Consumer) processMessage(ctx context.Context, msg kafka.Message) error {
	handler := c.handleMessage
	if handler == nil {
		handler = c.handle
	}
	var err error
	backoffs := []time.Duration{0, 50 * time.Millisecond, 100 * time.Millisecond}
	for attempt, backoff := range backoffs {
		if backoff > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
		err = handler(ctx, msg.Value)
		if err == nil {
			committer := c.commitMessage
			if committer == nil {
				committer = func(ctx context.Context, msg kafka.Message) error { return c.reader.CommitMessages(ctx, msg) }
			}
			return committer(ctx, msg)
		}
		if attempt < len(backoffs)-1 {
			c.log.Warn("delivery handle retry", zap.Int("attempt", attempt+1), zap.Error(err))
		}
	}
	return err
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
	case "group_member_kicked", "group_join_request_created", "group_join_request_approved", "group_join_request_rejected":
		return c.handleStructuredEvent(ctx, evt)
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
	var fanoutCount, onlineConnectionCount, successCount, failedCount, unresolvedCount int
	urlCache := map[string]string{}
	for {
		memberIDs, next, err := c.repo.ListActiveMemberIDs(ctx, groupID, cursor, 1000)
		if err != nil {
			return err
		}
		fanoutCount += len(memberIDs)
		routes, attemptedConnections, err := c.resolveOnlineRoutes(ctx, memberIDs)
		if err != nil {
			return err
		}
		pageOnlineConnections := 0
		for serverID, connectionIDs := range routes {
			pageOnlineConnections += len(connectionIDs)
			url, ok := urlCache[serverID]
			if !ok {
				pushURLResolver := c.pushURLResolver
				if pushURLResolver == nil {
					pushURLResolver = c.pushURL
				}
				url, err = pushURLResolver(ctx, serverID)
				if err != nil {
					return err
				}
				urlCache[serverID] = url
			}
			for i := 0; i < len(connectionIDs); i += batchSize {
				end := i + batchSize
				if end > len(connectionIDs) {
					end = len(connectionIDs)
				}
				target := connectionIDs[i:end]
				pushed, err := c.push(ctx, url, target, wsType, payload)
				if err != nil {
					failedCount += len(target)
					logx.From(ctx).Warn("delivery_push_failed",
						zap.String("event", "delivery_push_failed"), zap.Int64("groupId", groupID),
						zap.String("messageId", evt.MessageID), zap.Int64("sequence", evt.Sequence),
						zap.String("serverId", serverID), zap.String("pushUrl", url),
						zap.Int("targetConnectionCount", len(target)),
						zap.String("reason", err.Error()), zap.Bool("retryable", true))
					continue
				}
				successCount += pushed
				failedCount += len(target) - pushed
				logx.From(ctx).Info("delivery_push_task",
					zap.String("event", "delivery_push_task"), zap.Int64("groupId", groupID),
					zap.String("messageId", evt.MessageID), zap.Int64("sequence", evt.Sequence),
					zap.String("serverId", serverID), zap.Int("targetConnectionCount", len(target)),
					zap.Int("successCount", pushed), zap.Int("failedCount", len(target)-pushed))
			}
		}
		onlineConnectionCount += pageOnlineConnections
		unresolvedCount += attemptedConnections - pageOnlineConnections
		if next == 0 {
			break
		}
		cursor = next
	}
	logx.From(ctx).Info("group_delivery_completed",
		zap.String("event", "group_delivery_completed"), zap.Int64("groupId", groupID),
		zap.String("groupType", evt.GroupType), zap.String("messageId", evt.MessageID),
		zap.Int64("sequence", evt.Sequence), zap.Int("fanoutCount", fanoutCount),
		zap.Int("onlineConnectionCount", onlineConnectionCount), zap.Int("successCount", successCount),
		zap.Int("failedCount", failedCount), zap.Int("unresolvedConnectionCount", unresolvedCount),
		zap.Int64("durationMs", time.Since(start).Milliseconds()))
	return nil
}

// resolveOnlineRoutes 通过用户在线连接集合和 connection->server 路由，把在线连接按 WS 节点分组。
// 返回值为 serverId -> []connectionId；attemptedConnections 表示扫描到的连接总数，用于统计未解析成功的脏连接。
func (c *Consumer) resolveOnlineRoutes(ctx context.Context, userIDs []int64) (map[string][]string, int, error) {
	routes := map[string][]string{}
	if c.redis == nil {
		return routes, 0, nil
	}
	setPipe := c.redis.Pipeline()
	setCmds := make([]*redis.StringSliceCmd, 0, len(userIDs))
	for _, uid := range userIDs {
		setCmds = append(setCmds, setPipe.SMembers(ctx, fmt.Sprintf("online:user:%d:connections", uid)))
	}
	if _, err := setPipe.Exec(ctx); err != nil {
		return nil, 0, fmt.Errorf("resolve user connections: %w", err)
	}

	connectionIDsByUser := make([][]string, 0, len(setCmds))
	for _, cmd := range setCmds {
		connectionIDs, err := cmd.Result()
		if err != nil {
			connectionIDsByUser = append(connectionIDsByUser, nil)
			continue
		}
		connectionIDsByUser = append(connectionIDsByUser, connectionIDs)
	}
	uniqueConnectionIDs := flattenUniqueConnectionIDs(connectionIDsByUser)
	if len(uniqueConnectionIDs) == 0 {
		return routes, 0, nil
	}

	routePipe := c.redis.Pipeline()
	routeCmds := make([]*redis.StringCmd, 0, len(uniqueConnectionIDs))
	for _, connectionID := range uniqueConnectionIDs {
		routeCmds = append(routeCmds, routePipe.Get(ctx, fmt.Sprintf("connection:%s:server", connectionID)))
	}
	_, _ = routePipe.Exec(ctx)
	connectionToServer := make(map[string]string, len(uniqueConnectionIDs))
	for i, cmd := range routeCmds {
		serverID, err := cmd.Result()
		if err != nil || serverID == "" {
			continue
		}
		connectionToServer[uniqueConnectionIDs[i]] = serverID
	}
	return groupConnectionsByServer(uniqueConnectionIDs, connectionToServer), len(uniqueConnectionIDs), nil
}

func flattenUniqueConnectionIDs(connectionIDsByUser [][]string) []string {
	uniqueConnectionIDs := make([]string, 0)
	seen := make(map[string]struct{})
	for _, connectionIDs := range connectionIDsByUser {
		for _, connectionID := range connectionIDs {
			if connectionID == "" {
				continue
			}
			if _, ok := seen[connectionID]; ok {
				continue
			}
			seen[connectionID] = struct{}{}
			uniqueConnectionIDs = append(uniqueConnectionIDs, connectionID)
		}
	}
	return uniqueConnectionIDs
}

func groupConnectionsByServer(connectionIDs []string, connectionToServer map[string]string) map[string][]string {
	routes := make(map[string][]string)
	for _, connectionID := range connectionIDs {
		serverID := connectionToServer[connectionID]
		if serverID == "" {
			continue
		}
		routes[serverID] = append(routes[serverID], connectionID)
	}
	return routes
}

func (c *Consumer) handleStructuredEvent(ctx context.Context, evt groupEvent) error {
	var payload struct {
		TargetUserIDs []int64 `json:"targetUserIds"`
		Body          any     `json:"body"`
	}
	if err := json.Unmarshal(evt.Payload, &payload); err != nil {
		return fmt.Errorf("decode structured event payload: %w", err)
	}
	if len(payload.TargetUserIDs) == 0 {
		return nil
	}
	resolver := c.routeResolver
	if resolver == nil {
		resolver = c.resolveOnlineRoutes
	}
	routes, _, err := resolver(ctx, payload.TargetUserIDs)
	if err != nil {
		return err
	}
	pushURLResolver := c.pushURLResolver
	if pushURLResolver == nil {
		pushURLResolver = c.pushURL
	}
	const batchSize = 500
	for serverID, connectionIDs := range routes {
		url, err := pushURLResolver(ctx, serverID)
		if err != nil {
			return err
		}
		for i := 0; i < len(connectionIDs); i += batchSize {
			end := i + batchSize
			if end > len(connectionIDs) {
				end = len(connectionIDs)
			}
			if _, err := c.push(ctx, url, connectionIDs[i:end], evt.EventType, payload.Body); err != nil {
				return err
			}
		}
	}
	return nil
}

func buildCleanupCommands(serverID string, connections map[string]int64) []cleanupCommand {
	connectionIDs := make([]string, 0, len(connections))
	for connectionID := range connections {
		connectionIDs = append(connectionIDs, connectionID)
	}
	sort.Strings(connectionIDs)
	commands := make([]cleanupCommand, 0, len(connections)*3+1)
	for _, connectionID := range connectionIDs {
		userID := connections[connectionID]
		commands = append(commands,
			cleanupCommand{kind: "del", key: fmt.Sprintf("connection:%s:server", connectionID)},
			cleanupCommand{kind: "del", key: fmt.Sprintf("connection:%s:user", connectionID)},
			cleanupCommand{kind: "srem", key: fmt.Sprintf("online:user:%d:connections", userID), member: connectionID},
		)
	}
	commands = append(commands, cleanupCommand{kind: "del", key: fmt.Sprintf("server:%s:connections", serverID)})
	return commands
}

func (c *Consumer) pushURL(ctx context.Context, serverID string) (string, error) {
	if serverID == "" {
		if c.cfg.InternalPushURL == "" {
			return "", fmt.Errorf("missing default internal push url")
		}
		return c.cfg.InternalPushURL, nil
	}
	if c.redis == nil {
		return "", fmt.Errorf("missing redis client for server %s push url lookup", serverID)
	}
	url, err := c.redis.Get(ctx, fmt.Sprintf("server:%s:push_url", serverID)).Result()
	if err != nil || url == "" {
		return "", fmt.Errorf("missing push url for server %s", serverID)
	}
	return url, nil
}

// push 调用指定 WebSocket 节点的内部推送接口，返回实际推送成功的连接数。透传 X-Trace-Id 维持链路。
func (c *Consumer) push(ctx context.Context, url string, connectionIDs []string, wsType string, payload any) (int, error) {
	if len(connectionIDs) == 0 {
		return 0, nil
	}
	data, _ := json.Marshal(payload)
	body, _ := json.Marshal(map[string]any{"connectionIds": connectionIDs, "type": wsType, "data": json.RawMessage(data)})
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
		return 0, fmt.Errorf("push status %s for connections=%s", resp.Status, strconv.Itoa(len(connectionIDs)))
	}
	var out struct {
		Data struct {
			Pushed int `json:"pushed"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("decode internal push response: %w", err)
	}
	return out.Data.Pushed, nil
}

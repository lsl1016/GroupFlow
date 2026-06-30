package search

import (
	"context"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

// DefaultMapping 返回 group_message 索引的默认 mapping。content 使用 IK 中文分词（建索引 ik_max_word，
// 查询 ik_smart）；按 group_id 路由；created_at 以 epoch 毫秒存储。需 ES 预装 analysis-ik 插件。
func DefaultMapping() map[string]any {
	return map[string]any{
		"settings": map[string]any{
			"number_of_shards":   3,
			"number_of_replicas": 1,
		},
		"mappings": map[string]any{
			"properties": map[string]any{
				"message_id":   map[string]any{"type": "keyword"},
				"group_id":     map[string]any{"type": "long"},
				"sequence":     map[string]any{"type": "long"},
				"sender_id":    map[string]any{"type": "long"},
				"sender_name":  map[string]any{"type": "text", "fields": map[string]any{"keyword": map[string]any{"type": "keyword"}}},
				"content":      map[string]any{"type": "text", "analyzer": "ik_max_word", "search_analyzer": "ik_smart"},
				"message_type": map[string]any{"type": "keyword"},
				"status":       map[string]any{"type": "keyword"},
				"created_at":   map[string]any{"type": "date", "format": "epoch_millis"},
			},
		},
	}
}

// RunConsumer 订阅消息 topic（独立 consumer group）并将事件应用到索引，直到 ctx 取消。
// 处理失败不提交 offset，依赖 Kafka 重投递；以 messageId 幂等写入，重放安全。
func RunConsumer(ctx context.Context, brokers []string, topic, group string, idx Indexer, log *zap.Logger) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		Topic:    topic,
		GroupID:  group,
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	defer reader.Close()
	log.Info("es_indexer_started", zap.String("event", "es_indexer_started"), zap.Strings("brokers", brokers), zap.String("topic", topic), zap.String("consumerGroup", group))
	for {
		// FetchMessage 不自动提交 offset，仅在处理成功后 CommitMessages，保证至少一次且失败可重投。
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Warn("es_indexer_read_failed", zap.String("event", "es_indexer_read_failed"), zap.Error(err))
			continue
		}
		if err := HandleEvent(ctx, msg.Value, idx); err != nil {
			log.Error("es_indexer_handle_failed", zap.String("event", "es_indexer_handle_failed"), zap.Error(err))
			time.Sleep(time.Second)
			continue
		}
		if err := reader.CommitMessages(ctx, msg); err != nil {
			log.Warn("es_indexer_commit_failed", zap.String("event", "es_indexer_commit_failed"), zap.Error(err))
		}
	}
}

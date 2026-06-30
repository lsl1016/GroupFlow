package main

import (
	"context"

	"go.uber.org/zap"

	"groupflow/backend/internal/config"
	"groupflow/backend/internal/infra"
	"groupflow/backend/internal/repo"
	"groupflow/backend/internal/search"
)

// es-backfill 将 MySQL 中的存量正常消息按主键游标批量回填到 Elasticsearch。
// 幂等：以 messageId 为 _id，可重复执行。建议回填前临时调大 ES refresh_interval。
func main() {
	cfg := config.Load()
	log := infra.NewLogger(cfg, "es-backfill")
	defer log.Sync()

	if !cfg.ESEnabled {
		log.Fatal("es_disabled", zap.String("event", "es_disabled"), zap.String("hint", "set ES_ENABLED=true to run backfill"))
	}

	db, err := infra.NewDB(cfg)
	if err != nil {
		log.Fatal("mysql_connect_failed", zap.String("event", "mysql_connect_failed"), zap.Error(err))
	}
	r := repo.New(db, cfg.MessageShardCount)
	client := search.NewClient(cfg.ESAddrs, cfg.ESIndex)
	ctx := context.Background()

	if err := client.EnsureIndex(ctx, search.DefaultMapping()); err != nil {
		log.Warn("es_ensure_index_failed", zap.String("event", "es_ensure_index_failed"), zap.Error(err))
	}

	const batch = 1000
	var afterID int64
	var total int
	for {
		msgs, err := r.ScanMessagesAfterID(ctx, afterID, batch)
		if err != nil {
			log.Fatal("scan_failed", zap.String("event", "scan_failed"), zap.Int64("afterId", afterID), zap.Error(err))
		}
		if len(msgs) == 0 {
			break
		}
		body := search.BuildBulkNDJSON(cfg.ESIndex, msgs)
		if _, err := client.Bulk(ctx, body); err != nil {
			log.Fatal("bulk_failed", zap.String("event", "bulk_failed"), zap.Int64("afterId", afterID), zap.Error(err))
		}
		afterID = msgs[len(msgs)-1].ID
		total += len(msgs)
		log.Info("backfill_progress", zap.String("event", "backfill_progress"), zap.Int("total", total), zap.Int64("lastId", afterID))
	}
	log.Info("backfill_done", zap.String("event", "backfill_done"), zap.Int("total", total))
}

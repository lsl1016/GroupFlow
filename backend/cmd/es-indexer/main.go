package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"groupflow/backend/internal/config"
	"groupflow/backend/internal/infra"
	"groupflow/backend/internal/search"
)

func main() {
	cfg := config.Load()
	log := infra.NewLogger(cfg, "es-indexer-service")
	defer log.Sync()
	log.Info("es_indexer_starting", zap.String("event", "es_indexer_starting"), zap.String("env", cfg.Env), zap.Bool("esEnabled", cfg.ESEnabled), zap.Strings("esAddrs", cfg.ESAddrs), zap.String("esIndex", cfg.ESIndex))

	if !cfg.ESEnabled {
		log.Fatal("es_disabled", zap.String("event", "es_disabled"), zap.String("hint", "set ES_ENABLED=true to run the indexer"))
	}

	client := search.NewClient(cfg.ESAddrs, cfg.ESIndex)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动时确保索引存在（含 IK 中文分词 mapping）；失败仅告警，避免 ES 短暂不可用导致进程退出。
	if err := client.EnsureIndex(ctx, search.DefaultMapping()); err != nil {
		log.Warn("es_ensure_index_failed", zap.String("event", "es_ensure_index_failed"), zap.Error(err))
	}

	// 独立 consumer group 订阅消息 topic，与投递消费者互不影响。
	go search.RunConsumer(ctx, cfg.KafkaBrokers, cfg.KafkaTopic, cfg.ESIndexerGroup, client, log)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("es_indexer_health_server_started", zap.String("event", "es_indexer_health_server_started"), zap.String("addr", cfg.HTTPAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("health_server_failed", zap.String("event", "health_server_failed"), zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("es_indexer_shutting_down", zap.String("event", "es_indexer_shutting_down"), zap.String("signal", sig.String()))
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("es_indexer_shutdown_error", zap.String("event", "es_indexer_shutdown_error"), zap.Error(err))
	}
	log.Info("es_indexer_stopped", zap.String("event", "es_indexer_stopped"))
}

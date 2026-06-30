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
	"groupflow/backend/internal/delivery"
	"groupflow/backend/internal/infra"
	"groupflow/backend/internal/repo"
)

func main() {
	cfg := config.Load()
	log := infra.NewLogger(cfg, "delivery-service")
	defer log.Sync()
	log.Info("delivery_starting", zap.String("event", "delivery_starting"), zap.String("env", cfg.Env), zap.String("httpAddr", cfg.HTTPAddr), zap.Bool("kafkaEnabled", cfg.KafkaEnabled), zap.String("topic", cfg.KafkaTopic), zap.String("consumerGroup", cfg.KafkaGroup))

	db, err := infra.NewDB(cfg)
	if err != nil {
		log.Fatal("mysql_connect_failed", zap.String("event", "mysql_connect_failed"), zap.Error(err))
	}
	log.Info("mysql_connected", zap.String("event", "mysql_connected"))
	redis := infra.NewRedis(cfg)
	log.Info("redis_initialized", zap.String("event", "redis_initialized"), zap.String("addr", cfg.RedisAddr))
	repository := repo.New(db)
	consumer := delivery.New(cfg, repository, redis, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := consumer.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error("delivery_consumer_stopped", zap.String("event", "delivery_consumer_stopped"), zap.Error(err))
		}
	}()
	// Start health server
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("delivery_health_server_started", zap.String("event", "delivery_health_server_started"), zap.String("addr", cfg.HTTPAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("health_server_failed", zap.String("event", "health_server_failed"), zap.Error(err))
		}
	}()
	// Wait for termination signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("delivery_shutting_down", zap.String("event", "delivery_shutting_down"), zap.String("signal", sig.String()))
	cancel()
	_ = consumer.Close()
	// Shutdown health server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("delivery_shutdown_error", zap.String("event", "delivery_shutdown_error"), zap.Error(err))
	}
	log.Info("delivery_stopped", zap.String("event", "delivery_stopped"))
}

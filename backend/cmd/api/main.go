// @title GroupFlow / 群流 API 文档
// @version 1.1.0
// @description 面向大群与高并发场景的一期 MVP：群、成员、消息、@提醒、公告、审批、撤回、WebSocket。
// @BasePath /api/v1
// @schemes http https
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"groupflow/backend/internal/api"
	"groupflow/backend/internal/config"
	"groupflow/backend/internal/infra"
	"groupflow/backend/internal/repo"
	"groupflow/backend/internal/service"
	"groupflow/backend/internal/ws"
)

func main() {
	cfg := config.Load()
	log := infra.NewLogger(cfg, "api-service")
	defer log.Sync()
	log.Info("api_starting", zap.String("event", "api_starting"), zap.String("env", cfg.Env), zap.String("httpAddr", cfg.HTTPAddr), zap.String("serverId", cfg.ServerID), zap.Bool("kafkaEnabled", cfg.KafkaEnabled))

	db, err := infra.NewDB(cfg)
	if err != nil {
		log.Fatal("mysql_connect_failed", zap.String("event", "mysql_connect_failed"), zap.Error(err))
	}
	log.Info("mysql_connected", zap.String("event", "mysql_connected"))

	redis := infra.NewRedis(cfg)
	log.Info("redis_initialized", zap.String("event", "redis_initialized"), zap.String("addr", cfg.RedisAddr))
	producer := infra.NewKafkaProducer(cfg)
	defer producer.Close()
	log.Info("kafka_producer_initialized", zap.String("event", "kafka_producer_initialized"), zap.Bool("enabled", cfg.KafkaEnabled))

	r := repo.New(db)
	svc := service.New(cfg, r, redis, producer, log)
	hub := ws.NewHub(cfg, redis, log)
	engine := api.NewRouter(cfg, svc, r, hub, log)
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: engine, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("api_started", zap.String("event", "api_started"), zap.String("addr", cfg.HTTPAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("http_server_failed", zap.String("event", "http_server_failed"), zap.Error(err))
		}
	}()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("api_shutting_down", zap.String("event", "api_shutting_down"), zap.String("signal", sig.String()))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error("api_shutdown_error", zap.String("event", "api_shutdown_error"), zap.Error(err))
	}
	log.Info("api_stopped", zap.String("event", "api_stopped"))
}

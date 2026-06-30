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
	// 加载配置并初始化进程级结构化日志（服务名 delivery-service）
	cfg := config.Load()
	log := infra.NewLogger(cfg, "delivery-service")
	defer log.Sync()
	log.Info("delivery_starting", zap.String("event", "delivery_starting"), zap.String("env", cfg.Env), zap.String("httpAddr", cfg.HTTPAddr), zap.Bool("kafkaEnabled", cfg.KafkaEnabled), zap.String("topic", cfg.KafkaTopic), zap.String("consumerGroup", cfg.KafkaGroup))

	// 连接 MySQL（消息补偿查询），失败直接退出
	db, err := infra.NewDB(cfg)
	if err != nil {
		log.Fatal("mysql_connect_failed", zap.String("event", "mysql_connect_failed"), zap.Error(err))
	}
	log.Info("mysql_connected", zap.String("event", "mysql_connected"))
	// 初始化 Redis（查询成员在线状态用于投递过滤）
	redis := infra.NewRedis(cfg)
	log.Info("redis_initialized", zap.String("event", "redis_initialized"), zap.String("addr", cfg.RedisAddr))

	// 创建消费者并在后台启动：消费 Kafka 群消息事件并 fanout 投递到 WebSocket 节点
	repository := repo.New(db, cfg.MessageShardCount)
	consumer := delivery.New(cfg, repository, redis, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := consumer.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error("delivery_consumer_stopped", zap.String("event", "delivery_consumer_stopped"), zap.Error(err))
		}
	}()
	go consumer.RunCleanup(ctx)

	// 启动健康检查 / 指标 HTTP 服务（/health、/metrics）
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

	// 阻塞等待终止信号，收到后取消消费上下文并关闭消费者
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("delivery_shutting_down", zap.String("event", "delivery_shutting_down"), zap.String("signal", sig.String()))
	cancel()
	_ = consumer.Close()

	// 限时优雅关闭健康检查服务
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("delivery_shutdown_error", zap.String("event", "delivery_shutdown_error"), zap.Error(err))
	}
	log.Info("delivery_stopped", zap.String("event", "delivery_stopped"))
}

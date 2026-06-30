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
	"groupflow/backend/internal/search"
	"groupflow/backend/internal/service"
	"groupflow/backend/internal/ws"
)

func main() {
	// 加载配置并初始化进程级结构化日志（服务名 api-service）
	cfg := config.Load()
	log := infra.NewLogger(cfg, "api-service")
	defer log.Sync()
	log.Info("api_starting", zap.String("event", "api_starting"), zap.String("env", cfg.Env), zap.String("httpAddr", cfg.HTTPAddr), zap.String("serverId", cfg.ServerID), zap.Bool("kafkaEnabled", cfg.KafkaEnabled))

	// 连接 MySQL，失败直接退出（核心依赖不可用无法提供服务）
	db, err := infra.NewDB(cfg)
	if err != nil {
		log.Fatal("mysql_connect_failed", zap.String("event", "mysql_connect_failed"), zap.Error(err))
	}
	log.Info("mysql_connected", zap.String("event", "mysql_connected"))

	// 初始化 Redis（在线状态/sequence）与 Kafka 生产者（消息事件投递）
	redis := infra.NewRedis(cfg)
	log.Info("redis_initialized", zap.String("event", "redis_initialized"), zap.String("addr", cfg.RedisAddr))
	producer := infra.NewKafkaProducer(cfg)
	defer producer.Close()
	log.Info("kafka_producer_initialized", zap.String("event", "kafka_producer_initialized"), zap.Bool("enabled", cfg.KafkaEnabled))

	// 组装各层依赖：repo 数据访问 → service 业务逻辑 → ws 连接管理 → router HTTP 路由
	r := repo.New(db, cfg.MessageShardCount)
	svc := service.New(cfg, r, redis, producer, log)
	// 装配 Elasticsearch 搜索后端（关闭时搜索接口返回未启用）。
	if cfg.ESEnabled {
		esClient := search.NewClient(cfg.ESAddrs, cfg.ESIndex)
		svc.SetSearcher(r.ListUserGroupScopes, esClient.Search)
		log.Info("search_enabled", zap.String("event", "search_enabled"), zap.Strings("esAddrs", cfg.ESAddrs), zap.String("esIndex", cfg.ESIndex))
	}
	hub := ws.NewHub(cfg, redis, log)
	engine := api.NewRouter(cfg, svc, r, hub, log)

	// 后台启动 Outbox relay：将消息落库事务中写入的待发事件可靠投递到 Kafka（Kafka 关闭时为 no-op）。
	relayCtx, relayCancel := context.WithCancel(context.Background())
	defer relayCancel()
	go svc.RunOutboxRelay(relayCtx)
	go hub.RunHeartbeat(relayCtx)
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: engine, ReadHeaderTimeout: 10 * time.Second}
	// 后台启动 HTTP 服务，监听失败（非正常关闭）直接退出
	go func() {
		log.Info("api_started", zap.String("event", "api_started"), zap.String("addr", cfg.HTTPAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("http_server_failed", zap.String("event", "http_server_failed"), zap.Error(err))
		}
	}()
	// 阻塞等待 SIGINT/SIGTERM 终止信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("api_shutting_down", zap.String("event", "api_shutting_down"), zap.String("signal", sig.String()))

	// 限时优雅关闭：等待在途请求处理完毕，超时则强制结束
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("api_shutdown_error", zap.String("event", "api_shutdown_error"), zap.Error(err))
	}
	log.Info("api_stopped", zap.String("event", "api_stopped"))
}

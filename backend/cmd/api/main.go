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
	log := infra.NewLogger(cfg.Env)
	defer log.Sync()
	db, err := infra.NewDB(cfg)
	if err != nil {
		log.Fatal("db connect failed", zap.Error(err))
	}
	redis := infra.NewRedis(cfg)
	producer := infra.NewKafkaProducer(cfg)
	defer producer.Close()
	r := repo.New(db)
	svc := service.New(cfg, r, redis, producer, log)
	hub := ws.NewHub(cfg, redis, log)
	engine := api.NewRouter(cfg, svc, r, hub, log)
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: engine, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("api started", zap.String("addr", cfg.HTTPAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("http server failed", zap.Error(err))
		}
	}()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

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
	log := infra.NewLogger(cfg.Env)
	defer log.Sync()
	db, err := infra.NewDB(cfg)
	if err != nil {
		log.Fatal("db connect failed", zap.Error(err))
	}
	redis := infra.NewRedis(cfg)
	repository := repo.New(db)
	consumer := delivery.New(cfg, repository, redis, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := consumer.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error("delivery stopped", zap.Error(err))
		}
	}()
	// Start health server
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("delivery health server started", zap.String("addr", cfg.HTTPAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("health server failed", zap.Error(err))
		}
	}()
	// Wait for termination signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	cancel()
	_ = consumer.Close()
	// Shutdown health server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}

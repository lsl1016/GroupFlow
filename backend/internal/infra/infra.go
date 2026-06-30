package infra

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"groupflow/backend/internal/config"
	"groupflow/backend/pkg/logx"
)

// NewLogger 基于应用配置构造进程级结构化 logger，service 为服务名（如 api-service）。
func NewLogger(cfg config.Config, service string) *zap.Logger {
	return logx.Init(logx.Config{
		Level:          cfg.LogLevel,
		Format:         cfg.LogFormat,
		Output:         cfg.LogOutput,
		Env:            cfg.Env,
		FilePath:       cfg.LogFilePath,
		FileMaxSizeMB:  cfg.LogFileMaxSizeMB,
		FileMaxBackups: cfg.LogFileMaxBackups,
		FileMaxAgeDays: cfg.LogFileMaxAgeDays,
		FileCompress:   cfg.LogFileCompress,
		HTTPSlowMs:     cfg.HTTPSlowMs,
		RedisSlowMs:    cfg.RedisSlowMs,
		MySQLSlowMs:    cfg.MySQLSlowMs,
	}, service)
}

func NewDB(cfg config.Config) (*sql.DB, error) {
	db, err := sql.Open("mysql", cfg.MySQLDSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(80)
	db.SetMaxIdleConns(20)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	return db, nil
}

func NewRedis(cfg config.Config) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB})
}

type KafkaProducer interface {
	Produce(ctx context.Context, key string, value any) error
	Close() error
}

type NoopProducer struct{}

func (NoopProducer) Produce(ctx context.Context, key string, value any) error { return nil }
func (NoopProducer) Close() error                                             { return nil }

type WriterProducer struct{ writer *kafka.Writer }

func NewKafkaProducer(cfg config.Config) KafkaProducer {
	if !cfg.KafkaEnabled {
		return NoopProducer{}
	}
	return &WriterProducer{writer: &kafka.Writer{
		Addr:         kafka.TCP(cfg.KafkaBrokers...),
		Topic:        cfg.KafkaTopic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireOne,
		BatchTimeout: 10 * time.Millisecond,
	}}
}

func (p *WriterProducer) Produce(ctx context.Context, key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, kafka.Message{Key: []byte(key), Value: b, Time: time.Now()})
}

func (p *WriterProducer) Close() error { return p.writer.Close() }

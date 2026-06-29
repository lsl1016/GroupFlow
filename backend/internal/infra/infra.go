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
)

func NewLogger(env string) *zap.Logger {
	if env == "dev" {
		l, _ := zap.NewDevelopment()
		return l
	}
	l, _ := zap.NewProduction()
	return l
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

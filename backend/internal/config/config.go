package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env             string
	HTTPAddr        string
	ServerID        string
	PublicBaseURL   string
	MySQLDSN        string
	RedisAddr       string
	RedisPassword   string
	RedisDB         int
	KafkaEnabled    bool
	KafkaBrokers    []string
	KafkaTopic      string
	KafkaGroup      string
	AuthSecret      string
	TokenTTL        time.Duration
	DirectPush      bool
	InternalPushURL string
}

func Load() Config {
	ttlHours := envInt("TOKEN_TTL_HOURS", 168)
	return Config{
		Env:             env("APP_ENV", "dev"),
		HTTPAddr:        env("HTTP_ADDR", ":8080"),
		ServerID:        env("SERVER_ID", "ws-server-01"),
		PublicBaseURL:   env("PUBLIC_BASE_URL", "http://localhost"),
		MySQLDSN:        env("MYSQL_DSN", "groupflow:groupflow@tcp(localhost:3306)/groupflow?parseTime=true&loc=Local&charset=utf8mb4"),
		RedisAddr:       env("REDIS_ADDR", "localhost:6379"),
		RedisPassword:   env("REDIS_PASSWORD", ""),
		RedisDB:         envInt("REDIS_DB", 0),
		KafkaEnabled:    envBool("KAFKA_ENABLED", false),
		KafkaBrokers:    split(env("KAFKA_BROKERS", "localhost:9092")),
		KafkaTopic:      env("KAFKA_GROUP_MESSAGE_TOPIC", "group-message-topic"),
		KafkaGroup:      env("KAFKA_CONSUMER_GROUP", "groupflow-delivery"),
		AuthSecret:      env("AUTH_SECRET", "groupflow-dev-secret"),
		TokenTTL:        time.Duration(ttlHours) * time.Hour,
		DirectPush:      envBool("DIRECT_PUSH_WHEN_KAFKA_DISABLED", true),
		InternalPushURL: env("WS_INTERNAL_PUSH_URL", "http://localhost:8080/internal/push"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := env(key, "")
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	v := strings.ToLower(env(key, ""))
	if v == "" {
		return fallback
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func split(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

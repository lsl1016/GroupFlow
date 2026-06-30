// Package logx 提供 GroupFlow 统一的结构化日志能力：进程级 zap logger、
// 基于 context 的 traceId 贯穿、字段规范辅助函数与日志脱敏。
package logx

import (
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// Config 控制 logger 构造，字段来源于应用配置。
type Config struct {
	Level  string // debug/info/warn/error
	Format string // json/console
	Output string // stdout/file/both
	Env    string

	FilePath       string
	FileMaxSizeMB  int
	FileMaxBackups int
	FileMaxAgeDays int
	FileCompress   bool

	HTTPSlowMs  int64
	RedisSlowMs int64
	MySQLSlowMs int64
}

var (
	base = zap.NewNop()

	httpSlowMs  int64 = 500
	redisSlowMs int64 = 100
	mysqlSlowMs int64 = 200
)

// Init 构造进程级 logger 并将其设为全局 base，service 为服务名（如 api-service）。
// 每个进程（api / delivery）启动时调用一次即可。
func Init(cfg Config, service string) *zap.Logger {
	level := zapcore.InfoLevel
	if cfg.Level != "" {
		_ = level.UnmarshalText([]byte(strings.ToLower(cfg.Level)))
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "timestamp"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encCfg.LevelKey = "level"
	encCfg.MessageKey = "message"
	encCfg.EncodeLevel = zapcore.LowercaseLevelEncoder

	var enc zapcore.Encoder
	if strings.ToLower(cfg.Format) == "console" {
		enc = zapcore.NewConsoleEncoder(encCfg)
	} else {
		enc = zapcore.NewJSONEncoder(encCfg)
	}

	core := zapcore.NewCore(enc, buildWriteSyncer(cfg), zap.NewAtomicLevelAt(level))
	logger := zap.New(core, zap.AddCaller(), zap.ErrorOutput(zapcore.AddSync(os.Stderr))).
		With(zap.String("service", service), zap.String("env", cfg.Env))

	base = logger
	if cfg.HTTPSlowMs > 0 {
		httpSlowMs = cfg.HTTPSlowMs
	}
	if cfg.RedisSlowMs > 0 {
		redisSlowMs = cfg.RedisSlowMs
	}
	if cfg.MySQLSlowMs > 0 {
		mysqlSlowMs = cfg.MySQLSlowMs
	}
	return logger
}

func buildWriteSyncer(cfg Config) zapcore.WriteSyncer {
	out := strings.ToLower(cfg.Output)
	if out == "" {
		out = "stdout"
	}
	syncers := make([]zapcore.WriteSyncer, 0, 2)
	if out == "stdout" || out == "both" {
		syncers = append(syncers, zapcore.AddSync(os.Stdout))
	}
	if out == "file" || out == "both" {
		path := cfg.FilePath
		if path == "" {
			path = "logs/groupflow.log"
		}
		syncers = append(syncers, zapcore.AddSync(&lumberjack.Logger{
			Filename:   path,
			MaxSize:    orDefault(cfg.FileMaxSizeMB, 100),
			MaxBackups: orDefault(cfg.FileMaxBackups, 10),
			MaxAge:     orDefault(cfg.FileMaxAgeDays, 7),
			Compress:   cfg.FileCompress,
		}))
	}
	if len(syncers) == 0 {
		syncers = append(syncers, zapcore.AddSync(os.Stdout))
	}
	return zapcore.NewMultiWriteSyncer(syncers...)
}

func orDefault(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// L 返回进程级 base logger，用于无 context 的启动期日志。
func L() *zap.Logger { return base }

// ContentPreview 对消息正文脱敏：返回前 20 个字符的预览与原始字符长度，避免落库全文。
func ContentPreview(s string) (preview string, length int) {
	r := []rune(s)
	length = len(r)
	if length > 20 {
		return string(r[:20]), length
	}
	return s, length
}

// HTTPSlowMs / RedisSlowMs / MySQLSlowMs 返回各链路慢日志阈值（毫秒）。
func HTTPSlowMs() int64  { return httpSlowMs }
func RedisSlowMs() int64 { return redisSlowMs }
func MySQLSlowMs() int64 { return mysqlSlowMs }

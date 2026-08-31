package logger

import (
	"log/slog"
	"os"
)

var log *slog.Logger

// Init 初始化全局日志记录器
func Init(level string) {
	var lvl slog.Level
	switch level {
	case "debug", "DEBUG":
		lvl = slog.LevelDebug
	case "info", "INFO":
		lvl = slog.LevelInfo
	case "warn", "WARN":
		lvl = slog.LevelWarn
	case "error", "ERROR":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: lvl,
	}

	// 简单控制台文本输出
	handler := slog.NewTextHandler(os.Stdout, opts)
	log = slog.New(handler)
	slog.SetDefault(log)
}

// Info 包装 slog.Info
func Info(msg string, args ...any) {
	slog.Info(msg, args...)
}

// Error 包装 slog.Error
func Error(msg string, args ...any) {
	slog.Error(msg, args...)
}

// Debug 包装 slog.Debug
func Debug(msg string, args ...any) {
	slog.Debug(msg, args...)
}

// Warn 包装 slog.Warn
func Warn(msg string, args ...any) {
	slog.Warn(msg, args...)
}

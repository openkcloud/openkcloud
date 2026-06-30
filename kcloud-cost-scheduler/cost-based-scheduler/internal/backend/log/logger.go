package logger

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"sync"

	v1 "k8s.io/api/core/v1"
)

var defaultLogger *Logger

func Init(config Config) {
	defaultLogger = NewLogger(config)
}

type Logger struct {
	logger   *slog.Logger
	logLevel *slog.LevelVar // slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError
}

type Config struct {
	Level  slog.Level
	Output io.Writer
	Format string
}

func NewDefaultConfig() Config {
	return Config{
		Level:  slog.LevelInfo,
		Output: os.Stdout,
		Format: "text",
	}
}

func NewLogger(config Config) *Logger {
	if config.Output == nil {
		config.Output = os.Stdout
	}
	if config.Format == "" {
		config.Format = "text"
	}

	leveler := new(slog.LevelVar)
	leveler.Set(config.Level)

	handlerOpts := &slog.HandlerOptions{
		Level: leveler,
	}

	var handler slog.Handler
	switch config.Format {
	case "json":
		handler = slog.NewJSONHandler(config.Output, handlerOpts)
	default: // "text" or others
		handler = slog.NewTextHandler(config.Output, handlerOpts)
	}

	logger := slog.New(handler)

	return &Logger{
		logger:   logger,
		logLevel: leveler,
	}
}

func (l *Logger) SetLevel(level slog.Level) {
	l.logLevel.Set(level)
}

func (l *Logger) With(args ...any) *Logger {
	return &Logger{
		logger:   l.logger.With(args...),
		logLevel: l.logLevel,
	}
}

func (l *Logger) Debug(msg string, args ...any) {
	l.logger.Debug(msg, args...)
}

func (l *Logger) Info(msg string, args ...any) {
	l.logger.Info(msg, args...)
}

func (l *Logger) Warn(msg string, args ...any) {
	l.logger.Warn(msg, args...)
}

func (l *Logger) Error(msg string, err error, args ...any) {
	allArgs := append(args, slog.String("error", err.Error()))
	l.logger.Error(msg, allArgs...)
}

func Debug(msg string, args ...any) {
	defaultLogger.Debug(msg, args...)
}

func Info(msg string, args ...any) {
	defaultLogger.Info(msg, args...)
}

func Warn(msg string, args ...any) {
	defaultLogger.Warn(msg, args...)
}

func Error(msg string, err error, args ...any) {
	defaultLogger.Error(msg, err, args...)
}

type PodLogger struct {
	logger *slog.Logger
	mu     sync.Mutex
	buf    bytes.Buffer
}

func NewPodLogger(pod *v1.Pod) *PodLogger {
	pl := &PodLogger{}

	handler := slog.NewTextHandler(pl, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})

	logger := slog.New(handler).With(
		"podName", pod.Name,
		"podNamespace", pod.Namespace,
	)

	pl.logger = logger

	return pl
}

func (pl *PodLogger) Write(p []byte) (n int, err error) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.buf.Write(p)
}

func (pl *PodLogger) String() string {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.buf.String()
}

func (pl *PodLogger) Reset() {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.buf.Reset()
}

func (pl *PodLogger) Debug(msg string, args ...any) {
	pl.logger.Debug(msg, args...)
}

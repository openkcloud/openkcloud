package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/kcloud-opt/policy/internal/config"
)

// Logger wraps zap.Logger with additional functionality
type Logger struct {
	*zap.Logger
}

// NewLogger creates a new logger instance based on configuration
func NewLogger(cfg *config.LoggingConfig) (*Logger, error) {
	var zapConfig zap.Config

	if cfg.Format == "json" {
		zapConfig = zap.NewProductionConfig()
	} else {
		zapConfig = zap.NewDevelopmentConfig()
	}

	// Set log level
	level, err := zapcore.ParseLevel(cfg.Level)
	if err != nil {
		return nil, fmt.Errorf("invalid log level: %w", err)
	}
	zapConfig.Level = zap.NewAtomicLevelAt(level)

	// Configure output
	if cfg.Output == "file" && cfg.FilePath != "" {
		// Ensure directory exists
		dir := filepath.Dir(cfg.FilePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		zapConfig.OutputPaths = []string{cfg.FilePath}
		zapConfig.ErrorOutputPaths = []string{cfg.FilePath}
	} else {
		zapConfig.OutputPaths = []string{"stdout"}
		zapConfig.ErrorOutputPaths = []string{"stderr"}
	}

	// Configure time format
	zapConfig.EncoderConfig.TimeKey = "timestamp"
	zapConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	// Configure caller info
	zapConfig.EncoderConfig.CallerKey = "caller"
	zapConfig.EncoderConfig.EncodeCaller = zapcore.ShortCallerEncoder

	// Configure level encoding
	zapConfig.EncoderConfig.LevelKey = "level"
	zapConfig.EncoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder

	// Configure message key
	zapConfig.EncoderConfig.MessageKey = "message"

	// Build logger
	zapLogger, err := zapConfig.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build logger: %w", err)
	}

	// Add file rotation if configured for file output
	if cfg.Output == "file" && cfg.FilePath != "" {
		// Note: For production, consider using lumberjack for log rotation
		// This is a basic implementation
	}

	return &Logger{zapLogger}, nil
}

// WithFields creates a new logger with additional fields
func (l *Logger) WithFields(fields ...zap.Field) *Logger {
	return &Logger{l.Logger.With(fields...)}
}

// WithComponent creates a new logger with component field
func (l *Logger) WithComponent(component string) *Logger {
	return &Logger{l.Logger.With(zap.String("component", component))}
}

// WithRequestID creates a new logger with request ID
func (l *Logger) WithRequestID(requestID string) *Logger {
	return &Logger{l.Logger.With(zap.String("request_id", requestID))}
}

// WithPolicy creates a new logger with policy information
func (l *Logger) WithPolicy(policyID, policyName string) *Logger {
	return &Logger{l.Logger.With(
		zap.String("policy_id", policyID),
		zap.String("policy_name", policyName),
	)}
}

// WithWorkload creates a new logger with workload information
func (l *Logger) WithWorkload(workloadID, workloadType string) *Logger {
	return &Logger{l.Logger.With(
		zap.String("workload_id", workloadID),
		zap.String("workload_type", workloadType),
	)}
}

// WithCluster creates a new logger with cluster information
func (l *Logger) WithCluster(clusterID, clusterName string) *Logger {
	return &Logger{l.Logger.With(
		zap.String("cluster_id", clusterID),
		zap.String("cluster_name", clusterName),
	)}
}

// WithEvaluation creates a new logger with evaluation information
func (l *Logger) WithEvaluation(evaluationID string) *Logger {
	return &Logger{l.Logger.With(zap.String("evaluation_id", evaluationID))}
}

// WithDuration creates a new logger with duration field
func (l *Logger) WithDuration(duration time.Duration) *Logger {
	return &Logger{l.Logger.With(zap.Duration("duration", duration))}
}

// WithError creates a new logger with error field
func (l *Logger) WithError(err error) *Logger {
	return &Logger{l.Logger.With(zap.Error(err))}
}

// Sync flushes any buffered log entries
func (l *Logger) Sync() error {
	return l.Logger.Sync()
}

// GetZapLogger returns the underlying zap logger
func (l *Logger) GetZapLogger() *zap.Logger {
	return l.Logger
}

// Global logger instance
var (
	globalLogger *Logger
)

// InitGlobalLogger initializes the global logger
func InitGlobalLogger(cfg *config.LoggingConfig) error {
	logger, err := NewLogger(cfg)
	if err != nil {
		return err
	}
	globalLogger = logger
	return nil
}

// GetLogger returns the global logger instance
func GetLogger() *Logger {
	if globalLogger == nil {
		// Fallback to a basic logger if not initialized
		zapLogger, _ := zap.NewDevelopment()
		globalLogger = &Logger{zapLogger}
	}
	return globalLogger
}

// SetGlobalLogger sets the global logger instance
func SetGlobalLogger(logger *Logger) {
	globalLogger = logger
}

// Logging methods that delegate to the underlying zap logger

// Info logs an info message
func (l *Logger) Info(msg string, fields ...zap.Field) {
	l.Logger.Info(msg, fields...)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, fields ...zap.Field) {
	l.Logger.Warn(msg, fields...)
}

// Error logs an error message
func (l *Logger) Error(msg string, fields ...zap.Field) {
	l.Logger.Error(msg, fields...)
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, fields ...zap.Field) {
	l.Logger.Debug(msg, fields...)
}

// Fatal logs a fatal message and exits
func (l *Logger) Fatal(msg string, fields ...zap.Field) {
	l.Logger.Fatal(msg, fields...)
}

// Log logs a message at the specified level
func (l *Logger) Log(level zapcore.Level, msg string, fields ...zap.Field) {
	l.Logger.Log(level, msg, fields...)
}

// Helper functions for common logging patterns

// LogPolicyEvaluation logs policy evaluation results
func LogPolicyEvaluation(logger *Logger, policyID, workloadID string, result interface{}, duration time.Duration) {
	logger.Info("policy evaluation completed",
		zap.String("policy_id", policyID),
		zap.String("workload_id", workloadID),
		zap.Any("result", result),
		zap.Duration("duration", duration),
	)
}

// LogPolicyViolation logs policy violations
func LogPolicyViolation(logger *Logger, policyID, workloadID string, violation string, severity string) {
	logger.Warn("policy violation detected",
		zap.String("policy_id", policyID),
		zap.String("workload_id", workloadID),
		zap.String("violation", violation),
		zap.String("severity", severity),
	)
}

// LogAutomationRuleExecution logs automation rule execution
func LogAutomationRuleExecution(logger *Logger, ruleID string, action string, success bool, duration time.Duration) {
	level := zap.InfoLevel
	if !success {
		level = zap.ErrorLevel
	}

	logger.Log(level, "automation rule executed",
		zap.String("rule_id", ruleID),
		zap.String("action", action),
		zap.Bool("success", success),
		zap.Duration("duration", duration),
	)
}

// LogAPIRequest logs API requests
func LogAPIRequest(logger *Logger, method, path string, statusCode int, duration time.Duration) {
	level := zap.InfoLevel
	if statusCode >= 400 {
		level = zap.WarnLevel
	}
	if statusCode >= 500 {
		level = zap.ErrorLevel
	}

	logger.Log(level, "API request",
		zap.String("method", method),
		zap.String("path", path),
		zap.Int("status_code", statusCode),
		zap.Duration("duration", duration),
	)
}

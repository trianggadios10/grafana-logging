package logger

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// ContextKey type for context values
type ContextKey string

const (
	RequestIDKey ContextKey = "request_id"
	TraceIDKey   ContextKey = "trace_id"
	SpanIDKey    ContextKey = "span_id"
	UserIDKey    ContextKey = "user_id"
)

// Logger wraps zerolog with additional functionality
type Logger struct {
	zlog zerolog.Logger
}

// Config holds logger configuration
type Config struct {
	AppName    string
	Version    string
	Level      string
	Pretty     bool // Use console output (for development)
}

// New creates a new Logger instance
func New(cfg Config) *Logger {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.LevelFieldName = "level"
	zerolog.MessageFieldName = "msg"
	zerolog.TimestampFieldName = "time"
	zerolog.CallerFieldName = "caller"

	level := parseLevel(cfg.Level)

	var output zerolog.Logger
	if cfg.Pretty {
		output = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).
			Level(level).
			With().
			Timestamp().
			Caller().
			Str("app", cfg.AppName).
			Str("version", cfg.Version).
			Logger()
	} else {
		output = zerolog.New(os.Stdout).
			Level(level).
			With().
			Timestamp().
			Caller().
			Str("app", cfg.AppName).
			Str("version", cfg.Version).
			Logger()
	}

	return &Logger{zlog: output}
}

func parseLevel(level string) zerolog.Level {
	switch level {
	case "debug":
		return zerolog.DebugLevel
	case "info":
		return zerolog.InfoLevel
	case "warn":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "fatal":
		return zerolog.FatalLevel
	default:
		return zerolog.InfoLevel
	}
}

// WithContext returns a logger with context values
func (l *Logger) WithContext(ctx context.Context) zerolog.Logger {
	event := l.zlog.With()

	if requestID, ok := ctx.Value(RequestIDKey).(string); ok && requestID != "" {
		event = event.Str("request_id", requestID)
	}
	if traceID, ok := ctx.Value(TraceIDKey).(string); ok && traceID != "" {
		event = event.Str("trace_id", traceID)
	}
	if spanID, ok := ctx.Value(SpanIDKey).(string); ok && spanID != "" {
		event = event.Str("span_id", spanID)
	}
	if userID, ok := ctx.Value(UserIDKey).(string); ok && userID != "" {
		event = event.Str("user_id", userID)
	}

	return event.Logger()
}

// Info logs an info message
func (l *Logger) Info(ctx context.Context, msg string) {
	logger := l.WithContext(ctx)
	logger.Info().Msg(msg)
}

// Debug logs a debug message
func (l *Logger) Debug(ctx context.Context, msg string) {
	logger := l.WithContext(ctx)
	logger.Debug().Msg(msg)
}

// Warn logs a warning message
func (l *Logger) Warn(ctx context.Context, msg string) {
	logger := l.WithContext(ctx)
	logger.Warn().Msg(msg)
}

// Error logs an error message
func (l *Logger) Error(ctx context.Context, err error, msg string) {
	_, file, line, _ := runtime.Caller(1)
	logger := l.WithContext(ctx)
	logger.Error().
		Err(err).
		Str("error_location", fmt.Sprintf("%s:%d", file, line)).
		Msg(msg)
}

// ErrorWithStack logs an error with full stack trace
func (l *Logger) ErrorWithStack(ctx context.Context, err error, msg string) {
	stackBuf := make([]byte, 4096)
	stackSize := runtime.Stack(stackBuf, false)
	stackTrace := string(stackBuf[:stackSize])

	logger := l.WithContext(ctx)
	logger.Error().
		Err(err).
		Str("stacktrace", stackTrace).
		Msg(msg)
}

// Fatal logs a fatal error and exits
func (l *Logger) Fatal(ctx context.Context, err error, msg string) {
	stackBuf := make([]byte, 4096)
	stackSize := runtime.Stack(stackBuf, false)
	stackTrace := string(stackBuf[:stackSize])

	logger := l.WithContext(ctx)
	logger.Fatal().
		Err(err).
		Str("stacktrace", stackTrace).
		Msg(msg)
}

// Panic logs a panic with stack trace
func (l *Logger) Panic(ctx context.Context, recovered interface{}, msg string) {
	stackBuf := make([]byte, 4096)
	stackSize := runtime.Stack(stackBuf, false)
	stackTrace := string(stackBuf[:stackSize])

	logger := l.WithContext(ctx)
	logger.Error().
		Interface("panic", recovered).
		Str("stacktrace", stackTrace).
		Msg(msg)
}

// WithFields returns a logger event with additional fields
func (l *Logger) WithFields(ctx context.Context, fields map[string]interface{}) zerolog.Logger {
	event := l.WithContext(ctx).With()
	for k, v := range fields {
		event = event.Interface(k, v)
	}
	return event.Logger()
}

// NewTraceContext creates a new context with trace IDs
func NewTraceContext(ctx context.Context) context.Context {
	requestID := uuid.New().String()
	traceID := uuid.New().String()[:16]
	spanID := uuid.New().String()[:8]

	ctx = context.WithValue(ctx, RequestIDKey, requestID)
	ctx = context.WithValue(ctx, TraceIDKey, traceID)
	ctx = context.WithValue(ctx, SpanIDKey, spanID)

	return ctx
}

// ExtractTraceContext extracts trace context from headers
func ExtractTraceContext(ctx context.Context, requestID, traceID string) context.Context {
	if requestID == "" {
		requestID = uuid.New().String()
	}
	if traceID == "" {
		traceID = uuid.New().String()[:16]
	}
	spanID := uuid.New().String()[:8]

	ctx = context.WithValue(ctx, RequestIDKey, requestID)
	ctx = context.WithValue(ctx, TraceIDKey, traceID)
	ctx = context.WithValue(ctx, SpanIDKey, spanID)

	return ctx
}

// GetRequestID extracts request ID from context
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(RequestIDKey).(string); ok {
		return id
	}
	return ""
}

// GetTraceID extracts trace ID from context
func GetTraceID(ctx context.Context) string {
	if id, ok := ctx.Value(TraceIDKey).(string); ok {
		return id
	}
	return ""
}

// GetSpanID extracts span ID from context
func GetSpanID(ctx context.Context) string {
	if id, ok := ctx.Value(SpanIDKey).(string); ok {
		return id
	}
	return ""
}

// WithSpanID adds a span ID to an existing context
func WithSpanID(ctx context.Context, spanID string) context.Context {
	return context.WithValue(ctx, SpanIDKey, spanID)
}

// WithTraceID adds a trace ID to an existing context
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, TraceIDKey, traceID)
}

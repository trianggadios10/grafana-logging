package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/XSAM/otelsql"
	_ "github.com/lib/pq"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

// Config holds database configuration
type Config struct {
	Host         string
	Port         int
	User         string
	Password     string
	Database     string
	SSLMode      string
	MaxOpenConns int
	MaxIdleConns int
	MaxLifetime  time.Duration
}

// DB wraps the sql.DB with tracing
type DB struct {
	*sql.DB
}

// New creates a new database connection with OpenTelemetry instrumentation
func New(ctx context.Context, cfg Config) (*DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode,
	)

	// Register the otelsql wrapper for the postgres driver
	db, err := otelsql.Open("postgres", dsn,
		otelsql.WithAttributes(
			semconv.DBSystemPostgreSQL,
			semconv.DBName(cfg.Database),
			semconv.ServerAddress(cfg.Host),
			semconv.ServerPort(cfg.Port),
		),
		otelsql.WithSpanOptions(otelsql.SpanOptions{
			Ping:         true,
			RowsNext:     false,
			DisableQuery: false, // Include query in span attributes
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.MaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.MaxLifetime)
	}

	// Verify connection
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Register stats for metrics
	if err := otelsql.RegisterDBStatsMetrics(db, otelsql.WithAttributes(
		semconv.DBSystemPostgreSQL,
		semconv.DBName(cfg.Database),
	)); err != nil {
		return nil, fmt.Errorf("failed to register DB stats metrics: %w", err)
	}

	return &DB{DB: db}, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.DB.Close()
}

// User represents a user record
type User struct {
	ID        int       `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GetUsers retrieves all users (traced query)
func (db *DB) GetUsers(ctx context.Context) ([]User, error) {
	query := `SELECT id, username, email, created_at, updated_at FROM users ORDER BY id`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, u)
	}

	return users, rows.Err()
}

// GetUserByUsername retrieves a user by username (traced query)
func (db *DB) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	query := `SELECT id, username, email, created_at, updated_at FROM users WHERE username = $1`

	var u User
	err := db.QueryRowContext(ctx, query, username).Scan(&u.ID, &u.Username, &u.Email, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query user: %w", err)
	}

	return &u, nil
}

// Quote represents a quote record
type Quote struct {
	ID        int       `json:"id"`
	Content   string    `json:"content"`
	Author    string    `json:"author"`
	FetchedAt time.Time `json:"fetched_at"`
	Source    string    `json:"source"`
}

// SaveQuote stores a quote in the database (traced query)
func (db *DB) SaveQuote(ctx context.Context, content, author string) error {
	query := `INSERT INTO quotes (content, author) VALUES ($1, $2)`
	_, err := db.ExecContext(ctx, query, content, author)
	return err
}

// GetQuotes retrieves recent quotes (traced query)
func (db *DB) GetQuotes(ctx context.Context, limit int) ([]Quote, error) {
	query := `SELECT id, content, author, fetched_at, source FROM quotes ORDER BY fetched_at DESC LIMIT $1`

	rows, err := db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query quotes: %w", err)
	}
	defer rows.Close()

	var quotes []Quote
	for rows.Next() {
		var q Quote
		if err := rows.Scan(&q.ID, &q.Content, &q.Author, &q.FetchedAt, &q.Source); err != nil {
			return nil, fmt.Errorf("failed to scan quote: %w", err)
		}
		quotes = append(quotes, q)
	}

	return quotes, rows.Err()
}

// WeatherCache represents cached weather data
type WeatherCache struct {
	ID        int       `json:"id"`
	Location  string    `json:"location"`
	Data      []byte    `json:"data"`
	CachedAt  time.Time `json:"cached_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SaveWeatherCache caches weather data (traced query)
func (db *DB) SaveWeatherCache(ctx context.Context, location string, data []byte) error {
	query := `
		INSERT INTO weather_cache (location, data, expires_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (location) DO UPDATE SET data = $2, cached_at = CURRENT_TIMESTAMP, expires_at = $3
	`
	expiresAt := time.Now().Add(30 * time.Minute)
	_, err := db.ExecContext(ctx, query, location, data, expiresAt)
	return err
}

// GetWeatherCache retrieves cached weather data if not expired
func (db *DB) GetWeatherCache(ctx context.Context, location string) (*WeatherCache, error) {
	query := `SELECT id, location, data, cached_at, expires_at FROM weather_cache WHERE location = $1 AND expires_at > NOW()`

	var wc WeatherCache
	err := db.QueryRowContext(ctx, query, location).Scan(&wc.ID, &wc.Location, &wc.Data, &wc.CachedAt, &wc.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query weather cache: %w", err)
	}

	return &wc, nil
}

// RequestLog represents a request log entry
type RequestLog struct {
	ID         int       `json:"id"`
	TraceID    string    `json:"trace_id"`
	SpanID     string    `json:"span_id"`
	RequestID  string    `json:"request_id"`
	Endpoint   string    `json:"endpoint"`
	Method     string    `json:"method"`
	StatusCode int       `json:"status_code"`
	DurationMs int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

// LogRequest logs a request for analytics (traced query)
func (db *DB) LogRequest(ctx context.Context, traceID, spanID, requestID, endpoint, method string, statusCode int, durationMs int64) error {
	query := `
		INSERT INTO request_logs (trace_id, span_id, request_id, endpoint, method, status_code, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := db.ExecContext(ctx, query, traceID, spanID, requestID, endpoint, method, statusCode, durationMs)
	return err
}

// GetRequestLogs retrieves recent request logs (traced query)
func (db *DB) GetRequestLogs(ctx context.Context, limit int) ([]RequestLog, error) {
	query := `SELECT id, trace_id, span_id, request_id, endpoint, method, status_code, duration_ms, created_at
		FROM request_logs ORDER BY created_at DESC LIMIT $1`

	rows, err := db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query request logs: %w", err)
	}
	defer rows.Close()

	var logs []RequestLog
	for rows.Next() {
		var rl RequestLog
		if err := rows.Scan(&rl.ID, &rl.TraceID, &rl.SpanID, &rl.RequestID, &rl.Endpoint, &rl.Method, &rl.StatusCode, &rl.DurationMs, &rl.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan request log: %w", err)
		}
		logs = append(logs, rl)
	}

	return logs, rows.Err()
}

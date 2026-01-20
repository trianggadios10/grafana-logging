package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/example/go-api/pkg/client"
	"github.com/example/go-api/pkg/database"
	"github.com/example/go-api/pkg/logger"
	"github.com/example/go-api/pkg/middleware"
	"github.com/example/go-api/pkg/tracing"
)

// Global dependencies
var (
	db            *database.DB
	weatherClient *client.WeatherClient
	quoteClient   *client.QuoteClient
	tracerProvider *tracing.Provider
	appLogger     *logger.Logger
)

// Prometheus metrics (keeping original ones for backward compatibility)
var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	httpRequestsInFlight = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Number of HTTP requests currently being processed",
		},
	)

	errorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "errors_total",
			Help: "Total number of errors",
		},
		[]string{"type"},
	)

	panicRecoveries = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "panic_recoveries_total",
			Help: "Total number of panic recoveries",
		},
	)
)

func init() {
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpRequestDuration)
	prometheus.MustRegister(httpRequestsInFlight)
	prometheus.MustRegister(errorsTotal)
	prometheus.MustRegister(panicRecoveries)
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}

// Handlers
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"healthy"}`))
}

func readyHandler(w http.ResponseWriter, r *http.Request) {
	// Check database connectivity
	if db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"not ready","reason":"database unavailable"}`))
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ready"}`))
}

func helloHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	traceID := tracing.GetTraceID(ctx)

	log.Info().
		Str("trace_id", traceID).
		Msg("Hello endpoint called")

	response := map[string]interface{}{
		"message":  "Hello, World!",
		"trace_id": traceID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func errorHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	err := fmt.Errorf("simulated error for testing")

	tracing.RecordError(ctx, err)
	errorsTotal.WithLabelValues("application").Inc()

	log.Error().
		Str("trace_id", tracing.GetTraceID(ctx)).
		Err(err).
		Msg("Error endpoint triggered")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(map[string]string{
		"error":    "Something went wrong",
		"trace_id": tracing.GetTraceID(ctx),
	})
}

// weatherHandler fetches weather data with tracing
func weatherHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	location := vars["location"]
	if location == "" {
		location = "London"
	}

	// Create child span for weather operation
	tracer := tracerProvider.Tracer()
	ctx, span := tracer.Start(ctx, "fetch_weather",
		trace.WithAttributes(attribute.String("location", location)))
	defer span.End()

	// Fetch weather from external API
	weather, err := weatherClient.GetWeather(ctx, location)
	if err != nil {
		span.RecordError(err)
		log.Error().
			Str("trace_id", tracing.GetTraceID(ctx)).
			Err(err).
			Str("location", location).
			Msg("Failed to fetch weather")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error":    err.Error(),
			"trace_id": tracing.GetTraceID(ctx),
		})
		return
	}

	// Cache weather in database (if available)
	if db != nil {
		ctx, dbSpan := tracer.Start(ctx, "cache_weather_db")
		data, _ := json.Marshal(weather)
		if err := db.SaveWeatherCache(ctx, location, data); err != nil {
			dbSpan.RecordError(err)
			log.Warn().
				Str("trace_id", tracing.GetTraceID(ctx)).
				Err(err).
				Msg("Failed to cache weather data")
		}
		dbSpan.End()
	}

	response := map[string]interface{}{
		"weather":  weather,
		"trace_id": tracing.GetTraceID(ctx),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// quoteHandler fetches a random quote with tracing
func quoteHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tracer := tracerProvider.Tracer()

	// Fetch quote from external API
	ctx, span := tracer.Start(ctx, "fetch_quote")
	quote, err := quoteClient.GetRandomQuote(ctx)
	span.End()

	if err != nil {
		span.RecordError(err)
		log.Error().
			Str("trace_id", tracing.GetTraceID(ctx)).
			Err(err).
			Msg("Failed to fetch quote")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error":    err.Error(),
			"trace_id": tracing.GetTraceID(ctx),
		})
		return
	}

	// Save quote to database (if available)
	if db != nil {
		ctx, dbSpan := tracer.Start(ctx, "save_quote_db")
		if err := db.SaveQuote(ctx, quote.Content, quote.Author); err != nil {
			dbSpan.RecordError(err)
			log.Warn().
				Str("trace_id", tracing.GetTraceID(ctx)).
				Err(err).
				Msg("Failed to save quote to database")
		}
		dbSpan.End()
	}

	response := map[string]interface{}{
		"quote":    quote,
		"trace_id": tracing.GetTraceID(ctx),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// usersHandler retrieves users from database
func usersHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if db == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error":    "Database not available",
			"trace_id": tracing.GetTraceID(ctx),
		})
		return
	}

	users, err := db.GetUsers(ctx)
	if err != nil {
		tracing.RecordError(ctx, err)
		log.Error().
			Str("trace_id", tracing.GetTraceID(ctx)).
			Err(err).
			Msg("Failed to get users")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error":    err.Error(),
			"trace_id": tracing.GetTraceID(ctx),
		})
		return
	}

	response := map[string]interface{}{
		"users":    users,
		"count":    len(users),
		"trace_id": tracing.GetTraceID(ctx),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// dashboardHandler demonstrates nested spans: external APIs + DB queries
func dashboardHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	location := r.URL.Query().Get("location")
	if location == "" {
		location = "London"
	}

	tracer := tracerProvider.Tracer()

	// Parent span for entire dashboard operation
	ctx, span := tracer.Start(ctx, "build_dashboard",
		trace.WithAttributes(attribute.String("dashboard.location", location)))
	defer span.End()

	result := make(map[string]interface{})
	result["trace_id"] = tracing.GetTraceID(ctx)
	result["timestamp"] = time.Now().UTC()
	result["location"] = location

	// Child span 1: Fetch weather
	weatherCtx, weatherSpan := tracer.Start(ctx, "dashboard.fetch_weather")
	weather, err := weatherClient.GetWeather(weatherCtx, location)
	if err != nil {
		weatherSpan.RecordError(err)
		result["weather_error"] = err.Error()
	} else {
		result["weather"] = weather
	}
	weatherSpan.End()

	// Child span 2: Fetch quote
	quoteCtx, quoteSpan := tracer.Start(ctx, "dashboard.fetch_quote")
	quote, err := quoteClient.GetRandomQuote(quoteCtx)
	if err != nil {
		quoteSpan.RecordError(err)
		result["quote_error"] = err.Error()
	} else {
		result["quote"] = quote
	}
	quoteSpan.End()

	// Child span 3: Get users from DB (if available)
	if db != nil {
		dbCtx, dbSpan := tracer.Start(ctx, "dashboard.get_users")
		users, err := db.GetUsers(dbCtx)
		if err != nil {
			dbSpan.RecordError(err)
			result["users_error"] = err.Error()
		} else {
			result["users"] = users
			result["users_count"] = len(users)
		}
		dbSpan.End()

		// Child span 4: Get recent quotes from DB
		quotesCtx, quotesSpan := tracer.Start(ctx, "dashboard.get_recent_quotes")
		recentQuotes, err := db.GetQuotes(quotesCtx, 5)
		if err != nil {
			quotesSpan.RecordError(err)
			result["recent_quotes_error"] = err.Error()
		} else {
			result["recent_quotes"] = recentQuotes
		}
		quotesSpan.End()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func main() {
	ctx := context.Background()

	// Configure zerolog for JSON output (required for Loki parsing)
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.LevelFieldName = "level"
	zerolog.MessageFieldName = "msg"
	zerolog.TimestampFieldName = "time"
	zerolog.CallerFieldName = "caller"

	// Set global logger
	log.Logger = zerolog.New(os.Stdout).
		With().
		Timestamp().
		Caller().
		Str("app", "go-api").
		Str("version", "2.0.0").
		Logger()

	// Initialize structured logger for middleware
	appLogger = logger.New(logger.Config{
		AppName: "go-api",
		Version: "2.0.0",
		Level:   getEnvOrDefault("LOG_LEVEL", "info"),
		Pretty:  getEnvOrDefault("LOG_PRETTY", "false") == "true",
	})

	// Initialize OpenTelemetry tracing
	tracingEnabled := getEnvOrDefault("TRACING_ENABLED", "true") == "true"
	var err error
	tracerProvider, err = tracing.InitTracer(ctx, tracing.Config{
		ServiceName:    "go-api",
		ServiceVersion: "2.0.0",
		Environment:    getEnvOrDefault("ENVIRONMENT", "development"),
		OTLPEndpoint:   getEnvOrDefault("OTEL_EXPORTER_OTLP_ENDPOINT", "tempo.monitoring:4317"),
		Enabled:        tracingEnabled,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize tracer")
	}
	defer func() {
		if err := tracerProvider.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("Error shutting down tracer provider")
		}
	}()

	log.Info().
		Bool("tracing_enabled", tracingEnabled).
		Str("otlp_endpoint", getEnvOrDefault("OTEL_EXPORTER_OTLP_ENDPOINT", "tempo.monitoring:4317")).
		Msg("Tracing initialized")

	// Initialize database connection (optional - gracefully degrade if unavailable)
	dbHost := getEnvOrDefault("DB_HOST", "")
	if dbHost != "" {
		db, err = database.New(ctx, database.Config{
			Host:         dbHost,
			Port:         getEnvAsInt("DB_PORT", 5432),
			User:         getEnvOrDefault("DB_USER", "goapi"),
			Password:     getEnvOrDefault("DB_PASSWORD", "goapi-secret-password"),
			Database:     getEnvOrDefault("DB_NAME", "goapi"),
			SSLMode:      getEnvOrDefault("DB_SSLMODE", "disable"),
			MaxOpenConns: 25,
			MaxIdleConns: 5,
			MaxLifetime:  5 * time.Minute,
		})
		if err != nil {
			log.Warn().Err(err).Msg("Failed to connect to database - running without DB features")
			db = nil
		} else {
			log.Info().
				Str("host", dbHost).
				Int("port", getEnvAsInt("DB_PORT", 5432)).
				Msg("Database connected")
			defer db.Close()
		}
	} else {
		log.Info().Msg("No database configured - running without DB features")
	}

	// Initialize HTTP clients for external APIs
	httpTimeout := time.Duration(getEnvAsInt("HTTP_CLIENT_TIMEOUT", 10)) * time.Second
	weatherClient = client.NewWeatherClient(httpTimeout)
	quoteClient = client.NewQuoteClient(httpTimeout)

	log.Info().
		Dur("timeout", httpTimeout).
		Msg("HTTP clients initialized")

	// Get port from environment or use default
	port := getEnvOrDefault("PORT", "8080")

	// Create router
	r := mux.NewRouter()

	// Use existing Prometheus metrics (registered in init())
	metrics := &middleware.Metrics{
		RequestsTotal:    httpRequestsTotal,
		RequestDuration:  httpRequestDuration,
		RequestsInFlight: httpRequestsInFlight,
		PanicRecoveries:  panicRecoveries,
	}

	// Health and readiness endpoints (no middleware)
	r.HandleFunc("/health", healthHandler).Methods("GET")
	r.HandleFunc("/ready", readyHandler).Methods("GET")

	// Metrics endpoint
	r.Handle("/metrics", promhttp.Handler())

	// API routes with full middleware stack
	api := r.PathPrefix("/api").Subrouter()

	// Middleware order: OTel -> Recovery -> Logging -> Metrics
	api.Use(middleware.OTelMiddleware("go-api"))
	api.Use(middleware.Recovery(appLogger, metrics))
	api.Use(middleware.TracedLogging(appLogger))
	api.Use(middleware.MetricsMiddleware(metrics))

	// Existing endpoints
	api.HandleFunc("/hello", helloHandler).Methods("GET")
	api.HandleFunc("/error", errorHandler).Methods("GET")

	// New traced endpoints
	api.HandleFunc("/weather/{location}", weatherHandler).Methods("GET")
	api.HandleFunc("/weather", weatherHandler).Methods("GET")
	api.HandleFunc("/quote", quoteHandler).Methods("GET")
	api.HandleFunc("/users", usersHandler).Methods("GET")
	api.HandleFunc("/dashboard", dashboardHandler).Methods("GET")

	// Create server
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Info().
			Str("port", port).
			Bool("db_available", db != nil).
			Bool("tracing_enabled", tracingEnabled).
			Msg("Starting HTTP server")

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("Failed to start server")
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Shutting down server...")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatal().Err(err).Msg("Server forced to shutdown")
	}

	log.Info().Msg("Server exited properly")
}

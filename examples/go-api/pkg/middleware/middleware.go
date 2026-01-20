package middleware

import (
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/example/go-api/pkg/logger"
	"github.com/example/go-api/pkg/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Metrics holds Prometheus metrics for HTTP middleware
type Metrics struct {
	RequestsTotal    *prometheus.CounterVec
	RequestDuration  *prometheus.HistogramVec
	RequestsInFlight prometheus.Gauge
	PanicRecoveries  prometheus.Counter
}

// NewMetrics creates a new Metrics instance
func NewMetrics(namespace string) *Metrics {
	m := &Metrics{
		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "http_requests_total",
				Help:      "Total number of HTTP requests",
			},
			[]string{"method", "path", "status"},
		),
		RequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "http_request_duration_seconds",
				Help:      "HTTP request duration in seconds",
				Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"method", "path"},
		),
		RequestsInFlight: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "http_requests_in_flight",
				Help:      "Number of HTTP requests currently being processed",
			},
		),
		PanicRecoveries: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "panic_recoveries_total",
				Help:      "Total number of panic recoveries",
			},
		),
	}

	prometheus.MustRegister(m.RequestsTotal)
	prometheus.MustRegister(m.RequestDuration)
	prometheus.MustRegister(m.RequestsInFlight)
	prometheus.MustRegister(m.PanicRecoveries)

	return m
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Logging creates a logging middleware
func Logging(log *logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Extract or generate trace context
			requestID := r.Header.Get("X-Request-ID")
			traceID := r.Header.Get("X-Trace-ID")
			ctx := logger.ExtractTraceContext(r.Context(), requestID, traceID)
			r = r.WithContext(ctx)

			// Set response headers for tracing
			w.Header().Set("X-Request-ID", logger.GetRequestID(ctx))
			w.Header().Set("X-Trace-ID", logger.GetTraceID(ctx))

			// Wrap response writer
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Process request
			next.ServeHTTP(rw, r)

			duration := time.Since(start)

			// Log request
			logCtx := log.WithFields(ctx, map[string]interface{}{
				"method":      r.Method,
				"path":        r.URL.Path,
				"status":      rw.statusCode,
				"duration_ms": duration.Milliseconds(),
				"remote_addr": r.RemoteAddr,
				"user_agent":  r.UserAgent(),
			})
			logCtx.Info().Msg("HTTP request completed")
		})
	}
}

// Metrics creates a metrics middleware
func MetricsMiddleware(m *Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Track in-flight requests
			m.RequestsInFlight.Inc()
			defer m.RequestsInFlight.Dec()

			// Process request
			next.ServeHTTP(rw, r)

			duration := time.Since(start)

			// Record metrics
			m.RequestsTotal.WithLabelValues(r.Method, r.URL.Path, fmt.Sprintf("%d", rw.statusCode)).Inc()
			m.RequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration.Seconds())
		})
	}
}

// Recovery creates a panic recovery middleware
func Recovery(log *logger.Logger, m *Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					// Capture stack trace
					stackBuf := make([]byte, 4096)
					stackSize := runtime.Stack(stackBuf, false)
					stackTrace := string(stackBuf[:stackSize])

					// Log panic
					panicLog := log.WithFields(r.Context(), map[string]interface{}{
						"method":     r.Method,
						"path":       r.URL.Path,
						"panic":      err,
						"stacktrace": stackTrace,
					})
					panicLog.Error().Msg("Panic recovered")

					// Update metrics
					if m != nil {
						m.PanicRecoveries.Inc()
					}

					// Return 500 error
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// Chain chains multiple middleware functions
func Chain(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(final http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}

// OTelMiddleware returns the OpenTelemetry middleware for Gorilla Mux
func OTelMiddleware(serviceName string) func(http.Handler) http.Handler {
	return otelmux.Middleware(serviceName,
		otelmux.WithSpanNameFormatter(func(routeName string, r *http.Request) string {
			return r.Method + " " + routeName
		}),
	)
}

// TracedLogging creates a logging middleware that includes OpenTelemetry trace context
func TracedLogging(log *logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Get trace context from OTel span (set by OTelMiddleware)
			otelTraceID := tracing.GetTraceID(r.Context())
			otelSpanID := tracing.GetSpanID(r.Context())

			// Fall back to header-based trace context if OTel context not present
			if otelTraceID == "" {
				otelTraceID = r.Header.Get("X-Trace-ID")
			}

			// Extract or generate request ID (separate from trace ID)
			requestID := r.Header.Get("X-Request-ID")
			ctx := logger.ExtractTraceContext(r.Context(), requestID, otelTraceID)

			// Override span ID with OTel span ID if available
			if otelSpanID != "" {
				ctx = logger.WithSpanID(ctx, otelSpanID)
			}

			r = r.WithContext(ctx)

			// Set response headers for tracing
			w.Header().Set("X-Request-ID", logger.GetRequestID(ctx))
			w.Header().Set("X-Trace-ID", otelTraceID)
			if otelSpanID != "" {
				w.Header().Set("X-Span-ID", otelSpanID)
			}

			// Wrap response writer
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Process request
			next.ServeHTTP(rw, r)

			duration := time.Since(start)

			// Add span attributes for the response
			span := trace.SpanFromContext(r.Context())
			span.SetAttributes(
				attribute.Int("http.status_code", rw.statusCode),
				attribute.Int64("http.duration_ms", duration.Milliseconds()),
			)

			// Log with trace correlation
			tracedLog := log.WithFields(ctx, map[string]interface{}{
				"method":      r.Method,
				"path":        r.URL.Path,
				"status":      rw.statusCode,
				"duration_ms": duration.Milliseconds(),
				"remote_addr": r.RemoteAddr,
				"user_agent":  r.UserAgent(),
				"trace_id":    otelTraceID,
				"span_id":     otelSpanID,
			})
			tracedLog.Info().Msg("HTTP request completed")
		})
	}
}

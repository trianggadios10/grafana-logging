# Grafana Observability Stack for Kubernetes

A complete observability stack for Go APIs running on Kubernetes, featuring:
- **Prometheus** - Metrics collection and alerting with pre-configured alert rules
- **Loki** - Log aggregation (horizontally scalable, highly available)
- **Promtail** - Log shipping agent
- **Tempo** - Distributed tracing backend
- **Grafana** - Visualization and dashboards with NGINX ingress support
- **PostgreSQL** - Database for application data with traced queries
- **Kustomize** - Kubernetes-native configuration management

## Architecture

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                              Kubernetes Cluster                              │
│                                                                              │
│  ┌─────────────┐                                  ┌─────────────────────┐    │
│  │   Go API    │─────── metrics ─────────────────>│      Prometheus     │    │
│  │   :8080     │                                  │       :9090         │    │
│  └─────────────┘                                  │  (alerting rules)   │    │
│        │  │                                       └──────────┬──────────┘    │
│        │  │                                                  │               │
│        │  └─── traces (OTLP) ───┐                            │               │
│        │                        ↓                            ↓               │
│        │               ┌─────────────┐           ┌─────────────────────┐     │
│        │               │    Tempo    │──────────>│      Grafana        │     │
│        │               │    :4317    │           │       :3000         │     │
│        │               └─────────────┘           │   (NGINX Ingress)   │     │
│        │                                         └─────────────────────┘     │
│        │ stdout/stderr                                      ↑                │
│        ↓                                                    │                │
│  ┌─────────────┐     ┌─────────────┐                        │                │
│  │  Promtail   │────>│    Loki     │────────────────────────┘                │
│  │ (DaemonSet) │     │   :3100     │                                         │
│  └─────────────┘     └─────────────┘                                         │
│                                                                              │
│  ┌─────────────┐                                                             │
│  │  PostgreSQL │<──── traced queries ──── Go API                             │
│  │   :5432     │                                                             │
│  └─────────────┘                                                             │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

## Prerequisites

- Kubernetes cluster (1.24+)
- kubectl configured
- Kustomize (v4.0+) or kubectl with built-in kustomize support
- Docker (for building Go API image)
- NGINX Ingress Controller (optional, for ingress support)

## Quick Start

### 1. Deploy the Stack

**Using Kustomize (recommended):**
```bash
kubectl apply -k k8s/
```

**Using deployment scripts:**
```bash
# Linux/Mac
chmod +x deploy.sh
./deploy.sh

# Windows PowerShell
.\deploy.ps1
```

### 2. Access Services

**Port forwarding:**
```bash
# Grafana (admin/admin123)
kubectl port-forward svc/grafana 3000:3000 -n monitoring

# Prometheus
kubectl port-forward svc/prometheus 9090:9090 -n monitoring

# Tempo (trace queries)
kubectl port-forward svc/tempo 3200:3200 -n monitoring

# PostgreSQL
kubectl port-forward svc/postgres 5432:5432
```

**Using Ingress (requires NGINX Ingress Controller):**
- Grafana: http://grafana.local
- Prometheus: http://prometheus.local

Add entries to your hosts file or configure DNS accordingly.

### 3. Deploy Example Go API

```bash
cd examples/go-api
docker build -t go-api:latest .
kubectl apply -f k8s-deployment.yaml
```

### 4. Test the API Endpoints

```bash
# Health check
curl http://localhost:8080/health

# Hello endpoint (with tracing)
curl http://localhost:8080/api/hello

# Weather endpoint (external API + tracing)
curl http://localhost:8080/api/weather/London

# Quote endpoint (external API + DB storage)
curl http://localhost:8080/api/quote

# Dashboard (aggregates multiple data sources)
curl http://localhost:8080/api/dashboard?location=Paris
```

## Go API Integration

### Structured Logging with Zerolog

The example Go API uses zerolog for JSON-structured logging that Promtail can parse:

```go
import "github.com/rs/zerolog/log"

// Basic logging
log.Info().
    Str("user_id", userID).
    Str("action", "login").
    Msg("User logged in")

// Error logging with stack trace
log.Error().
    Err(err).
    Str("trace_id", traceID).
    Str("stacktrace", stackTrace).
    Msg("Operation failed")
```

### Required Log Format

For proper parsing, logs must be JSON with these fields:
```json
{
  "level": "info",
  "time": "2024-01-15T10:30:00.000Z",
  "msg": "Request completed",
  "caller": "main.go:123",
  "trace_id": "abc123def456",
  "request_id": "req-789",
  "stacktrace": "goroutine 1 [running]:..."
}
```

### Prometheus Metrics

Add these annotations to your pod spec:
```yaml
annotations:
  prometheus.io/scrape: "true"
  prometheus.io/port: "8080"
  prometheus.io/path: "/metrics"
```

Expose metrics endpoint:
```go
import "github.com/prometheus/client_golang/prometheus/promhttp"

http.Handle("/metrics", promhttp.Handler())
```

### OpenTelemetry Tracing

The Go API integrates with Tempo via OpenTelemetry for distributed tracing:

```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
)

// Initialize tracer
tracerProvider, err := tracing.InitTracer(ctx, tracing.Config{
    ServiceName:    "go-api",
    ServiceVersion: "2.0.0",
    Environment:    "production",
    OTLPEndpoint:   "tempo.monitoring:4317",
    Enabled:        true,
})
```

**Creating spans:**
```go
tracer := tracerProvider.Tracer()
ctx, span := tracer.Start(ctx, "operation_name",
    trace.WithAttributes(attribute.String("key", "value")))
defer span.End()

// Record errors
span.RecordError(err)
```

**Database tracing with otelsql:**
```go
import "github.com/XSAM/otelsql"

db, err := otelsql.Open("postgres", dsn,
    otelsql.WithAttributes(
        semconv.DBSystemPostgreSQL,
        semconv.DBName("goapi"),
    ),
)
```

### Recommended Metrics

```go
var (
    httpRequestsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "http_requests_total",
            Help: "Total HTTP requests",
        },
        []string{"method", "path", "status"},
    )

    httpRequestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "http_request_duration_seconds",
            Help:    "HTTP request duration",
            Buckets: prometheus.DefBuckets,
        },
        []string{"method", "path"},
    )

    errorsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "errors_total",
            Help: "Total errors",
        },
        []string{"type"},
    )
)
```

## Querying Logs in Grafana

### LogQL Examples

```logql
# All logs from go-api
{app="go-api"}

# Error logs only
{app="go-api"} | json | level = "error"

# Logs with stack traces
{app="go-api"} |= "stacktrace"

# Filter by trace ID
{app="go-api"} | json | trace_id = "abc123"

# Errors in last hour with rate
sum(rate({app="go-api"} | json | level = "error" [5m]))
```

### Prometheus Queries

```promql
# Request rate
sum(rate(http_requests_total{app="go-api"}[5m]))

# Error rate percentage
sum(rate(http_requests_total{app="go-api",status=~"5.."}[5m]))
/ sum(rate(http_requests_total{app="go-api"}[5m])) * 100

# P99 latency
histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{app="go-api"}[5m])) by (le))

# Panic recoveries
increase(panic_recoveries_total{app="go-api"}[1h])
```

### TraceQL Queries (Tempo)

```traceql
# Find traces by service name
{resource.service.name="go-api"}

# Find traces with errors
{resource.service.name="go-api" && status=error}

# Find traces by span name
{name="fetch_weather"}

# Find slow database queries
{name=~".*db.*" && duration>100ms}
```

## Prometheus Alerting

Pre-configured alerting rules are included in `k8s/prometheus/alerting-rules.yaml`:

### Application Alerts
- **HighErrorRate**: Triggers when error rate exceeds 5% for 5 minutes
- **HighLatency**: Triggers when P99 latency exceeds 1 second
- **PanicRecovered**: Triggers immediately when a panic is recovered
- **PodDown**: Triggers when the Go API pod is down for 2 minutes
- **HighMemoryUsage**: Triggers when memory usage exceeds 85% of limit
- **HighCPUUsage**: Triggers when CPU usage exceeds 85% of limit

### Infrastructure Alerts
- **PrometheusTargetMissing**: Triggers when any scrape target is down
- **LokiDown**: Triggers when Loki is unavailable
- **PrometheusStorageFull**: Triggers when storage is 90% full
- **HighLogVolume**: Triggers when log ingestion rate is high

## Stack Trace Tracking

### Capturing Stack Traces

```go
func LogErrorWithStackTrace(ctx context.Context, err error, msg string) {
    stackBuf := make([]byte, 4096)
    stackSize := runtime.Stack(stackBuf, false)
    stackTrace := string(stackBuf[:stackSize])

    log.Error().
        Err(err).
        Str("trace_id", getTraceID(ctx)).
        Str("stacktrace", stackTrace).
        Msg(msg)
}
```

### Panic Recovery Middleware

```go
func RecoveryMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if err := recover(); err != nil {
                stackBuf := make([]byte, 4096)
                stackSize := runtime.Stack(stackBuf, false)

                log.Error().
                    Interface("error", err).
                    Str("stacktrace", string(stackBuf[:stackSize])).
                    Msg("Panic recovered")

                http.Error(w, "Internal Server Error", 500)
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```

## Directory Structure

```
grafana-logging/
├── k8s/
│   ├── kustomization.yaml          # Kustomize configuration
│   ├── base/
│   │   └── namespace.yaml
│   ├── prometheus/
│   │   ├── configmap.yaml
│   │   ├── deployment.yaml
│   │   ├── rbac.yaml
│   │   └── alerting-rules.yaml     # Pre-configured alert rules
│   ├── loki/
│   │   ├── configmap.yaml
│   │   └── deployment.yaml
│   ├── promtail/
│   │   ├── configmap.yaml
│   │   └── daemonset.yaml
│   ├── tempo/                       # Distributed tracing
│   │   ├── configmap.yaml
│   │   └── deployment.yaml
│   ├── postgres/                    # PostgreSQL database
│   │   ├── configmap.yaml
│   │   └── deployment.yaml
│   └── grafana/
│       ├── configmap.yaml
│       ├── dashboards-configmap.yaml
│       ├── deployment.yaml
│       └── ingress.yaml             # NGINX ingress
├── examples/
│   └── go-api/
│       ├── main.go
│       ├── go.mod
│       ├── go.sum
│       ├── Dockerfile
│       ├── k8s-deployment.yaml
│       └── pkg/
│           ├── client/              # HTTP clients for external APIs
│           │   └── httpclient.go
│           ├── database/            # PostgreSQL with traced queries
│           │   └── db.go
│           ├── logger/              # Structured logging
│           │   └── logger.go
│           ├── middleware/          # HTTP middleware stack
│           │   └── middleware.go
│           └── tracing/             # OpenTelemetry tracing
│               └── tracing.go
├── deploy.sh
├── deploy.ps1
├── undeploy.sh
└── README.md
```

## Configuration

### Prometheus Retention

Edit `k8s/prometheus/deployment.yaml`:
```yaml
args:
  - "--storage.tsdb.retention.time=30d"  # Adjust retention
```

### Loki Retention

Edit `k8s/loki/configmap.yaml`:
```yaml
table_manager:
  retention_deletes_enabled: true
  retention_period: 168h  # 7 days
```

### Grafana Password

Update `k8s/grafana/deployment.yaml`:
```yaml
stringData:
  admin-password: "your-secure-password"
```

### Tempo Configuration

Edit `k8s/tempo/configmap.yaml` for trace retention:
```yaml
compactor:
  compaction:
    block_retention: 48h  # Adjust retention period
```

### PostgreSQL Configuration

Update credentials in `k8s/postgres/deployment.yaml`:
```yaml
stringData:
  username: "goapi"
  password: "your-secure-password"
```

### Go API Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP server port |
| `LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `LOG_PRETTY` | `false` | Pretty print logs (development) |
| `TRACING_ENABLED` | `true` | Enable OpenTelemetry tracing |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `tempo.monitoring:4317` | Tempo OTLP endpoint |
| `DB_HOST` | (empty) | PostgreSQL host (optional) |
| `DB_PORT` | `5432` | PostgreSQL port |
| `DB_USER` | `goapi` | PostgreSQL username |
| `DB_PASSWORD` | `goapi-secret-password` | PostgreSQL password |
| `DB_NAME` | `goapi` | PostgreSQL database name |
| `HTTP_CLIENT_TIMEOUT` | `10` | HTTP client timeout in seconds |

## Troubleshooting

### Check Pod Status
```bash
kubectl get pods -n monitoring
```

### View Logs
```bash
# Prometheus
kubectl logs -f deployment/prometheus -n monitoring

# Loki
kubectl logs -f deployment/loki -n monitoring

# Promtail
kubectl logs -f daemonset/promtail -n monitoring

# Grafana
kubectl logs -f deployment/grafana -n monitoring

# Tempo
kubectl logs -f deployment/tempo -n monitoring

# PostgreSQL
kubectl logs -f deployment/postgres

# Go API
kubectl logs -f deployment/go-api
```

### Common Issues

1. **Promtail can't read logs**: Ensure correct host paths for your container runtime
2. **Loki not receiving logs**: Check Promtail config and network policies
3. **Prometheus not scraping**: Verify pod annotations and RBAC permissions
4. **Tempo not receiving traces**: Check OTLP endpoint configuration and network connectivity
5. **PostgreSQL connection refused**: Verify secrets and service DNS resolution
6. **Traces not correlating with logs**: Ensure `trace_id` is included in log output

## Uninstall

**Using Kustomize:**
```bash
kubectl delete -k k8s/
```

**Using deployment scripts:**
```bash
# Linux/Mac
./undeploy.sh

# Windows PowerShell
kubectl delete -f k8s/grafana/ --ignore-not-found
kubectl delete -f k8s/tempo/ --ignore-not-found
kubectl delete -f k8s/promtail/ --ignore-not-found
kubectl delete -f k8s/loki/ --ignore-not-found
kubectl delete -f k8s/prometheus/ --ignore-not-found
kubectl delete -f k8s/postgres/ --ignore-not-found
kubectl delete -f k8s/base/namespace.yaml --ignore-not-found
```

## API Reference

### Go API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/ready` | GET | Readiness check (includes DB connectivity) |
| `/metrics` | GET | Prometheus metrics |
| `/api/hello` | GET | Simple hello endpoint with tracing |
| `/api/error` | GET | Test error handling and tracing |
| `/api/weather/{location}` | GET | Fetch weather data with external API call |
| `/api/quote` | GET | Fetch random quote with DB persistence |
| `/api/users` | GET | List users from database |
| `/api/dashboard` | GET | Aggregated dashboard with multiple data sources |

## License

MIT

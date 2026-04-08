# Deployment Guide: Grafana Observability Stack

> **Target pembaca:** DevOps Engineer, Infrastructure Engineer
> **Terakhir diperbarui:** April 2026

---

## Daftar Isi

1. [Prerequisites](#1-prerequisites)
2. [Arsitektur & Komponen](#2-arsitektur--komponen)
3. [Struktur File](#3-struktur-file)
4. [Deploy ke Docker Desktop (Local)](#4-deploy-ke-docker-desktop-local)
5. [Deploy ke GKE (Production)](#5-deploy-ke-gke-production)
6. [Konfigurasi Komponen](#6-konfigurasi-komponen)
7. [Onboarding Service Baru](#7-onboarding-service-baru)
8. [Monitoring & Troubleshooting](#8-monitoring--troubleshooting)
9. [Maintenance](#9-maintenance)

---

## 1. Prerequisites

### Tools yang Diperlukan

| Tool | Minimum Version | Cek Instalasi |
|------|----------------|---------------|
| kubectl | 1.24+ | `kubectl version --client` |
| Docker Desktop | 4.x | `docker --version` |
| Kustomize | 4.0+ | `kubectl kustomize --help` |
| Git | 2.x | `git --version` |

### Kubernetes Cluster

- **Local**: Docker Desktop dengan Kubernetes enabled
- **Production**: GKE 1.24+ (atau cluster Kubernetes lain yang support PVC dan RBAC)

### Resource Requirements

| Komponen | CPU Request | CPU Limit | Memory Request | Memory Limit | Storage |
|----------|------------|-----------|----------------|-------------|---------|
| Prometheus | 250m | 1000m | 512Mi | 2Gi | 10Gi PVC |
| Loki | 100m | 500m | 256Mi | 1Gi | 10Gi PVC |
| Tempo | 100m | 500m | 256Mi | 1Gi | 10Gi PVC |
| Promtail (per node) | 50m | 200m | 64Mi | 256Mi | - |
| Grafana | 100m | 500m | 256Mi | 512Mi | 5Gi PVC |
| PostgreSQL | 100m | 500m | 256Mi | 512Mi | 5Gi PVC |
| **Total minimum** | **~750m** | **~3200m** | **~1.6Gi** | **~5.3Gi** | **~40Gi** |

---

## 2. Arsitektur & Komponen

```
┌─────────────────────────────────────────────────────────────────┐
│                        Kubernetes Cluster                       │
│                                                                 │
│  Namespace: monitoring                                          │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                                                         │    │
│  │  ┌────────────┐  scrape   ┌──────────────┐              │    │
│  │  │ Prometheus │ <──────── │  Go API Pod  │              │    │
│  │  │   :9090    │  /metrics │   :8080      │              │    │
│  │  └─────┬──────┘           └──┬─────┬─────┘              │    │
│  │        │                     │     │                     │    │
│  │        │              traces │     │ stdout              │    │
│  │        │              (OTLP) │     │                     │    │
│  │        │                     ↓     ↓                     │    │
│  │        │            ┌────────┐  ┌──────────┐            │    │
│  │        │            │ Tempo  │  │ Promtail │            │    │
│  │        │            │ :4317  │  │(DaemonSet)│           │    │
│  │        │            └───┬────┘  └────┬─────┘            │    │
│  │        │                │            │                   │    │
│  │        ↓                ↓            ↓                   │    │
│  │  ┌──────────────────────────────────────┐               │    │
│  │  │            Grafana :3000             │               │    │
│  │  │  Datasources: Prometheus,Loki,Tempo  │               │    │
│  │  └──────────────────────────────────────┘               │    │
│  │                                              ┌────────┐ │    │
│  │                                              │  Loki  │ │    │
│  │                                              │ :3100  │ │    │
│  │                                              └────────┘ │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                 │
│  ┌──────────────┐                                               │
│  │  PostgreSQL   │  ← traced queries dari Go API                │
│  │    :5432      │                                               │
│  └──────────────┘                                               │
└─────────────────────────────────────────────────────────────────┘
```

### Komponen & Port

| Komponen | Ports | Protocol | Tipe Service |
|----------|-------|----------|-------------|
| Prometheus | 9090 | HTTP | ClusterIP |
| Loki | 3100 (HTTP), 9096 (gRPC) | HTTP/gRPC | ClusterIP |
| Tempo | 3200 (HTTP), 4317 (OTLP gRPC), 4318 (OTLP HTTP), 9095 (gRPC) | HTTP/gRPC | ClusterIP |
| Promtail | 9080 | HTTP | DaemonSet (no service) |
| Grafana | 3000 | HTTP | ClusterIP |
| PostgreSQL | 5432 | TCP | ClusterIP |

### Data Flow

1. **Metrics**: Go API expose `/metrics` → Prometheus scrape setiap 15s → Grafana query
2. **Logs**: Go API stdout (JSON) → Promtail collect dari `/var/log/pods/` → push ke Loki → Grafana query
3. **Traces**: Go API kirim via OTLP gRPC ke Tempo :4317 → Grafana query
4. **Korelasi**: Trace ID embedded di log → Loki derived field extract → link ke Tempo

---

## 3. Struktur File

```
grafana-logging/
├── k8s/
│   ├── kustomization.yaml              # Entry point Kustomize
│   ├── base/
│   │   └── namespace.yaml              # Namespace "monitoring"
│   ├── prometheus/
│   │   ├── rbac.yaml                   # ServiceAccount, ClusterRole, ClusterRoleBinding
│   │   ├── configmap.yaml              # prometheus.yml (scrape config)
│   │   ├── deployment.yaml             # Deployment + PVC 10Gi + Service :9090
│   │   └── alerting-rules.yaml         # Alert rules (HighErrorRate, HighLatency, dll)
│   ├── loki/
│   │   ├── configmap.yaml              # loki.yaml (storage, retention, limits)
│   │   └── deployment.yaml             # Deployment + PVC 10Gi + Service :3100
│   ├── promtail/
│   │   ├── configmap.yaml              # promtail.yaml (scrape config, pipeline)
│   │   └── daemonset.yaml              # DaemonSet + RBAC + host path mounts
│   ├── tempo/
│   │   ├── configmap.yaml              # tempo.yaml (receivers, storage, metrics generator)
│   │   └── deployment.yaml             # Deployment + PVC 10Gi + Service multi-port
│   ├── grafana/
│   │   ├── configmap.yaml              # grafana.ini + datasources + dashboard provider
│   │   ├── dashboards-configmap.yaml   # Pre-built dashboard JSON
│   │   ├── deployment.yaml             # Deployment + PVC 5Gi + Service :3000 + Secret
│   │   └── ingress.yaml               # NGINX ingress (grafana.local, prometheus.local)
│   └── postgres/
│       ├── configmap.yaml              # init.sql (schema + sample data)
│       └── deployment.yaml             # Deployment + PVC 5Gi + Service :5432 + Secret
├── examples/go-api/                    # Contoh Go API terinstrumentasi
├── deploy.sh                           # Script deploy (Linux/Mac)
├── deploy.ps1                          # Script deploy (Windows)
└── undeploy.sh                         # Script uninstall
```

---

## 4. Deploy ke Docker Desktop (Local)

### 4.1. Enable Kubernetes di Docker Desktop

1. Buka **Docker Desktop** → **Settings** → **Kubernetes**
2. Centang **Enable Kubernetes**
3. Klik **Apply & Restart**
4. Tunggu sampai status Kubernetes hijau

### 4.2. Verifikasi Cluster

```bash
kubectl config use-context docker-desktop
kubectl cluster-info
kubectl get nodes
```

Output yang diharapkan:
```
Kubernetes control plane is running at https://kubernetes.docker.internal:6443
NAME             STATUS   ROLES           AGE   VERSION
docker-desktop   Ready    control-plane   ...   v1.32.x
```

### 4.3. Deploy Stack

**Cara 1: Kustomize (recommended)**

```bash
cd grafana-logging
kubectl apply -k k8s/
```

**Cara 2: Script (step-by-step)**

```bash
# Windows PowerShell
.\deploy.ps1

# Linux/Mac
chmod +x deploy.sh
./deploy.sh
```

**Cara 3: Manual (satu per satu)**

```bash
# 1. Namespace
kubectl apply -f k8s/base/namespace.yaml

# 2. Prometheus (RBAC harus duluan)
kubectl apply -f k8s/prometheus/rbac.yaml
kubectl apply -f k8s/prometheus/configmap.yaml
kubectl apply -f k8s/prometheus/alerting-rules.yaml
kubectl apply -f k8s/prometheus/deployment.yaml

# 3. Loki
kubectl apply -f k8s/loki/configmap.yaml
kubectl apply -f k8s/loki/deployment.yaml

# 4. Promtail
kubectl apply -f k8s/promtail/configmap.yaml
kubectl apply -f k8s/promtail/daemonset.yaml

# 5. Tempo
kubectl apply -f k8s/tempo/configmap.yaml
kubectl apply -f k8s/tempo/deployment.yaml

# 6. Grafana
kubectl apply -f k8s/grafana/configmap.yaml
kubectl apply -f k8s/grafana/dashboards-configmap.yaml
kubectl apply -f k8s/grafana/deployment.yaml

# 7. PostgreSQL
kubectl apply -f k8s/postgres/configmap.yaml
kubectl apply -f k8s/postgres/deployment.yaml
```

### 4.4. Verifikasi Deployment

```bash
# Cek semua pods running
kubectl get pods -n monitoring

# Expected output:
# NAME                          READY   STATUS    RESTARTS   AGE
# grafana-xxx                   1/1     Running   0          ...
# loki-xxx                      1/1     Running   0          ...
# postgres-xxx                  1/1     Running   0          ...
# prometheus-xxx                1/1     Running   0          ...
# promtail-xxx                  1/1     Running   0          ...
# tempo-xxx                     1/1     Running   0          ...

# Cek services
kubectl get svc -n monitoring

# Cek PVCs
kubectl get pvc -n monitoring
```

### 4.5. Akses Dashboard

```bash
# Grafana (http://localhost:3000)
kubectl port-forward svc/grafana 3000:3000 -n monitoring

# Prometheus (http://localhost:9090)
kubectl port-forward svc/prometheus 9090:9090 -n monitoring

# Tempo query API (http://localhost:3200)
kubectl port-forward svc/tempo 3200:3200 -n monitoring
```

**Grafana login:** `admin` / `admin123`

### 4.6. Catatan Docker Desktop

- Promtail pipeline menggunakan **Docker JSON format** karena Docker Desktop menggunakan Docker sebagai container runtime
- Volume mount `/var/lib/docker/containers` diperlukan untuk akses log container
- Semua service menggunakan **ClusterIP** — akses via `kubectl port-forward`

---

## 5. Deploy ke GKE (Production)

### 5.1. Perbedaan dengan Local

| Aspek | Docker Desktop | GKE |
|-------|---------------|-----|
| Container runtime | Docker (JSON logs) | containerd (CRI logs) |
| Log format | `{"log":"...","stream":"...","time":"..."}` | `<timestamp> <stream> <flag> <message>` |
| Volume mount | `/var/lib/docker/containers` diperlukan | Tidak diperlukan |
| Ingress | Port-forward | NGINX Ingress / GCE Ingress |
| RBAC | Otomatis tersedia | Perlu IAM permission |
| Storage | Default StorageClass | `standard-rwo` atau `premium-rwo` |
| Prometheus service | Bisa freshly deploy | Mungkin sudah ada (cek dulu) |

### 5.2. Adjustment yang Diperlukan

#### A. Promtail Pipeline — Ubah ke CRI Format

Edit `k8s/promtail/configmap.yaml`, ganti Docker JSON pipeline:

```yaml
# SEBELUM (Docker Desktop):
pipeline_stages:
  - json:
      expressions:
        log: log
        stream: stream
        docker_time: time
  - output:
      source: log

# SESUDAH (GKE/containerd):
pipeline_stages:
  - cri: {}
```

Lakukan untuk **kedua** job (`kubernetes-pods` dan `kubernetes-pods-raw`).

#### B. Promtail DaemonSet — Hapus Docker Volume

Edit `k8s/promtail/daemonset.yaml`, hapus volume mount dan volume Docker containers:

```yaml
# HAPUS dari volumeMounts:
- name: containers
  mountPath: /var/lib/docker/containers
  readOnly: true

# HAPUS dari volumes:
- name: containers
  hostPath:
    path: /var/lib/docker/containers
```

#### C. Tempo — Sesuaikan Prometheus Remote Write URL

Jika cluster sudah memiliki Prometheus yang existing, sesuaikan URL di `k8s/tempo/configmap.yaml`:

```yaml
# Cek nama service prometheus yang ada:
# kubectl get svc -n monitoring | grep prometheus

# Sesuaikan URL di tempo config:
metrics_generator:
  storage:
    remote_write:
      - url: http://<NAMA_SERVICE_PROMETHEUS>.<NAMESPACE>:<PORT>/api/v1/write
```

#### D. Prometheus — Enable Remote Write Receiver

Jika menggunakan Prometheus yang existing, tambahkan flag berikut di deployment args agar Tempo bisa push metrics:

```yaml
args:
  - "--web.enable-remote-write-receiver"
```

### 5.3. GKE RBAC Requirements

User yang men-deploy memerlukan IAM permissions berikut:

| Permission | Resource | Digunakan oleh |
|-----------|----------|---------------|
| `container.clusterRoles.create` | ClusterRole | Prometheus RBAC, Promtail RBAC |
| `container.clusterRoleBindings.create` | ClusterRoleBinding | Prometheus RBAC, Promtail RBAC |
| `container.deployments.create` | Deployment | Semua komponen |
| `container.services.create` | Service | Semua komponen |
| `container.configMaps.create` | ConfigMap | Semua komponen |
| `container.secrets.create` | Secret | Grafana, PostgreSQL |
| `container.persistentVolumeClaims.create` | PVC | Prometheus, Loki, Tempo, Grafana, PostgreSQL |

Jika tidak punya permission ClusterRole, minta admin cluster untuk menjalankan:

```bash
kubectl apply -f k8s/prometheus/rbac.yaml
kubectl apply -f k8s/promtail/daemonset.yaml  # bagian RBAC saja
```

### 5.4. Deploy ke GKE

```bash
# 1. Switch context ke GKE
kubectl config use-context <GKE_CONTEXT_NAME>

# 2. Verifikasi cluster
kubectl cluster-info
kubectl get nodes

# 3. Cek existing resources di monitoring namespace
kubectl get all -n monitoring

# 4. Deploy (setelah adjustment di atas)
kubectl apply -k k8s/

# 5. Verifikasi
kubectl get pods -n monitoring -w
```

### 5.5. Ingress (Opsional)

Jika menggunakan NGINX Ingress Controller:

```bash
# Install NGINX Ingress Controller di GKE
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.9.4/deploy/static/provider/cloud/deploy.yaml

# Apply ingress
kubectl apply -f k8s/grafana/ingress.yaml

# Cek external IP
kubectl get ingress -n monitoring
```

Tambahkan DNS record atau `/etc/hosts`:
```
<EXTERNAL_IP>  grafana.local
<EXTERNAL_IP>  prometheus.local
```

---

## 6. Konfigurasi Komponen

### 6.1. Prometheus

**File:** `k8s/prometheus/configmap.yaml`

| Parameter | Default | Keterangan |
|-----------|---------|------------|
| `scrape_interval` | 15s | Interval scrape metrics |
| `evaluation_interval` | 15s | Interval evaluasi alert rules |
| `retention.time` | 15d | Berapa lama data metrics disimpan |

**Scrape config** sudah otomatis discover pods dengan annotation:
```yaml
annotations:
  prometheus.io/scrape: "true"
  prometheus.io/port: "8080"
  prometheus.io/path: "/metrics"
```

**Alert rules** (`k8s/prometheus/alerting-rules.yaml`):

| Alert | Kondisi | Severity |
|-------|---------|----------|
| HighErrorRate | Error rate > 5% selama 5 menit | critical |
| HighLatency | P99 latency > 1 detik selama 5 menit | warning |
| PanicRecovered | Ada panic recovery | critical |
| PodDown | Pod down selama 2 menit | critical |
| HighMemoryUsage | Memory > 85% limit selama 5 menit | warning |
| HighCPUUsage | CPU > 85% limit selama 5 menit | warning |
| PrometheusTargetMissing | Target down selama 5 menit | critical |
| LokiDown | Loki unavailable selama 2 menit | critical |
| PrometheusStorageFull | Storage > 90% selama 5 menit | warning |
| HighLogVolume | Ingestion > 10MB/s selama 10 menit | warning |

### 6.2. Loki

**File:** `k8s/loki/configmap.yaml`

| Parameter | Default | Keterangan |
|-----------|---------|------------|
| `retention_period` | 168h (7 hari) | Berapa lama log disimpan |
| `ingestion_rate_mb` | 16 | Max ingestion rate per user (MB/s) |
| `ingestion_burst_size_mb` | 24 | Max burst size (MB) |
| `max_streams_per_user` | 50000 | Max concurrent log streams |
| `max_line_size` | 256kb | Max ukuran satu baris log |
| `cache max_size_mb` | 100 | Ukuran embedded cache |

### 6.3. Tempo

**File:** `k8s/tempo/configmap.yaml`

| Parameter | Default | Keterangan |
|-----------|---------|------------|
| OTLP gRPC port | 4317 | Port untuk menerima traces via gRPC |
| OTLP HTTP port | 4318 | Port untuk menerima traces via HTTP |
| `block_retention` | 1h | Berapa lama trace disimpan |
| `max_block_bytes` | 100MB | Max ukuran block setelah compaction |
| metrics_generator processors | `service-graphs`, `span-metrics` | Generate metrics dari traces |

Untuk menambah trace retention:
```yaml
compactor:
  compaction:
    block_retention: 48h  # ubah dari 1h ke 48h
```

### 6.4. Promtail

**File:** `k8s/promtail/configmap.yaml`

Dua scrape job:

| Job | Target | Pipeline |
|-----|--------|----------|
| `kubernetes-pods` | Pod dengan annotation `logging.enabled: "true"` | Parse JSON, extract labels (level, path, method) |
| `kubernetes-pods-raw` | Pod tanpa annotation logging | Minimal parsing, cegah high cardinality |

**Labels yang di-extract (low cardinality):**
- `level` — log level (info, warn, error, debug)
- `path` — API endpoint path
- `method` — HTTP method (GET, POST, dll)

**High cardinality fields** (trace_id, span_id) tetap di log content, query via `| json`:
```logql
{app="go-api"} | json | trace_id = "abc123"
```

### 6.5. Grafana

**File:** `k8s/grafana/configmap.yaml`

**Credentials:**
- Username: `admin`
- Password: dari Secret `grafana-secrets` (default: `admin123`)

**Datasources (auto-provisioned):**

| Datasource | URL | Fitur Korelasi |
|-----------|-----|---------------|
| Prometheus | `http://prometheus:9090` | Exemplar → Tempo (via trace_id) |
| Loki | `http://loki:3100` | Derived field → Tempo (regex extract trace_id) |
| Tempo | `http://tempo:3200` | Traces→Logs (Loki), Traces→Metrics (Prometheus), Service Map, Node Graph |

### 6.6. PostgreSQL

**File:** `k8s/postgres/deployment.yaml`

| Parameter | Default | Keterangan |
|-----------|---------|------------|
| Username | `goapi` | Dari Secret `postgres-secrets` |
| Password | `goapi-secret-password` | Dari Secret `postgres-secrets` |
| Database | `goapi` | Dibuat otomatis |
| Storage | 5Gi PVC | Persistent storage |

**Init script** (`k8s/postgres/configmap.yaml`) otomatis membuat tabel:
- `users`, `quotes`, `weather_cache`, `request_logs` + indexes + sample data

---

## 7. Onboarding Service Baru

Untuk mengintegrasikan service baru ke observability stack:

### 7.1. Metrics (Prometheus)

Tambahkan annotation di pod spec:

```yaml
metadata:
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "8080"      # port metrics
    prometheus.io/path: "/metrics"  # path metrics
```

Prometheus akan otomatis discover dan scrape metrics dari pod ini.

### 7.2. Logging (Loki via Promtail)

**Untuk structured JSON logging:**

Tambahkan annotation di pod spec:
```yaml
metadata:
  annotations:
    logging.enabled: "true"
```

Pastikan aplikasi output log ke stdout dalam format JSON:
```json
{"level":"info","time":"2026-01-15T10:30:00Z","msg":"Request completed","trace_id":"abc123","path":"/api/users","method":"GET"}
```

**Untuk non-JSON logs:**

Tidak perlu annotation — Promtail job `kubernetes-pods-raw` akan otomatis mengumpulkan.

### 7.3. Tracing (Tempo via OpenTelemetry)

Set environment variable di deployment:
```yaml
env:
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: "tempo.monitoring:4317"
  - name: TRACING_ENABLED
    value: "true"
```

Gunakan OpenTelemetry SDK di aplikasi. Contoh implementasi lengkap: lihat project **go-gin-boilerplate**.

### 7.4. Checklist Onboarding

```
[ ] Pod annotations untuk Prometheus scraping
[ ] Pod annotations untuk Promtail JSON parsing (jika applicable)
[ ] App label (`app: <service-name>`) di pod metadata
[ ] OpenTelemetry SDK terintegrasi, OTLP endpoint ke tempo.monitoring:4317
[ ] Structured JSON logging ke stdout dengan trace_id
[ ] Health check endpoints (/health/live, /health/ready)
[ ] Resource requests dan limits defined
[ ] Test: metrics muncul di Prometheus targets
[ ] Test: logs muncul di Loki (Grafana Explore)
[ ] Test: traces muncul di Tempo (Grafana Explore)
[ ] Test: korelasi trace_id dari logs ke traces berfungsi
```

---

## 8. Monitoring & Troubleshooting

### 8.1. Cek Status Komponen

```bash
# Semua pods
kubectl get pods -n monitoring

# Logs per komponen
kubectl logs -f deployment/prometheus -n monitoring
kubectl logs -f deployment/loki -n monitoring
kubectl logs -f deployment/tempo -n monitoring
kubectl logs -f daemonset/promtail -n monitoring
kubectl logs -f deployment/grafana -n monitoring
kubectl logs -f deployment/postgres -n monitoring

# Cek PVC usage
kubectl exec -n monitoring deploy/prometheus -- df -h /prometheus
kubectl exec -n monitoring deploy/loki -- df -h /loki
kubectl exec -n monitoring deploy/tempo -- df -h /var/tempo
```

### 8.2. Verifikasi Data Flow

**Prometheus scrape targets:**
```bash
kubectl port-forward svc/prometheus 9090:9090 -n monitoring
# Buka http://localhost:9090/targets — pastikan semua targets "UP"
```

**Loki menerima log:**
```bash
# Query Loki API langsung
kubectl exec -n monitoring deploy/loki -- wget -q -O - \
  "http://localhost:3100/loki/api/v1/labels"
```

**Tempo menerima traces:**
```bash
# Search traces di Tempo
kubectl exec -n monitoring deploy/tempo -- wget -q -O - \
  "http://localhost:3200/api/search?limit=5"
```

**PostgreSQL connectivity:**
```bash
kubectl exec -n monitoring deploy/postgres -- pg_isready -U goapi
```

### 8.3. Common Issues

| Problem | Diagnosis | Solusi |
|---------|-----------|-------|
| Promtail tidak baca log | `kubectl logs -f daemonset/promtail -n monitoring` | Cek host path mount dan container runtime format (Docker JSON vs CRI) |
| Loki tidak terima log | Cek Promtail config URL | Pastikan `url: http://loki:3100/loki/api/v1/push` |
| Prometheus tidak scrape pod | Cek `kubectl get pod -o yaml` annotations | Pastikan ada `prometheus.io/scrape: "true"` |
| Tempo tidak terima trace | Cek app log untuk OTLP error | Pastikan endpoint `tempo.monitoring:4317` reachable dari app pod |
| Trace-Log korelasi tidak jalan | Cek format log output | Pastikan `trace_id` ada di JSON log dan regex di Loki derived field cocok |
| Grafana datasource error | Cek Grafana logs | Pastikan service name dan port di datasource config benar |
| PVC pending | `kubectl get events -n monitoring` | Cek StorageClass tersedia dan ada kapasitas |
| Pod OOMKilled | `kubectl describe pod <name> -n monitoring` | Naikkan memory limit di deployment spec |
| RBAC error (GKE) | Error "cannot create resource clusterroles" | Minta cluster-admin untuk apply RBAC manifests |

### 8.4. Useful Queries

**LogQL (Loki):**
```logql
# Semua error logs dari app tertentu
{app="gin-boilerplate-api"} | json | level = "error"

# Cari berdasarkan trace ID
{app="gin-boilerplate-api"} | json | trace_id = "<TRACE_ID>"

# Error rate per 5 menit
sum(rate({app="gin-boilerplate-api"} | json | level = "error" [5m]))

# Top error messages
{app="gin-boilerplate-api"} | json | level = "error" | line_format "{{.msg}}"
```

**PromQL (Prometheus):**
```promql
# Request rate per endpoint
sum by (path) (rate(http_requests_total[5m]))

# Error rate percentage
sum(rate(http_requests_total{status=~"5.."}[5m]))
/ sum(rate(http_requests_total[5m])) * 100

# P95 latency
histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket[5m])) by (le))

# Memory usage
process_resident_memory_bytes{app="gin-boilerplate-api"}
```

**TraceQL (Tempo):**
```traceql
# Traces dari service tertentu
{resource.service.name="gin-boilerplate-api"}

# Traces dengan error
{resource.service.name="gin-boilerplate-api" && status=error}

# Slow traces (> 500ms)
{resource.service.name="gin-boilerplate-api" && duration > 500ms}

# Database queries lambat
{name=~".*db.*" && duration > 100ms}
```

---

## 9. Maintenance

### 9.1. Update Komponen

```bash
# Update image version di deployment.yaml, lalu:
kubectl apply -f k8s/<komponen>/deployment.yaml

# Atau rolling restart tanpa ubah config:
kubectl rollout restart deployment/<nama> -n monitoring
```

### 9.2. Backup & Restore

**PostgreSQL:**
```bash
# Backup
kubectl exec -n monitoring deploy/postgres -- pg_dump -U goapi goapi > backup.sql

# Restore
kubectl exec -i -n monitoring deploy/postgres -- psql -U goapi goapi < backup.sql
```

**Grafana dashboards:**

Dashboard di-provisioning dari ConfigMap sehingga otomatis tersedia setelah redeploy. Untuk custom dashboard yang dibuat via UI:

```bash
# Export via API
curl http://localhost:3000/api/dashboards/uid/<UID> \
  -H "Authorization: Bearer <API_KEY>" > dashboard.json
```

### 9.3. Scale Up

```bash
# Naikkan replicas (Loki, Grafana - stateless)
kubectl scale deployment grafana --replicas=2 -n monitoring

# Naikkan storage (edit PVC - jika StorageClass support expansion)
kubectl edit pvc prometheus-storage -n monitoring
# Ubah spec.resources.requests.storage

# Naikkan resource limits
kubectl edit deployment prometheus -n monitoring
# Ubah resources.limits
```

### 9.4. Uninstall

```bash
# Hapus semua via Kustomize
kubectl delete -k k8s/

# Atau manual (reverse order)
kubectl delete -f k8s/grafana/ --ignore-not-found
kubectl delete -f k8s/tempo/ --ignore-not-found
kubectl delete -f k8s/promtail/ --ignore-not-found
kubectl delete -f k8s/loki/ --ignore-not-found
kubectl delete -f k8s/prometheus/ --ignore-not-found
kubectl delete -f k8s/postgres/ --ignore-not-found
kubectl delete -f k8s/base/namespace.yaml --ignore-not-found

# Hapus PVCs (data akan hilang!)
kubectl delete pvc -n monitoring --all
```

### 9.5. Versi Komponen Saat Ini

| Komponen | Image | Versi |
|----------|-------|-------|
| Prometheus | `prom/prometheus` | v2.47.0 |
| Loki | `grafana/loki` | 2.9.2 |
| Promtail | `grafana/promtail` | 2.9.2 |
| Tempo | `grafana/tempo` | 2.3.1 |
| Grafana | `grafana/grafana` | 10.2.0 |
| PostgreSQL | `postgres` | 15-alpine |

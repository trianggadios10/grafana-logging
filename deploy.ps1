# PowerShell deployment script for Windows
$ErrorActionPreference = "Stop"

Write-Host "=== Deploying Grafana Logging Stack ===" -ForegroundColor Green

# Create namespace
Write-Host "Creating monitoring namespace..." -ForegroundColor Yellow
kubectl apply -f k8s/base/namespace.yaml

# Deploy Prometheus
Write-Host "Deploying Prometheus..." -ForegroundColor Yellow
kubectl apply -f k8s/prometheus/rbac.yaml
kubectl apply -f k8s/prometheus/configmap.yaml
kubectl apply -f k8s/prometheus/deployment.yaml

# Deploy Loki
Write-Host "Deploying Loki..." -ForegroundColor Yellow
kubectl apply -f k8s/loki/configmap.yaml
kubectl apply -f k8s/loki/deployment.yaml

# Deploy Promtail
Write-Host "Deploying Promtail..." -ForegroundColor Yellow
kubectl apply -f k8s/promtail/configmap.yaml
kubectl apply -f k8s/promtail/daemonset.yaml

# Deploy Grafana
Write-Host "Deploying Grafana..." -ForegroundColor Yellow
kubectl apply -f k8s/grafana/configmap.yaml
kubectl apply -f k8s/grafana/dashboards-configmap.yaml
kubectl apply -f k8s/grafana/deployment.yaml

Write-Host "=== Waiting for pods to be ready ===" -ForegroundColor Green
kubectl wait --for=condition=ready pod -l app=prometheus -n monitoring --timeout=120s
kubectl wait --for=condition=ready pod -l app=loki -n monitoring --timeout=120s
kubectl wait --for=condition=ready pod -l app=grafana -n monitoring --timeout=120s

Write-Host "=== Deployment Complete ===" -ForegroundColor Green
Write-Host ""
Write-Host "Access Grafana:" -ForegroundColor Cyan
Write-Host "  kubectl port-forward svc/grafana 3000:3000 -n monitoring"
Write-Host "  Open http://localhost:3000 (admin/admin123)"
Write-Host ""
Write-Host "Access Prometheus:" -ForegroundColor Cyan
Write-Host "  kubectl port-forward svc/prometheus 9090:9090 -n monitoring"
Write-Host "  Open http://localhost:9090"
Write-Host ""
Write-Host "To deploy the example Go API:" -ForegroundColor Cyan
Write-Host "  cd examples/go-api"
Write-Host "  docker build -t go-api:latest ."
Write-Host "  kubectl apply -f k8s-deployment.yaml"

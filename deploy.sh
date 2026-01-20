#!/bin/bash
set -e

echo "=== Deploying Grafana Logging Stack ==="

# Create namespace
echo "Creating monitoring namespace..."
kubectl apply -f k8s/base/namespace.yaml

# Deploy Prometheus
echo "Deploying Prometheus..."
kubectl apply -f k8s/prometheus/rbac.yaml
kubectl apply -f k8s/prometheus/configmap.yaml
kubectl apply -f k8s/prometheus/deployment.yaml

# Deploy Loki
echo "Deploying Loki..."
kubectl apply -f k8s/loki/configmap.yaml
kubectl apply -f k8s/loki/deployment.yaml

# Deploy Promtail
echo "Deploying Promtail..."
kubectl apply -f k8s/promtail/configmap.yaml
kubectl apply -f k8s/promtail/daemonset.yaml

# Deploy Grafana
echo "Deploying Grafana..."
kubectl apply -f k8s/grafana/configmap.yaml
kubectl apply -f k8s/grafana/dashboards-configmap.yaml
kubectl apply -f k8s/grafana/deployment.yaml

echo "=== Waiting for pods to be ready ==="
kubectl wait --for=condition=ready pod -l app=prometheus -n monitoring --timeout=120s
kubectl wait --for=condition=ready pod -l app=loki -n monitoring --timeout=120s
kubectl wait --for=condition=ready pod -l app=grafana -n monitoring --timeout=120s

echo "=== Deployment Complete ==="
echo ""
echo "Access Grafana:"
echo "  kubectl port-forward svc/grafana 3000:3000 -n monitoring"
echo "  Open http://localhost:3000 (admin/admin123)"
echo ""
echo "Access Prometheus:"
echo "  kubectl port-forward svc/prometheus 9090:9090 -n monitoring"
echo "  Open http://localhost:9090"
echo ""
echo "To deploy the example Go API:"
echo "  cd examples/go-api"
echo "  docker build -t go-api:latest ."
echo "  kubectl apply -f k8s-deployment.yaml"

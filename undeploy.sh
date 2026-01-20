#!/bin/bash
set -e

echo "=== Removing Grafana Logging Stack ==="

# Delete Grafana
echo "Removing Grafana..."
kubectl delete -f k8s/grafana/ --ignore-not-found

# Delete Promtail
echo "Removing Promtail..."
kubectl delete -f k8s/promtail/ --ignore-not-found

# Delete Loki
echo "Removing Loki..."
kubectl delete -f k8s/loki/ --ignore-not-found

# Delete Prometheus
echo "Removing Prometheus..."
kubectl delete -f k8s/prometheus/ --ignore-not-found

# Delete namespace
echo "Removing namespace..."
kubectl delete -f k8s/base/namespace.yaml --ignore-not-found

echo "=== Removal Complete ==="

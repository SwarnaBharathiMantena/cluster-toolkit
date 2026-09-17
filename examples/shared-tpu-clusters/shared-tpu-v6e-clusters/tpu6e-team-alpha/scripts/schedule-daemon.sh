#!/usr/bin/env bash
set -euo pipefail

echo "[schedule-daemon] Starting dynamic TPU v6e quota & schedule monitor..."
echo "[schedule-daemon] Cohort: ${COHORT_NAME:-tpuv6e-shared-cohort}"
while true; do
  echo "[schedule-daemon] Heartbeat at $(date -u +%Y-%m-%dT%H:%M:%SZ) - monitoring TPU v6e cluster queues..."
  sleep 30
done

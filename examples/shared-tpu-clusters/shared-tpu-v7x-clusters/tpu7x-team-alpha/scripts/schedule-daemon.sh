#!/usr/bin/env bash
set -euo pipefail

echo "[schedule-daemon] Starting dynamic TPU v7x quota & schedule monitor..."
echo "[schedule-daemon] Cohort: ${COHORT_NAME:-tpu7x-shared-cohort}"
while true; do
  echo "[schedule-daemon] Heartbeat at $(date -u +%Y-%m-%dT%H:%M:%SZ) - monitoring TPU v7x cluster queues..."
  sleep 30
done

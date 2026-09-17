#!/usr/bin/env bash
set -euo pipefail

echo "[schedule-daemon] Starting dynamic quota & schedule monitor..."
echo "[schedule-daemon] Cohort: ${COHORT_NAME:-cpu-shared-cohort}"
while true; do
  echo "[schedule-daemon] Heartbeat at $(date -u +%Y-%m-%dT%H:%M:%SZ) - monitoring cluster queues..."
  sleep 30
done

#!/bin/bash
# Mock Slice Operator
# Watches for Slice resources and automatically patches their status to Ready=True

KUBECONFIG_path="${1}"
if [ -z "${KUBECONFIG_path}" ]; then
  echo "Usage: $0 <path-to-kubeconfig>"
  exit 1
fi

export KUBECONFIG="${KUBECONFIG_path}"

echo "Starting Mock Slice Operator..."
while true; do
  # Get all slices
  slices=$(kubectl get slice -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true)
  for slice in ${slices}; do
    # Check if it is already ready
    ready=$(kubectl get slice "${slice}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
    if [ "${ready}" != "True" ]; then
      echo "Patching Slice ${slice} to Ready..."
      now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
      kubectl patch slice "${slice}" --subresource=status --type merge -p '{"status": {"conditions": [{"type": "Ready", "status": "True", "lastTransitionTime": "'"${now}"'"}]}}' 2>/dev/null || {
        # If status subresource patch fails, try regular patch
        kubectl patch slice "${slice}" --type merge -p '{"status": {"conditions": [{"type": "Ready", "status": "True", "lastTransitionTime": "'"${now}"'"}]}}'
      }
    fi
  done
  sleep 2
done

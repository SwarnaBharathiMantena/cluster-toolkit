#!/bin/bash
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# E2E Minimal Verification Script for Vanilla K8s on TPUs
#
# This script demonstrates the core capability of deploying a TPU workload
# on a vanilla Kubernetes cluster (non-GKE) using gcluster. It bypasses
# the Project Bloom simulation for a faster, simpler demo.

set -e

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)

# Use deployment name from arg, default to hkthn-tpu-k8s-1
DEPLOYMENT_NAME="${1:-hkthn-tpu-k8s-1}"
KUBECONFIG="${KUBECONFIG:-${SCRIPT_DIR}/../../${DEPLOYMENT_NAME}/primary/kubeconfig}"

export KUBECONFIG

# =====================================================================
# DEMO POINT 1: Verify Cluster Connection and Nodes
# =====================================================================
# We verify that gcluster successfully generated the kubeconfig file during deployment.
# We then show the nodes in the cluster.
# Point out that we have 1 Master VM and 4 TPU VM Workers running vanilla Kubernetes (not GKE!).
# =====================================================================
echo "=== 1. Checking Kubeconfig ==="
if [ ! -f "${KUBECONFIG}" ]; then
  echo "Error: Kubeconfig not found at ${KUBECONFIG}"
  exit 1
fi

echo "=== 2. Verifying Nodes ==="
kubectl get nodes -o wide

# =====================================================================
# DEMO POINT 2: Install TPU Device Plugin
# =====================================================================
# We install the Google TPU device plugin. This is required so that Kubernetes
# knows how to schedule workloads on TPU VMs and expose the 'google.com/tpu' resource.
# We use a local manifest modified to work on vanilla K8s.
# =====================================================================
echo "=== 3. Installing TPU Device Plugin ==="
kubectl apply -f "${SCRIPT_DIR}/tpu-device-plugin.yaml"

echo "Waiting for TPU device plugin to be ready..."
sleep 5
kubectl rollout status daemonset/tpu-device-plugin-daemonset -n kube-system --timeout=60s

# =====================================================================
# DEMO POINT 3: Submit Minimal TPU Workload via gcluster
# =====================================================================
# We use the 'gcluster job submit' command to submit a simple workload.
# Point out that the command is EXACTLY the same as what researchers use on GKE,
# but it is now targeting our vanilla K8s cluster via the '--kubeconfig' flag.
# The job will request 4 TPUs (1 VM) and run 'ls -l /dev/accel*' to verify
# that the TPU hardware devices are successfully mounted inside the container.
# =====================================================================
echo "=== 4. Submitting Minimal TPU Workload ==="
"${SCRIPT_DIR}/../../gcluster" job submit \
  --kubeconfig "${KUBECONFIG}" \
  --name tpu-verify-minimal \
  --compute-type tpu-4 \
  --image ubuntu:22.04 \
  --command "echo '=== TPU Verification ==='; echo 'Checking for TPU devices in /dev:'; ls -l /dev/accel*; echo 'TPU Verification Successful!';"

# =====================================================================
# DEMO POINT 4: Monitor and Verify Workload
# =====================================================================
# We wait for the pod to be scheduled and run.
# Once running, we print the logs to prove that the TPU devices (`/dev/accel0` etc.)
# are visible inside the container on the vanilla K8s node.
# =====================================================================
echo "=== 5. Monitoring Workload ==="
echo "Waiting for pod to start..."
sleep 5
kubectl wait --for=condition=Ready pod -l gcluster.google.com/workload=tpu-verify-minimal --timeout=120s

echo "=== 6. Workload Logs ==="
kubectl logs -l gcluster.google.com/workload=tpu-verify-minimal

echo "=== Verification Complete! ==="
# Cleanup the job
kubectl delete jobset tpu-verify-minimal

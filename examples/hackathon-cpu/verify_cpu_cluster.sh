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

# E2E Verification Script for Vanilla K8s on CPUs
#
# This script demonstrates the core capability of deploying a CPU workload
# on a vanilla Kubernetes cluster (non-GKE) using gcluster. This is a fast,
# highly reliable demo that uses standard GCE VMs.

set -e

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)

# Use deployment name from arg, default to hkthn-cpu-k8s-1
DEPLOYMENT_NAME="${1:-hkthn-cpu-k8s-1}"
KUBECONFIG="${KUBECONFIG:-${SCRIPT_DIR}/../../${DEPLOYMENT_NAME}/primary/kubeconfig}"

export KUBECONFIG

# =====================================================================
# DEMO POINT 1: Verify Cluster Connection and Nodes
# =====================================================================
# We verify that gcluster successfully generated the kubeconfig file during deployment.
# We then show the nodes in the cluster.
# Point out that we have 1 Master VM and 2 Worker VMs running vanilla Kubernetes (not GKE!).
# =====================================================================
echo "=== 1. Checking Kubeconfig ==="
if [ ! -f "${KUBECONFIG}" ]; then
  echo "Error: Kubeconfig not found at ${KUBECONFIG}"
  exit 1
fi

echo "=== 2. Verifying Nodes ==="
kubectl get nodes -o wide

# =====================================================================
# DEMO POINT 2: Submit CPU Workload via gcluster
# =====================================================================
# We use the 'gcluster job submit' command to submit a simple workload.
# Point out that the command is EXACTLY the same as what researchers use on GKE,
# but it is now targeting our vanilla K8s cluster via the '--kubeconfig' flag.
# The job will run on one of the CPU worker nodes and print its hostname.
# =====================================================================
echo "=== 3. Submitting CPU Workload ==="
"${SCRIPT_DIR}/../../gcluster" job submit \
  --kubeconfig "${KUBECONFIG}" \
  --name cpu-verify \
  --image ubuntu:22.04 \
  --command "echo '=== CPU Verification ==='; echo 'Running on host:'; hostname; echo 'CPU Verification Successful!';"

# =====================================================================
# DEMO POINT 3: Monitor and Verify Workload
# =====================================================================
# We wait for the pod to be scheduled and run.
# Once running, we print the logs to prove that the job executed successfully
# on our vanilla K8s worker node.
# =====================================================================
echo "=== 4. Monitoring Workload ==="
echo "Waiting for pod to start..."
sleep 5
kubectl wait --for=condition=Ready pod -l gcluster.google.com/workload=cpu-verify --timeout=120s

echo "=== 5. Workload Logs ==="
kubectl logs -l gcluster.google.com/workload=cpu-verify

echo "=== Verification Complete! ==="
# Cleanup the job
kubectl delete jobset cpu-verify

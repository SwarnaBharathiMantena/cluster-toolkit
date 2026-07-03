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

# E2E Verification Script for Vanilla K8s on TPUs (with Project Bloom Simulation)
#
# This script demonstrates the full integration of gcluster with a vanilla K8s cluster
# and the Project Bloom control plane simulation (Slice CRDs and Mock Operators).

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
# DEMO POINT 3: Simulate Project Bloom Control Plane
# =====================================================================
# We install the custom 'Slice' Custom Resource Definition (CRD) and start the
# Mock Slice Operator in the background. This simulates the Project Bloom
# environment where TPU slices are managed as custom Kubernetes resources.
# =====================================================================
echo "=== 4. Simulating Project Bloom Control Plane ==="
echo "Applying Slice CRD..."
kubectl apply -f "${SCRIPT_DIR}/slice-crd.yaml"

echo "Starting Mock Slice Operator in background..."
"${SCRIPT_DIR}/mock-slice-operator.sh" &
OPERATOR_PID=$!
# Ensure the background operator is cleaned up when this script exits
trap 'kill ${OPERATOR_PID} 2>/dev/null || true' EXIT

# =====================================================================
# DEMO POINT 4: Bootstrap Master VM for Bloom CLI
# =====================================================================
# We copy a mock 'grpc_cli' to the Master VM. In a real Bloom deployment,
# this CLI is used by the master to communicate with the TPU VMs.
# We use SSH to copy the script and set up a alias.
# =====================================================================
echo "=== 5. Bootstrapping Master VM for Bloom CLI ==="
MASTER_IP=$(kubectl config view -o jsonpath='{.clusters[0].cluster.server}' | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+')
SSH_KEY="${SCRIPT_DIR}/../../${DEPLOYMENT_NAME}/primary/k8s-hackathon-ssh-key.pem"

echo "Copying mock grpc_cli to master (${MASTER_IP})..."
# If the primary scp fails, we fall back to a secondary method
scp -i "${SSH_KEY}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "${SCRIPT_DIR}/mock-grpc-cli.sh" ubuntu@"${MASTER_IP}":/tmp/grpc_cli || \
ssh -i "${SSH_KEY}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null ubuntu@"${MASTER_IP}" "cat > /tmp/grpc_cli" < "${SCRIPT_DIR}/mock-grpc-cli.sh"

ssh -i "${SSH_KEY}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null ubuntu@"${MASTER_IP}" "chmod +x /tmp/grpc_cli && sudo mv /tmp/grpc_cli /usr/local/bin/grpc_cli"

# =====================================================================
# DEMO POINT 5: Submit TPU Workload via gcluster
# =====================================================================
# We use the 'gcluster job submit' command to submit a JAX job.
# Point out that the command is EXACTLY the same as what researchers use on GKE,
# but it is now targeting our vanilla K8s cluster via the '--kubeconfig' flag.
# The job will request 4 TPUs (1 VM) and run a JAX verification command.
# =====================================================================
echo "=== 6. Submitting JAX TPU Workload ==="
"${SCRIPT_DIR}/../../gcluster" job submit \
  --kubeconfig "${KUBECONFIG}" \
  --name tpu-verify \
  --compute-type tpu-4 \
  --image us-docker.pkg.dev/urika-gke/images/jax-tpu-tpu-only:latest \
  --command "python -c 'import jax; print(\"JAX Devices:\", jax.devices())'"

# =====================================================================
# DEMO POINT 6: Monitor and Verify Workload
# =====================================================================
# We wait for the pod to be scheduled and run.
# Once running, we print the logs to prove that JAX successfully detected
# the 4 TPU chips on the vanilla K8s node.
# =====================================================================
echo "=== 7. Monitoring Workload ==="
echo "Waiting for pod to start..."
sleep 10
kubectl wait --for=condition=Ready pod -l gcluster.google.com/workload=tpu-verify --timeout=300s

echo "=== 8. Workload Logs ==="
# Show the logs. You should see "JAX Devices: [TpuDevice(id=0, ...)]"
kubectl logs -l gcluster.google.com/workload=tpu-verify

echo "=== Verification Complete! ==="
# Cleanup the job
kubectl delete jobset tpu-verify

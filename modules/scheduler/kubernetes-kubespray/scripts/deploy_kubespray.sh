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

set -e

export ANSIBLE_HOST_KEY_CHECKING=False
export ANSIBLE_SSH_COMMON_ARGS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"

# Inputs from environment variables
: "${INVENTORY_FILE:?INVENTORY_FILE must be set}"
: "${SSH_PRIVATE_KEY_PATH:?SSH_PRIVATE_KEY_PATH must be set}"
: "${MASTER_IP:?MASTER_IP must be set}"

KUBESPRAY_VERSION="${KUBESPRAY_VERSION:-release-2.24}"
K8S_VERSION="${K8S_VERSION:-v1.28.6}"
SSH_USER="${SSH_USER:-ubuntu}"
KUBECONFIG_OUTPUT_PATH="${KUBECONFIG_OUTPUT_PATH:-kubeconfig}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
WORK_DIR="${MODULE_DIR}/.kubespray_work"

mkdir -p "${WORK_DIR}"
cd "${WORK_DIR}"

echo "=== 1. Cloning Kubespray (${KUBESPRAY_VERSION}) ==="
if [ ! -d "kubespray" ]; then
  git clone --depth 1 -b "${KUBESPRAY_VERSION}" https://github.com/kubernetes-sigs/kubespray.git
else
  echo "Kubespray already cloned."
fi

cd kubespray

# Upgrade ruamel.yaml.clib to support Python 3.13
sed -i 's/ruamel.yaml.clib==0.2.8/ruamel.yaml.clib>=0.2.12/' requirements.txt

echo "=== 2. Setting up Python Virtual Environment and installing dependencies ==="
if [ ! -d "venv" ]; then
  python3 -m venv venv
fi
source venv/bin/activate

# Upgrade pip and install wheel
pip install -q --upgrade pip wheel

# Install Kubespray requirements (Ansible, etc.)
CFLAGS="-Wno-error=incompatible-pointer-types" pip install -q -r requirements.txt

echo "=== 2.5. Waiting for SSH and APT locks on all nodes ==="
IPS=$(grep -oE 'ansible_host=[0-9.]+' "${INVENTORY_FILE}" | cut -d= -f2)
for IP in ${IPS}; do
  echo "Checking node ${IP}..."
  for i in {1..30}; do
    if ssh -i "${SSH_PRIVATE_KEY_PATH}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "${SSH_USER}"@"${IP}" \
      "sudo bash -c 'while ! flock -n /var/lib/dpkg/lock-frontend true >/dev/null 2>&1 || ! flock -n /var/lib/dpkg/lock true >/dev/null 2>&1 || ! flock -n /var/lib/apt/lists/lock true >/dev/null 2>&1; do echo \"Waiting for APT lock...\"; sleep 5; done'" 2>/dev/null; then
      echo "Node ${IP} is ready (SSH up, APT free)"
      break
    fi
    echo "Waiting for SSH to be ready on ${IP}... (attempt $i/30)"
    sleep 5
  done
done

echo "=== 3. Running Kubespray Ansible Playbook ==="
# We pass extra vars to configure the Kubernetes version
ansible-playbook -i "${INVENTORY_FILE}" \
  --private-key="${SSH_PRIVATE_KEY_PATH}" \
  -u "${SSH_USER}" \
  --become \
  -e "kube_version=${K8S_VERSION}" \
  -e "supplementary_addresses_in_ssl_keys=['${MASTER_IP}']" \
  cluster.yml

echo "=== 4. Fetching and patching Kubeconfig ==="
# We need to fetch /etc/kubernetes/admin.conf from the master node
# and replace 127.0.0.1 with the master's IP so we can access it externally.
mkdir -p "$(dirname "${KUBECONFIG_OUTPUT_PATH}")"

# SSH and fetch the file
ssh -o StrictHostKeyChecking=no -i "${SSH_PRIVATE_KEY_PATH}" "${SSH_USER}@${MASTER_IP}" "sudo cat /etc/kubernetes/admin.conf" > "${KUBECONFIG_OUTPUT_PATH}.raw"

# Replace 127.0.0.1 or localhost with the master IP
sed "s/127.0.0.1/${MASTER_IP}/g" "${KUBECONFIG_OUTPUT_PATH}.raw" | sed "s/localhost/${MASTER_IP}/g" > "${KUBECONFIG_OUTPUT_PATH}"
rm "${KUBECONFIG_OUTPUT_PATH}.raw"

# Set restrictive permissions on the kubeconfig
chmod 600 "${KUBECONFIG_OUTPUT_PATH}"

echo "=== Kubernetes Bootstrap Complete! Kubeconfig saved to: ${KUBECONFIG_OUTPUT_PATH} ==="

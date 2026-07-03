---
name: vanilla-k8s-tpu
description: Manage and verify vanilla Kubernetes clusters with TPUs and Bloom mocks
---

Vanilla Kubernetes with TPUs and Bloom Mocks
============================================

This skill helps you manage, deploy, and verify vanilla Kubernetes clusters with Cloud TPUs, including setting up mocks for the Bloom control plane.

Prerequisites
-------------

- `gcluster` CLI built with generic Kubernetes support (compiled with `kubeconfig` flag).
- A target vanilla Kubernetes cluster (e.g. deployed via Kubespray).
- `kubectl` installed and configured.

Deployment
----------

To deploy a vanilla K8s cluster with TPUs using Kubespray:

1. Use the blueprint [hackathon-tpu-k8s.yaml](file:///usr/local/google/home/swarnabm/cluster-toolkit/examples/hackathon-tpu/hackathon-tpu-k8s.yaml).
2. Run `gcluster deploy`:

    ```bash
    gcluster deploy examples/hackathon-tpu/hackathon-tpu-k8s.yaml -w
    ```

3. This will provision:
    - A CPU master VM.
    - TPU VM workers (using `modules/compute/tpu-vm`).
    - Bootstrap them via `modules/scheduler/kubernetes-kubespray`.

Verification
------------

To verify the TPU cluster and run JAX workloads:

1. Ensure you have the following files in `examples/hackathon-tpu/`:
    - [tpu-device-plugin.yaml](file:///usr/local/google/home/swarnabm/cluster-toolkit/examples/hackathon-tpu/tpu-device-plugin.yaml)
    - [slice-crd.yaml](file:///usr/local/google/home/swarnabm/cluster-toolkit/examples/hackathon-tpu/slice-crd.yaml)
    - [mock-slice-operator.sh](file:///usr/local/google/home/swarnabm/cluster-toolkit/examples/hackathon-tpu/mock-slice-operator.sh)
    - [mock-grpc-cli.sh](file:///usr/local/google/home/swarnabm/cluster-toolkit/examples/hackathon-tpu/mock-grpc-cli.sh)
    - [submit_maxtext_tpu.sh](file:///usr/local/google/home/swarnabm/cluster-toolkit/examples/hackathon-tpu/submit_maxtext_tpu.sh)
2. Run the verification script:

    ```bash
    ./examples/hackathon-tpu/verify_tpu_cluster.sh
    ```

### How the Mocks Work

- **Slice CRD**: [slice-crd.yaml](file:///usr/local/google/home/swarnabm/cluster-toolkit/examples/hackathon-tpu/slice-crd.yaml) defines the `Slice` resource used by Bloom.
- **Mock Slice Operator**: [mock-slice-operator.sh](file:///usr/local/google/home/swarnabm/cluster-toolkit/examples/hackathon-tpu/mock-slice-operator.sh) watches for `Slice` resources and automatically marks them as `Ready`.
- **Mock grpc_cli**: [mock-grpc-cli.sh](file:///usr/local/google/home/swarnabm/cluster-toolkit/examples/hackathon-tpu/mock-grpc-cli.sh) mocks the TPU Command Center (TPU-CC) `GetPartitionState` response, returning `STATE_AVAILABLE` for the expected partition (stored in `/tmp/expected_partition`).

Job Submission
--------------

To submit JAX jobs to the vanilla K8s cluster:

1. Use `gcluster job submit` with the `--kubeconfig` flag.
2. The helper script [submit_maxtext_tpu.sh](file:///usr/local/google/home/swarnabm/cluster-toolkit/examples/hackathon-tpu/submit_maxtext_tpu.sh) automates this:

    ```bash
    ./examples/hackathon-tpu/submit_maxtext_tpu.sh
    ```

    This script dynamically configures JAX multi-host environment variables (`JAX_PROCESS_ID`, `JAX_NUM_PROCESSES`, `JAX_COORDINATOR_ADDRESS`) and calls `gcluster job submit` with `--kubeconfig`.

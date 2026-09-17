# Shared TPU Clusters Multi-Machine-Type Architecture (`shared-tpu-clusters/`)

This directory demonstrates how to organize shared GKE TPU cluster deployments across multiple TPU machine types (`TPU v7x` and `TPU v6e`) while maintaining:
1. **One `base.yaml` per machine type** (`shared-tpu-v7x-clusters/base.yaml` and `shared-tpu-v6e-clusters/base.yaml`) to prevent architectural drift across clusters of the same accelerator generation.
2. **Parallel cluster/team child directories (`<machine-type>-<team>/`)** sitting side-by-side with `base.yaml`, parking `deployment-vars.yaml`, `scripts/schedule-daemon.sh`, and all numbered Day-2 Kubernetes manifests (`01-multi-nic.yaml` through `09-custom-addons.yaml`).
3. **Parameterized 100%-Borrowing Default Queues (`08-quota-namespaces.yaml.tftpl`)**: Each cluster defines a `default-cq` with `nominalQuota: "0"` and `borrowingLimit: "${total_tpu_capacity}"` (100% borrowing capacity across the cohort) alongside nominal tenant queues (`team-alpha-cq`, `team-beta-cq`) with `reclaimWithinCohort: Any`.
4. **Pushed & Mounted Shell Scripts (`06-schedule-daemon.yaml.tftpl`)**: Local shell scripts (`scripts/schedule-daemon.sh`) are staged via `ghpc_stage(vars.cluster_dir)`, injected into a Kubernetes `ConfigMap` (`${indent(4, file(script_path))}`), mounted executable (`0755`), and tracked via a SHA-256 checksum annotation (`checksum/script: "${sha256(file(script_path))}"`) so pods automatically roll on script updates.

---

## Directory Structure

```text
shared-tpu-clusters/                         # Multi-machine-type top-level directory
├── README.md
├── shared-tpu-v7x-clusters/                 # Machine-type directory for TPU v7x
│   ├── base.yaml                            # ONE base blueprint for TPU v7x
│   └── tpu7x-team-alpha/                    # Parallel child directory (<machine-type>-<team>)
│       ├── deployment-vars.yaml             # Cluster variables (-d) including cluster_dir: "./tpu7x-team-alpha"
│       ├── scripts/
│       │   └── schedule-daemon.sh           # Shell script pushed to cluster via ConfigMap
│       ├── 01-multi-nic.yaml                # Populated multi-NIC GKENetworkParamSet/Network CRDs for v7x DCN
│       ├── 02-quota-setup.yaml              # ResourceFlavor for tpu7x
│       ├── 03-kyverno.yaml
│       ├── 04-priority-class.yaml
│       ├── 05-time-limit-controller.yaml
│       ├── 06-schedule-daemon.yaml.tftpl    # Mounts & runs scripts/schedule-daemon.sh
│       ├── 07-rbac.yaml
│       ├── 08-quota-namespaces.yaml.tftpl   # Parameterized 100%-borrowing default + nominal google.com/tpu queues
│       └── 09-custom-addons.yaml
└── shared-tpu-v6e-clusters/                 # Machine-type directory for TPU v6e
    ├── base.yaml                            # ONE base blueprint for TPU v6e
    └── tpu6e-team-alpha/                    # Parallel child directory (<machine-type>-<team>)
        ├── deployment-vars.yaml             # Cluster variables (-d) including cluster_dir: "./tpu6e-team-alpha"
        ├── scripts/
        │   └── schedule-daemon.sh           # Shell script pushed to cluster via ConfigMap
        ├── 01-multi-nic.yaml                # Empty manifest (v6e opts out of secondary multi-NIC DCN)
        ├── 02-quota-setup.yaml              # ResourceFlavor for tpu-v6e-slice
        ├── 03-kyverno.yaml
        ├── 04-priority-class.yaml
        ├── 05-time-limit-controller.yaml
        ├── 06-schedule-daemon.yaml.tftpl    # Mounts & runs scripts/schedule-daemon.sh
        ├── 07-rbac.yaml
        ├── 08-quota-namespaces.yaml.tftpl   # Parameterized 100%-borrowing default + nominal google.com/tpu queues
        └── 09-custom-addons.yaml
```

---

## Deployment Commands

### 1. Deploy or Reconcile a Full Cluster (Prevent Drift)

**For TPU v7x (`tpu7x-team-alpha`):**
```bash
./gcluster deploy examples/shared-tpu-clusters/shared-tpu-v7x-clusters/base.yaml \
  -d examples/shared-tpu-clusters/shared-tpu-v7x-clusters/tpu7x-team-alpha/deployment-vars.yaml \
  -w
```

**For TPU v6e (`tpu6e-team-alpha`):**
```bash
./gcluster deploy examples/shared-tpu-clusters/shared-tpu-v6e-clusters/base.yaml \
  -d examples/shared-tpu-clusters/shared-tpu-v6e-clusters/tpu6e-team-alpha/deployment-vars.yaml \
  -w
```

### 2. Fast Day-2 Granular Updates (`--only=<group>`)

To update only the Kueue quotas (`08-quota-namespaces`) or the schedule daemon script (`06-schedule-daemon`) in ~10 seconds without touching infrastructure:

```bash
# Update quotas only on TPU v7x cluster
./gcluster deploy examples/shared-tpu-clusters/shared-tpu-v7x-clusters/base.yaml \
  -d examples/shared-tpu-clusters/shared-tpu-v7x-clusters/tpu7x-team-alpha/deployment-vars.yaml \
  -w --only=08-quota-namespaces

# Update schedule-daemon.sh ConfigMap & trigger automatic rolling restart on TPU v6e cluster
./gcluster deploy examples/shared-tpu-clusters/shared-tpu-v6e-clusters/base.yaml \
  -d examples/shared-tpu-clusters/shared-tpu-v6e-clusters/tpu6e-team-alpha/deployment-vars.yaml \
  -w --only=06-schedule-daemon
```

# Register a Cluster Toolkit deployment into Cluster Director

This example provisions a small Cluster Toolkit deployment (a VPC, a Cloud
Storage bucket and two VMs) and registers it into
[Cluster Director](https://cloud.google.com/cluster-director) so that the whole
deployment is observable as a single cluster.

Registration *imports* pre-existing resources. Cluster Director does not create
or destroy any of the infrastructure that the Cluster Toolkit manages, and
deregistering a cluster leaves the deployment untouched.

## How it works

**The blueprint contains no registration wiring at all.** It declares a VPC, a
bucket and two VMs, and nothing else. Registration is part of the `gcluster`
lifecycle:

| Command            | What happens                                              |
| ------------------ | --------------------------------------------------------- |
| `gcluster deploy`  | After every group applies, the deployment is registered.    |
| `gcluster destroy` | Before anything is torn down, the deployment is deregistered. |
| `gcluster destroy --only`/`--skip` | The registration is shrunk to the resources that will survive, not deleted. |

It is **on by default**. Pass `--skip-cluster-director-registration` to either
command to opt out.

### Where the resources come from

Nothing is declared, so nothing has to be kept in sync. After the apply,
`gcluster` reads the **Terraform state** of every group in the deployment and
picks out the resources Cluster Director can import:

| Terraform resource type | Registered as |
| --- | --- |
| `google_compute_network` | `networkResources.ctk-vpc-mapping` |
| `google_compute_subnetwork` | (same entry, paired with the network) |
| `google_storage_bucket` | `storageResources.ctk-bucket-N` |
| `google_filestore_instance` | `storageResources.ctk-filestore-N` |
| `google_lustre_instance` | `storageResources.ctk-lustre-N` |
| `google_compute_instance_group_manager` (and the regional form) | `existingInstances.ctk-mig-N` |
| the `ghpc_deployment` label | `existingInstances.ctk-nodes` |

Compute is adopted by label, not by discovery: every resource the Cluster
Toolkit creates carries `ghpc_deployment=<deployment_name>`, and Cluster
Director re-evaluates that selector continuously, so scaling needs no
re-registration.

Identity comes from the blueprint globals every blueprint already has:

| Value | Source |
| --- | --- |
| Cluster ID | `deployment_name`, sanitized via `SanitizeClusterID` (1–10 lowercase alphanumeric chars, no hyphens; names ≤10 chars after stripping non-alphanumerics are used directly, longer names append a 4-char hash) |
| Project, region, zone | `project_id`, `region`, `zone` |
| Endpoint | Always prod; not configurable on `gcluster deploy` |

> [!NOTE]
> Terraform records each resource in state as either `managed` (Terraform
> created it) or `data` (Terraform only read it). Data sources count only for
> the network and subnetwork, because reading a VPC that already exists
> (`modules/network/pre-existing-vpc`) is a normal pattern and a cluster cannot
> be registered without its network. For storage and compute, a data source is
> far more likely to be an incidental lookup — a script reading some unrelated
> bucket — than a statement of membership, so it is ignored.

Partial deploys are registered like any other. `--only` and `--skip` change
which groups are applied, not which are discovered: state is read from every
Terraform group, so the registration always reflects the deployment as it
stands after the run, whether the run added resources or removed them.

Partial *destroys* mirror that. `gcluster destroy --only <group>` leaves a
cluster standing, so it updates the registration to the resources that will
survive rather than deleting it, and keeps the record. Only a destroy that
leaves no Terraform group behind deregisters — including one spelled
`--only a,b,c` that happens to name them all.

Registration is skipped, with a message and without failing, when the blueprint
has no `project_id`/`region`, since there is nothing to register against. Other
problems — a bare reservation name with no `zone` to qualify it, for instance —
fail the command rather than being skipped, so a deployment never looks
registered when it is not.

The implementation is Go: [`pkg/clusterdirector`](../../pkg/clusterdirector)
(discovery, payload construction, API client),
[`cmd/clusterdirector`](../../cmd/clusterdirector) (the manual
`gcluster cluster-director` commands) and
[`cmd/clusterdirector_lifecycle.go`](../../cmd/clusterdirector_lifecycle.go)
(the deploy and destroy hooks). It calls the REST API with Application Default
Credentials.

## Prerequisites

1. A `gcluster` binary, see
   [Set up Cluster Toolkit](https://cloud.google.com/cluster-toolkit/docs/setup/configure-environment).
1. Application Default Credentials:

   ```shell
   gcloud auth application-default login
   ```

1. Access to the Cluster Director import path, see
   [go/cd-import-ug](http://go/cd-import-ug); request allowlisting through
   [go/cd-import-allowlist](http://go/cd-import-allowlist). The gate is a
   per-project allowlist rather than per environment, so **`gcluster deploy`
   always registers into prod**. The manual `gcluster cluster-director`
   commands accept `--endpoint prod|staging|autopush` (or an explicit host or
   URL, or `$CLUSTER_DIRECTOR_ENDPOINT`) for testing against a non-prod
   environment; the automatic path deliberately ignores it.
1. The API enabled in your project:

   ```shell
   gcloud services enable hypercomputecluster.googleapis.com
   ```

## Deploy

```shell
./gcluster deploy examples/cluster-director-registration/blueprint.yaml \
  --vars project_id=YOUR_PROJECT_ID --auto-approve
```

Registration runs automatically at the end. To deploy the infrastructure
without registering it:

```shell
./gcluster deploy examples/cluster-director-registration/blueprint.yaml \
  --vars project_id=YOUR_PROJECT_ID --auto-approve \
  --skip-cluster-director-registration
```

> [!IMPORTANT]
> If registration fails, `gcluster deploy` fails with it. The infrastructure is
> **not** rolled back — the error explains how to retry the registration on its
> own, or how to skip it.

## Verify

The cluster ID is derived from `deployment_name` (`ctkdemo`).

```shell
# Show the registered cluster resource
./gcluster cluster-director describe \
  --project YOUR_PROJECT_ID --region us-central1 \
  --cluster-name ctkdemo

# List the compute nodes imported into the cluster in Cluster Director
./gcluster cluster-director describe nodes \
  --project YOUR_PROJECT_ID --region us-central1 \
  --cluster-name ctkdemo

# Cross-check the underlying Compute Engine instances matched by label
gcloud compute instances list --project YOUR_PROJECT_ID \
  --filter="labels.ghpc_deployment:ctkdemo"
```

## Destroy

```shell
./gcluster destroy ctkdemo --auto-approve
```

Deregistration happens first, before any resource is deleted, so the API call
still refers to live resources. It is driven by a record written under the
deployment's artifacts directory at registration time, so a deployment that was
never registered destroys normally with no flag.

To tear down part of a deployment without losing the cluster record:

```shell
./gcluster destroy ctkdemo --only compute --auto-approve
```

That leaves the network and storage groups standing, so the registration is
patched down to what remains instead of being deleted.

> [!NOTE]
> Because the update runs before the teardown, the surviving set is discovered
> from the groups the destroy did *not* select — the selected ones still hold
> their resources in state at that point.

## Registering more resources

The `register` command accepts every resource type supported by the
registration path. All list flags are repeatable and also accept a comma
separated list, so Cluster Toolkit outputs can be passed straight through:

```shell
gcluster cluster-director register \
  --project YOUR_PROJECT_ID --region us-central1 --zone us-central1-a \
  --cluster-name ctktest --deployment-name my-deployment \
  --network $(network.network_name) --subnet $(network.subnetwork_name) \
  --bucket bucket-one,bucket-two \
  --filestore projects/YOUR_PROJECT_ID/locations/us-central1-a/instances/ctk-filestore \
  --lustre projects/YOUR_PROJECT_ID/locations/us-central1-a/instances/ctk-lustre \
  --mig https://www.googleapis.com/compute/v1/projects/YOUR_PROJECT_ID/zones/us-central1-a/instanceGroupManagers/mig-pool \
  --reservation my-reservation \
  --label env=dev \
  --wait
```

Every flag also falls back to the environment variable of the same name in
upper snake case (`PROJECT_ID`, `REGION`, `ZONE`, `CLUSTER_NAME`,
`DEPLOYMENT_NAME`, `NETWORK_NAME`, `SUBNET_NAME`, `BUCKETS`, `FILESTORES`,
`LUSTRES`, `MIGS`, `RESERVATIONS`), which makes it easy to call the command
from a script that already exports the deployment outputs.

Use `--dry-run` to print the payload that would be sent, without calling the
API:

```shell
./gcluster cluster-director register --dry-run \
  --project YOUR_PROJECT_ID --region us-central1 \
  --cluster-name ctktest --deployment-name my-deployment \
  --network ctk-net --subnet ctk-subnet --bucket my-bucket
```

## GKE deployments

GKE node pools are built on ordinary managed instance groups, VPC networks and
storage, so a Cluster Toolkit GKE deployment can be registered the same way by
passing the node pool MIG self links to `--mig` and the cluster's VPC to
`--network`/`--subnet`. Cluster Director then shows the raw Compute Engine
footprint of the GKE cluster (nodes, networking, storage); Kubernetes
abstractions such as Pods and Deployments stay in the GKE control plane.

## Idempotency and re-deploys

* A deployment that is not yet registered is **created**.
* A deployment that is already registered is **updated** in place, with the
  resource set discovered by this run.
* Deregistration treats a missing registration as success.

This keeps repeated `gcluster deploy` / `gcluster destroy` runs safe, and keeps
the registration honest: add a bucket to the blueprint, re-deploy, and the
cluster record picks it up. No deregister-and-recreate cycle, and the cluster ID
never changes because it is derived from `deployment_name`.

The update is a `PATCH` with
`updateMask=network_resources,storage_resources,orchestrator`, so only the
imported resource sets are written; anything else on the cluster is left alone.

Scaling needs no update at all: compute is adopted through the
`ghpc_deployment` label selector, and Cluster Director resolves membership live
against the Compute Engine API rather than caching it.

## Debugging

Print the payload that would be sent, without calling the API:

```shell
./gcluster cluster-director register --dry-run \
  --project YOUR_PROJECT_ID --region us-central1 \
  --cluster-name ctkdemo \
  --deployment-name ctkdemo \
  --network ctk-net --subnet ctk-subnet --bucket my-bucket
```

To see what discovery found, inspect the same state it reads:

```shell
terraform -chdir=<deployment_name>/primary show -json \
  | jq -r '.values.root_module | .. | .resources? // empty | .[]
           | select(.mode=="managed") | .type' | sort -u
```

The record written at registration time lives at
`<deployment_name>/.ghpc/artifacts/cluster-director-registration.json`.
Deleting it makes `gcluster destroy` skip deregistration.

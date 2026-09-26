// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package clusterdirector

import (
	"encoding/json"
	"fmt"

	cdapi "hpc-toolkit/pkg/clusterdirector"
	"hpc-toolkit/pkg/logging"

	"github.com/spf13/cobra"
)

// importOptions holds the flags specific to `cluster-director import`.
type importOptions struct {
	zone           string
	deploymentName string
	network        string
	subnet         string
	buckets        []string
	filestores     []string
	lustres        []string
	migs           []string
	reservations   []string
	labels         map[string]string
	instanceLabels map[string]string
	dryRun         bool
}

var importOpts importOptions

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import the resources of a Cluster Toolkit deployment into Cluster Director.",
	Long: `Import the VPC, filesystems and compute of an existing Cluster Toolkit
deployment into a Cluster Director cluster.

Compute is adopted by label (every resource created by the Cluster Toolkit
carries the ghpc_deployment label), and optionally by managed instance group
or reservation (including reservation blocks and sub-blocks). Importing is
idempotent: importing an already imported cluster is reported and succeeds.

Every flag falls back to the environment variable of the same name in upper
snake case (BUCKETS, FILESTORES, NETWORK_NAME, ...), so that a deployment
group can pass Cluster Toolkit outputs straight through as environment
variables.`,
	Example: `  # Import a deployment into Cluster Director.
  gcluster cluster-director import --project my-project --region us-central1 \
    --cluster-name ctkdemo --deployment-name ctkdemo \
    --network ctk-net --subnet ctk-subnet --bucket my-ctk-bucket-1a2b --wait

  # Preview the payload without calling the API.
  gcluster cluster-director import --dry-run`,
	RunE:         runImport,
	SilenceUsage: true,
}

func init() {
	fs := importCmd.Flags()
	fs.StringVar(&importOpts.zone, "zone", envOr("ZONE", ""), "Zone of the deployment, used to qualify reservation names. Defaults to $ZONE.")
	fs.StringVar(&importOpts.deploymentName, "deployment-name", envOr("DEPLOYMENT_NAME", ""), "Cluster Toolkit deployment name, used as the ghpc_deployment instance selector. Defaults to $DEPLOYMENT_NAME.")
	fs.StringVar(&importOpts.network, "network", envOr("NETWORK_NAME", ""), "VPC network name to import. Defaults to $NETWORK_NAME.")
	fs.StringVar(&importOpts.subnet, "subnet", envOr("SUBNET_NAME", ""), "Subnetwork name to import. Defaults to $SUBNET_NAME.")
	stringSliceFlag(fs, &importOpts.buckets, "bucket", "BUCKETS", "Cloud Storage bucket to import. Repeatable, or a comma separated list. Defaults to $BUCKETS.")
	stringSliceFlag(fs, &importOpts.filestores, "filestore", "FILESTORES", "Filestore instance ID to import. Repeatable, or a comma separated list. Defaults to $FILESTORES.")
	stringSliceFlag(fs, &importOpts.lustres, "lustre", "LUSTRES", "Managed Lustre instance ID to import. Repeatable, or a comma separated list. Defaults to $LUSTRES.")
	stringSliceFlag(fs, &importOpts.migs, "mig", "MIGS", "Managed instance group self link to import. Repeatable, or a comma separated list. Defaults to $MIGS.")
	stringSliceFlag(fs, &importOpts.reservations, "reservation", "RESERVATIONS", "Reservation, reservation block, or reservation sub-block to import. Repeatable, or a comma separated list. Defaults to $RESERVATIONS.")
	fs.StringToStringVar(&importOpts.labels, "label", nil, "Label to set on the Cluster Director cluster, e.g. --label env=dev.")
	fs.StringToStringVar(&importOpts.instanceLabels, "instance-label", nil, "Label selector used to adopt instances. Overrides the default ghpc_deployment selector.")
	fs.BoolVar(&importOpts.dryRun, "dry-run", false, "Print the import payload instead of calling the API.")
}

// spec assembles the import spec from the command line flags.
func (o importOptions) spec(shared options, projectID string) cdapi.Spec {
	return cdapi.Spec{
		ProjectID:      projectID,
		Region:         shared.region,
		Zone:           o.zone,
		ClusterName:    shared.clusterName,
		DeploymentName: o.deploymentName,
		NetworkName:    o.network,
		SubnetName:     o.subnet,
		Buckets:        o.buckets,
		Filestores:     o.filestores,
		Lustres:        o.lustres,
		MIGs:           o.migs,
		Reservations:   o.reservations,
		ClusterLabels:  o.labels,
		InstanceLabels: o.instanceLabels,
	}
}

func runImport(cmd *cobra.Command, args []string) error {
	projectID := resolveProject(opts.projectID)
	spec := importOpts.spec(opts, projectID)

	cluster, err := spec.Cluster()
	if err != nil {
		return fmt.Errorf("invalid import request: %w", err)
	}

	if importOpts.dryRun {
		payload, err := json.MarshalIndent(cluster, "", "  ")
		if err != nil {
			return fmt.Errorf("could not render the import payload: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(payload))
		return nil
	}

	ctx := cmd.Context()
	client, err := newClient(ctx, opts.clientOptions()...)
	if err != nil {
		return err
	}

	logging.Info("Importing cluster %q into Cluster Director in %s/%s...", spec.ClusterName, projectID, spec.Region)
	op, err := client.CreateCluster(ctx, projectID, spec.Region, spec.ClusterName, cluster)
	if err != nil {
		if cdapi.IsAlreadyExists(err) {
			logging.Info("Cluster %q is already imported, nothing to do.", spec.ClusterName)
			return nil
		}
		return fmt.Errorf("could not import cluster %q: %w", spec.ClusterName, err)
	}

	return reportOperation(cmd, client, op, fmt.Sprintf("Imported cluster %q", spec.ClusterName))
}

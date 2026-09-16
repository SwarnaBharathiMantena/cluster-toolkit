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

// registerOptions holds the flags specific to `cluster-director register`.
type registerOptions struct {
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

var registerOpts registerOptions

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Register the resources of a Cluster Toolkit deployment into Cluster Director.",
	Long: `Import the VPC, filesystems and compute of an existing Cluster Toolkit
deployment into a Cluster Director cluster.

Compute is adopted by label (every resource created by the Cluster Toolkit
carries the ghpc_deployment label), and optionally by managed instance group
or reservation. Registration is idempotent: registering an already registered
cluster is reported and succeeds.

Every flag falls back to the environment variable of the same name in upper
snake case (BUCKETS, FILESTORES, NETWORK_NAME, ...), so that a deployment
group can pass Cluster Toolkit outputs straight through as environment
variables.`,
	Example: `  # Register a deployment from a blueprint's registration group.
  gcluster cluster-director register --project my-project --region us-central1 \
    --cluster-name ctktest --deployment-name ctk-cluster-director-demo \
    --network ctk-net --subnet ctk-subnet --bucket my-ctk-bucket-1a2b --wait

  # Preview the payload without calling the API.
  gcluster cluster-director register --dry-run`,
	RunE:         runRegister,
	SilenceUsage: true,
}

func init() {
	fs := registerCmd.Flags()
	fs.StringVar(&registerOpts.zone, "zone", envOr("ZONE", ""), "Zone of the deployment, used to qualify bare reservation names. Defaults to $ZONE.")
	fs.StringVar(&registerOpts.deploymentName, "deployment-name", envOr("DEPLOYMENT_NAME", ""), "Cluster Toolkit deployment name, used as the ghpc_deployment instance selector. Defaults to $DEPLOYMENT_NAME.")
	fs.StringVar(&registerOpts.network, "network", envOr("NETWORK_NAME", ""), "VPC network name to import. Defaults to $NETWORK_NAME.")
	fs.StringVar(&registerOpts.subnet, "subnet", envOr("SUBNET_NAME", ""), "Subnetwork name to import. Defaults to $SUBNET_NAME.")
	stringSliceFlag(fs, &registerOpts.buckets, "bucket", "BUCKETS", "Cloud Storage bucket to import. Repeatable, or a comma separated list. Defaults to $BUCKETS.")
	stringSliceFlag(fs, &registerOpts.filestores, "filestore", "FILESTORES", "Filestore instance ID to import. Repeatable, or a comma separated list. Defaults to $FILESTORES.")
	stringSliceFlag(fs, &registerOpts.lustres, "lustre", "LUSTRES", "Managed Lustre instance ID to import. Repeatable, or a comma separated list. Defaults to $LUSTRES.")
	stringSliceFlag(fs, &registerOpts.migs, "mig", "MIGS", "Managed instance group self link to import. Repeatable, or a comma separated list. Defaults to $MIGS.")
	stringSliceFlag(fs, &registerOpts.reservations, "reservation", "RESERVATIONS", "Reservation to import. Repeatable, or a comma separated list. Defaults to $RESERVATIONS.")
	fs.StringToStringVar(&registerOpts.labels, "label", nil, "Label to set on the Cluster Director cluster, e.g. --label env=dev.")
	fs.StringToStringVar(&registerOpts.instanceLabels, "instance-label", nil, "Label selector used to adopt instances. Overrides the default ghpc_deployment selector.")
	fs.BoolVar(&registerOpts.dryRun, "dry-run", false, "Print the registration payload instead of calling the API.")
}

// spec assembles the registration spec from the command line flags.
func (o registerOptions) spec(shared options, projectID string) cdapi.Spec {
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

func runRegister(cmd *cobra.Command, args []string) error {
	projectID := resolveProject(opts.projectID)
	spec := registerOpts.spec(opts, projectID)

	cluster, err := spec.Cluster()
	if err != nil {
		return fmt.Errorf("invalid registration request: %w", err)
	}

	if registerOpts.dryRun {
		payload, err := json.MarshalIndent(cluster, "", "  ")
		if err != nil {
			return fmt.Errorf("could not render the registration payload: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(payload))
		return nil
	}

	ctx := cmd.Context()
	client, err := cdapi.NewClient(ctx, opts.clientOptions()...)
	if err != nil {
		return err
	}

	logging.Info("Registering cluster %q into Cluster Director in %s/%s...", spec.ClusterName, projectID, spec.Region)
	op, err := client.CreateCluster(ctx, projectID, spec.Region, spec.ClusterName, cluster)
	if err != nil {
		if cdapi.IsAlreadyExists(err) {
			logging.Info("Cluster %q is already registered, nothing to do.", spec.ClusterName)
			return nil
		}
		return fmt.Errorf("could not register cluster %q: %w", spec.ClusterName, err)
	}

	return reportOperation(cmd, client, op, fmt.Sprintf("Registered cluster %q", spec.ClusterName))
}

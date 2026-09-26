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
	"fmt"

	cdapi "hpc-toolkit/pkg/clusterdirector"
	"hpc-toolkit/pkg/logging"

	"github.com/spf13/cobra"
)

var deleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete an imported Cluster Toolkit deployment from Cluster Director.",
	Long: `Delete an imported cluster from Cluster Director.

Only the Cluster Director cluster record is deleted: the imported networks,
filesystems and instances keep running and remain managed by the Cluster
Toolkit deployment. Deletion is idempotent, so it is safe to run it from the
destroy path of a deployment group.`,
	Example: `  gcluster cluster-director delete --project my-project --region us-central1 \
    --cluster-name ctkdemo --wait`,
	RunE:         runDelete,
	SilenceUsage: true,
}

func runDelete(cmd *cobra.Command, args []string) error {
	projectID := resolveProject(opts.projectID)
	if err := requireValues(map[string]string{
		"project":      projectID,
		"region":       opts.region,
		"cluster-name": opts.clusterName,
	}); err != nil {
		return err
	}

	ctx := cmd.Context()
	client, err := newClient(ctx, opts.clientOptions()...)
	if err != nil {
		return err
	}

	logging.Info("Deleting imported cluster %q from Cluster Director in %s/%s...", opts.clusterName, projectID, opts.region)
	op, err := client.DeleteCluster(ctx, projectID, opts.region, opts.clusterName)
	if err != nil {
		if cdapi.IsNotFound(err) {
			logging.Info("Cluster %q is not found in Cluster Director, nothing to do.", opts.clusterName)
			return nil
		}
		return fmt.Errorf("could not delete imported cluster %q: %w", opts.clusterName, err)
	}

	return reportOperation(cmd, client, op, fmt.Sprintf("Deleted imported cluster %q", opts.clusterName))
}

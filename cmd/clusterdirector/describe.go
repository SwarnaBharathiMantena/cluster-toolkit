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
	"sort"
	"strings"

	cdapi "hpc-toolkit/pkg/clusterdirector"
	"hpc-toolkit/pkg/logging"

	"github.com/spf13/cobra"
)

var describeCmd = &cobra.Command{
	Use:     "describe",
	Aliases: []string{"get"},
	Short:   "Show a cluster registered in Cluster Director.",
	Example: `  gcluster cluster-director describe --project my-project --region us-central1 \
    --cluster-name ctktest`,
	RunE:         runDescribe,
	SilenceUsage: true,
}

func runDescribe(cmd *cobra.Command, args []string) error {
	projectID := resolveProject(opts.projectID)
	if err := requireValues(map[string]string{
		"project":      projectID,
		"region":       opts.region,
		"cluster-name": opts.clusterName,
	}); err != nil {
		return err
	}

	ctx := cmd.Context()
	client, err := cdapi.NewClient(ctx, opts.clientOptions()...)
	if err != nil {
		return err
	}

	cluster, err := client.GetCluster(ctx, projectID, opts.region, opts.clusterName)
	if err != nil {
		return fmt.Errorf("could not read cluster %q: %w", opts.clusterName, err)
	}

	payload, err := json.MarshalIndent(cluster, "", "  ")
	if err != nil {
		return fmt.Errorf("could not render cluster %q: %w", opts.clusterName, err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(payload))
	return nil
}

// requireValues returns an error naming every entry of values that is empty.
func requireValues(values map[string]string) error {
	var missing []string
	for name, value := range values {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, "--"+name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("missing required flag(s): %s", strings.Join(missing, ", "))
}

// reportOperation optionally waits for op and logs its outcome.
func reportOperation(cmd *cobra.Command, client *cdapi.Client, op *cdapi.Operation, successMsg string) error {
	if op == nil {
		logging.Info("%s.", successMsg)
		return nil
	}
	if !opts.wait {
		if op.Done {
			logging.Info("%s.", successMsg)
			return nil
		}
		logging.Info("%s is in progress as operation %s. Re-run with --wait to block until it completes.", successMsg, op.Name)
		return nil
	}

	logging.Info("Waiting for operation %s...", op.Name)
	op, err := client.WaitForOperation(cmd.Context(), op, opts.pollInterval, opts.timeout)
	if err != nil {
		return err
	}
	logging.Info("%s (operation %s).", successMsg, op.Name)
	return nil
}

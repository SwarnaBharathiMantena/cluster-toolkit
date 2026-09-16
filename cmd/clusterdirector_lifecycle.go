// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	cdapi "hpc-toolkit/pkg/clusterdirector"
	"hpc-toolkit/pkg/config"
	"hpc-toolkit/pkg/logging"
	"hpc-toolkit/pkg/shell"
	"os"
	"path/filepath"
	"time"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/spf13/cobra"
	"github.com/zclconf/go-cty/cty"
)

// Registration and deregistration run inside a deploy or destroy, where the
// user is already waiting, so they poll a little more eagerly than the
// standalone `gcluster cluster-director` commands.
const (
	cdPollInterval = 10 * time.Second
	cdWaitTimeout  = 30 * time.Minute
)

// cdMarkerFile records, inside the artifacts directory, that a deployment was
// registered. `gcluster destroy` reads it to know what to deregister, which is
// why the values are stored rather than recomputed: by destroy time the
// blueprint may have been edited, and the registration must be undone with the
// values it was made with.
const cdMarkerFile = "cluster-director-registration.json"

var flagSkipClusterDirector bool

// addClusterDirectorFlags registers the opt-out flag. Registration is on by
// default; this is the escape hatch.
func addClusterDirectorFlags(c *cobra.Command) *cobra.Command {
	c.Flags().BoolVar(&flagSkipClusterDirector, "skip-cluster-director-registration", false,
		"Do not register this deployment into Cluster Director")
	return c
}

// cdMarker is the on-disk record of a registration. It deliberately carries no
// endpoint: the automatic path always talks to the default (prod) endpoint, so
// register and deregister cannot disagree about where the cluster lives.
type cdMarker struct {
	ClusterName string `json:"cluster_name"`
	ProjectID   string `json:"project_id"`
	Region      string `json:"region"`
}

func cdMarkerPath(artifactsDir string) string {
	return filepath.Join(artifactsDir, cdMarkerFile)
}

func writeCdMarker(artifactsDir string, m cdMarker) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cdMarkerPath(artifactsDir), append(data, '\n'), 0644)
}

// readCdMarker returns the marker, or ok=false when the deployment was never
// registered. A malformed marker is an error: silently ignoring it would leak
// a registration.
func readCdMarker(artifactsDir string) (cdMarker, bool, error) {
	data, err := os.ReadFile(cdMarkerPath(artifactsDir))
	if os.IsNotExist(err) {
		return cdMarker{}, false, nil
	}
	if err != nil {
		return cdMarker{}, false, err
	}
	var m cdMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return cdMarker{}, false, fmt.Errorf("could not read %s: %w", cdMarkerPath(artifactsDir), err)
	}
	return m, true, nil
}

// stringVar reads a global blueprint variable, returning "" when it is absent
// or is not a string.
func stringVar(bp config.Blueprint, key string) string {
	if !bp.Vars.Has(key) {
		return ""
	}
	v := bp.Vars.Get(key)
	if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
		return ""
	}
	return v.AsString()
}

// gatherTerraformStates reads the state of the Terraform groups selected by
// include. Groups that cannot be read are skipped rather than fatal: a Packer
// group has no state, and a group that was not selected for deployment may not
// be initialized.
func gatherTerraformStates(deplRoot string, bp config.Blueprint, include func(config.GroupName) bool) []*tfjson.State {
	var states []*tfjson.State
	for _, group := range bp.Groups {
		if group.Kind() != config.TerraformKind {
			continue
		}
		if !include(group.Name) {
			continue
		}
		groupDir := filepath.Join(deplRoot, string(group.Name))
		tf, err := shell.ConfigureTerraform(groupDir)
		if err != nil {
			logging.Info("Cluster Director: skipping group %q, terraform is unavailable: %v", group.Name, err)
			continue
		}
		st, err := tf.Show(context.Background())
		if err != nil {
			logging.Info("Cluster Director: skipping group %q, could not read its state: %v", group.Name, err)
			continue
		}
		states = append(states, st)
	}
	return states
}

// allGroups includes every group in discovery.
func allGroups(config.GroupName) bool { return true }

// survivingGroups includes only the groups a destroy will leave behind, i.e.
// those --only or --skip did not select for destruction.
func survivingGroups(n config.GroupName) bool { return !isGroupSelected(n) }

// buildSpecFromDeployment assembles a registration spec by inspecting the
// Terraform state of the groups selected by include. Nothing is read from the
// blueprint beyond the globals every blueprint already defines, so no blueprint
// changes registration.
//
// ok=false means the deployment is not registerable at all (for example it
// declares no project or region). That is a structural fact, not a failure, so
// the caller skips quietly rather than failing the deploy.
func buildSpecFromDeployment(deplRoot string, bp config.Blueprint, include func(config.GroupName) bool) (cdapi.Spec, bool) {
	projectID := stringVar(bp, "project_id")
	region := stringVar(bp, "region")
	deploymentName := bp.DeploymentName()

	if projectID == "" || region == "" || deploymentName == "" {
		logging.Info("Cluster Director: skipping registration, the blueprint does not define project_id, region and deployment_name")
		return cdapi.Spec{}, false
	}

	d := cdapi.Discover(gatherTerraformStates(deplRoot, bp, include))

	network, networkAmbiguous := d.Network()
	subnet, subnetAmbiguous := d.Subnetwork(region)
	if networkAmbiguous {
		logging.Info("Cluster Director: the deployment defines several VPCs %v, registering %q", d.Networks, network)
	}
	if subnetAmbiguous {
		logging.Info("Cluster Director: the deployment defines several subnetworks in %s, registering %q", region, subnet)
	}
	// The API takes the pair or neither.
	if network == "" || subnet == "" {
		network, subnet = "", ""
	}

	return cdapi.Spec{
		ProjectID:      projectID,
		Region:         region,
		Zone:           stringVar(bp, "zone"),
		ClusterName:    cdapi.SanitizeKey(deploymentName),
		DeploymentName: deploymentName,
		NetworkName:    network,
		SubnetName:     subnet,
		Buckets:        d.Buckets,
		Filestores:     d.Filestores,
		Lustres:        d.Lustres,
		MIGs:           d.MIGs,
		Reservations:   d.Reservations,
	}, true
}

// registerClusterDirector is called by `gcluster deploy` once the
// infrastructure is up. Failures fail the command: a registration that did not
// happen must never look like one that did.
func registerClusterDirector(cmd *cobra.Command, deplRoot string, artifactsDir string, bp config.Blueprint) error {
	if flagSkipClusterDirector {
		logging.Info("Skipping Cluster Director registration (--skip-cluster-director-registration)")
		return nil
	}
	// --only and --skip are not consulted. Registration reflects the current
	// state of the whole deployment, not the subset of groups applied by this
	// invocation: a partial apply still changes the cluster, whether it adds
	// resources or removes them. gatherTerraformStates reads every group's
	// state, so the registration stays accurate either way.
	spec, ok := buildSpecFromDeployment(deplRoot, bp, allGroups)
	if !ok {
		return nil
	}
	// A spec that fails validation is a misconfiguration, not a deployment
	// that happens not to be registerable — buildSpecFromDeployment already
	// returned ok for that. The reachable case is a bare reservation name with
	// no `zone` global to qualify it. Skipping would leave the user believing
	// the cluster was registered.
	if err := spec.Validate(); err != nil {
		return fmt.Errorf(`cannot register %q into Cluster Director: %w

The infrastructure was deployed successfully and has NOT been rolled back.
Correct the blueprint and retry, or deploy without registering using
--skip-cluster-director-registration`, spec.ClusterName, err)
	}

	cluster, err := spec.Cluster()
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	// The automatic path is deliberately pinned to the default (prod) endpoint.
	// $CLUSTER_DIRECTOR_ENDPOINT is honoured only by the manual
	// `gcluster cluster-director` commands, where a human chose it: an
	// environment variable read implicitly during `gcluster deploy` could
	// silently register a production cluster somewhere else.
	client, err := cdapi.NewClient(ctx)
	if err != nil {
		return err
	}

	logging.Info("Registering deployment %q into Cluster Director as cluster %q...", spec.DeploymentName, spec.ClusterName)
	if err := createOrUpdateCluster(ctx, client, spec, cluster); err != nil {
		return err
	}

	if err := writeCdMarker(artifactsDir, cdMarker{
		ClusterName: spec.ClusterName,
		ProjectID:   spec.ProjectID,
		Region:      spec.Region,
	}); err != nil {
		return fmt.Errorf("registered %q but could not record it: %w", spec.ClusterName, err)
	}
	logging.Info("Registered cluster %q into Cluster Director.", spec.ClusterName)
	return nil
}

// createOrUpdateCluster registers the cluster, or patches it when it is
// already registered.
//
// There is no "create or update" verb, so the create is the probe: an
// ALREADY_EXISTS is not an error but the answer to "does this cluster exist
// yet?". The patch that follows carries the resource set just discovered, so a
// deployment that grew or shrank since the first registration is reflected
// rather than left stale. The cluster ID is derived from the deployment name
// and so is stable across deploys, which is what makes this safe to repeat.
func createOrUpdateCluster(ctx context.Context, client *cdapi.Client, spec cdapi.Spec, cluster *cdapi.Cluster) error {
	op, err := client.CreateCluster(ctx, spec.ProjectID, spec.Region, spec.ClusterName, cluster)
	switch {
	case err == nil:
		if _, err := client.WaitForOperation(ctx, op, cdPollInterval, cdWaitTimeout); err != nil {
			return fmt.Errorf("registration of %q did not complete: %w", spec.ClusterName, err)
		}
		return nil
	case cdapi.IsAlreadyExists(err):
		logging.Info("Cluster %q is already registered, updating its resource set...", spec.ClusterName)
		op, err := client.UpdateCluster(ctx, spec.ProjectID, spec.Region, spec.ClusterName, cluster, cdapi.UpdateResourceMask)
		if err != nil {
			return fmt.Errorf(`could not update the Cluster Director registration for %q: %w

The infrastructure was deployed successfully and has NOT been rolled back.
The cluster is still registered, but with its previous resource set.`,
				spec.ClusterName, err)
		}
		if _, err := client.WaitForOperation(ctx, op, cdPollInterval, cdWaitTimeout); err != nil {
			return fmt.Errorf("update of %q did not complete: %w", spec.ClusterName, err)
		}
		return nil
	default:
		return fmt.Errorf(`could not register %q into Cluster Director: %w

The infrastructure was deployed successfully and has NOT been rolled back.
Retry the registration on its own with:
  gcluster cluster-director register --project %s --region %s --cluster-name %s --deployment-name %s
or deploy without registering using --skip-cluster-director-registration`,
			spec.ClusterName, err, spec.ProjectID, spec.Region, spec.ClusterName, spec.DeploymentName)
	}
}

// hasSurvivingTerraformGroup reports whether any Terraform group will still be
// standing once this destroy finishes.
func hasSurvivingTerraformGroup(bp config.Blueprint) bool {
	for _, g := range bp.Groups {
		if g.Kind() == config.TerraformKind && survivingGroups(g.Name) {
			return true
		}
	}
	return false
}

// deregisterClusterDirector is called by `gcluster destroy` before the
// infrastructure is torn down, so the API call still references live
// resources. It is driven entirely by the marker: a deployment that was never
// registered needs no flag to skip this.
//
// A partial destroy is not a deregistration. `--only` and `--skip` shrink the
// deployment; the cluster still exists, so the registration is updated to the
// resources that will survive rather than deleted outright. This mirrors
// partial deploy, which registers the deployment as it stands. A destroy that
// leaves no Terraform group behind is a full destroy however it was spelled,
// and deregisters.
func deregisterClusterDirector(cmd *cobra.Command, deplRoot string, artifactsDir string, bp config.Blueprint) error {
	if flagSkipClusterDirector {
		logging.Info("Skipping Cluster Director deregistration (--skip-cluster-director-registration)")
		return nil
	}

	m, ok, err := readCdMarker(artifactsDir)
	if err != nil {
		return err
	}
	if !ok {
		return nil // never registered
	}

	if hasSurvivingTerraformGroup(bp) {
		return shrinkClusterDirectorRegistration(cmd, deplRoot, bp, m)
	}

	ctx := cmd.Context()
	// Same default endpoint as registration used; see registerClusterDirector.
	client, err := cdapi.NewClient(ctx)
	if err != nil {
		return err
	}

	logging.Info("Deregistering cluster %q from Cluster Director...", m.ClusterName)
	op, err := client.DeleteCluster(ctx, m.ProjectID, m.Region, m.ClusterName)
	switch {
	case err == nil:
		if _, err := client.WaitForOperation(ctx, op, cdPollInterval, cdWaitTimeout); err != nil {
			return fmt.Errorf("deregistration of %q did not complete: %w", m.ClusterName, err)
		}
	case cdapi.IsNotFound(err):
		logging.Info("Cluster %q is not registered, nothing to do.", m.ClusterName)
	default:
		return fmt.Errorf(`could not deregister %q from Cluster Director: %w

The infrastructure has NOT been destroyed. Resolve the error and retry, or
remove the registration by hand with:
  gcluster cluster-director deregister --project %s --region %s --cluster-name %s`,
			m.ClusterName, err, m.ProjectID, m.Region, m.ClusterName)
	}

	if err := os.Remove(cdMarkerPath(artifactsDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deregistered %q but could not clear its record: %w", m.ClusterName, err)
	}
	return nil
}

// shrinkClusterDirectorRegistration handles a partial destroy by updating the
// registration to the resources that will survive, instead of deleting it.
//
// The surviving set is discovered from the groups this destroy did not select,
// which is why this runs before the teardown: the selected groups still hold
// their resources in state, and reading them would overstate what remains.
// The cluster identity comes from the marker, not from the recomputed spec, so
// the update lands on the cluster that registration actually created even if
// the blueprint has been edited since.
//
// The marker is deliberately left in place: the cluster is still registered.
func shrinkClusterDirectorRegistration(cmd *cobra.Command, deplRoot string, bp config.Blueprint, m cdMarker) error {
	spec, ok := buildSpecFromDeployment(deplRoot, bp, survivingGroups)
	if !ok {
		return nil
	}
	if err := spec.Validate(); err != nil {
		return fmt.Errorf(`cannot update the Cluster Director registration for %q: %w

Nothing has been destroyed. Correct the blueprint and retry, or destroy without
touching the registration using --skip-cluster-director-registration`,
			m.ClusterName, err)
	}
	cluster, err := spec.Cluster()
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	// Same default endpoint as registration used; see registerClusterDirector.
	client, err := cdapi.NewClient(ctx)
	if err != nil {
		return err
	}

	logging.Info("Partial destroy: shrinking the Cluster Director registration for %q to the resources that will remain...", m.ClusterName)
	op, err := client.UpdateCluster(ctx, m.ProjectID, m.Region, m.ClusterName, cluster, cdapi.UpdateResourceMask)
	switch {
	case err == nil:
		if _, err := client.WaitForOperation(ctx, op, cdPollInterval, cdWaitTimeout); err != nil {
			return fmt.Errorf("update of %q did not complete: %w", m.ClusterName, err)
		}
	case cdapi.IsNotFound(err):
		// Someone deregistered it out of band. Nothing to shrink, and the
		// destroy should not be blocked by that.
		logging.Info("Cluster %q is not registered, nothing to update.", m.ClusterName)
	default:
		return fmt.Errorf(`could not update the Cluster Director registration for %q: %w

Nothing has been destroyed. Resolve the error and retry, or destroy without
touching the registration using --skip-cluster-director-registration`,
			m.ClusterName, err)
	}
	return nil
}

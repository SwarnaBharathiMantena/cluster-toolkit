/*
Copyright 2026 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"hpc-toolkit/pkg/config"

	"github.com/zclconf/go-cty/cty"
)

func TestCdMarkerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := cdMarker{
		ClusterName: "ctk-demo",
		ProjectID:   "my-project",
		Region:      "us-central1",
	}
	if err := writeCdMarker(dir, want); err != nil {
		t.Fatalf("writeCdMarker() failed: %v", err)
	}

	got, ok, err := readCdMarker(dir)
	if err != nil {
		t.Fatalf("readCdMarker() failed: %v", err)
	}
	if !ok {
		t.Fatal("readCdMarker() reported no marker, want the one just written")
	}
	if got != want {
		t.Errorf("readCdMarker() = %+v, want %+v", got, want)
	}
}

func TestCdMarkerAbsentIsNotAnError(t *testing.T) {
	// A deployment that was never registered must destroy cleanly.
	_, ok, err := readCdMarker(t.TempDir())
	if err != nil {
		t.Errorf("readCdMarker() on a fresh dir failed: %v", err)
	}
	if ok {
		t.Error("readCdMarker() found a marker in a fresh dir, want none")
	}
}

func TestCdMarkerMalformedIsAnError(t *testing.T) {
	// Ignoring a corrupt marker would silently leak a registration.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, cdMarkerFile), []byte("{not json"), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if _, _, err := readCdMarker(dir); err == nil {
		t.Error("readCdMarker() succeeded on a corrupt marker, want an error")
	}
}

func TestStringVar(t *testing.T) {
	bp := config.Blueprint{}
	bp.Vars = config.NewDict(map[string]cty.Value{
		"project_id": cty.StringVal("my-project"),
		"count":      cty.NumberIntVal(3),
		"nothing":    cty.NullVal(cty.String),
	})

	tests := []struct {
		key  string
		want string
	}{
		{"project_id", "my-project"},
		{"count", ""},   // not a string
		{"nothing", ""}, // null
		{"absent", ""},  // missing
	}
	for _, tc := range tests {
		if got := stringVar(bp, tc.key); got != tc.want {
			t.Errorf("stringVar(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestBuildSpecSkipsWhenBlueprintCannotBeRegistered(t *testing.T) {
	// No project_id or region means there is nowhere to register to. That is
	// a structural fact, not a failure, so the deploy must continue.
	bp := config.Blueprint{}
	bp.Vars = config.NewDict(map[string]cty.Value{
		"deployment_name": cty.StringVal("depl"),
	})

	if _, ok := buildSpecFromDeployment(t.TempDir(), bp, allGroups); ok {
		t.Error("buildSpecFromDeployment() reported a registerable deployment, want it skipped")
	}
}

func TestBuildSpecUsesDeploymentNameAsClusterName(t *testing.T) {
	bp := config.Blueprint{}
	bp.Vars = config.NewDict(map[string]cty.Value{
		"project_id":      cty.StringVal("my-project"),
		"region":          cty.StringVal("us-central1"),
		"zone":            cty.StringVal("us-central1-a"),
		"deployment_name": cty.StringVal("My_Deployment"),
	})

	// The deployment directory is empty, so no state is readable and the
	// resource lists stay empty. The identity fields must still be derived.
	spec, ok := buildSpecFromDeployment(t.TempDir(), bp, allGroups)
	if !ok {
		t.Fatal("buildSpecFromDeployment() skipped a registerable deployment")
	}
	if spec.DeploymentName != "My_Deployment" {
		t.Errorf("DeploymentName = %q, want %q", spec.DeploymentName, "My_Deployment")
	}
	// The cluster ID must be RFC-1034 even when the deployment name is not.
	if spec.ClusterName == "" || strings.ContainsAny(spec.ClusterName, "_ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		t.Errorf("ClusterName = %q, want it sanitized from the deployment name", spec.ClusterName)
	}
	if spec.ProjectID != "my-project" || spec.Region != "us-central1" || spec.Zone != "us-central1-a" {
		t.Errorf("spec identity = %+v, want it taken from the blueprint globals", spec)
	}
}

// tfGroup builds a Terraform deployment group. Group.Kind() is derived from
// the modules, so a group needs at least one to count as Terraform.
func tfGroup(name string) config.Group {
	return config.Group{
		Name:    config.GroupName(name),
		Modules: []config.Module{{Kind: config.TerraformKind}},
	}
}

// withGroupSelection sets the --only / --skip globals for the duration of a
// test and restores them afterwards.
func withGroupSelection(t *testing.T, only, skip []string) {
	t.Helper()
	prevOnly, prevSkip := flagOnlyGroups, flagSkipGroups
	flagOnlyGroups, flagSkipGroups = only, skip
	t.Cleanup(func() { flagOnlyGroups, flagSkipGroups = prevOnly, prevSkip })
}

func TestGatherTerraformStatesConsultsTheGroupFilter(t *testing.T) {
	bp := config.Blueprint{Groups: []config.Group{tfGroup("network"), tfGroup("compute")}}

	// Excluding everything keeps the test off the filesystem entirely: the
	// filter is applied before terraform is configured.
	var asked []string
	states := gatherTerraformStates(t.TempDir(), bp, func(n config.GroupName) bool {
		asked = append(asked, string(n))
		return false
	})

	if len(states) != 0 {
		t.Errorf("gatherTerraformStates() read %d states, want none when every group is filtered out", len(states))
	}
	if want := []string{"network", "compute"}; !slices.Equal(asked, want) {
		t.Errorf("filter consulted for %v, want %v", asked, want)
	}
}

// A destroy deregisters only when it leaves nothing behind. Anything else is a
// partial destroy, which shrinks the registration instead.
func TestHasSurvivingTerraformGroup(t *testing.T) {
	bp := config.Blueprint{Groups: []config.Group{tfGroup("network"), tfGroup("compute")}}

	for _, tc := range []struct {
		name string
		only []string
		skip []string
		want bool
	}{
		{"a full destroy leaves nothing", nil, nil, false},
		{"--only one group leaves the other", []string{"compute"}, nil, true},
		{"--only every group is a full destroy", []string{"network", "compute"}, nil, false},
		{"--skip one group leaves that one", nil, []string{"network"}, true},
		{"--skip every group destroys nothing", nil, []string{"network", "compute"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withGroupSelection(t, tc.only, tc.skip)
			if got := hasSurvivingTerraformGroup(bp); got != tc.want {
				t.Errorf("hasSurvivingTerraformGroup() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A blueprint with no Terraform groups at all must not be mistaken for a
// partial destroy.
func TestHasSurvivingTerraformGroupIgnoresNonTerraformGroups(t *testing.T) {
	withGroupSelection(t, nil, nil)
	bp := config.Blueprint{Groups: []config.Group{{
		Name:    "image",
		Modules: []config.Module{{Kind: config.PackerKind}},
	}}}
	if hasSurvivingTerraformGroup(bp) {
		t.Error("hasSurvivingTerraformGroup() = true for a Packer-only blueprint, want false")
	}
}

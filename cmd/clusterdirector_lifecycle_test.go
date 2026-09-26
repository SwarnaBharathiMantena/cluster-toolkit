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
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cdapi "hpc-toolkit/pkg/clusterdirector"
	"hpc-toolkit/pkg/config"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/spf13/cobra"
	"github.com/zclconf/go-cty/cty"
)

func withFakeCdClient(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	prev := newCdClient
	newCdClient = func(ctx context.Context, _ ...cdapi.Option) (*cdapi.Client, error) {
		return cdapi.NewClient(ctx, cdapi.WithEndpoint(srv.URL), cdapi.WithHTTPClient(srv.Client()))
	}
	t.Cleanup(func() {
		newCdClient = prev
		srv.Close()
	})
}

func withFakeTerraformState(t *testing.T, fn func(groupDir string) (*tfjson.State, error)) {
	t.Helper()
	prev := readGroupTerraformState
	readGroupTerraformState = fn
	t.Cleanup(func() { readGroupTerraformState = prev })
}

func testBlueprint(zone string) config.Blueprint {
	bp := config.Blueprint{
		Groups: []config.Group{tfGroup("network"), tfGroup("compute")},
	}
	vars := map[string]cty.Value{
		"project_id":      cty.StringVal("my-project"),
		"region":          cty.StringVal("us-central1"),
		"deployment_name": cty.StringVal("ctkdemo"),
	}
	if zone != "" {
		vars["zone"] = cty.StringVal(zone)
	}
	bp.Vars = config.NewDict(vars)
	return bp
}

func dummyCmd() *cobra.Command {
	c := &cobra.Command{}
	c.SetContext(context.Background())
	return c
}

func TestCdMarkerRoundTripAndRemove(t *testing.T) {
	dir := t.TempDir()
	want := cdMarker{
		ClusterName: "ctkdemo",
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

	if err := removeCdMarker(dir); err != nil {
		t.Fatalf("removeCdMarker() failed: %v", err)
	}
	// Removing again when absent must be a no-op.
	if err := removeCdMarker(dir); err != nil {
		t.Fatalf("removeCdMarker() on absent file failed: %v", err)
	}
}

func TestCdMarkerAbsentIsNotAnError(t *testing.T) {
	// A deployment that was never imported must destroy cleanly.
	_, ok, err := readCdMarker(t.TempDir())
	if err != nil {
		t.Errorf("readCdMarker() on a fresh dir failed: %v", err)
	}
	if ok {
		t.Error("readCdMarker() found a marker in a fresh dir, want none")
	}
}

func TestCdMarkerMalformedIsAnError(t *testing.T) {
	// Ignoring a corrupt marker would silently leak an imported cluster record.
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

func TestBuildSpecSkipsWhenBlueprintCannotBeImported(t *testing.T) {
	// No project_id or region means there is nowhere to import to. That is
	// a structural fact, not a failure, so the deploy must continue.
	bp := config.Blueprint{}
	bp.Vars = config.NewDict(map[string]cty.Value{
		"deployment_name": cty.StringVal("depl"),
	})

	if _, ok := buildSpecFromDeployment(t.TempDir(), bp, allGroups); ok {
		t.Error("buildSpecFromDeployment() reported an importable deployment, want it skipped")
	}
}

func TestBuildSpecUsesDeploymentNameAndDiscoversAmbiguousNetworks(t *testing.T) {
	bp := config.Blueprint{Groups: []config.Group{tfGroup("primary")}}
	bp.Vars = config.NewDict(map[string]cty.Value{
		"project_id":      cty.StringVal("my-project"),
		"region":          cty.StringVal("us-central1"),
		"zone":            cty.StringVal("us-central1-a"),
		"deployment_name": cty.StringVal("My_Deployment"),
	})

	withFakeTerraformState(t, func(string) (*tfjson.State, error) {
		return &tfjson.State{Values: &tfjson.StateValues{RootModule: &tfjson.StateModule{
			Resources: []*tfjson.StateResource{
				{Mode: tfjson.ManagedResourceMode, Type: "google_compute_network", AttributeValues: map[string]any{"name": "net-b"}},
				{Mode: tfjson.ManagedResourceMode, Type: "google_compute_network", AttributeValues: map[string]any{"name": "net-a"}},
				{Mode: tfjson.ManagedResourceMode, Type: "google_compute_subnetwork", AttributeValues: map[string]any{"name": "sub-b", "region": "us-central1"}},
				{Mode: tfjson.ManagedResourceMode, Type: "google_compute_subnetwork", AttributeValues: map[string]any{"name": "sub-a", "region": "us-central1"}},
				{Mode: tfjson.ManagedResourceMode, Type: "google_storage_bucket", AttributeValues: map[string]any{"name": "bkt-1"}},
			},
		}}}, nil
	})

	spec, ok := buildSpecFromDeployment(t.TempDir(), bp, allGroups)
	if !ok {
		t.Fatal("buildSpecFromDeployment() skipped an importable deployment")
	}
	if spec.DeploymentName != "My_Deployment" {
		t.Errorf("DeploymentName = %q, want %q", spec.DeploymentName, "My_Deployment")
	}
	if spec.ClusterName == "" || strings.ContainsAny(spec.ClusterName, "_ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		t.Errorf("ClusterName = %q, want it sanitized from the deployment name", spec.ClusterName)
	}
	if spec.NetworkName != "net-a" || spec.SubnetName != "sub-a" {
		t.Errorf("Network/Subnet = (%q, %q), want (net-a, sub-a)", spec.NetworkName, spec.SubnetName)
	}
	if !slices.Equal(spec.Buckets, []string{"bkt-1"}) {
		t.Errorf("Buckets = %v, want [bkt-1]", spec.Buckets)
	}
}

func TestImportClusterDirectorLifecycle(t *testing.T) {
	t.Run("skip flag skips import", func(t *testing.T) {
		flagSkipClusterDirector = true
		t.Cleanup(func() { flagSkipClusterDirector = false })
		if err := importClusterDirector(dummyCmd(), t.TempDir(), t.TempDir(), testBlueprint("us-central1-a")); err != nil {
			t.Fatalf("importClusterDirector() with skip flag failed: %v", err)
		}
	})

	t.Run("unimportable blueprint skips quietly", func(t *testing.T) {
		if err := importClusterDirector(dummyCmd(), t.TempDir(), t.TempDir(), config.Blueprint{}); err != nil {
			t.Fatalf("importClusterDirector() on empty blueprint failed: %v", err)
		}
	})

	t.Run("invalid spec fails with guidance", func(t *testing.T) {
		// Bare reservation with no zone fails Validate().
		withFakeTerraformState(t, func(string) (*tfjson.State, error) {
			return &tfjson.State{Values: &tfjson.StateValues{RootModule: &tfjson.StateModule{
				Resources: []*tfjson.StateResource{{
					Mode: tfjson.ManagedResourceMode,
					Type: "google_compute_instance",
					AttributeValues: map[string]any{
						"reservation_affinity": []any{map[string]any{
							"type": "SPECIFIC_RESERVATION",
							"specific_reservation": []any{map[string]any{
								"values": []any{"unqualified-res"},
							}},
						}},
					},
				}},
			}}}, nil
		})
		err := importClusterDirector(dummyCmd(), t.TempDir(), t.TempDir(), testBlueprint(""))
		if err == nil || !strings.Contains(err.Error(), "cannot import") {
			t.Errorf("importClusterDirector() error = %v, want cannot import validation failure", err)
		}
	})

	t.Run("create succeeds and writes marker", func(t *testing.T) {
		withFakeTerraformState(t, func(string) (*tfjson.State, error) { return nil, errors.New("no state") })
		withFakeCdClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"name": "operations/op-create", "done": true}`)
		})
		artDir := t.TempDir()
		if err := importClusterDirector(dummyCmd(), t.TempDir(), artDir, testBlueprint("us-central1-a")); err != nil {
			t.Fatalf("importClusterDirector() failed: %v", err)
		}
		m, ok, err := readCdMarker(artDir)
		if err != nil || !ok || m.ClusterName != "ctkdemo" {
			t.Errorf("readCdMarker() = (%+v, %t, %v), want ctkdemo marker", m, ok, err)
		}
	})

	t.Run("already exists triggers UpdateCluster", func(t *testing.T) {
		withFakeTerraformState(t, func(string) (*tfjson.State, error) { return nil, errors.New("no state") })
		patched := false
		withFakeCdClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusConflict)
				io.WriteString(w, `{"error": {"code": 409, "message": "exists", "status": "ALREADY_EXISTS"}}`)
				return
			}
			if r.Method == http.MethodPatch {
				patched = true
				io.WriteString(w, `{"name": "operations/op-patch", "done": true}`)
				return
			}
		})
		artDir := t.TempDir()
		if err := importClusterDirector(dummyCmd(), t.TempDir(), artDir, testBlueprint("us-central1-a")); err != nil {
			t.Fatalf("importClusterDirector() on ALREADY_EXISTS failed: %v", err)
		}
		if !patched {
			t.Error("expected UpdateCluster (PATCH) to be called after ALREADY_EXISTS")
		}
	})

	t.Run("already exists with failing UpdateCluster returns error", func(t *testing.T) {
		withFakeTerraformState(t, func(string) (*tfjson.State, error) { return nil, errors.New("no state") })
		withFakeCdClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusConflict)
				io.WriteString(w, `{"error": {"code": 409, "message": "exists", "status": "ALREADY_EXISTS"}}`)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"error": {"code": 500, "message": "patch failed", "status": "INTERNAL"}}`)
		})
		err := importClusterDirector(dummyCmd(), t.TempDir(), t.TempDir(), testBlueprint("us-central1-a"))
		if err == nil || !strings.Contains(err.Error(), "could not update the Cluster Director import") {
			t.Errorf("importClusterDirector() error = %v, want update failure", err)
		}
	})

	t.Run("create API error fails", func(t *testing.T) {
		withFakeTerraformState(t, func(string) (*tfjson.State, error) { return nil, errors.New("no state") })
		withFakeCdClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error": {"code": 403, "message": "denied", "status": "PERMISSION_DENIED"}}`)
		})
		err := importClusterDirector(dummyCmd(), t.TempDir(), t.TempDir(), testBlueprint("us-central1-a"))
		if err == nil || !strings.Contains(err.Error(), "could not import") {
			t.Errorf("importClusterDirector() error = %v, want could not import", err)
		}
	})
}

func TestDeleteClusterDirectorLifecycle(t *testing.T) {
	t.Run("absent marker is a no-op", func(t *testing.T) {
		if err := deleteClusterDirector(dummyCmd(), t.TempDir(), t.TempDir(), testBlueprint("us-central1-a")); err != nil {
			t.Fatalf("deleteClusterDirector() without marker failed: %v", err)
		}
	})

	t.Run("corrupt marker fails", func(t *testing.T) {
		artDir := t.TempDir()
		os.WriteFile(filepath.Join(artDir, cdMarkerFile), []byte("{bad"), 0644)
		if err := deleteClusterDirector(dummyCmd(), t.TempDir(), artDir, testBlueprint("us-central1-a")); err == nil {
			t.Error("deleteClusterDirector() on corrupt marker succeeded, want error")
		}
	})

	t.Run("full destroy deletes cluster and removes marker", func(t *testing.T) {
		withGroupSelection(t, nil, nil)
		artDir := t.TempDir()
		writeCdMarker(artDir, cdMarker{ClusterName: "ctkdemo", ProjectID: "my-project", Region: "us-central1"})
		deleted := false
		withFakeCdClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				deleted = true
				io.WriteString(w, `{"name": "operations/op-del", "done": true}`)
			}
		})
		if err := deleteClusterDirector(dummyCmd(), t.TempDir(), artDir, testBlueprint("us-central1-a")); err != nil {
			t.Fatalf("deleteClusterDirector() failed: %v", err)
		}
		if !deleted {
			t.Error("expected DeleteCluster to be called")
		}
		if _, ok, _ := readCdMarker(artDir); ok {
			t.Error("marker still exists after successful delete")
		}
	})

	t.Run("full destroy tolerates NOT_FOUND and removes marker", func(t *testing.T) {
		withGroupSelection(t, nil, nil)
		artDir := t.TempDir()
		writeCdMarker(artDir, cdMarker{ClusterName: "ctkdemo", ProjectID: "my-project", Region: "us-central1"})
		withFakeCdClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error": {"code": 404, "message": "not found", "status": "NOT_FOUND"}}`)
		})
		if err := deleteClusterDirector(dummyCmd(), t.TempDir(), artDir, testBlueprint("us-central1-a")); err != nil {
			t.Fatalf("deleteClusterDirector() on NOT_FOUND failed: %v", err)
		}
		if _, ok, _ := readCdMarker(artDir); ok {
			t.Error("marker still exists after NOT_FOUND delete")
		}
	})

	t.Run("full destroy API error fails and preserves marker", func(t *testing.T) {
		withGroupSelection(t, nil, nil)
		artDir := t.TempDir()
		writeCdMarker(artDir, cdMarker{ClusterName: "ctkdemo", ProjectID: "my-project", Region: "us-central1"})
		withFakeCdClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error": {"code": 403, "message": "denied", "status": "PERMISSION_DENIED"}}`)
		})
		err := deleteClusterDirector(dummyCmd(), t.TempDir(), artDir, testBlueprint("us-central1-a"))
		if err == nil || !strings.Contains(err.Error(), "could not delete") {
			t.Errorf("deleteClusterDirector() error = %v, want could not delete", err)
		}
		if _, ok, _ := readCdMarker(artDir); !ok {
			t.Error("marker was removed after failed delete, want it preserved")
		}
	})

	t.Run("partial destroy shrinks cluster and keeps marker", func(t *testing.T) {
		withGroupSelection(t, []string{"compute"}, nil)
		withFakeTerraformState(t, func(string) (*tfjson.State, error) { return nil, errors.New("no state") })
		artDir := t.TempDir()
		writeCdMarker(artDir, cdMarker{ClusterName: "ctkdemo", ProjectID: "my-project", Region: "us-central1"})
		shrunk := false
		withFakeCdClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPatch {
				shrunk = true
				io.WriteString(w, `{"name": "operations/op-shrink", "done": true}`)
			}
		})
		if err := deleteClusterDirector(dummyCmd(), t.TempDir(), artDir, testBlueprint("us-central1-a")); err != nil {
			t.Fatalf("deleteClusterDirector() partial destroy failed: %v", err)
		}
		if !shrunk {
			t.Error("expected UpdateCluster (PATCH) on partial destroy")
		}
		if _, ok, _ := readCdMarker(artDir); !ok {
			t.Error("marker was removed on partial destroy, want it kept")
		}
	})

	t.Run("partial destroy tolerates NOT_FOUND", func(t *testing.T) {
		withGroupSelection(t, []string{"compute"}, nil)
		withFakeTerraformState(t, func(string) (*tfjson.State, error) { return nil, errors.New("no state") })
		artDir := t.TempDir()
		writeCdMarker(artDir, cdMarker{ClusterName: "ctkdemo", ProjectID: "my-project", Region: "us-central1"})
		withFakeCdClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error": {"code": 404, "message": "not found", "status": "NOT_FOUND"}}`)
		})
		if err := deleteClusterDirector(dummyCmd(), t.TempDir(), artDir, testBlueprint("us-central1-a")); err != nil {
			t.Fatalf("deleteClusterDirector() partial destroy on NOT_FOUND failed: %v", err)
		}
	})

	t.Run("partial destroy API error fails", func(t *testing.T) {
		withGroupSelection(t, []string{"compute"}, nil)
		withFakeTerraformState(t, func(string) (*tfjson.State, error) { return nil, errors.New("no state") })
		artDir := t.TempDir()
		writeCdMarker(artDir, cdMarker{ClusterName: "ctkdemo", ProjectID: "my-project", Region: "us-central1"})
		withFakeCdClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"error": {"code": 500, "message": "internal", "status": "INTERNAL"}}`)
		})
		err := deleteClusterDirector(dummyCmd(), t.TempDir(), artDir, testBlueprint("us-central1-a"))
		if err == nil || !strings.Contains(err.Error(), "could not update") {
			t.Errorf("deleteClusterDirector() partial destroy error = %v, want could not update", err)
		}
	})
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
	bp := config.Blueprint{Groups: []config.Group{
		{Name: "packer", Modules: []config.Module{{Kind: config.PackerKind}}},
		tfGroup("network"),
		tfGroup("compute"),
	}}

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

// A destroy deletes the imported cluster record only when it leaves nothing
// behind. Anything else is a partial destroy, which shrinks the imported
// cluster instead.
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


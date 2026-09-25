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
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	cdapi "hpc-toolkit/pkg/clusterdirector"

	"github.com/google/go-cmp/cmp"
)

// runCommand executes the cluster-director command with args and returns its
// stdout.
func runCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out := &bytes.Buffer{}
	ClusterDirectorCmd.SetOut(out)
	ClusterDirectorCmd.SetErr(out)
	ClusterDirectorCmd.SetArgs(args)
	defer ClusterDirectorCmd.SetArgs(nil)
	err := ClusterDirectorCmd.Execute()
	return out.String(), err
}

func TestRegisterDryRun(t *testing.T) {
	out, err := runCommand(t, "register",
		"--dry-run",
		"--project", "my-project",
		"--region", "us-central1",
		"--cluster-name", "ctktest",
		"--deployment-name", "ctk-cluster-director-demo",
		"--network", "ctk-net",
		"--subnet", "ctk-subnet",
		"--bucket", "ctk-bucket-1a2b,ctk-bucket-3c4d")
	if err != nil {
		t.Fatalf("register --dry-run returned an unexpected error: %v", err)
	}

	got := &cdapi.Cluster{}
	if err := json.Unmarshal([]byte(out), got); err != nil {
		t.Fatalf("register --dry-run did not print a cluster payload: %v\n%s", err, out)
	}

	want := &cdapi.Cluster{
		Name: "ctktest",
		NetworkResources: map[string]cdapi.NetworkResource{
			cdapi.NetworkResourceKey: {Config: cdapi.NetworkResourceConfig{ExistingNetwork: &cdapi.ExistingNetworkConfig{
				Network:    "projects/my-project/global/networks/ctk-net",
				Subnetwork: "projects/my-project/regions/us-central1/subnetworks/ctk-subnet",
			}}},
		},
		StorageResources: map[string]cdapi.StorageResource{
			"ctk-bucket-0": {Config: cdapi.StorageResourceConfig{ExistingBucket: &cdapi.ExistingBucketConfig{Bucket: "ctk-bucket-1a2b"}}},
			"ctk-bucket-1": {Config: cdapi.StorageResourceConfig{ExistingBucket: &cdapi.ExistingBucketConfig{Bucket: "ctk-bucket-3c4d"}}},
		},
		Orchestrator: &cdapi.Orchestrator{ComputeEngine: &cdapi.ComputeEngineOrchestrator{
			ExistingInstances: map[string]cdapi.ExistingInstances{
				cdapi.NodesResourceKey: {
					Project: "projects/my-project",
					Labels:  map[string]string{cdapi.DeploymentLabel: "ctk-cluster-director-demo"},
				},
			},
		}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("register --dry-run payload diff (-want +got):\n%s", diff)
	}
}

func TestRegisterRejectsIncompleteRequest(t *testing.T) {
	_, err := runCommand(t, "register",
		"--dry-run",
		"--project", "my-project",
		"--region", "us-central1",
		"--cluster-name", "ctktest",
		"--deployment-name", "",
		"--network", "",
		"--subnet", "",
		"--bucket", "")
	if err == nil || !strings.Contains(err.Error(), "no compute to register") {
		t.Errorf("register --dry-run error = %v, want it to reject a deployment without compute", err)
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("CTK_TEST_VALUE", " value ")
	if got := envOr("CTK_TEST_VALUE", "fallback"); got != "value" {
		t.Errorf("envOr() = %q, want %q", got, "value")
	}
	t.Setenv("CTK_TEST_VALUE", "")
	if got := envOr("CTK_TEST_VALUE", "fallback"); got != "fallback" {
		t.Errorf("envOr() = %q, want %q", got, "fallback")
	}
}

func TestRequireValues(t *testing.T) {
	err := requireValues(map[string]string{"project": "p", "region": "", "cluster-name": " "})
	if err == nil {
		t.Fatalf("requireValues() succeeded, want an error")
	}
	if want := "missing required flag(s): --cluster-name, --region"; err.Error() != want {
		t.Errorf("requireValues() error = %q, want %q", err, want)
	}
	if err := requireValues(map[string]string{"project": "p"}); err != nil {
		t.Errorf("requireValues() returned an unexpected error: %v", err)
	}
}

func TestReportOperationWithoutWait(t *testing.T) {
	opts.wait = false
	tests := []struct {
		name string
		op   *cdapi.Operation
	}{
		{"no operation", nil},
		{"completed operation", &cdapi.Operation{Name: "operations/op-1", Done: true}},
		{"pending operation", &cdapi.Operation{Name: "operations/op-1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := reportOperation(describeCmd, nil, tc.op, "Registered cluster"); err != nil {
				t.Errorf("reportOperation() returned an unexpected error: %v", err)
			}
		})
	}
}

func TestClientOptions(t *testing.T) {
	if got := len(options{}.clientOptions()); got != 2 {
		t.Errorf("clientOptions() returned %d options, want 2", got)
	}
}

func TestDescribeNodesMissingFlags(t *testing.T) {
	_, err := runCommand(t, "describe", "nodes",
		"--project", "my-project",
		"--region", "",
		"--cluster-name", "")
	if err == nil || !strings.Contains(err.Error(), "missing required flag(s): --cluster-name, --region") {
		t.Errorf("describe nodes error = %v, want missing required flag(s)", err)
	}
}

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
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cdapi "hpc-toolkit/pkg/clusterdirector"
	"hpc-toolkit/pkg/shell"

	"github.com/google/go-cmp/cmp"
)

// withTestServer wires newClient to an httptest.Server for the duration of t.
func withTestServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	prev := newClient
	newClient = func(ctx context.Context, _ ...cdapi.Option) (*cdapi.Client, error) {
		return cdapi.NewClient(ctx, cdapi.WithEndpoint(srv.URL), cdapi.WithHTTPClient(srv.Client()))
	}
	t.Cleanup(func() {
		newClient = prev
		srv.Close()
	})
}

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

func TestImportDryRun(t *testing.T) {
	out, err := runCommand(t, "import",
		"--dry-run",
		"--project", "my-project",
		"--region", "us-central1",
		"--cluster-name", "ctktest",
		"--deployment-name", "ctk-cluster-director-demo",
		"--network", "ctk-net",
		"--subnet", "ctk-subnet",
		"--bucket", "ctk-bucket-1a2b,ctk-bucket-3c4d")
	if err != nil {
		t.Fatalf("import --dry-run returned an unexpected error: %v", err)
	}

	got := &cdapi.Cluster{}
	if err := json.Unmarshal([]byte(out), got); err != nil {
		t.Fatalf("import --dry-run did not print a cluster payload: %v\n%s", err, out)
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
		t.Errorf("import --dry-run payload diff (-want +got):\n%s", diff)
	}
}

func TestImportRejectsIncompleteRequest(t *testing.T) {
	_, err := runCommand(t, "import",
		"--dry-run",
		"--project", "my-project",
		"--region", "us-central1",
		"--cluster-name", "ctktest",
		"--deployment-name", "",
		"--network", "",
		"--subnet", "",
		"--bucket", "")
	if err == nil || !strings.Contains(err.Error(), "no compute to import") {
		t.Errorf("import --dry-run error = %v, want it to reject a deployment without compute", err)
	}
}

func TestImportCreateWaitAlreadyExistsAndError(t *testing.T) {
	t.Run("create and wait", func(t *testing.T) {
		withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				io.WriteString(w, `{"name": "operations/op-1", "done": false}`)
				return
			}
			io.WriteString(w, `{"name": "operations/op-1", "done": true}`)
		})
		_, err := runCommand(t, "import",
			"--dry-run=false",
			"--project", "my-project",
			"--region", "us-central1",
			"--cluster-name", "ctktest",
			"--deployment-name", "ctkdemo",
			"--wait",
			"--poll-interval", "1ms",
			"--timeout", "1s")
		if err != nil {
			t.Fatalf("import --wait returned an unexpected error: %v", err)
		}
	})

	t.Run("already exists is idempotent", func(t *testing.T) {
		withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, `{"error": {"code": 409, "message": "already exists", "status": "ALREADY_EXISTS"}}`)
		})
		_, err := runCommand(t, "import",
			"--dry-run=false",
			"--project", "my-project",
			"--region", "us-central1",
			"--cluster-name", "ctktest",
			"--deployment-name", "ctkdemo")
		if err != nil {
			t.Fatalf("import on ALREADY_EXISTS returned an unexpected error: %v", err)
		}
	})

	t.Run("api error fails", func(t *testing.T) {
		withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error": {"code": 403, "message": "denied", "status": "PERMISSION_DENIED"}}`)
		})
		_, err := runCommand(t, "import",
			"--dry-run=false",
			"--project", "my-project",
			"--region", "us-central1",
			"--cluster-name", "ctktest",
			"--deployment-name", "ctkdemo")
		if err == nil || !strings.Contains(err.Error(), "could not import cluster") {
			t.Errorf("import error = %v, want could not import cluster", err)
		}
	})
}

func TestDeleteCommand(t *testing.T) {
	t.Run("missing flags", func(t *testing.T) {
		_, err := runCommand(t, "delete", "--project", "my-project", "--region", "", "--cluster-name", "")
		if err == nil || !strings.Contains(err.Error(), "missing required flag(s)") {
			t.Errorf("delete error = %v, want missing required flag(s)", err)
		}
	})

	t.Run("delete success", func(t *testing.T) {
		withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"name": "operations/op-del", "done": true}`)
		})
		_, err := runCommand(t, "delete",
			"--project", "my-project",
			"--region", "us-central1",
			"--cluster-name", "ctktest",
			"--wait=false")
		if err != nil {
			t.Fatalf("delete returned an unexpected error: %v", err)
		}
	})

	t.Run("not found is idempotent", func(t *testing.T) {
		withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error": {"code": 404, "message": "not found", "status": "NOT_FOUND"}}`)
		})
		_, err := runCommand(t, "delete",
			"--project", "my-project",
			"--region", "us-central1",
			"--cluster-name", "ctktest")
		if err != nil {
			t.Fatalf("delete on NOT_FOUND returned an unexpected error: %v", err)
		}
	})

	t.Run("api error fails", func(t *testing.T) {
		withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error": {"code": 403, "message": "denied", "status": "PERMISSION_DENIED"}}`)
		})
		_, err := runCommand(t, "delete",
			"--project", "my-project",
			"--region", "us-central1",
			"--cluster-name", "ctktest")
		if err == nil || !strings.Contains(err.Error(), "could not delete imported cluster") {
			t.Errorf("delete error = %v, want could not delete imported cluster", err)
		}
	})
}

func TestDescribeAndDescribeNodesCommands(t *testing.T) {
	t.Run("describe missing flags", func(t *testing.T) {
		_, err := runCommand(t, "describe", "--project", "my-project", "--region", "", "--cluster-name", "")
		if err == nil || !strings.Contains(err.Error(), "missing required flag(s)") {
			t.Errorf("describe error = %v, want missing required flag(s)", err)
		}
	})

	t.Run("describe success", func(t *testing.T) {
		withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"name": "projects/my-project/locations/us-central1/clusters/ctktest"}`)
		})
		out, err := runCommand(t, "describe",
			"--project", "my-project",
			"--region", "us-central1",
			"--cluster-name", "ctktest")
		if err != nil {
			t.Fatalf("describe returned an unexpected error: %v", err)
		}
		if !strings.Contains(out, "ctktest") {
			t.Errorf("describe output = %q, want ctktest", out)
		}
	})

	t.Run("describe error", func(t *testing.T) {
		withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error": {"code": 404, "message": "not found", "status": "NOT_FOUND"}}`)
		})
		_, err := runCommand(t, "describe",
			"--project", "my-project",
			"--region", "us-central1",
			"--cluster-name", "ctktest")
		if err == nil || !strings.Contains(err.Error(), "could not read cluster") {
			t.Errorf("describe error = %v, want could not read cluster", err)
		}
	})

	t.Run("describe nodes missing flags", func(t *testing.T) {
		_, err := runCommand(t, "describe", "nodes",
			"--project", "my-project",
			"--region", "",
			"--cluster-name", "")
		if err == nil || !strings.Contains(err.Error(), "missing required flag(s): --cluster-name, --region") {
			t.Errorf("describe nodes error = %v, want missing required flag(s)", err)
		}
	})

	t.Run("describe nodes success", func(t *testing.T) {
		withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"nodes": [{"name": "node-0", "state": "ACTIVE"}]}`)
		})
		out, err := runCommand(t, "describe", "nodes",
			"--project", "my-project",
			"--region", "us-central1",
			"--cluster-name", "ctktest")
		if err != nil {
			t.Fatalf("describe nodes returned an unexpected error: %v", err)
		}
		if !strings.Contains(out, "node-0") {
			t.Errorf("describe nodes output = %q, want node-0", out)
		}
	})

	t.Run("describe nodes error", func(t *testing.T) {
		withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error": {"code": 404, "message": "not found", "status": "NOT_FOUND"}}`)
		})
		_, err := runCommand(t, "describe", "nodes",
			"--project", "my-project",
			"--region", "us-central1",
			"--cluster-name", "ctktest")
		if err == nil || !strings.Contains(err.Error(), "could not list nodes") {
			t.Errorf("describe nodes error = %v, want could not list nodes", err)
		}
	})
}

func TestNewClientErrorPropagation(t *testing.T) {
	prev := newClient
	newClient = func(context.Context, ...cdapi.Option) (*cdapi.Client, error) {
		return nil, errors.New("no ADC")
	}
	t.Cleanup(func() { newClient = prev })

	for _, args := range [][]string{
		{"import", "--dry-run=false", "--project", "p", "--region", "us-central1", "--cluster-name", "ctktest", "--deployment-name", "d"},
		{"delete", "--project", "p", "--region", "us-central1", "--cluster-name", "ctktest"},
		{"describe", "--project", "p", "--region", "us-central1", "--cluster-name", "ctktest"},
		{"describe", "nodes", "--project", "p", "--region", "us-central1", "--cluster-name", "ctktest"},
	} {
		if _, err := runCommand(t, args...); err == nil || !strings.Contains(err.Error(), "no ADC") {
			t.Errorf("runCommand(%v) error = %v, want no ADC", args, err)
		}
	}
}

func TestResolveProject(t *testing.T) {
	if got := resolveProject("explicit-proj"); got != "explicit-proj" {
		t.Errorf("resolveProject(explicit-proj) = %q, want explicit-proj", got)
	}

	prev := shell.ExecuteCommand
	t.Cleanup(func() { shell.ExecuteCommand = prev })

	shell.ExecuteCommand = func(string, ...string) shell.CommandResult {
		return shell.CommandResult{Stdout: " ambient-proj \n", ExitCode: 0}
	}
	if got := resolveProject(""); got != "ambient-proj" {
		t.Errorf("resolveProject(\"\") = %q, want ambient-proj", got)
	}

	shell.ExecuteCommand = func(string, ...string) shell.CommandResult {
		return shell.CommandResult{Stdout: "(unset)\n", ExitCode: 0}
	}
	if got := resolveProject(""); got != "" {
		t.Errorf("resolveProject(\"\") on (unset) = %q, want empty", got)
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

func TestReportOperationWithoutAndWithWait(t *testing.T) {
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
			if err := reportOperation(describeCmd, nil, tc.op, "Imported cluster"); err != nil {
				t.Errorf("reportOperation() returned an unexpected error: %v", err)
			}
		})
	}

	opts.wait = true
	opts.pollInterval = time.Millisecond
	opts.timeout = time.Second
	t.Cleanup(func() { opts.wait = false })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"name": "operations/op-err", "done": true, "error": {"code": 3, "message": "failed op"}}`)
	}))
	defer srv.Close()
	c, _ := cdapi.NewClient(context.Background(), cdapi.WithEndpoint(srv.URL), cdapi.WithHTTPClient(srv.Client()))

	if err := reportOperation(describeCmd, c, &cdapi.Operation{Name: "operations/op-err"}, "Imported cluster"); err == nil {
		t.Error("reportOperation() with failing op succeeded, want error")
	}
}

func TestClientOptions(t *testing.T) {
	if got := len(options{}.clientOptions()); got != 2 {
		t.Errorf("clientOptions() returned %d options, want 2", got)
	}
}


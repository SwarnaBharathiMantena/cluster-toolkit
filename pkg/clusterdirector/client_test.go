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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// testClient returns a Client talking to srv without authentication.
func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	client, err := NewClient(context.Background(),
		WithEndpoint(srv.URL),
		WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("NewClient() returned an unexpected error: %v", err)
	}
	return client
}

func TestCreateCluster(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotContentType string
	var gotBody Cluster

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("request body is not a valid cluster payload: %v", err)
		}
		io.WriteString(w, `{"name": "projects/p/locations/us-central1/operations/op-1", "done": false}`)
	}))
	defer srv.Close()

	want := &Cluster{Name: "ctktest"}
	op, err := testClient(t, srv).CreateCluster(context.Background(), "p", "us-central1", "ctktest", want)
	if err != nil {
		t.Fatalf("CreateCluster() returned an unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("CreateCluster() method = %s, want %s", gotMethod, http.MethodPost)
	}
	if wantPath := "/v1alpha/projects/p/locations/us-central1/clusters"; gotPath != wantPath {
		t.Errorf("CreateCluster() path = %s, want %s", gotPath, wantPath)
	}
	if wantQuery := "clusterId=ctktest"; gotQuery != wantQuery {
		t.Errorf("CreateCluster() query = %s, want %s", gotQuery, wantQuery)
	}
	if gotContentType != "application/json" {
		t.Errorf("CreateCluster() Content-Type = %s, want application/json", gotContentType)
	}
	if diff := cmp.Diff(*want, gotBody); diff != "" {
		t.Errorf("CreateCluster() payload diff (-want +got):\n%s", diff)
	}
	if op.Name != "projects/p/locations/us-central1/operations/op-1" || op.Done {
		t.Errorf("CreateCluster() operation = %+v, want the pending operation op-1", op)
	}
}

func TestGetCluster(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/v1alpha/projects/p/locations/us-central1/clusters/ctktest"; r.URL.Path != want {
			t.Errorf("GetCluster() path = %s, want %s", r.URL.Path, want)
		}
		io.WriteString(w, `{"name": "projects/p/locations/us-central1/clusters/ctktest", "labels": {"env": "dev"}}`)
	}))
	defer srv.Close()

	got, err := testClient(t, srv).GetCluster(context.Background(), "p", "us-central1", "ctktest")
	if err != nil {
		t.Fatalf("GetCluster() returned an unexpected error: %v", err)
	}
	want := &Cluster{
		Name:   "projects/p/locations/us-central1/clusters/ctktest",
		Labels: map[string]string{"env": "dev"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetCluster() diff (-want +got):\n%s", diff)
	}
}

func TestUpdateCluster(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	var gotBody Cluster

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("request body is not a valid cluster payload: %v", err)
		}
		io.WriteString(w, `{"name": "operations/op-3", "done": false}`)
	}))
	defer srv.Close()

	want := &Cluster{Name: "ctktest"}
	op, err := testClient(t, srv).UpdateCluster(context.Background(), "p", "us-central1", "ctktest", want, UpdateResourceMask)
	if err != nil {
		t.Fatalf("UpdateCluster() returned an unexpected error: %v", err)
	}

	if gotMethod != http.MethodPatch {
		t.Errorf("UpdateCluster() method = %s, want %s", gotMethod, http.MethodPatch)
	}
	if wantPath := "/v1alpha/projects/p/locations/us-central1/clusters/ctktest"; gotPath != wantPath {
		t.Errorf("UpdateCluster() path = %s, want %s", gotPath, wantPath)
	}
	// The API rejects wildcards, so every path is listed explicitly.
	if wantQuery := "updateMask=network_resources%2Cstorage_resources%2Corchestrator"; gotQuery != wantQuery {
		t.Errorf("UpdateCluster() query = %s, want %s", gotQuery, wantQuery)
	}
	if diff := cmp.Diff(*want, gotBody); diff != "" {
		t.Errorf("UpdateCluster() payload diff (-want +got):\n%s", diff)
	}
	if op.Name != "operations/op-3" {
		t.Errorf("UpdateCluster() operation = %+v, want op-3", op)
	}
}

func TestUpdateClusterDefaultsTheMask(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		io.WriteString(w, `{"name": "operations/op-4", "done": true}`)
	}))
	defer srv.Close()

	if _, err := testClient(t, srv).UpdateCluster(
		context.Background(), "p", "us-central1", "ctktest", &Cluster{Name: "ctktest"}, ""); err != nil {
		t.Fatalf("UpdateCluster() returned an unexpected error: %v", err)
	}
	if wantQuery := "updateMask=network_resources%2Cstorage_resources%2Corchestrator"; gotQuery != wantQuery {
		t.Errorf("UpdateCluster() with an empty mask sent query = %s, want %s", gotQuery, wantQuery)
	}
}

func TestDeleteCluster(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("DeleteCluster() method = %s, want %s", r.Method, http.MethodDelete)
		}
		io.WriteString(w, `{"name": "operations/op-2", "done": true}`)
	}))
	defer srv.Close()

	op, err := testClient(t, srv).DeleteCluster(context.Background(), "p", "us-central1", "ctktest")
	if err != nil {
		t.Fatalf("DeleteCluster() returned an unexpected error: %v", err)
	}
	if !op.Done {
		t.Errorf("DeleteCluster() operation = %+v, want a completed operation", op)
	}
}

func TestAPIErrors(t *testing.T) {
	tests := []struct {
		name            string
		code            int
		body            string
		wantAlreadyHave bool
		wantNotFound    bool
		wantMessage     string
	}{
		{
			name:            "already exists",
			code:            http.StatusConflict,
			body:            `{"error": {"code": 409, "message": "Cluster ctktest already exists.", "status": "ALREADY_EXISTS"}}`,
			wantAlreadyHave: true,
			wantMessage:     "already exists",
		},
		{
			name:         "not found",
			code:         http.StatusNotFound,
			body:         `{"error": {"code": 404, "message": "Cluster ctktest not found.", "status": "NOT_FOUND"}}`,
			wantNotFound: true,
			wantMessage:  "not found",
		},
		{
			name:        "permission denied",
			code:        http.StatusForbidden,
			body:        `{"error": {"code": 403, "message": "Permission denied.", "status": "PERMISSION_DENIED"}}`,
			wantMessage: "Permission denied",
		},
		{
			name:        "unparsable body",
			code:        http.StatusBadGateway,
			body:        `<html>bad gateway</html>`,
			wantMessage: "bad gateway",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			_, err := testClient(t, srv).CreateCluster(context.Background(), "p", "us-central1", "ctktest", &Cluster{})
			if err == nil {
				t.Fatalf("CreateCluster() succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tc.wantMessage) {
				t.Errorf("CreateCluster() error = %q, want it to contain %q", err, tc.wantMessage)
			}
			if got := IsAlreadyExists(err); got != tc.wantAlreadyHave {
				t.Errorf("IsAlreadyExists(%v) = %t, want %t", err, got, tc.wantAlreadyHave)
			}
			if got := IsNotFound(err); got != tc.wantNotFound {
				t.Errorf("IsNotFound(%v) = %t, want %t", err, got, tc.wantNotFound)
			}
		})
	}
}

func TestWaitForOperation(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls++
		if want := "/v1alpha/operations/op-1"; r.URL.Path != want {
			t.Errorf("WaitForOperation() path = %s, want %s", r.URL.Path, want)
		}
		if polls < 2 {
			io.WriteString(w, `{"name": "operations/op-1", "done": false}`)
			return
		}
		io.WriteString(w, `{"name": "operations/op-1", "done": true}`)
	}))
	defer srv.Close()

	op, err := testClient(t, srv).WaitForOperation(context.Background(),
		&Operation{Name: "operations/op-1"}, time.Millisecond, time.Minute)
	if err != nil {
		t.Fatalf("WaitForOperation() returned an unexpected error: %v", err)
	}
	if !op.Done {
		t.Errorf("WaitForOperation() operation = %+v, want a completed operation", op)
	}
	if polls < 2 {
		t.Errorf("WaitForOperation() polled %d times, want at least 2", polls)
	}
}

func TestWaitForFailedOperation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"name": "operations/op-1", "done": true, "error": {"code": 3, "message": "invalid subnetwork"}}`)
	}))
	defer srv.Close()

	_, err := testClient(t, srv).WaitForOperation(context.Background(),
		&Operation{Name: "operations/op-1"}, time.Millisecond, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "invalid subnetwork") {
		t.Errorf("WaitForOperation() error = %v, want it to report the operation failure", err)
	}
}

func TestWaitForOperationTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"name": "operations/op-1", "done": false}`)
	}))
	defer srv.Close()

	_, err := testClient(t, srv).WaitForOperation(context.Background(),
		&Operation{Name: "operations/op-1"}, time.Millisecond, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("WaitForOperation() error = %v, want a timeout", err)
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"hypercomputecluster.googleapis.com", "https://hypercomputecluster.googleapis.com"},
		{"https://staging-hypercomputecluster.sandbox.googleapis.com/", "https://staging-hypercomputecluster.sandbox.googleapis.com"},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080"},
	}
	for _, tc := range tests {
		if got := normalizeEndpoint(tc.in); got != tc.want {
			t.Errorf("normalizeEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveEndpoint(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"staging", StagingEndpoint},
		{" Staging ", StagingEndpoint},
		{"prod", ProdEndpoint},
		{"autopush", AutopushEndpoint},
		// "sandbox" is not an alias: that environment has no routable host.
		{"sandbox", "sandbox"},
		{"my-test-endpoint.example.com", "my-test-endpoint.example.com"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := ResolveEndpoint(tc.in); got != tc.want {
			t.Errorf("ResolveEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDefaultEndpointIsProd(t *testing.T) {
	client, err := NewClient(context.Background(), WithHTTPClient(http.DefaultClient))
	if err != nil {
		t.Fatalf("NewClient() returned an unexpected error: %v", err)
	}
	want := "https://" + ProdEndpoint + "/v1alpha/projects/p/locations/us-central1/clusters"
	if got := client.baseURL("p", "us-central1"); got != want {
		t.Errorf("baseURL() = %q, want %q", got, want)
	}
}

func TestWithEndpointResolvesAlias(t *testing.T) {
	// Use a non-default alias so the assertion cannot pass by accident.
	client, err := NewClient(context.Background(), WithEndpoint("staging"), WithHTTPClient(http.DefaultClient))
	if err != nil {
		t.Fatalf("NewClient() returned an unexpected error: %v", err)
	}
	want := "https://" + StagingEndpoint + "/v1alpha/projects/p/locations/us-central1/clusters"
	if got := client.baseURL("p", "us-central1"); got != want {
		t.Errorf("baseURL() = %q, want %q", got, want)
	}
}

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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// basicSpec is the equivalent of the simple Cluster Toolkit demo deployment:
// one VPC, one bucket and label selected VM instances.
func basicSpec() Spec {
	return Spec{
		ProjectID:      "my-project",
		Region:         "us-central1",
		Zone:           "us-central1-a",
		ClusterName:    "ctktest",
		DeploymentName: "ctk-cluster-director-demo",
		NetworkName:    "ctk-net",
		SubnetName:     "ctk-subnet",
		Buckets:        []string{"ctk-bucket-1a2b"},
	}
}

func TestClusterBasic(t *testing.T) {
	got, err := basicSpec().Cluster()
	if err != nil {
		t.Fatalf("Cluster() returned an unexpected error: %v", err)
	}

	want := &Cluster{
		Name: "ctktest",
		NetworkResources: map[string]NetworkResource{
			NetworkResourceKey: {Config: NetworkResourceConfig{ExistingNetwork: &ExistingNetworkConfig{
				Network:    "projects/my-project/global/networks/ctk-net",
				Subnetwork: "projects/my-project/regions/us-central1/subnetworks/ctk-subnet",
			}}},
		},
		StorageResources: map[string]StorageResource{
			"ctk-bucket-0": {Config: StorageResourceConfig{ExistingBucket: &ExistingBucketConfig{Bucket: "ctk-bucket-1a2b"}}},
		},
		Orchestrator: &Orchestrator{ComputeEngine: &ComputeEngineOrchestrator{
			ExistingInstances: map[string]ExistingInstances{
				NodesResourceKey: {
					Project: "projects/my-project",
					Labels:  map[string]string{DeploymentLabel: "ctk-cluster-director-demo"},
				},
			},
		}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Cluster() returned diff (-want +got):\n%s", diff)
	}
}

func TestClusterComprehensive(t *testing.T) {
	spec := basicSpec()
	spec.Buckets = []string{"gs://bucket-one", "bucket-two"}
	spec.Filestores = []string{"projects/my-project/locations/us-central1-a/instances/ctk-filestore"}
	spec.Lustres = []string{"https://lustre.googleapis.com/v1/projects/my-project/locations/us-central1-a/instances/ctk-lustre"}
	spec.MIGs = []string{
		"https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/instanceGroupManagers/mig-pool",
		"projects/my-project/regions/us-central1/instanceGroupManagers/regional-pool",
	}
	spec.Reservations = []string{"my-reservation"}
	spec.ClusterLabels = map[string]string{"env": "dev"}

	got, err := spec.Cluster()
	if err != nil {
		t.Fatalf("Cluster() returned an unexpected error: %v", err)
	}

	wantStorage := map[string]StorageResource{
		"ctk-bucket-0":    {Config: StorageResourceConfig{ExistingBucket: &ExistingBucketConfig{Bucket: "bucket-one"}}},
		"ctk-bucket-1":    {Config: StorageResourceConfig{ExistingBucket: &ExistingBucketConfig{Bucket: "bucket-two"}}},
		"ctk-filestore-0": {Config: StorageResourceConfig{ExistingFilestore: &ExistingFilestoreConfig{Filestore: "projects/my-project/locations/us-central1-a/instances/ctk-filestore"}}},
		"ctk-lustre-0":    {Config: StorageResourceConfig{ExistingLustre: &ExistingLustreConfig{Lustre: "projects/my-project/locations/us-central1-a/instances/ctk-lustre"}}},
	}
	if diff := cmp.Diff(wantStorage, got.StorageResources); diff != "" {
		t.Errorf("Cluster() storage resources diff (-want +got):\n%s", diff)
	}

	wantInstances := map[string]ExistingInstances{
		NodesResourceKey: {
			Project: "projects/my-project",
			Labels:  map[string]string{DeploymentLabel: "ctk-cluster-director-demo"},
		},
		"ctk-mig-0":         {InstanceGroupManager: "projects/my-project/zones/us-central1-a/instanceGroupManagers/mig-pool"},
		"ctk-mig-1":         {RegionInstanceGroupManager: "projects/my-project/regions/us-central1/instanceGroupManagers/regional-pool"},
		"ctk-reservation-0": {Reservation: "projects/my-project/zones/us-central1-a/reservations/my-reservation"},
	}
	if diff := cmp.Diff(wantInstances, got.Orchestrator.ComputeEngine.ExistingInstances); diff != "" {
		t.Errorf("Cluster() existing instances diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string]string{"env": "dev"}, got.Labels); diff != "" {
		t.Errorf("Cluster() labels diff (-want +got):\n%s", diff)
	}
}

func TestClusterWithoutNetwork(t *testing.T) {
	spec := basicSpec()
	spec.NetworkName = ""
	spec.SubnetName = ""

	got, err := spec.Cluster()
	if err != nil {
		t.Fatalf("Cluster() returned an unexpected error: %v", err)
	}
	if got.NetworkResources != nil {
		t.Errorf("Cluster() network resources = %v, want nil", got.NetworkResources)
	}
}

func TestClusterInstanceLabelsOverride(t *testing.T) {
	spec := basicSpec()
	spec.InstanceLabels = map[string]string{"pool": "training"}

	got, err := spec.Cluster()
	if err != nil {
		t.Fatalf("Cluster() returned an unexpected error: %v", err)
	}
	want := map[string]string{"pool": "training"}
	if diff := cmp.Diff(want, got.Orchestrator.ComputeEngine.ExistingInstances[NodesResourceKey].Labels); diff != "" {
		t.Errorf("Cluster() instance labels diff (-want +got):\n%s", diff)
	}
}

func TestClusterErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Spec)
		wantErr string
	}{
		{"no project", func(s *Spec) { s.ProjectID = "" }, "missing required value(s): project"},
		{"no region", func(s *Spec) { s.Region = "" }, "missing required value(s): region"},
		{"no cluster name", func(s *Spec) { s.ClusterName = "" }, "missing required value(s): cluster name"},
		{"invalid cluster name", func(s *Spec) { s.ClusterName = "CTK_Test" }, "must start with a letter"},
		{"subnet without network", func(s *Spec) { s.NetworkName = "" }, "must be provided together"},
		{"no compute", func(s *Spec) { s.DeploymentName = "" }, "no compute to register"},
		{"bare reservation without zone", func(s *Spec) { s.Zone = ""; s.Reservations = []string{"res"} }, "a zone is required"},
		{"bucket with path", func(s *Spec) { s.Buckets = []string{"bucket/with/path"} }, "must be a bare bucket name"},
		{"short filestore", func(s *Spec) { s.Filestores = []string{"my-filestore"} }, "must be of the form"},
		{"bad mig", func(s *Spec) { s.MIGs = []string{"projects/p/zones/z/instances/i"} }, "must be of the form"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := basicSpec()
			tc.mutate(&spec)
			_, err := spec.Cluster()
			if err == nil {
				t.Fatalf("Cluster() succeeded, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Cluster() error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestSanitizeKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"ctk-bucket-0", "ctk-bucket-0"},
		{"CTK_Bucket 0", "ctk-bucket-0"},
		{"0-leading-digit", "ctk-0-leading-digit"},
		{"--trimmed--", "trimmed"},
		{"", "ctk"},
		{strings.Repeat("a", 80), strings.Repeat("a", 63)},
	}
	for _, tc := range tests {
		if got := SanitizeKey(tc.in); got != tc.want {
			t.Errorf("SanitizeKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSplitList(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b ,, c ", []string{"a", "b", "c"}},
		{"a\nb", []string{"a", "b"}},
	}
	for _, tc := range tests {
		if diff := cmp.Diff(tc.want, SplitList(tc.in)); diff != "" {
			t.Errorf("SplitList(%q) diff (-want +got):\n%s", tc.in, diff)
		}
	}
}

func TestQualifiedLeafAcceptsSelfLinks(t *testing.T) {
	spec := basicSpec()
	spec.NetworkName = "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/ctk-net"
	spec.SubnetName = "projects/my-project/regions/us-central1/subnetworks/ctk-subnet"

	got, err := spec.Cluster()
	if err != nil {
		t.Fatalf("Cluster() returned an unexpected error: %v", err)
	}
	want := &ExistingNetworkConfig{
		Network:    "projects/my-project/global/networks/ctk-net",
		Subnetwork: "projects/my-project/regions/us-central1/subnetworks/ctk-subnet",
	}
	if diff := cmp.Diff(want, got.NetworkResources[NetworkResourceKey].Config.ExistingNetwork); diff != "" {
		t.Errorf("Cluster() network diff (-want +got):\n%s", diff)
	}
}

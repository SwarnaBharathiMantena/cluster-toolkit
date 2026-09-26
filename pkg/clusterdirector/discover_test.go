/**
 * Copyright 2026 Google LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package clusterdirector

import (
	"reflect"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
)

func res(mode tfjson.ResourceMode, typ string, attrs map[string]any) *tfjson.StateResource {
	return &tfjson.StateResource{Mode: mode, Type: typ, AttributeValues: attrs}
}

func managed(typ string, attrs map[string]any) *tfjson.StateResource {
	return res(tfjson.ManagedResourceMode, typ, attrs)
}

func data(typ string, attrs map[string]any) *tfjson.StateResource {
	return res(tfjson.DataResourceMode, typ, attrs)
}

func state(root *tfjson.StateModule) *tfjson.State {
	return &tfjson.State{Values: &tfjson.StateValues{RootModule: root}}
}

func TestDiscoverFindsResourcesInNestedModules(t *testing.T) {
	// Cluster Toolkit always nests: the root module calls a module per
	// blueprint module, which itself may call upstream modules.
	st := state(&tfjson.StateModule{
		ChildModules: []*tfjson.StateModule{
			{
				ChildModules: []*tfjson.StateModule{
					{
						Resources: []*tfjson.StateResource{
							managed(tfNetwork, map[string]any{"name": "ctk-net"}),
							managed(tfSubnetwork, map[string]any{"name": "ctk-subnet", "region": "us-central1"}),
						},
					},
				},
			},
			{
				Resources: []*tfjson.StateResource{
					managed(tfBucket, map[string]any{"name": "bucket-a"}),
					managed(tfFilestore, map[string]any{"id": "projects/p/locations/us-central1-a/instances/fs"}),
					managed(tfLustre, map[string]any{"id": "projects/p/locations/us-central1-a/instances/lu"}),
					managed(tfMIG, map[string]any{"self_link": "https://example/zones/us-central1-a/instanceGroupManagers/mig"}),
					managed(tfRegionMIG, map[string]any{"self_link": "https://example/regions/us-central1/instanceGroupManagers/rmig"}),
				},
			},
		},
	})

	got := Discover([]*tfjson.State{st})

	if want := []string{"ctk-net"}; !reflect.DeepEqual(got.Networks, want) {
		t.Errorf("Networks = %v, want %v", got.Networks, want)
	}
	if want := []string{"bucket-a"}; !reflect.DeepEqual(got.Buckets, want) {
		t.Errorf("Buckets = %v, want %v", got.Buckets, want)
	}
	if want := []string{"projects/p/locations/us-central1-a/instances/fs"}; !reflect.DeepEqual(got.Filestores, want) {
		t.Errorf("Filestores = %v, want %v", got.Filestores, want)
	}
	if want := []string{"projects/p/locations/us-central1-a/instances/lu"}; !reflect.DeepEqual(got.Lustres, want) {
		t.Errorf("Lustres = %v, want %v", got.Lustres, want)
	}
	if len(got.MIGs) != 2 {
		t.Errorf("MIGs = %v, want both the zonal and the regional group", got.MIGs)
	}
	if r := got.Subnetworks["ctk-subnet"]; r != "us-central1" {
		t.Errorf("Subnetworks[ctk-subnet] = %q, want %q", r, "us-central1")
	}
}

func TestDiscoverIgnoresLookalikeResourceTypes(t *testing.T) {
	// google_storage_bucket_iam_binding must not be read as a bucket.
	st := state(&tfjson.StateModule{
		Resources: []*tfjson.StateResource{
			managed("google_storage_bucket_iam_binding", map[string]any{"name": "not-a-bucket"}),
			managed("google_storage_bucket_iam_member", map[string]any{"name": "also-not"}),
			managed("google_compute_network_peering", map[string]any{"name": "not-a-network"}),
		},
	})

	got := Discover([]*tfjson.State{st})
	if len(got.Buckets) != 0 || len(got.Networks) != 0 {
		t.Errorf("Discover() = %+v, want nothing matched", got)
	}
}

func TestDiscoverDataSourcePolicy(t *testing.T) {
	// A pre-existing VPC is read, not created, and must still be imported.
	// An incidentally read bucket must not be.
	st := state(&tfjson.StateModule{
		Resources: []*tfjson.StateResource{
			data(tfNetwork, map[string]any{"name": "shared-net"}),
			data(tfSubnetwork, map[string]any{"name": "shared-subnet", "region": "us-central1"}),
			data(tfBucket, map[string]any{"name": "someone-elses-bucket"}),
			data(tfFilestore, map[string]any{"id": "projects/p/locations/z/instances/other"}),
			data(tfMIG, map[string]any{"self_link": "https://example/other"}),
		},
	})

	got := Discover([]*tfjson.State{st})

	if want := []string{"shared-net"}; !reflect.DeepEqual(got.Networks, want) {
		t.Errorf("Networks = %v, want the pre-existing VPC %v", got.Networks, want)
	}
	if _, ok := got.Subnetworks["shared-subnet"]; !ok {
		t.Errorf("Subnetworks = %v, want the pre-existing subnetwork", got.Subnetworks)
	}
	if len(got.Buckets) != 0 || len(got.Filestores) != 0 || len(got.MIGs) != 0 {
		t.Errorf("Discover() picked up data sources it should ignore: %+v", got)
	}
}

func TestDiscoverFindsGKENodePoolInstanceGroups(t *testing.T) {
	// A GKE node pool is not a MIG resource: GKE creates the groups and the
	// provider exports their URLs, so matching on MIG types alone finds none.
	st := state(&tfjson.StateModule{Resources: []*tfjson.StateResource{
		managed(tfNodePool, map[string]any{
			"name": "e2-pool",
			"managed_instance_group_urls": []any{
				"https://example/zones/us-central1-a/instanceGroupManagers/gke-pool-a",
			},
			"instance_group_urls": []any{
				// Same group reported under both attributes: must dedupe.
				"https://example/zones/us-central1-a/instanceGroupManagers/gke-pool-a",
				"https://example/zones/us-central1-b/instanceGroupManagers/gke-pool-b",
			},
		}),
	}})

	got := Discover([]*tfjson.State{st})
	want := []string{
		"https://example/zones/us-central1-a/instanceGroupManagers/gke-pool-a",
		"https://example/zones/us-central1-b/instanceGroupManagers/gke-pool-b",
	}
	if !reflect.DeepEqual(got.MIGs, want) {
		t.Errorf("MIGs = %v, want %v", got.MIGs, want)
	}
}

func TestDiscoverFindsReservationsFromAffinity(t *testing.T) {
	// A reservation is consumed, not created, so it only shows up as an
	// affinity block on the instance.
	affinity := func(kind, name string) map[string]any {
		return map[string]any{
			"reservation_affinity": []any{map[string]any{
				"type": kind,
				"specific_reservation": []any{map[string]any{
					"key":    "compute.googleapis.com/reservation-name",
					"values": []any{name},
				}},
			}},
		}
	}

	st := state(&tfjson.StateModule{Resources: []*tfjson.StateResource{
		managed(tfInstance, affinity("SPECIFIC_RESERVATION", "my-reservation")),
		managed(tfInstance, affinity("SPECIFIC_RESERVATION", "my-reservation")), // dedupe
		managed(tfInstanceTemplate, affinity("SPECIFIC_RESERVATION", "mig-reservation")),
		// Not a named reservation: a policy, nothing to import.
		managed(tfInstance, affinity("ANY_RESERVATION", "ignored")),
		// Single (non-repeated) map block form is tolerated too.
		managed(tfInstance, map[string]any{
			"reservation_affinity": map[string]any{
				"type": "SPECIFIC_RESERVATION",
				"specific_reservation": map[string]any{
					"values": []any{"single-block-res"},
				},
			},
		}),
		// No affinity at all, the common case.
		managed(tfInstance, map[string]any{"name": "plain-vm"}),
	}})

	got := Discover([]*tfjson.State{st})
	want := []string{"mig-reservation", "my-reservation", "single-block-res"}
	if !reflect.DeepEqual(got.Reservations, want) {
		t.Errorf("Reservations = %v, want %v", got.Reservations, want)
	}
}

func TestReservationNamesToleratesMalformedBlocks(t *testing.T) {
	cases := []map[string]any{
		{"reservation_affinity": "not-a-block"},
		{"reservation_affinity": []any{"not-an-object"}},
		{"reservation_affinity": []any{map[string]any{"type": "SPECIFIC_RESERVATION"}}},
		{"reservation_affinity": []any{map[string]any{
			"type":                 "SPECIFIC_RESERVATION",
			"specific_reservation": []any{map[string]any{"values": "not-a-list"}},
		}}},
		{"reservation_affinity": []any{map[string]any{
			"type":                 "SPECIFIC_RESERVATION",
			"specific_reservation": []any{map[string]any{"values": []any{42, ""}}},
		}}},
	}
	for i, attrs := range cases {
		if got := reservationNames(managed(tfInstance, attrs)); len(got) != 0 {
			t.Errorf("case %d: reservationNames() = %v, want none", i, got)
		}
	}
}

func TestDiscoverDeduplicatesAcrossGroups(t *testing.T) {
	mk := func() *tfjson.State {
		return state(&tfjson.StateModule{Resources: []*tfjson.StateResource{
			managed(tfBucket, map[string]any{"name": "shared"}),
		}})
	}
	got := Discover([]*tfjson.State{mk(), mk()})
	if want := []string{"shared"}; !reflect.DeepEqual(got.Buckets, want) {
		t.Errorf("Buckets = %v, want %v", got.Buckets, want)
	}
}

func TestDiscoverToleratesEmptyAndMalformedStates(t *testing.T) {
	got := Discover([]*tfjson.State{
		nil,
		{},                           // no Values
		state(nil),                   // no root module
		state(&tfjson.StateModule{}), // no resources
		state(&tfjson.StateModule{Resources: []*tfjson.StateResource{
			nil,
			managed(tfBucket, map[string]any{}), // attribute missing
			managed(tfBucket, map[string]any{"name": 42}), // wrong type
			managed(tfBucket, map[string]any{"name": ""}), // empty
		}}),
	})
	if len(got.Buckets) != 0 {
		t.Errorf("Buckets = %v, want none", got.Buckets)
	}
}

func TestSubnetworkPrefersDeploymentRegion(t *testing.T) {
	d := Discovered{Subnetworks: map[string]string{
		"eu-subnet": "europe-west4",
		"us-subnet": "us-central1",
	}}
	got, ambiguous := d.Subnetwork("us-central1")
	if got != "us-subnet" {
		t.Errorf("Subnetwork() = %q, want %q", got, "us-subnet")
	}
	if ambiguous {
		t.Error("Subnetwork() reported ambiguity, want none: only one subnetwork is in region")
	}
}

func TestSubnetworkReportsAmbiguityAndStaysDeterministic(t *testing.T) {
	d := Discovered{Subnetworks: map[string]string{
		"b-subnet": "us-central1",
		"a-subnet": "us-central1",
	}}
	// Map iteration order is random; the choice must not be.
	for i := 0; i < 20; i++ {
		got, ambiguous := d.Subnetwork("us-central1")
		if got != "a-subnet" {
			t.Fatalf("Subnetwork() = %q, want %q on every call", got, "a-subnet")
		}
		if !ambiguous {
			t.Fatal("Subnetwork() did not report ambiguity, want it reported")
		}
	}
}

func TestSubnetworkFallsBackOutsideRegion(t *testing.T) {
	d := Discovered{Subnetworks: map[string]string{"eu-subnet": "europe-west4"}}
	got, _ := d.Subnetwork("us-central1")
	if got != "eu-subnet" {
		t.Errorf("Subnetwork() = %q, want the only subnetwork available", got)
	}
}

func TestSubnetworkEmpty(t *testing.T) {
	d := Discovered{Subnetworks: map[string]string{}}
	if got, ambiguous := d.Subnetwork("us-central1"); got != "" || ambiguous {
		t.Errorf("Subnetwork() = (%q, %t), want (\"\", false)", got, ambiguous)
	}
}

func TestNetwork(t *testing.T) {
	tests := []struct {
		name          string
		networks      []string
		want          string
		wantAmbiguous bool
	}{
		{"none", nil, "", false},
		{"one", []string{"net"}, "net", false},
		{"several", []string{"a", "b"}, "a", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := Discovered{Networks: tc.networks}
			got, ambiguous := d.Network()
			if got != tc.want || ambiguous != tc.wantAmbiguous {
				t.Errorf("Network() = (%q, %t), want (%q, %t)", got, ambiguous, tc.want, tc.wantAmbiguous)
			}
		})
	}
}

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
	"sort"

	tfjson "github.com/hashicorp/terraform-json"
)

// Terraform resource types that map onto Cluster Director resources. Matching
// is exact: "google_storage_bucket_iam_binding" must not be mistaken for
// "google_storage_bucket".
const (
	tfNetwork    = "google_compute_network"
	tfSubnetwork = "google_compute_subnetwork"
	tfBucket     = "google_storage_bucket"
	tfFilestore  = "google_filestore_instance"
	tfLustre     = "google_lustre_instance"
	tfMIG        = "google_compute_instance_group_manager"
	tfRegionMIG  = "google_compute_region_instance_group_manager"
	// A GKE node pool is backed by managed instance groups, but the resource
	// is not a MIG resource: GKE creates those itself and exports their URLs.
	tfNodePool = "google_container_node_pool"
	// Reservations are not created by a deployment, they are consumed by it,
	// so they appear as an affinity block on instances rather than as a
	// resource of their own.
	tfInstance         = "google_compute_instance"
	tfInstanceTemplate = "google_compute_instance_template"
)

// Discovered holds the registerable resources found in a deployment's
// Terraform state.
type Discovered struct {
	// Networks are VPC names. Cluster Director accepts exactly one, so more
	// than one entry means the caller has to choose.
	Networks []string
	// Subnetworks maps a subnetwork name to the region it lives in, so that
	// the subnetwork matching the deployment region can be preferred.
	Subnetworks map[string]string

	Buckets    []string
	Filestores []string
	Lustres    []string
	MIGs       []string
	// Reservations are the names of reservations the deployment's instances
	// are pinned to.
	Reservations []string
}

// Discover walks Terraform states and collects the resources that can be
// registered into Cluster Director.
//
// Managed resources are always considered. Data sources are considered only
// for networks and subnetworks, because reading a VPC that already exists is a
// first-class Cluster Toolkit pattern (modules/network/pre-existing-vpc) and a
// cluster without its network would be of little use. Data sources are ignored
// for storage and compute, where a read is far more likely to be incidental to
// a script than a statement that the resource belongs to the cluster.
func Discover(states []*tfjson.State) Discovered {
	d := Discovered{Subnetworks: map[string]string{}}
	for _, st := range states {
		if st == nil || st.Values == nil {
			continue
		}
		d.walk(st.Values.RootModule)
	}

	sort.Strings(d.Networks)
	sort.Strings(d.Buckets)
	sort.Strings(d.Filestores)
	sort.Strings(d.Lustres)
	sort.Strings(d.MIGs)
	sort.Strings(d.Reservations)
	return d
}

// walk recurses through a state module and its children. Terraform nests every
// module call, and Cluster Toolkit modules are themselves nested, so the
// resources of interest are never in the root module.
func (d *Discovered) walk(m *tfjson.StateModule) {
	if m == nil {
		return
	}
	for _, r := range m.Resources {
		d.collect(r)
	}
	for _, c := range m.ChildModules {
		d.walk(c)
	}
}

func (d *Discovered) collect(r *tfjson.StateResource) {
	if r == nil {
		return
	}
	isData := r.Mode == tfjson.DataResourceMode

	switch r.Type {
	case tfNetwork:
		d.Networks = appendUnique(d.Networks, attrString(r, "name"))
	case tfSubnetwork:
		if name := attrString(r, "name"); name != "" {
			if _, seen := d.Subnetworks[name]; !seen {
				d.Subnetworks[name] = attrString(r, "region")
			}
		}
	case tfBucket:
		if !isData {
			d.Buckets = appendUnique(d.Buckets, attrString(r, "name"))
		}
	case tfFilestore:
		if !isData {
			d.Filestores = appendUnique(d.Filestores, attrString(r, "id"))
		}
	case tfLustre:
		if !isData {
			d.Lustres = appendUnique(d.Lustres, attrString(r, "id"))
		}
	case tfMIG, tfRegionMIG:
		if !isData {
			d.MIGs = appendUnique(d.MIGs, attrString(r, "self_link"))
		}
	case tfNodePool:
		if !isData {
			// GKE reports the groups it created; either attribute may be
			// populated depending on provider version.
			for _, u := range attrStringSlice(r, "managed_instance_group_urls") {
				d.MIGs = appendUnique(d.MIGs, u)
			}
			for _, u := range attrStringSlice(r, "instance_group_urls") {
				d.MIGs = appendUnique(d.MIGs, u)
			}
		}
	case tfInstance, tfInstanceTemplate:
		if !isData {
			for _, name := range reservationNames(r) {
				d.Reservations = appendUnique(d.Reservations, name)
			}
		}
	}
}

// reservationNames extracts the reservations an instance is pinned to, from
// the nested reservation_affinity block:
//
//	reservation_affinity { type = "SPECIFIC_RESERVATION"
//	  specific_reservation { key = "...reservation-name" values = ["my-res"] } }
//
// Only SPECIFIC_RESERVATION carries a name; ANY_RESERVATION and
// NO_RESERVATION describe a policy rather than a resource to register.
func reservationNames(r *tfjson.StateResource) []string {
	var out []string
	for _, aff := range nestedBlocks(r.AttributeValues, "reservation_affinity") {
		if t, _ := aff["type"].(string); t != "SPECIFIC_RESERVATION" {
			continue
		}
		for _, spec := range nestedBlocks(aff, "specific_reservation") {
			vals, ok := spec["values"].([]any)
			if !ok {
				continue
			}
			for _, v := range vals {
				if s, ok := v.(string); ok && s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// nestedBlocks reads a repeated Terraform block, which the state encodes as a
// list of objects. A single (non-repeated) block is tolerated too.
func nestedBlocks(m map[string]any, key string) []map[string]any {
	switch v := m[key].(type) {
	case []any:
		var out []map[string]any
		for _, e := range v {
			if o, ok := e.(map[string]any); ok {
				out = append(out, o)
			}
		}
		return out
	case map[string]any:
		return []map[string]any{v}
	default:
		return nil
	}
}

// attrStringSlice reads a list-of-strings attribute, tolerating absence and
// non-string entries.
func attrStringSlice(r *tfjson.StateResource, key string) []string {
	raw, ok := r.AttributeValues[key].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Subnetwork picks the subnetwork to register. A deployment may define several
// (the VPC module supports additional subnetworks), but the API accepts one,
// so the one in the deployment region wins. The result is deterministic:
// candidates are sorted before choosing.
//
// The second return value reports whether the choice was ambiguous, which the
// caller should surface rather than silently resolve.
func (d Discovered) Subnetwork(region string) (string, bool) {
	var inRegion, others []string
	for name, r := range d.Subnetworks {
		if r == region {
			inRegion = append(inRegion, name)
		} else {
			others = append(others, name)
		}
	}
	sort.Strings(inRegion)
	sort.Strings(others)

	switch {
	case len(inRegion) > 0:
		return inRegion[0], len(inRegion) > 1
	case len(others) > 0:
		return others[0], len(others) > 1
	default:
		return "", false
	}
}

// Network picks the VPC to register, reporting whether the choice was
// ambiguous.
func (d Discovered) Network() (string, bool) {
	if len(d.Networks) == 0 {
		return "", false
	}
	return d.Networks[0], len(d.Networks) > 1
}

// attrString reads a string attribute, tolerating resources that do not carry
// it. A state written by an older provider may omit an attribute entirely.
func attrString(r *tfjson.StateResource, key string) string {
	v, ok := r.AttributeValues[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func appendUnique(xs []string, v string) []string {
	if v == "" {
		return xs
	}
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

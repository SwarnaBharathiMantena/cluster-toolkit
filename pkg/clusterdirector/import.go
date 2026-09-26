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
	"fmt"
	"hash/crc32"
	"regexp"
	"strings"
)

const (
	// DeploymentLabel is the label that the Cluster Toolkit stamps on every
	// resource it creates. It is used to select the instances belonging to a
	// deployment when importing them into Cluster Director.
	DeploymentLabel = "ghpc_deployment"

	// NetworkResourceKey is the key used for the VPC imported from the
	// Cluster Toolkit deployment.
	NetworkResourceKey = "ctk-vpc-mapping"

	// NodesResourceKey is the key used for the label selected Compute Engine
	// instances imported from the Cluster Toolkit deployment.
	NodesResourceKey = "ctk-nodes"
)

// maxKeyLength is the RFC-1034 limit applied to resource map keys.
const maxKeyLength = 63

// maxClusterIDLength is the Cluster Director API limit on cluster IDs
// ("Start with a lowercase letter. Use only lowercase letters and numbers.
// Limit to 10 characters.").
const maxClusterIDLength = 10

var (
	// clusterIDPattern matches valid Cluster Director cluster IDs: 1-10
	// lower-case alphanumeric characters starting with a letter (no hyphens).
	clusterIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,9}$`)
	// invalidClusterIDChars matches every character not allowed in a cluster ID.
	invalidClusterIDChars = regexp.MustCompile(`[^a-z0-9]+`)
	// rfc1034 matches lower-case alphanumeric strings separated by hyphens,
	// starting with a letter and ending with an alphanumeric character.
	rfc1034 = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)
	// invalidKeyChars matches every character that is not allowed in a key.
	invalidKeyChars = regexp.MustCompile(`[^a-z0-9-]+`)
	// computeAPIPrefix matches the various self link prefixes returned by the
	// Compute Engine API, e.g.
	// https://www.googleapis.com/compute/v1/projects/... .
	computeAPIPrefix = regexp.MustCompile(`^(https?://[^/]+)?/?(compute/(v1|beta|alpha)/)?`)
	// serviceAPIPrefix matches the self link prefix of the regional service
	// APIs (Filestore, Managed Lustre), e.g.
	// https://file.googleapis.com/v1/projects/... .
	serviceAPIPrefix = regexp.MustCompile(`^(https?://[^/]+)?/?((v1|v1beta1|v1alpha1)/)?`)
)

// Spec captures the outputs of a Cluster Toolkit deployment that should be
// imported as a single cluster in Cluster Director.
type Spec struct {
	// ProjectID hosting the deployment, e.g. "my-project".
	ProjectID string
	// Region of the Cluster Director cluster, e.g. "us-central1". It is also
	// used to qualify the subnetwork.
	Region string
	// Zone of the deployment, e.g. "us-central1-a". Only required to qualify
	// reservation references that do not already include a zone.
	Zone string
	// ClusterName is the Cluster Director cluster ID to create.
	ClusterName string
	// DeploymentName is the Cluster Toolkit deployment name. Instances are
	// selected by the `ghpc_deployment` label carrying this value.
	DeploymentName string

	// NetworkName is the VPC name, typically $(network.network_name).
	NetworkName string
	// SubnetName is the subnetwork name, typically
	// $(network.subnetwork_name).
	SubnetName string

	// Buckets are Cloud Storage bucket names, typically
	// $(bucket.gcs_bucket_name). A "gs://" prefix is tolerated.
	Buckets []string
	// Filestores are Filestore instance IDs, typically
	// $(filestore.filestore_id).
	Filestores []string
	// Lustres are Managed Lustre instance IDs, typically
	// $(lustre.lustre_id).
	Lustres []string

	// MIGs are managed instance group self links or partial URLs, typically
	// $(mig.self_link). Both zonal and regional MIGs are supported.
	MIGs []string
	// Reservations are reservation, reservation block, or reservation
	// sub-block names or resource paths. Unqualified references are qualified
	// with ProjectID and Zone.
	Reservations []string

	// ClusterLabels are labels applied to the Cluster Director cluster.
	ClusterLabels map[string]string
	// InstanceLabels overrides the label selector used to adopt instances. If
	// empty, `ghpc_deployment: <DeploymentName>` is used.
	InstanceLabels map[string]string
}

// Validate reports whether the spec contains everything needed to build an
// import payload.
func (s Spec) Validate() error {
	var missing []string
	if s.ProjectID == "" {
		missing = append(missing, "project")
	}
	if s.Region == "" {
		missing = append(missing, "region")
	}
	if s.ClusterName == "" {
		missing = append(missing, "cluster name")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required value(s): %s", strings.Join(missing, ", "))
	}
	if !clusterIDPattern.MatchString(s.ClusterName) {
		return fmt.Errorf("cluster name %q must start with a lowercase letter and contain only lowercase letters and digits (at most %d characters, no hyphens)", s.ClusterName, maxClusterIDLength)
	}
	if (s.NetworkName == "") != (s.SubnetName == "") {
		return fmt.Errorf("network and subnetwork must be provided together, got network=%q subnetwork=%q", s.NetworkName, s.SubnetName)
	}
	if len(s.instanceLabels()) == 0 && len(s.MIGs) == 0 && len(s.Reservations) == 0 {
		return fmt.Errorf("no compute to import: provide a deployment name, at least one MIG or at least one reservation")
	}
	return s.validateReservations()
}

// validateReservations reports whether every reservation reference can be
// qualified into a full reservation, reservation block, or reservation
// sub-block resource path.
func (s Spec) validateReservations() error {
	for _, r := range s.Reservations {
		if _, err := parseReservation(s.ProjectID, s.Zone, r); err != nil {
			return err
		}
	}
	return nil
}

// instanceLabels returns the label selector used to adopt instances.
func (s Spec) instanceLabels() map[string]string {
	if len(s.InstanceLabels) > 0 {
		return s.InstanceLabels
	}
	if s.DeploymentName == "" {
		return nil
	}
	return map[string]string{DeploymentLabel: s.DeploymentName}
}

// Cluster renders the spec as a Cluster Director cluster payload.
func (s Spec) Cluster() (*Cluster, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}

	c := &Cluster{
		Name:             s.ClusterName,
		Labels:           s.ClusterLabels,
		NetworkResources: s.networkResources(),
	}

	storage, err := s.storageResources()
	if err != nil {
		return nil, err
	}
	if len(storage) > 0 {
		c.StorageResources = storage
	}

	instances, err := s.existingInstances()
	if err != nil {
		return nil, err
	}
	if len(instances) > 0 {
		c.Orchestrator = &Orchestrator{ComputeEngine: &ComputeEngineOrchestrator{ExistingInstances: instances}}
	}

	return c, nil
}

// networkResources renders the VPC of the deployment, if any.
func (s Spec) networkResources() map[string]NetworkResource {
	if s.NetworkName == "" {
		return nil
	}
	return map[string]NetworkResource{
		NetworkResourceKey: {Config: NetworkResourceConfig{
			ExistingNetwork: &ExistingNetworkConfig{
				Network:    fmt.Sprintf("projects/%s/global/networks/%s", s.ProjectID, qualifiedLeaf(s.NetworkName)),
				Subnetwork: fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s", s.ProjectID, s.Region, qualifiedLeaf(s.SubnetName)),
			},
		}},
	}
}

// storageResources renders the buckets, Filestore and Managed Lustre
// instances of the deployment.
func (s Spec) storageResources() (map[string]StorageResource, error) {
	storage := map[string]StorageResource{}

	for i, b := range s.Buckets {
		name, err := normalizeBucket(b)
		if err != nil {
			return nil, err
		}
		storage[indexedKey("ctk-bucket", i)] = StorageResource{
			Config: StorageResourceConfig{ExistingBucket: &ExistingBucketConfig{Bucket: name}},
		}
	}
	for i, f := range s.Filestores {
		id, err := normalizeInstanceID("filestore", f)
		if err != nil {
			return nil, err
		}
		storage[indexedKey("ctk-filestore", i)] = StorageResource{
			Config: StorageResourceConfig{ExistingFilestore: &ExistingFilestoreConfig{Filestore: id}},
		}
	}
	for i, l := range s.Lustres {
		id, err := normalizeInstanceID("lustre", l)
		if err != nil {
			return nil, err
		}
		storage[indexedKey("ctk-lustre", i)] = StorageResource{
			Config: StorageResourceConfig{ExistingLustre: &ExistingLustreConfig{Lustre: id}},
		}
	}
	return storage, nil
}

// existingInstances renders the compute of the deployment: label selected
// instances, managed instance groups and reservations (including reservation
// blocks and sub-blocks).
func (s Spec) existingInstances() (map[string]ExistingInstances, error) {
	instances := map[string]ExistingInstances{}

	if labels := s.instanceLabels(); len(labels) > 0 {
		instances[NodesResourceKey] = ExistingInstances{
			Project: fmt.Sprintf("projects/%s", s.ProjectID),
			Labels:  labels,
		}
	}
	for i, m := range s.MIGs {
		mig, regional, err := normalizeMIG(m)
		if err != nil {
			return nil, err
		}
		key := indexedKey("ctk-mig", i)
		if regional {
			instances[key] = ExistingInstances{RegionInstanceGroupManager: mig}
		} else {
			instances[key] = ExistingInstances{InstanceGroupManager: mig}
		}
	}
	for i, r := range s.Reservations {
		entry, err := parseReservation(s.ProjectID, s.Zone, r)
		if err != nil {
			return nil, err
		}
		instances[indexedKey("ctk-reservation", i)] = entry
	}
	return instances, nil
}

// indexedKey builds an RFC-1034 compliant map key such as "ctk-bucket-0".
func indexedKey(prefix string, i int) string {
	return SanitizeKey(fmt.Sprintf("%s-%d", prefix, i))
}

// SanitizeKey coerces s into an RFC-1034 compliant resource map key
// (lower-case alphanumerics and hyphens, starting with a letter, at most 63
// characters).
func SanitizeKey(s string) string {
	s = invalidKeyChars.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "ctk"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "ctk-" + s
	}
	if len(s) > maxKeyLength {
		s = strings.TrimRight(s[:maxKeyLength], "-")
	}
	return s
}

// SanitizeClusterID coerces s into a valid Cluster Director cluster ID: 1-10
// lower-case alphanumeric characters starting with a letter (no hyphens).
// If stripping non-alphanumerics yields at most 10 characters, that string is
// used directly (e.g. "ctk-demo" -> "ctkdemo"). Longer names retain their
// 6-character prefix and append a deterministic 4-character hex checksum so
// distinct deployment names do not collide.
func SanitizeClusterID(s string) string {
	clean := invalidClusterIDChars.ReplaceAllString(strings.ToLower(s), "")
	if clean == "" {
		return "ctk"
	}
	if clean[0] >= '0' && clean[0] <= '9' {
		clean = "c" + clean
	}
	if len(clean) <= maxClusterIDLength {
		return clean
	}
	return fmt.Sprintf("%s%04x", clean[:6], crc32.ChecksumIEEE([]byte(s))&0xffff)
}

// qualifiedLeaf returns the last path segment of a resource name or self
// link, so that both bare names and fully qualified references are accepted.
func qualifiedLeaf(s string) string {
	s = strings.TrimSuffix(strings.TrimSpace(s), "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// normalizeBucket strips the optional gs:// scheme from a bucket name.
func normalizeBucket(b string) (string, error) {
	b = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(b), "gs://"), "/")
	if b == "" {
		return "", fmt.Errorf("empty bucket name")
	}
	if strings.Contains(b, "/") {
		return "", fmt.Errorf("bucket %q must be a bare bucket name, optionally prefixed with gs://", b)
	}
	return b, nil
}

// normalizeInstanceID converts a Filestore or Managed Lustre reference into
// the `projects/{project}/locations/{location}/instances/{instance}` form
// expected by the API. Self links are accepted and reduced to that form.
func normalizeInstanceID(kind, id string) (string, error) {
	id = strings.Trim(strings.TrimSpace(id), "/")
	if id == "" {
		return "", fmt.Errorf("empty %s instance ID", kind)
	}
	id = serviceAPIPrefix.ReplaceAllString(id, "")
	parts := strings.Split(id, "/")
	if len(parts) != 6 || parts[0] != "projects" || parts[2] != "locations" || parts[4] != "instances" {
		return "", fmt.Errorf("%s %q must be of the form projects/{project}/locations/{location}/instances/{instance}", kind, id)
	}
	return id, nil
}

// normalizeMIG converts a managed instance group self link or partial URL
// into a `projects/...` reference and reports whether it is regional.
func normalizeMIG(mig string) (string, bool, error) {
	mig = strings.Trim(strings.TrimSpace(mig), "/")
	if mig == "" {
		return "", false, fmt.Errorf("empty managed instance group")
	}
	mig = computeAPIPrefix.ReplaceAllString(mig, "")
	parts := strings.Split(mig, "/")
	if len(parts) != 6 || parts[0] != "projects" || parts[4] != "instanceGroupManagers" {
		return "", false, fmt.Errorf("managed instance group %q must be of the form projects/{project}/[zones|regions]/{location}/instanceGroupManagers/{name}", mig)
	}
	switch parts[2] {
	case "zones":
		return mig, false, nil
	case "regions":
		return mig, true, nil
	default:
		return "", false, fmt.Errorf("managed instance group %q must be scoped by `zones` or `regions`, got %q", mig, parts[2])
	}
}

// parseReservation qualifies a reservation, reservation block, or reservation
// sub-block reference and returns the corresponding ExistingInstances entry.
//
// Supported input forms:
//   - bare or relative: "{res}[/reservationBlocks/{block}[/reservationSubBlocks/{sub_block}]]"
//   - GCE affinity path: "projects/{project}/reservations/{res}[/reservationBlocks/{block}[/reservationSubBlocks/{sub_block}]]"
//   - full resource path or self link: "projects/{project}/zones/{zone}/reservations/{res}[/reservationBlocks/{block}[/reservationSubBlocks/{sub_block}]]"
func parseReservation(project, zone, reservation string) (ExistingInstances, error) {
	raw := strings.Trim(strings.TrimSpace(reservation), "/")
	if raw == "" {
		return ExistingInstances{}, fmt.Errorf("empty reservation")
	}
	cleaned := computeAPIPrefix.ReplaceAllString(raw, "")
	parts := strings.Split(cleaned, "/")

	switch {
	case parts[0] != "projects":
		if zone == "" {
			return ExistingInstances{}, fmt.Errorf("reservation %q is not zone-qualified, a zone is required to qualify it", reservation)
		}
		if parts[0] == "reservations" {
			parts = append([]string{"projects", project, "zones", zone}, parts...)
		} else {
			parts = append([]string{"projects", project, "zones", zone, "reservations"}, parts...)
		}
	case len(parts) >= 4 && parts[0] == "projects" && parts[2] == "reservations":
		// Shared-project reservation affinity values omit "/zones/{zone}".
		if zone == "" {
			return ExistingInstances{}, fmt.Errorf("reservation %q is not zone-qualified, a zone is required to qualify it", reservation)
		}
		qualified := make([]string, 0, len(parts)+2)
		qualified = append(qualified, "projects", parts[1], "zones", zone)
		qualified = append(qualified, parts[2:]...)
		parts = qualified
	}

	validBase := len(parts) >= 6 &&
		parts[0] == "projects" && parts[1] != "" &&
		parts[2] == "zones" && parts[3] != "" &&
		parts[4] == "reservations" && parts[5] != ""

	if validBase {
		path := strings.Join(parts, "/")
		switch {
		case len(parts) == 6:
			return ExistingInstances{Reservation: path}, nil
		case len(parts) == 8 && parts[6] == "reservationBlocks" && parts[7] != "":
			return ExistingInstances{ReservationBlock: path}, nil
		case len(parts) == 10 && parts[6] == "reservationBlocks" && parts[7] != "" && parts[8] == "reservationSubBlocks" && parts[9] != "":
			return ExistingInstances{ReservationSubBlock: path}, nil
		}
	}

	return ExistingInstances{}, fmt.Errorf("reservation %q must be a bare name or of the form projects/{project}/zones/{zone}/reservations/{reservation}[/reservationBlocks/{block}[/reservationSubBlocks/{sub_block}]]", reservation)
}

// SplitList splits a comma (or whitespace) separated list into its non-empty
// elements. It is used to accept the comma separated lists that Terraform
// renders for multi-valued Cluster Toolkit outputs.
func SplitList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	// ProdEndpoint is the production Cluster Director (Hypercompute Cluster)
	// API endpoint.
	ProdEndpoint = "hypercomputecluster.googleapis.com"
	// StagingEndpoint is the staging Cluster Director API endpoint.
	StagingEndpoint = "staging-hypercomputecluster.sandbox.googleapis.com"
	// AutopushEndpoint is the autopush Cluster Director API endpoint.
	AutopushEndpoint = "autopush-hypercomputecluster.sandbox.googleapis.com"

	// DefaultEndpoint is the endpoint used when none is specified. The
	// imported clusters path is gated by a per-project allowlist rather than by
	// environment, so prod is the right default.
	DefaultEndpoint = ProdEndpoint

	// DefaultAPIVersion is the API version exposing the imported clusters
	// (import existing resources) path.
	DefaultAPIVersion = "v1alpha"

	cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"
	defaultTimeout     = 60 * time.Second
)

// endpointAliases maps short environment names to their endpoint host.
var endpointAliases = map[string]string{
	"prod":     ProdEndpoint,
	"staging":  StagingEndpoint,
	"autopush": AutopushEndpoint,
}

// ResolveEndpoint expands an environment alias ("prod", "staging" or
// "autopush") into its endpoint host. Any other value, such as an explicit
// host or URL, is returned unchanged.
func ResolveEndpoint(endpoint string) string {
	if host, ok := endpointAliases[strings.ToLower(strings.TrimSpace(endpoint))]; ok {
		return host
	}
	return endpoint
}

// Client talks to the Cluster Director REST API.
type Client struct {
	httpClient *http.Client
	endpoint   string
	apiVersion string
}

// Option customizes a Client.
type Option func(*Client)

// WithEndpoint overrides the API endpoint. It accepts an environment alias
// ("prod", "staging" or "autopush"), a host, or a full URL.
func WithEndpoint(endpoint string) Option {
	return func(c *Client) {
		if endpoint != "" {
			c.endpoint = ResolveEndpoint(endpoint)
		}
	}
}

// WithAPIVersion overrides the API version, e.g. "v1alpha".
func WithAPIVersion(version string) Option {
	return func(c *Client) {
		if version != "" {
			c.apiVersion = version
		}
	}
}

// WithHTTPClient injects an HTTP client. The caller is responsible for
// authentication; this is primarily useful in tests.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// NewClient returns a Client authenticated with Application Default
// Credentials, i.e. the credentials produced by
// `gcloud auth application-default login`.
func NewClient(ctx context.Context, opts ...Option) (*Client, error) {
	c := &Client{endpoint: DefaultEndpoint, apiVersion: DefaultAPIVersion}
	for _, opt := range opts {
		opt(c)
	}
	if c.httpClient == nil {
		ts, err := google.DefaultTokenSource(ctx, cloudPlatformScope)
		if err != nil {
			return nil, fmt.Errorf("could not find Application Default Credentials, run `gcloud auth application-default login`: %w", err)
		}
		c.httpClient = oauth2.NewClient(ctx, ts)
		c.httpClient.Timeout = defaultTimeout
	}
	return c, nil
}

// baseURL returns the collection URL for clusters in a location.
func (c *Client) baseURL(project, location string) string {
	return fmt.Sprintf("%s/%s/projects/%s/locations/%s/clusters",
		normalizeEndpoint(c.endpoint), c.apiVersion, url.PathEscape(project), url.PathEscape(location))
}

// normalizeEndpoint turns a bare host into an https URL.
func normalizeEndpoint(endpoint string) string {
	endpoint = strings.TrimSuffix(endpoint, "/")
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	return "https://" + endpoint
}

// CreateCluster imports cluster under clusterID. It returns the long
// running operation started by the API.
func (c *Client) CreateCluster(ctx context.Context, project, location, clusterID string, cluster *Cluster) (*Operation, error) {
	body, err := json.Marshal(cluster)
	if err != nil {
		return nil, fmt.Errorf("could not encode cluster payload: %w", err)
	}
	u := fmt.Sprintf("%s?clusterId=%s", c.baseURL(project, location), url.QueryEscape(clusterID))

	op := &Operation{}
	if err := c.do(ctx, http.MethodPost, u, body, op); err != nil {
		return nil, err
	}
	return op, nil
}

// GetCluster reads an imported cluster.
func (c *Client) GetCluster(ctx context.Context, project, location, clusterID string) (*Cluster, error) {
	u := fmt.Sprintf("%s/%s", c.baseURL(project, location), url.PathEscape(clusterID))

	cluster := &Cluster{}
	if err := c.do(ctx, http.MethodGet, u, nil, cluster); err != nil {
		return nil, err
	}
	return cluster, nil
}

// UpdateResourceMask is the update mask covering every field this package
// writes: the imported network, storage and compute sets. Wildcards are not
// supported by the API, so the paths are listed explicitly.
const UpdateResourceMask = "network_resources,storage_resources,orchestrator"

// UpdateCluster amends an imported cluster in place. mask selects which
// fields of cluster to write; an empty mask defaults to UpdateResourceMask.
//
// This is what keeps an imported cluster current when a deployment grows or
// shrinks: the cluster ID is stable, so the resource set is patched rather than
// the cluster being deleted and recreated.
func (c *Client) UpdateCluster(ctx context.Context, project, location, clusterID string, cluster *Cluster, mask string) (*Operation, error) {
	if mask == "" {
		mask = UpdateResourceMask
	}
	body, err := json.Marshal(cluster)
	if err != nil {
		return nil, fmt.Errorf("could not encode cluster payload: %w", err)
	}
	u := fmt.Sprintf("%s/%s?updateMask=%s",
		c.baseURL(project, location), url.PathEscape(clusterID), url.QueryEscape(mask))

	op := &Operation{}
	if err := c.do(ctx, http.MethodPatch, u, body, op); err != nil {
		return nil, err
	}
	return op, nil
}

// DeleteCluster removes an imported cluster from Cluster Director. Deleting an
// imported cluster removes only the Cluster Director record; it does not
// destroy the underlying GCP resources.
func (c *Client) DeleteCluster(ctx context.Context, project, location, clusterID string) (*Operation, error) {
	u := fmt.Sprintf("%s/%s", c.baseURL(project, location), url.PathEscape(clusterID))

	op := &Operation{}
	if err := c.do(ctx, http.MethodDelete, u, nil, op); err != nil {
		return nil, err
	}
	return op, nil
}

// ListNodes returns all compute nodes associated with a cluster in Cluster
// Director, following pagination tokens until all pages have been collected.
func (c *Client) ListNodes(ctx context.Context, project, location, clusterID string) ([]Node, error) {
	base := fmt.Sprintf("%s/%s/nodes", c.baseURL(project, location), url.PathEscape(clusterID))

	nodes := []Node{}
	pageToken := ""
	for {
		u := base
		if pageToken != "" {
			u = fmt.Sprintf("%s?pageToken=%s", base, url.QueryEscape(pageToken))
		}
		var page ListNodesResponse
		if err := c.do(ctx, http.MethodGet, u, nil, &page); err != nil {
			return nil, err
		}
		nodes = append(nodes, page.Nodes...)
		if page.NextPageToken == "" {
			return nodes, nil
		}
		pageToken = page.NextPageToken
	}
}

// GetOperation polls a long running operation by its resource name.
func (c *Client) GetOperation(ctx context.Context, name string) (*Operation, error) {
	u := fmt.Sprintf("%s/%s/%s", normalizeEndpoint(c.endpoint), c.apiVersion, strings.TrimPrefix(name, "/"))

	op := &Operation{}
	if err := c.do(ctx, http.MethodGet, u, nil, op); err != nil {
		return nil, err
	}
	return op, nil
}

// WaitForOperation polls op until it completes, the context is cancelled or
// timeout elapses. A nil or already completed operation is returned as is.
func (c *Client) WaitForOperation(ctx context.Context, op *Operation, pollInterval, timeout time.Duration) (*Operation, error) {
	if op == nil || op.Done || op.Name == "" {
		return op, operationError(op)
	}
	if pollInterval <= 0 {
		pollInterval = 10 * time.Second
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return op, fmt.Errorf("timed out waiting for operation %s: %w", op.Name, ctx.Err())
		case <-ticker.C:
			polled, err := c.GetOperation(ctx, op.Name)
			if err != nil {
				if ctx.Err() != nil {
					return op, fmt.Errorf("timed out waiting for operation %s: %w", op.Name, ctx.Err())
				}
				return op, err
			}
			op = polled
			if op.Done {
				return op, operationError(op)
			}
		}
	}
}

// operationError converts the error carried by a completed operation into a
// Go error.
func operationError(op *Operation) error {
	if op == nil || op.Error == nil {
		return nil
	}
	return fmt.Errorf("operation %s failed: %s (code %d)", op.Name, op.Error.Message, op.Error.Code)
}

// do executes an authenticated request and decodes a successful response into
// out. Non 2xx responses are returned as an *APIError.
func (c *Client) do(ctx context.Context, method, u string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return fmt.Errorf("could not build %s request for %s: %w", method, u, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s failed: %w", method, u, err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("could not read response from %s %s: %w", method, u, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return newAPIError(method, u, resp.StatusCode, payload)
	}
	if out == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("could not decode response from %s %s: %w", method, u, err)
	}
	return nil
}

// APIError is returned for non 2xx responses from the Cluster Director API.
type APIError struct {
	Method     string
	URL        string
	StatusCode int
	Status     *Status
	Body       string
}

// Error implements the error interface.
func (e *APIError) Error() string {
	if e.Status != nil && e.Status.Message != "" {
		return fmt.Sprintf("%s %s: %d %s: %s", e.Method, e.URL, e.StatusCode, e.Status.Status, e.Status.Message)
	}
	return fmt.Sprintf("%s %s: %d: %s", e.Method, e.URL, e.StatusCode, e.Body)
}

// newAPIError parses an error response body into an *APIError.
func newAPIError(method, u string, code int, payload []byte) *APIError {
	e := &APIError{Method: method, URL: u, StatusCode: code, Body: strings.TrimSpace(string(payload))}
	var wrapper struct {
		Error *Status `json:"error"`
	}
	if err := json.Unmarshal(payload, &wrapper); err == nil {
		e.Status = wrapper.Error
	}
	return e
}

// IsAlreadyExists reports whether err indicates that the cluster is already
// imported, which makes importing idempotent.
func IsAlreadyExists(err error) bool {
	return hasStatus(err, http.StatusConflict, "ALREADY_EXISTS")
}

// IsNotFound reports whether err indicates that the cluster is not (or no
// longer) imported, which makes deletion idempotent.
func IsNotFound(err error) bool {
	return hasStatus(err, http.StatusNotFound, "NOT_FOUND")
}

// hasStatus reports whether err is an *APIError with the given HTTP status
// code or canonical status string.
func hasStatus(err error, code int, status string) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode == code {
		return true
	}
	return apiErr.Status != nil && apiErr.Status.Status == status
}

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

// Package clusterdirector implements the `gcluster cluster-director`
// subcommands, which import (and remove) the resources of a Cluster
// Toolkit deployment in Cluster Director.
package clusterdirector

import (
	"os"
	"strings"
	"time"

	cdapi "hpc-toolkit/pkg/clusterdirector"
	"hpc-toolkit/pkg/logging"
	"hpc-toolkit/pkg/shell"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// options holds the flags shared by all `cluster-director` subcommands.
type options struct {
	projectID    string
	region       string
	clusterName  string
	endpoint     string
	apiVersion   string
	wait         bool
	timeout      time.Duration
	pollInterval time.Duration
}

var (
	opts      options
	newClient = cdapi.NewClient
)

// ClusterDirectorCmd is the parent command for Cluster Director operations.
var ClusterDirectorCmd = &cobra.Command{
	Use:     "cluster-director",
	Aliases: []string{"cd"},
	Short:   "[EXPERIMENTAL] Import Cluster Toolkit deployments into Cluster Director.",
	Long: `Import the networks, filesystems and compute of an existing Cluster Toolkit
deployment into Cluster Director, so that the deployment is observable as a
single cluster. Importing adopts pre-existing resources: it never creates
or destroys the underlying infrastructure.

This feature is under active development.`,
	SilenceUsage: true,
}

func init() {
	pf := ClusterDirectorCmd.PersistentFlags()
	pf.StringVarP(&opts.projectID, "project", "p", envOr("PROJECT_ID", ""), "Google Cloud project hosting the deployment. Defaults to $PROJECT_ID, then to the gcloud default project.")
	pf.StringVar(&opts.region, "region", envOr("REGION", ""), "Region of the Cluster Director cluster, e.g. us-central1. Defaults to $REGION.")
	pf.StringVar(&opts.clusterName, "cluster-name", envOr("CLUSTER_NAME", ""), "Cluster Director cluster ID. Defaults to $CLUSTER_NAME.")
	pf.StringVar(&opts.endpoint, "endpoint", envOr("CLUSTER_DIRECTOR_ENDPOINT", cdapi.DefaultEndpoint), "Cluster Director API endpoint: an environment alias (prod, staging, autopush), a host, or a URL. Defaults to $CLUSTER_DIRECTOR_ENDPOINT, then to prod.")
	pf.StringVar(&opts.apiVersion, "api-version", envOr("CLUSTER_DIRECTOR_API_VERSION", cdapi.DefaultAPIVersion), "Cluster Director API version.")
	pf.BoolVar(&opts.wait, "wait", false, "Wait for the long running operation to complete.")
	pf.DurationVar(&opts.timeout, "timeout", 30*time.Minute, "Maximum time to wait for the operation when --wait is set.")
	pf.DurationVar(&opts.pollInterval, "poll-interval", 10*time.Second, "Interval between operation polls when --wait is set.")

	ClusterDirectorCmd.AddCommand(importCmd)
	ClusterDirectorCmd.AddCommand(deleteCmd)
	ClusterDirectorCmd.AddCommand(describeCmd)
}

// clientOptions returns the options used to build the API client.
func (o options) clientOptions() []cdapi.Option {
	return []cdapi.Option{
		cdapi.WithEndpoint(o.endpoint),
		cdapi.WithAPIVersion(o.apiVersion),
	}
}

// resolveProject falls back to the gcloud default project when neither the
// --project flag nor $PROJECT_ID is set.
func resolveProject(projectID string) string {
	if projectID != "" {
		return projectID
	}
	result := shell.ExecuteCommand("gcloud", "config", "get-value", "project")
	ambient := strings.TrimSpace(result.Stdout)
	if result.ExitCode != 0 || ambient == "" || ambient == "(unset)" {
		return ""
	}
	logging.Info("Using ambient project ID: %s", ambient)
	return ambient
}

// envOr returns the value of the environment variable key, or fallback when
// it is unset or empty.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// stringSliceFlag registers a repeatable flag that also accepts a comma
// separated list, seeded from the environment variable env.
func stringSliceFlag(fs *pflag.FlagSet, target *[]string, name, env, usage string) {
	fs.StringSliceVar(target, name, cdapi.SplitList(os.Getenv(env)), usage)
}

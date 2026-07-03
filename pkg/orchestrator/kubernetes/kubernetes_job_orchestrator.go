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

package kubernetes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hpc-toolkit/pkg/logging"
	"hpc-toolkit/pkg/orchestrator"
	"hpc-toolkit/pkg/shell"
	"os"
	"strings"
	"text/template"
)

// KubernetesOrchestrator implements JobOrchestrator for generic Kubernetes clusters.
type KubernetesOrchestrator struct {
	kubeconfigPath string
}

// NewKubernetesOrchestrator creates a new KubernetesOrchestrator instance.
func NewKubernetesOrchestrator(kubeconfigPath string) *KubernetesOrchestrator {
	return &KubernetesOrchestrator{kubeconfigPath: kubeconfigPath}
}

// JobSetTemplateData holds the data needed to render the JobSet template.
type JobSetTemplateData struct {
	WorkloadName                  string
	KueueQueueName                string
	NumSlices                     int
	NodesPerSlice                 int
	MaxRestarts                   int
	TtlSecondsAfterFinished       int
	TerminationGracePeriodSeconds int
	ServiceAccountName            string
	PriorityClassName             string
	FullImageName                 string
	Command                       []string
	CpuLimit                      string
	MemoryLimit                   string
	GpuLimit                      string
	TpuLimit                      string
}

const jobsetTemplateStr = `apiVersion: jobset.x-k8s.io/v1alpha2
kind: JobSet
metadata:
  name: {{.WorkloadName}}
  labels:
    gcluster.google.com/workload: {{.WorkloadName}}
    {{- if .KueueQueueName }}
    kueue.x-k8s.io/queue-name: {{.KueueQueueName}}
    {{- end }}
spec:
  ttlSecondsAfterFinished: {{.TtlSecondsAfterFinished}}
  failurePolicy:
    maxRestarts: {{.MaxRestarts}}
    rules:
      - action: FailJobSet
        onJobFailureReasons:
          - PodFailurePolicy
  replicatedJobs:
    - name: main-job
      replicas: {{.NumSlices}}
      template:
        spec:
          parallelism: {{.NodesPerSlice}}
          completions: {{.NodesPerSlice}}
          backoffLimit: 0
          template:
            metadata:
              labels:
                gcluster.google.com/workload: {{.WorkloadName}}
            spec:
              terminationGracePeriodSeconds: {{.TerminationGracePeriodSeconds}}
              {{- if .PriorityClassName }}
              priorityClassName: {{.PriorityClassName}}
              {{- end }}
              restartPolicy: Never
              containers:
              - name: workload-container
                image: {{ .FullImageName }}
                command:
                {{- range .Command }}
                - {{ printf "%q" . }}
                {{- end }}
                {{- if or .CpuLimit .MemoryLimit .GpuLimit .TpuLimit }}
                resources:
                  limits:
                    {{- if .CpuLimit }}
                    cpu: {{.CpuLimit}}
                    {{- end }}
                    {{- if .MemoryLimit }}
                    memory: {{.MemoryLimit}}
                    {{- end }}
                    {{- if .GpuLimit }}
                    nvidia.com/gpu: {{.GpuLimit}}
                    {{- end }}
                    {{- if .TpuLimit }}
                    google.com/tpu: {{.TpuLimit}}
                    {{- end }}
                {{- end }}
              {{- if .ServiceAccountName }}
              serviceAccountName: {{.ServiceAccountName}}
              {{- end }}
`

// SubmitJob generates the JobSet manifest and applies it using kubectl.
func (k *KubernetesOrchestrator) SubmitJob(job orchestrator.JobDefinition) error {
	logging.Info("Submitting job %q to generic Kubernetes cluster...", job.WorkloadName)

	manifest, err := k.generateManifest(job)
	if err != nil {
		return fmt.Errorf("failed to generate manifest: %w", err)
	}

	if job.DryRunManifest != "" {
		logging.Info("Dry-run enabled. Writing manifest to %q...", job.DryRunManifest)
		return writeToFile(job.DryRunManifest, manifest)
	}

	// Execute kubectl apply -f - --kubeconfig <kubeconfig>
	args := []string{"apply", "-f", "-"}
	if k.kubeconfigPath != "" {
		args = append(args, "--kubeconfig", k.kubeconfigPath)
	}

	cmd := shell.NewCommand("kubectl", args...)
	cmd.SetInput(manifest)
	res := cmd.Execute()

	if res.ExitCode != 0 {
		return fmt.Errorf("kubectl apply failed (exit code %d): %s\nStderr: %s", res.ExitCode, res.Stdout, res.Stderr)
	}

	logging.Info("Successfully submitted job %q. Kubectl output: %s", job.WorkloadName, strings.TrimSpace(res.Stdout))
	return nil
}

// ListJobs retrieves jobsets from the cluster and returns their status.
func (k *KubernetesOrchestrator) ListJobs(opts orchestrator.ListOptions) ([]orchestrator.JobStatus, error) {
	args := []string{"get", "jobset", "-o", "json"}
	if k.kubeconfigPath != "" {
		args = append(args, "--kubeconfig", k.kubeconfigPath)
	}

	res := shell.ExecuteCommand("kubectl", args...)
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("failed to list jobsets: %s (stderr: %s)", res.Stdout, res.Stderr)
	}

	var raw struct {
		Items []struct {
			Metadata struct {
				Name              string `json:"name"`
				CreationTimestamp string `json:"creationTimestamp"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}

	if err := json.Unmarshal([]byte(res.Stdout), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse kubectl JSON output: %w", err)
	}

	var jobs []orchestrator.JobStatus
	for _, item := range raw.Items {
		status := "Running"
		for _, cond := range item.Status.Conditions {
			if cond.Status == "True" {
				status = cond.Type
			}
		}

		jobs = append(jobs, orchestrator.JobStatus{
			Name:         item.Metadata.Name,
			Status:       status,
			CreationTime: item.Metadata.CreationTimestamp,
		})
	}

	return jobs, nil
}

// CancelJob deletes the JobSet from the cluster.
func (k *KubernetesOrchestrator) CancelJob(name string, opts orchestrator.CancelOptions) error {
	logging.Info("Cancelling job %q...", name)
	args := []string{"delete", "jobset", name}
	if k.kubeconfigPath != "" {
		args = append(args, "--kubeconfig", k.kubeconfigPath)
	}

	res := shell.ExecuteCommand("kubectl", args...)
	if res.ExitCode != 0 {
		return fmt.Errorf("failed to delete jobset %q: %s (stderr: %s)", name, res.Stdout, res.Stderr)
	}

	logging.Info("Successfully cancelled job %q: %s", name, strings.TrimSpace(res.Stdout))
	return nil
}

// GetJobLogs retrieves logs for all pods matching the jobset label.
func (k *KubernetesOrchestrator) GetJobLogs(name string, opts orchestrator.LogsOptions) (string, error) {
	args := []string{"logs", "-l", "jobset.x-k8s.io/jobset-name=" + name, "--all-containers=true", "--tail=100", "--max-log-requests=50"}
	if k.kubeconfigPath != "" {
		args = append(args, "--kubeconfig", k.kubeconfigPath)
	}

	res := shell.ExecuteCommand("kubectl", args...)
	if res.ExitCode != 0 {
		return "", fmt.Errorf("failed to retrieve logs for job %q: %s (stderr: %s)", name, res.Stdout, res.Stderr)
	}

	return res.Stdout, nil
}

func (k *KubernetesOrchestrator) generateManifest(job orchestrator.JobDefinition) (string, error) {
	tmpl, err := template.New("jobset").Parse(jobsetTemplateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse embedded jobset template: %w", err)
	}

	// Parse resources from ComputeType
	cpu, mem, gpu, tpu := parseResources(job.ComputeType)

	data := JobSetTemplateData{
		WorkloadName:                  job.WorkloadName,
		KueueQueueName:                job.KueueQueueName,
		NumSlices:                     job.NumSlices,
		NodesPerSlice:                 job.NodesPerSlice,
		MaxRestarts:                   job.MaxRestarts,
		TtlSecondsAfterFinished:       job.TtlSecondsAfterFinished,
		TerminationGracePeriodSeconds: job.TerminationGracePeriodSeconds,
		ServiceAccountName:            job.ServiceAccountName,
		PriorityClassName:             job.PriorityClassName,
		FullImageName:                 job.ImageName,
		Command:                       []string{"/bin/bash", "-c", job.CommandToRun},
		CpuLimit:                      cpu,
		MemoryLimit:                   mem,
		GpuLimit:                      gpu,
		TpuLimit:                      tpu,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute jobset template: %w", err)
	}

	return buf.String(), nil
}

// Simple resource parser for Hackathon POC.
func parseResources(computeType string) (cpu, mem, gpu, tpu string) {
	parts := strings.Split(strings.ToLower(computeType), "-")
	if len(parts) == 0 {
		return
	}

	if strings.HasPrefix(parts[0], "v6e") || strings.HasPrefix(parts[0], "v5") || strings.HasPrefix(parts[0], "v4") || strings.HasPrefix(parts[0], "tpu") {
		if len(parts) > 1 {
			tpu = parts[1]
		} else {
			tpu = "4"
		}
		return
	}

	if strings.Contains(computeType, "nvidia") || strings.Contains(computeType, "gpu") || strings.Contains(computeType, "l4") || strings.Contains(computeType, "h100") || strings.Contains(computeType, "a100") {
		if len(parts) > 1 {
			gpu = parts[len(parts)-1]
		} else {
			gpu = "1"
		}
		return
	}

	if len(parts) > 2 {
		cpu = parts[2]
	} else {
		cpu = "4"
	}
	return
}

func writeToFile(path, content string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}

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

variable "control_plane_ips" {
  description = "List of external IP addresses for the Kubernetes control plane nodes, used by Ansible to connect."
  type        = list(string)
}

variable "control_plane_internal_ips" {
  description = "List of internal IP addresses for the Kubernetes control plane nodes, used for internal cluster binding."
  type        = list(string)
}

variable "worker_node_ips" {
  description = "List of external IP addresses for the Kubernetes worker nodes, used by Ansible to connect."
  type        = list(string)
}

variable "worker_node_internal_ips" {
  description = "List of internal IP addresses for the Kubernetes worker nodes, used for internal cluster binding."
  type        = list(string)
}

variable "ssh_user" {
  description = "SSH username to connect to the nodes."
  type        = string
  default     = "ubuntu"
}

variable "ssh_private_key_path" {
  description = "Path to the private SSH key used to connect to the nodes."
  type        = string
}

variable "kubeconfig_output_path" {
  description = "Path where the generated kubeconfig file should be saved on the deployer machine."
  type        = string
  default     = "kubeconfig"
}

variable "kubespray_version" {
  description = "Git tag or branch of Kubespray to use."
  type        = string
  default     = "release-2.24"
}

variable "k8s_version" {
  description = "Version of Kubernetes to install."
  type        = string
  default     = "v1.28.6"
}

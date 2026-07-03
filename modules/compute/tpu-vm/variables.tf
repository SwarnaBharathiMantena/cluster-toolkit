# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

variable "name_prefix" {
  description = "Prefix for the TPU VM name"
  type        = string
}

variable "project_id" {
  description = "The GCP project ID"
  type        = string
}

variable "zone" {
  description = "The zone to deploy the TPU VM in"
  type        = string
}

variable "network_self_link" {
  description = "The VPC network name or self_link"
  type        = string
}

variable "subnetwork_self_link" {
  description = "The subnetwork name or self_link"
  type        = string
}

variable "accelerator_type" {
  description = "The TPU accelerator type (e.g., v5litepod-16)"
  type        = string
  default     = "v5litepod-16"
}

variable "runtime_version" {
  description = "The TPU runtime version (e.g., v5-lite-base)"
  type        = string
  default     = "v5-lite-base"
}

variable "metadata" {
  description = "Metadata key/value pairs to assign to the TPU VM (e.g., ssh-keys)"
  type        = map(string)
  default     = {}
}

variable "disable_public_ips" {
  description = "If true, the TPU VM will not have external IP addresses"
  type        = bool
  default     = false
}

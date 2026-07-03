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

output "kubeconfig_path" {
  description = "The path to the generated kubeconfig file on the deployer machine."
  value       = var.kubeconfig_output_path
  depends_on  = [null_resource.kubespray_deployment]
}

output "kubernetes_api_endpoint" {
  description = "The endpoint URL for the Kubernetes API server."
  value       = "https://${var.control_plane_ips[0]}:6443"
}

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

resource "local_file" "ansible_inventory" {
  content = templatefile("${path.module}/templates/hosts.ini.tftpl", {
    control_plane_ips          = var.control_plane_ips
    control_plane_internal_ips = var.control_plane_internal_ips
    worker_node_ips            = var.worker_node_ips
    worker_node_internal_ips   = var.worker_node_internal_ips
    ssh_user                   = var.ssh_user
    ssh_private_key_path       = abspath(var.ssh_private_key_path)
  })
  filename = "${path.module}/.kubespray_work/hosts.ini"
}

resource "null_resource" "kubespray_deployment" {
  depends_on = [local_file.ansible_inventory]

  triggers = {
    control_plane_ips = join(",", var.control_plane_ips)
    worker_node_ips   = join(",", var.worker_node_ips)
    kubespray_version = var.kubespray_version
    k8s_version       = var.k8s_version
    script_hash       = filesha256("${path.module}/scripts/deploy_kubespray.sh")
  }

  provisioner "local-exec" {
    command = "/bin/bash ${path.module}/scripts/deploy_kubespray.sh"

    environment = {
      KUBESPRAY_VERSION      = var.kubespray_version
      K8S_VERSION            = var.k8s_version
      INVENTORY_FILE         = abspath(local_file.ansible_inventory.filename)
      SSH_PRIVATE_KEY_PATH   = abspath(var.ssh_private_key_path)
      SSH_USER               = var.ssh_user
      KUBECONFIG_OUTPUT_PATH = abspath(var.kubeconfig_output_path)
      MASTER_IP              = var.control_plane_ips[0]
    }
  }
}

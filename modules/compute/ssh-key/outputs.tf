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

output "private_key_path" {
  description = "The local path where the private SSH key is stored."
  value       = local_sensitive_file.private_key.filename
}

output "public_key_openssh" {
  description = "The public key in OpenSSH format, ready to be injected into VM metadata."
  value       = tls_private_key.ssh.public_key_openssh
}

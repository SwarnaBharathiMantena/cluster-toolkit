/*
 * Copyright 2025 Google LLC
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

resource "google_tags_tag_key" "key" {
  parent       = var.tag_key_parent
  short_name   = var.tag_key_short_name
  description  = var.tag_key_description
  purpose      = var.tag_key_purpose
  purpose_data = var.tag_key_purpose_data
}

resource "google_tags_tag_value" "values" {
  for_each = toset(var.tag_value)

  parent      = google_tags_tag_key.key.id
  short_name  = each.value.short_name
  description = each.value.description
}

resource "google_tags_location_tag_binding" "bindings" {
  for_each = toset(var.tag_binding)

  parent    = each.value.parent
  tag_value = google_tags_tag_value.values[each.value].id
  location  = each.value.location
}

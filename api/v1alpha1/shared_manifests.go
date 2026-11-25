/*
Copyright 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

const (
	// ManagedCopyLabel marks a ConfigMap as a SiteConfig-managed copy of a shared extra-manifest
	// ConfigMap. It allows the controller to distinguish its own copies from user-created ConfigMaps
	// that happen to share the same name.
	ManagedCopyLabel = Group + "/managed-copy"

	// ManagedCopyLabelValue is the value set on the ManagedCopyLabel.
	ManagedCopyLabelValue = "true"

	// SourceNamespaceAnnotation records the namespace a managed copy was copied from.
	SourceNamespaceAnnotation = Group + "/source-namespace"

	// SourceResourceVersionAnnotation records the resourceVersion of the source ConfigMap as of the
	// most recent propagation. It is updated on every propagation (install-time copy and subsequent
	// SharedManifestSync updates) when content is propagated; metadata-only source edits need not advance it.
	SourceResourceVersionAnnotation = Group + "/source-resource-version"

	// InitialResourceVersionAnnotation records the resourceVersion of the source ConfigMap at the
	// time the managed copy was first created or adopted. It is set once and never updated,
	// providing a copy-creation audit trail. For post-install reference changes this is not the
	// installation version.
	InitialResourceVersionAnnotation = Group + "/initial-resource-version"
)

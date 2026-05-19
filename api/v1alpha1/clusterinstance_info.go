/*
Copyright 2024.

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

import "fmt"

// ClusterInstance preservation label constants
const (
	PreservationLabelKey      = Group + "/preserve"
	ClusterIdentityLabelValue = "cluster-identity"
)

// PausedAnnotation is the annotation that pauses the reconciliation.
const PausedAnnotation = "clusterinstance." + Group + "/paused"

// ExternallyProvisionedPullSecretAnnotation, when set on a ClusterInstance to an
// allowed value, skips controller validation that the pull secret exists. Use when
// the pull secret is supplied by another mechanism.
const ExternallyProvisionedPullSecretAnnotation = "clusterinstance." + Group + "/externally-provisioned-pull-secret"

// ExternallyProvisionedPullSecretValueTrue is the non-empty value for
// ExternallyProvisionedPullSecretAnnotation.
const ExternallyProvisionedPullSecretValueTrue = "true"

// ExternallyProvisionedPullSecretEnabled reports whether pull secret presence
// validation should be skipped. Returns an error if the annotation is set to an
// unsupported value.
func ExternallyProvisionedPullSecretEnabled(annotations map[string]string) (bool, error) {
	value, ok := annotations[ExternallyProvisionedPullSecretAnnotation]
	if !ok {
		return false, nil
	}
	switch value {
	case "", ExternallyProvisionedPullSecretValueTrue:
		return true, nil
	default:
		return false, fmt.Errorf(
			"annotation %q must be empty or %q, got %q",
			ExternallyProvisionedPullSecretAnnotation,
			ExternallyProvisionedPullSecretValueTrue,
			value,
		)
	}
}

// ExternallyProvisionedBmcSecretAnnotation, when set on a ClusterInstance to an
// allowed value, skips controller validation that node BMC credential secrets exist.
// Use when BMC credentials are supplied by another mechanism.
const ExternallyProvisionedBmcSecretAnnotation = "clusterinstance." + Group + "/externally-provisioned-bmc-secret"

// ExternallyProvisionedBmcSecretValueTrue is the non-empty value for
// ExternallyProvisionedBmcSecretAnnotation.
const ExternallyProvisionedBmcSecretValueTrue = "true"

// ExternallyProvisionedBmcSecretEnabled reports whether BMC credential secret presence
// validation should be skipped. Returns an error if the annotation is set to an
// unsupported value.
func ExternallyProvisionedBmcSecretEnabled(annotations map[string]string) (bool, error) {
	value, ok := annotations[ExternallyProvisionedBmcSecretAnnotation]
	if !ok {
		return false, nil
	}
	switch value {
	case "", ExternallyProvisionedBmcSecretValueTrue:
		return true, nil
	default:
		return false, fmt.Errorf(
			"annotation %q must be empty or %q, got %q",
			ExternallyProvisionedBmcSecretAnnotation,
			ExternallyProvisionedBmcSecretValueTrue,
			value,
		)
	}
}

const LastClusterInstanceSpecAnnotation = "clusterinstance." + Group + "/last-observed-spec"

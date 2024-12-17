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

package clusterinstance

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	cierrors "github.com/stolostron/siteconfig/internal/controller/errors"
)

// OwnedByLabel is the key for the "owned-by" label, used to identify the owner of a resource.
const OwnedByLabel = v1alpha1.Group + "/owned-by"

// GetOwner retrieves the value of the "owned-by" label from an object.
// If the label is not present, it returns an empty string.
func GetOwner(obj client.Object) string {
	return obj.GetLabels()[OwnedByLabel]
}

// VerifyOwnership checks if an object is logically owned by a specified owner reference label.
func VerifyOwnership(obj client.Object, ownerRefLabel string) error {
	if GetOwner(obj) != ownerRefLabel {
		resourceId := GetResourceId(obj.GetName(), obj.GetNamespace(), obj.GetObjectKind().GroupVersionKind().Kind)
		return cierrors.NewNotOwnedObjectError(ownerRefLabel, resourceId)
	}
	return nil
}

// GenerateOwnedByLabelValue generates a consistent value for the "owned-by" label using the
// namespace and name of a ClusterInstance.
func GenerateOwnedByLabelValue(namespace, name string) string {
	return fmt.Sprintf("%s_%s", namespace, name)
}

// GetNamespacedNameFromOwnedByLabel parses the value of the "owned-by" label to extract the
// namespace and name of the owning resource.
func GetNamespacedNameFromOwnedByLabel(ownedByLabel string) (types.NamespacedName, error) {
	res := strings.Split(ownedByLabel, "_")
	if len(res) != 2 {
		return types.NamespacedName{}, fmt.Errorf("expecting single underscore delimiter in %s label value", ownedByLabel)
	}

	return types.NamespacedName{Namespace: res[0], Name: res[1]}, nil
}

// GetNameFromOwnedByLabel extracts only the name portion from the "owned-by" label value.
func GetNameFromOwnedByLabel(ownedByLabel string) string {
	res, err := GetNamespacedNameFromOwnedByLabel(ownedByLabel)
	if err != nil {
		return ""
	}
	return res.Name
}

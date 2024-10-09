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

package common

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/types"
)

func GetResourceId(name, namespace, kind string) string {
	if namespace != "" {
		return fmt.Sprintf("%s:%s/%s", kind, namespace, name)
	}
	return fmt.Sprintf("%s:%s", kind, name)
}

// GenerateOwnedByLabelValue is a utility function that generates the ownedBy label value
// using the ClusterInstance namespace and name
func GenerateOwnedByLabelValue(namespace, name string) string {
	return fmt.Sprintf("%s_%s", namespace, name)
}

// GetNamespacedNameFromOwnedByLabel extracts the namespace and name from the ownedBy label value
func GetNamespacedNameFromOwnedByLabel(ownedByLabel string) (types.NamespacedName, error) {
	res := strings.Split(ownedByLabel, "_")
	if len(res) != 2 {
		return types.NamespacedName{}, fmt.Errorf("expecting single underscore delimiter in %s label value", ownedByLabel)
	}

	return types.NamespacedName{Namespace: res[0], Name: res[1]}, nil
}

/*
Copyright 2025.

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

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("validateExtraManifestReferences", func() {
	const clusterNamespace = "cluster-1-ns"

	newClusterInstance := func(refs ...ExtraManifestReference) *ClusterInstance {
		return &ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-1", Namespace: clusterNamespace},
			Spec:       ClusterInstanceSpec{ExtraManifestsRefs: refs},
		}
	}

	It("accepts a reference with an empty namespace", func() {
		ci := newClusterInstance(ExtraManifestReference{Name: "cm"})
		Expect(validateExtraManifestReferences(ci)).To(Succeed())
	})

	It("accepts a reference to the ClusterInstance's own namespace", func() {
		ci := newClusterInstance(ExtraManifestReference{Name: "cm", Namespace: clusterNamespace})
		Expect(validateExtraManifestReferences(ci)).To(Succeed())
	})

	It("accepts a reference to the shared-resources namespace", func() {
		ci := newClusterInstance(ExtraManifestReference{Name: "cm", Namespace: SharedResourcesNamespace})
		Expect(validateExtraManifestReferences(ci)).To(Succeed())
	})

	It("rejects a reference to a disallowed namespace", func() {
		ci := newClusterInstance(ExtraManifestReference{Name: "cm", Namespace: "some-other-namespace"})
		err := validateExtraManifestReferences(ci)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("some-other-namespace"))
	})

	It("rejects when any reference in a mixed list targets a disallowed namespace", func() {
		ci := newClusterInstance(
			ExtraManifestReference{Name: "cm-local"},
			ExtraManifestReference{Name: "cm-shared", Namespace: SharedResourcesNamespace},
			ExtraManifestReference{Name: "cm-bad", Namespace: "some-other-namespace"},
		)
		err := validateExtraManifestReferences(ci)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cm-bad"))
	})
})

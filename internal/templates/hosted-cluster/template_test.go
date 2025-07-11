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

package hostedcluster

import (
	"testing"

	"k8s.io/client-go/kubernetes/scheme"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHostedClusterTemplates(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Hosted Cluster Installation Templates")
}

var _ = BeforeSuite(func() {
	Expect(v1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
})

var _ = Describe("Get Installation Templates", func() {

	It("should return the expected Cluster-level installation templates", func() {
		clusterTemplates := GetClusterTemplates()
		expectedCRs := map[string]string{
			"HostedCluster":         HostedCluster,
			"ManagedCluster":        ManagedCluster,
			"KlusterletAddonConfig": KlusterletAddonConfig,
			"SshPubKeySecret":       SshPubKeySecret,
		}
		Expect(clusterTemplates).To(Equal(expectedCRs))
	})

	It("should return the expected Nodel-level installation templates", func() {
		nodeTemplates := GetNodeTemplates()
		expectedCRs := map[string]string{
			"InfraEnv":      InfraEnv,
			"NodePool":      NodePool,
			"BareMetalHost": BareMetalHost,
			"NMStateConfig": NMStateConfig,
		}
		Expect(nodeTemplates).To(Equal(expectedCRs))
	})
})

var _ = Describe("Validate Installation Templates", func() {

	It("should be able to parse the Cluster-level installation templates", func() {
		clusterTemplates := GetClusterTemplates()
		for templateKey, template := range clusterTemplates {
			_, err := ci.ParseTemplate(templateKey, template)
			Expect(err).ToNot(HaveOccurred())
		}
	})

	It("should be able to parse the Node-level installation templates", func() {
		nodeTemplates := GetNodeTemplates()
		for templateKey, template := range nodeTemplates {
			_, err := ci.ParseTemplate(templateKey, template)
			Expect(err).ToNot(HaveOccurred())
		}
	})
})

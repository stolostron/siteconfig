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

package controller

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"

	bmh_v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	"github.com/openshift/assisted-service/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"

	siteconfigv1alpha1 "github.com/sakhoury/siteconfig/api/v1alpha1"
	//+kubebuilder:scaffold:imports
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "ControllerSuite")
}

var _ = BeforeSuite(func() {
	Expect(siteconfigv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(hivev1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(v1beta1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(clusterv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(bmh_v1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
})

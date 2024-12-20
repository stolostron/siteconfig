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

package preservation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"

	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/stolostron/siteconfig/api/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Constants for unit-tests
const (
	testReinstallGeneration = "123"
	testNamespace1          = "test-cluster-1"
	testNamespace2          = "test-cluster-2"
	testTimestamp           = "some-timestamp"
)

type testdataConfigMap struct {
	input, expected *corev1.ConfigMap
}

type testdataSecret struct {
	input, expected *corev1.Secret
}

type testBackupConfigMap testdataConfigMap
type testBackupSecret testdataSecret
type testRestoreConfigMap testdataConfigMap
type testRestoreSecret testdataSecret

func (t *testBackupConfigMap) generate(namespace, name string, data map[string]string, labels map[string]string, mode v1alpha1.PreservationMode) {
	inputLabels := make(map[string]string)
	expectedLabels := make(map[string]string)
	for k, v := range labels {
		inputLabels[k] = v
		expectedLabels[k] = v
	}

	switch mode {
	case v1alpha1.PreservationModeAll:
		inputLabels[v1alpha1.PreservationLabelKey] = ""
		expectedLabels[v1alpha1.PreservationLabelKey] = ""
		expectedLabels[preservedLabel] = testTimestamp
	case v1alpha1.PreservationModeClusterIdentity:
		inputLabels[v1alpha1.PreservationLabelKey] = v1alpha1.ClusterIdentityLabelValue
		expectedLabels[v1alpha1.PreservationLabelKey] = v1alpha1.ClusterIdentityLabelValue
		expectedLabels[preservedLabel] = testTimestamp
	}

	t.input = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    inputLabels,
		},
		Data: data,
	}
	t.expected = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateName(name, testReinstallGeneration),
			Namespace: namespace,
			Labels:    expectedLabels,
		},
		Data: map[string]string{
			name: string(formatCMDataToJSON(data)),
		},
	}
}

func (t *testBackupSecret) generate(namespace, name string, data map[string][]byte, labels map[string]string, mode v1alpha1.PreservationMode) {
	inputLabels := make(map[string]string)
	expectedLabels := make(map[string]string)
	for k, v := range labels {
		inputLabels[k] = v
		expectedLabels[k] = v
	}

	switch mode {
	case v1alpha1.PreservationModeAll:
		inputLabels[v1alpha1.PreservationLabelKey] = ""
		expectedLabels[v1alpha1.PreservationLabelKey] = ""
		expectedLabels[preservedLabel] = testTimestamp
	case v1alpha1.PreservationModeClusterIdentity:
		inputLabels[v1alpha1.PreservationLabelKey] = v1alpha1.ClusterIdentityLabelValue
		expectedLabels[v1alpha1.PreservationLabelKey] = v1alpha1.ClusterIdentityLabelValue
		expectedLabels[preservedLabel] = testTimestamp
	}

	t.input = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    inputLabels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
	t.expected = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateName(name, testReinstallGeneration),
			Namespace: namespace,
			Labels:    expectedLabels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			name: formatSecretDataToJSON(data),
		},
	}
}

func (t *testRestoreConfigMap) generate(namespace, name string, data map[string]string, labels map[string]string, mode v1alpha1.PreservationMode) {
	inputLabels := make(map[string]string)
	expectedLabels := make(map[string]string)
	for k, v := range labels {
		inputLabels[k] = v
		expectedLabels[k] = v
	}

	switch mode {
	case v1alpha1.PreservationModeAll:
		inputLabels[v1alpha1.PreservationLabelKey] = ""
		inputLabels[preservedLabel] = testTimestamp
		expectedLabels[v1alpha1.PreservationLabelKey] = ""
	case v1alpha1.PreservationModeClusterIdentity:
		inputLabels[v1alpha1.PreservationLabelKey] = v1alpha1.ClusterIdentityLabelValue
		inputLabels[preservedLabel] = testTimestamp
		expectedLabels[v1alpha1.PreservationLabelKey] = v1alpha1.ClusterIdentityLabelValue
	}

	t.input = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateName(name, testReinstallGeneration),
			Namespace: namespace,
			Labels:    inputLabels,
		},
		Data: map[string]string{
			name: string(formatCMDataToJSON(data)),
		},
	}
	t.expected = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    expectedLabels,
		},
		Data: data,
	}
}

func (t *testRestoreSecret) generate(namespace, name string, data map[string][]byte, labels map[string]string, mode v1alpha1.PreservationMode) {
	inputLabels := make(map[string]string)
	expectedLabels := make(map[string]string)
	for k, v := range labels {
		inputLabels[k] = v
		expectedLabels[k] = v
	}

	switch mode {
	case v1alpha1.PreservationModeAll:
		inputLabels[v1alpha1.PreservationLabelKey] = ""
		inputLabels[preservedLabel] = testTimestamp
		expectedLabels[v1alpha1.PreservationLabelKey] = ""
	case v1alpha1.PreservationModeClusterIdentity:
		inputLabels[v1alpha1.PreservationLabelKey] = v1alpha1.ClusterIdentityLabelValue
		inputLabels[preservedLabel] = testTimestamp
		expectedLabels[v1alpha1.PreservationLabelKey] = v1alpha1.ClusterIdentityLabelValue
	}

	t.input = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateName(name, testReinstallGeneration),
			Namespace: namespace,
			Labels:    inputLabels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			name: formatSecretDataToJSON(data),
		},
	}
	t.expected = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    expectedLabels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
}

func formatCMDataToJSON(data map[string]string) []byte {
	jsonData, _ := json.Marshal(data)
	return jsonData
}

func formatSecretDataToJSON(data map[string][]byte) []byte {
	jsonData, _ := json.Marshal(data)
	return jsonData
}

// ensureLabelsEquality checks the equality of labels, allowing a specific key to be dynamically updated.
func ensureLabelsEquality(actual, expected map[string]string, key, value string) {
	Expect(actual).To(HaveKey(key))
	actual[key] = value
	Expect(actual).To(Equal(expected))
}

// ensureAnnotationsEquality verifies that annotations are equal.
func ensureAnnotationsEquality(actual, expected map[string]string) {
	Expect(actual).To(Equal(expected))
}

// ensureDataEquality checks the equality of data in ConfigMaps or Secrets.
func ensureDataEquality(actual, expected interface{}) {
	Expect(actual).To(Equal(expected))
}

// ensureBackupConfigMapEquality ensures equality between actual and expected ConfigMaps during backup.
func ensureBackupConfigMapEquality(actual, expected *corev1.ConfigMap) {
	ensureLabelsEquality(actual.Labels, expected.Labels, preservedLabel, testTimestamp)
	ensureAnnotationsEquality(actual.Annotations, expected.Annotations)
	ensureDataEquality(actual.Data, expected.Data)
}

// ensureBackupSecretEquality ensures equality between actual and expected Secrets during backup.
func ensureBackupSecretEquality(actual, expected *corev1.Secret) {
	ensureLabelsEquality(actual.Labels, expected.Labels, preservedLabel, testTimestamp)
	ensureAnnotationsEquality(actual.Annotations, expected.Annotations)
	Expect(actual.Type).To(Equal(expected.Type)) // Secrets have a `Type` field.
	ensureDataEquality(actual.Data, expected.Data)
}

// ensureRestoreConfigMapEquality ensures equality between actual and expected ConfigMaps during restore.
func ensureRestoreConfigMapEquality(actual, expected *corev1.ConfigMap) {
	Expect(actual.Labels).To(Equal(expected.Labels))
	ensureAnnotationsEquality(actual.Annotations, expected.Annotations)
	ensureDataEquality(actual.Data, expected.Data)
}

// ensureRestoreSecretEquality ensures equality between actual and expected Secrets during restore.
func ensureRestoreSecretEquality(actual, expected *corev1.Secret) {
	Expect(actual.Labels).To(Equal(expected.Labels))
	ensureAnnotationsEquality(actual.Annotations, expected.Annotations)
	Expect(actual.Type).To(Equal(expected.Type))
	ensureDataEquality(actual.Data, expected.Data)
}

var _ = Describe("PreservationHandler", func() {
	Describe("buildBackupLabelSelector", func() {
		var (
			pHandler  *PreservationHandler
			namespace = testNamespace1
		)

		Context("preservationMode is valid", func() {
			When("preservationMode is All", func() {
				It("returns a label.selector for retrieving any resources labeled with the PreservationLabel key", func() {
					pHandler = NewPreservationHandler(namespace, v1alpha1.PreservationModeAll)

					expected, err := labels.NewRequirement(
						v1alpha1.PreservationLabelKey,
						selection.Exists,
						nil,
					)
					Expect(err).NotTo(HaveOccurred())

					lSelector, err := pHandler.buildBackupLabelSelector()
					Expect(err).NotTo(HaveOccurred())
					Expect(lSelector).To(Equal(labels.NewSelector().Add(*expected)))
				})
			})
			When("preservationMode is ClusterIdentity", func() {
				It("returns a label.selector for retrieving specifif resources labeled with the PreservationLabel key and ClusterIdentity value", func() {
					pHandler = NewPreservationHandler(namespace, v1alpha1.PreservationModeClusterIdentity)

					expected, err := labels.NewRequirement(
						v1alpha1.PreservationLabelKey,
						selection.Equals,
						[]string{v1alpha1.ClusterIdentityLabelValue},
					)
					Expect(err).NotTo(HaveOccurred())

					lSelector, err := pHandler.buildBackupLabelSelector()
					Expect(err).NotTo(HaveOccurred())
					Expect(lSelector).To(Equal(labels.NewSelector().Add(*expected)))
				})
			})
			When("preservationMode is None", func() {
				It("does not error and returns a nil label.selector", func() {
					pHandler = NewPreservationHandler(namespace, v1alpha1.PreservationModeNone)

					lSelector, err := pHandler.buildBackupLabelSelector()
					Expect(err).NotTo(HaveOccurred())
					Expect(lSelector).To(BeNil())
				})
			})
		})

		Context("preservationMode is invalid", func() {
			It("errors and returns a nil label.selector", func() {
				pHandler = NewPreservationHandler(namespace, "unknown")

				lSelector, err := pHandler.buildBackupLabelSelector()
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(fmt.Errorf("unknown PreservationMode: unknown")))
				Expect(lSelector).To(BeNil())
			})
		})
	})

	Describe("buildRestoreLabelSelector", func() {
		var (
			pHandler  *PreservationHandler
			namespace = testNamespace1
		)

		Context("preservationMode is valid", func() {
			When("preservationMode is All", func() {
				It("returns a label.selector for retrieving any resources that were preserved", func() {
					pHandler = NewPreservationHandler(namespace, v1alpha1.PreservationModeAll)

					expected, err := labels.NewRequirement(
						preservedLabel,
						selection.Exists,
						nil,
					)
					Expect(err).NotTo(HaveOccurred())

					lSelector, err := pHandler.buildRestoreLabelSelector()
					Expect(err).NotTo(HaveOccurred())
					Expect(lSelector).To(Equal(labels.NewSelector().Add(*expected)))
				})
			})
			When("preservationMode is ClusterIdentity", func() {
				It("returns a label.selector for retrieving specifif resources labeled with the PreservationLabel key and ClusterIdentity value", func() {
					pHandler = NewPreservationHandler(namespace, v1alpha1.PreservationModeClusterIdentity)

					expected, err := labels.NewRequirement(
						preservedLabel,
						selection.Exists,
						nil,
					)
					Expect(err).NotTo(HaveOccurred())

					lSelector, err := pHandler.buildRestoreLabelSelector()
					Expect(err).NotTo(HaveOccurred())
					Expect(lSelector).To(Equal(labels.NewSelector().Add(*expected)))
				})
			})
			When("preservationMode is None", func() {
				It("does not error and returns a nil label.selector", func() {
					pHandler = NewPreservationHandler(namespace, v1alpha1.PreservationModeNone)

					lSelector, err := pHandler.buildRestoreLabelSelector()
					Expect(err).NotTo(HaveOccurred())
					Expect(lSelector).To(BeNil())
				})
			})
		})

		Context("preservationMode is invalid", func() {
			It("errors and returns a nil label.selector", func() {
				pHandler = NewPreservationHandler(namespace, "unknown")

				lSelector, err := pHandler.buildRestoreLabelSelector()
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(fmt.Errorf("unknown PreservationMode: unknown")))
				Expect(lSelector).To(BeNil())
			})
		})
	})

	Describe("buildCleanupLabelSelector", func() {
		It("returns a label.selector for retrieving any resources that were preserved", func() {
			pHandler := NewPreservationHandler(testNamespace1, "")

			expected, err := labels.NewRequirement(
				preservedLabel,
				selection.Exists,
				nil,
			)
			Expect(err).NotTo(HaveOccurred())

			lSelector, err := pHandler.buildCleanupLabelSelector()
			Expect(err).NotTo(HaveOccurred())
			Expect(lSelector).To(Equal(labels.NewSelector().Add(*expected)))
		})
	})

	Describe("Test Backup and Restore functionality", func() {

		var (
			c   client.Client
			ctx context.Context

			testLogger = zap.NewNop().Named("Test")

			cmData     = map[string]string{"foo": "bar"}
			SecretData = map[string][]byte{"username": []byte("admin"), "password": []byte("password")}
			labels     = map[string]string{"x": "y"}

			backupCM1, backupCM2, backupCM3, backupCM4                 testBackupConfigMap
			backupSecret1, backupSecret2, backupSecret3, backupSecret4 testBackupSecret

			restoreCM1, restoreCM2, restoreCM3, restoreCM4                 testRestoreConfigMap
			restoreSecret1, restoreSecret2, restoreSecret3, restoreSecret4 testRestoreSecret

			backupCMData      []testBackupConfigMap
			backupSecretData  []testBackupSecret
			restoreCMData     []testRestoreConfigMap
			restoreSecretData []testRestoreSecret
		)

		BeforeEach(func() {
			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				Build()
			ctx = context.Background()

			// Generate test backup data consisting of ConfigMaps and Secrets
			backupCM1.generate(testNamespace1, "backup-cm-1", cmData, labels, v1alpha1.PreservationModeAll)
			backupCM2.generate(testNamespace1, "backup-cm-2", cmData, labels, v1alpha1.PreservationModeNone)
			backupCM3.generate(testNamespace2, "backup-cm-3", cmData, labels, v1alpha1.PreservationModeClusterIdentity)
			backupCM4.generate(testNamespace1, "backup-cm-4", cmData, labels, v1alpha1.PreservationModeClusterIdentity)
			backupSecret1.generate(testNamespace2, "backup-secret-1", SecretData, labels, v1alpha1.PreservationModeNone)
			backupSecret2.generate(testNamespace1, "backup-secret-2", SecretData, labels, v1alpha1.PreservationModeAll)
			backupSecret3.generate(testNamespace2, "backup-secret-3", SecretData, labels, v1alpha1.PreservationModeClusterIdentity)
			backupSecret4.generate(testNamespace1, "backup-secret-4", SecretData, labels, v1alpha1.PreservationModeClusterIdentity)

			// Collect ConfigMaps and Secrets
			backupCMData = append([]testBackupConfigMap{}, backupCM1, backupCM2, backupCM3, backupCM4)
			backupSecretData = append([]testBackupSecret{}, backupSecret1, backupSecret2, backupSecret3, backupSecret4)

			// Create the Kubernetes ConfigMaps and Secrets
			for _, cm := range backupCMData {
				Expect(c.Create(ctx, cm.input)).To(Succeed())
			}
			for _, secret := range backupSecretData {
				Expect(c.Create(ctx, secret.input)).To(Succeed())
			}

			// Generate test restore data consisting of ConfigMaps and Secrets
			restoreCM1.generate(testNamespace2, "restore-cm-1", cmData, labels, v1alpha1.PreservationModeAll)
			restoreCM2.generate(testNamespace2, "restore-cm-2", cmData, labels, v1alpha1.PreservationModeNone)
			restoreCM3.generate(testNamespace2, "restore-cm-3", cmData, labels, v1alpha1.PreservationModeClusterIdentity)
			restoreCM4.generate(testNamespace1, "restore-cm-4", cmData, labels, v1alpha1.PreservationModeClusterIdentity)
			restoreSecret1.generate(testNamespace2, "restore-secret-1", SecretData, labels, v1alpha1.PreservationModeAll)
			restoreSecret2.generate(testNamespace2, "restore-secret-2", SecretData, labels, v1alpha1.PreservationModeNone)
			restoreSecret3.generate(testNamespace2, "restore-secret-3", SecretData, labels, v1alpha1.PreservationModeClusterIdentity)
			restoreSecret4.generate(testNamespace1, "restore-secret-4", SecretData, labels, v1alpha1.PreservationModeClusterIdentity)

			// Collect ConfigMaps and Secrets
			restoreCMData = append([]testRestoreConfigMap{}, restoreCM1, restoreCM2, restoreCM3, restoreCM4)
			restoreSecretData = append([]testRestoreSecret{}, restoreSecret1, restoreSecret2, restoreSecret3, restoreSecret4)

			// Create the Kubernetes ConfigMaps and Secrets
			for _, cm := range restoreCMData {
				Expect(c.Create(ctx, cm.input)).To(Succeed())
			}
			for _, secret := range restoreSecretData {
				Expect(c.Create(ctx, secret.input)).To(Succeed())
			}
		})

		AfterEach(func() {
			// Attempt to delete all test data resources
			for _, cm := range backupCMData {
				Expect(c.Delete(ctx, cm.input)).To(Succeed())
				_ = c.Delete(ctx, cm.expected)
			}
			for _, secret := range backupSecretData {
				Expect(c.Delete(ctx, secret.input)).To(Succeed())
				_ = c.Delete(ctx, secret.expected)
			}

			for _, cm := range restoreCMData {
				Expect(c.Delete(ctx, cm.input)).To(Succeed())
				_ = c.Delete(ctx, cm.expected)
			}
			for _, secret := range restoreSecretData {
				Expect(c.Delete(ctx, secret.input)).To(Succeed())
				_ = c.Delete(ctx, secret.expected)
			}

		})

		Describe("Backup", func() {

			It("sucessfully backups all labeled ConfigMaps and Secrets when preservationMode is All", func() {

				pHandler := NewPreservationHandler(testNamespace1, v1alpha1.PreservationModeAll)
				Expect(pHandler).NotTo(BeNil())

				expectedBackupCMs := []testBackupConfigMap{backupCM1, backupCM4}
				expectedBackupSecrets := []testBackupSecret{backupSecret2, backupSecret4}

				err := pHandler.Backup(ctx, c, testReinstallGeneration, testLogger)
				Expect(err).NotTo(HaveOccurred())
				for _, testData := range expectedBackupCMs {

					// Fetch the original ConfigMap and compare data
					actual := &corev1.ConfigMap{}
					key := types.NamespacedName{Namespace: testData.expected.Namespace, Name: testData.expected.Name}
					Expect(c.Get(ctx, key, actual)).To(Succeed())

					ensureBackupConfigMapEquality(actual, testData.expected)
				}

				for _, testData := range expectedBackupSecrets {

					// Fetch the original Secret and compare data
					actual := &corev1.Secret{}
					key := types.NamespacedName{Namespace: testData.expected.Namespace, Name: testData.expected.Name}
					Expect(c.Get(ctx, key, actual)).To(Succeed())

					ensureBackupSecretEquality(actual, testData.expected)
				}

			})

			It("sucessfully backups only cluster identity ConfigMaps and Secrets when preservationMode is ClusterIdentity", func() {

				pHandler := NewPreservationHandler(testNamespace1, v1alpha1.PreservationModeClusterIdentity)
				Expect(pHandler).NotTo(BeNil())

				expectedBackupCMs := []testBackupConfigMap{backupCM4}
				expectedBackupSecrets := []testBackupSecret{backupSecret4}

				err := pHandler.Backup(ctx, c, testReinstallGeneration, testLogger)
				Expect(err).NotTo(HaveOccurred())
				for _, testData := range expectedBackupCMs {

					// Fetch the original ConfigMap and compare data
					actual := &corev1.ConfigMap{}
					key := types.NamespacedName{Namespace: testNamespace1, Name: testData.expected.Name}
					Expect(c.Get(ctx, key, actual)).To(Succeed())

					ensureBackupConfigMapEquality(actual, testData.expected)
				}

				for _, testData := range expectedBackupSecrets {

					// Fetch the original Secret and compare data
					actual := &corev1.Secret{}
					key := types.NamespacedName{Namespace: testData.expected.Namespace, Name: testData.expected.Name}
					Expect(c.Get(ctx, key, actual)).To(Succeed())

					ensureBackupSecretEquality(actual, testData.expected)
				}
			})

			It("does not backup any data when preservationMode is None", func() {

				pHandler := NewPreservationHandler(testNamespace1, v1alpha1.PreservationModeNone)
				Expect(pHandler).NotTo(BeNil())

				err := pHandler.Backup(ctx, c, testReinstallGeneration, testLogger)
				Expect(err).NotTo(HaveOccurred())
				for _, testData := range backupCMData {

					actual := &corev1.ConfigMap{}
					key := types.NamespacedName{Namespace: testData.expected.Namespace, Name: testData.expected.Name}
					Expect(c.Get(ctx, key, actual)).ToNot(Succeed())

				}
				for _, testData := range backupSecretData {

					actual := &corev1.Secret{}
					key := types.NamespacedName{Namespace: testData.expected.Namespace, Name: testData.expected.Name}
					Expect(c.Get(ctx, key, actual)).ToNot(Succeed())

				}

			})

		})

		Describe("Restore", func() {

			It("sucessfully restore all labeled ConfigMaps and Secrets when preservationMode is All", func() {

				pHandler := NewPreservationHandler(testNamespace2, v1alpha1.PreservationModeAll)
				Expect(pHandler).NotTo(BeNil())

				expectedRestoredCMs := []testRestoreConfigMap{restoreCM1, restoreCM3}
				expectedRestoredSecrets := []testRestoreSecret{restoreSecret1, restoreSecret3}

				err := pHandler.Restore(ctx, c, testLogger)
				Expect(err).NotTo(HaveOccurred())
				for _, testData := range expectedRestoredCMs {

					// Fetch the original ConfigMap and compare data
					actual := &corev1.ConfigMap{}
					key := types.NamespacedName{Namespace: testData.expected.Namespace, Name: testData.expected.Name}
					Expect(c.Get(ctx, key, actual)).To(Succeed())

					ensureRestoreConfigMapEquality(actual, testData.expected)
				}
				for _, testData := range expectedRestoredSecrets {

					// Fetch the original ConfigMap and compare data
					actual := &corev1.Secret{}
					key := types.NamespacedName{Namespace: testData.expected.Namespace, Name: testData.expected.Name}
					Expect(c.Get(ctx, key, actual)).To(Succeed())

					ensureRestoreSecretEquality(actual, testData.expected)
				}

			})

			It("sucessfully restore only cluster identity ConfigMaps and Secrets when preservationMode is ClusterIdentity", func() {

				pHandler := NewPreservationHandler(testNamespace2, v1alpha1.PreservationModeAll)
				Expect(pHandler).NotTo(BeNil())

				expectedRestoredCMs := []testRestoreConfigMap{restoreCM3}
				expectedRestoredSecrets := []testRestoreSecret{restoreSecret3}

				err := pHandler.Restore(ctx, c, testLogger)
				Expect(err).NotTo(HaveOccurred())
				for _, testData := range expectedRestoredCMs {

					// Fetch the original ConfigMap and compare data
					actual := &corev1.ConfigMap{}
					key := types.NamespacedName{Namespace: testData.expected.Namespace, Name: testData.expected.Name}
					Expect(c.Get(ctx, key, actual)).To(Succeed())

					ensureRestoreConfigMapEquality(actual, testData.expected)
				}
				for _, testData := range expectedRestoredSecrets {

					// Fetch the original ConfigMap and compare data
					actual := &corev1.Secret{}
					key := types.NamespacedName{Namespace: testData.expected.Namespace, Name: testData.expected.Name}
					Expect(c.Get(ctx, key, actual)).To(Succeed())

					ensureRestoreSecretEquality(actual, testData.expected)
				}

			})

			It("does not restore any data when preservationMode is None", func() {

				pHandler := NewPreservationHandler(testNamespace1, v1alpha1.PreservationModeNone)
				Expect(pHandler).NotTo(BeNil())

				err := pHandler.Restore(ctx, c, testLogger)
				Expect(err).NotTo(HaveOccurred())
				for _, testData := range restoreCMData {
					actual := &corev1.ConfigMap{}
					key := types.NamespacedName{Namespace: testData.expected.Namespace, Name: testData.expected.Name}
					Expect(c.Get(ctx, key, actual)).ToNot(Succeed())
				}
				for _, testData := range restoreSecretData {
					actual := &corev1.Secret{}
					key := types.NamespacedName{Namespace: testData.expected.Namespace, Name: testData.expected.Name}
					Expect(c.Get(ctx, key, actual)).ToNot(Succeed())
				}

			})

		})
	})

	Describe("Cleanup", func() {
		var (
			ctx      context.Context
			c        client.Client
			pHandler *PreservationHandler
			// ConfigMap resources
			cm1, cm2, cm3, cm4 *corev1.ConfigMap
			// Secret resources
			secret1, secret2, secret3, secret4 *corev1.Secret
			// Objects
			objects []client.Object
		)
		BeforeEach(func() {
			ctx = context.Background()

			pLabel := map[string]string{preservedLabel: testTimestamp}
			otherLabel := map[string]string{"foo": "bar"}
			cm1 = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm1", Namespace: testNamespace1, Labels: pLabel}}
			cm2 = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm2", Namespace: testNamespace1, Labels: otherLabel}}
			cm3 = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm3", Namespace: testNamespace2, Labels: pLabel}}
			cm4 = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm4", Namespace: testNamespace2, Labels: otherLabel}}

			secret1 = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret1", Namespace: testNamespace1, Labels: pLabel}}
			secret2 = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret2", Namespace: testNamespace1, Labels: otherLabel}}
			secret3 = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret3", Namespace: testNamespace2, Labels: pLabel}}
			secret4 = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret4", Namespace: testNamespace2, Labels: otherLabel}}

			objects = []client.Object{cm1, cm2, cm3, cm4, secret1, secret2, secret3, secret4}

			// Intended deletion: namespace=testNamespace1, label=pLabel
			pHandler = NewPreservationHandler(testNamespace1, "")
			Expect(pHandler).NotTo(BeNil())

		})
		It("sucessfully deletes all preserved ConfigMaps and Secrets in a given namespace", func() {
			c = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).
				WithObjects(objects...).
				Build()

			// Ensure resources exist before attempting Cleanup
			for _, obj := range objects {
				Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
			}

			err := pHandler.Cleanup(ctx, c, zap.NewNop().Named("Test"))
			Expect(err).NotTo(HaveOccurred())

			// Verify correct resources are deleted
			for _, obj := range []client.Object{cm1, secret1} {
				Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), obj)).ToNot(Succeed())
			}
			for _, obj := range []client.Object{cm2, cm3, cm4, secret2, secret3, secret4} {
				Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
			}
		})

		It("does not return an error when deletion fails because a resource was not found", func() {
			deleteCalled := false
			c = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).
				WithObjects(objects...).
				WithInterceptorFuncs(interceptor.Funcs{
					Delete: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
						// trigger a deletion failure on secret1
						if obj.GetName() == secret1.Name && obj.GetNamespace() == secret1.Namespace {
							deleteCalled = true
							return k8serrors.NewNotFound(
								schema.GroupResource{
									Group:    secret1.GroupVersionKind().Group,
									Resource: secret1.Kind,
								},
								secret1.Name,
							)
						}
						return nil
					},
				}).Build()

			err := pHandler.Cleanup(ctx, c, zap.NewNop().Named("Test"))
			Expect(err).NotTo(HaveOccurred())
			Expect(deleteCalled).To(BeTrue())
		})

		It("returns an error when Cleanup fails for one or more preserved ConfigMaps and Secrets", func() {
			fakeErrorMsg := "induced fake error"
			deleteCalled := false
			c = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).
				WithObjects(objects...).
				WithInterceptorFuncs(interceptor.Funcs{
					Delete: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
						// trigger a deletion failure on secret1
						if obj.GetName() == secret1.Name && obj.GetNamespace() == secret1.Namespace {
							deleteCalled = true
							return errors.New(fakeErrorMsg)
						}
						return nil
					},
				}).
				Build()

			err := pHandler.Cleanup(ctx, c, zap.NewNop().Named("Test"))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(fakeErrorMsg))
			Expect(deleteCalled).To(BeTrue())
		})

	})
})

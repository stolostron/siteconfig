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
	"errors"
	"fmt"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/yaml"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/deletion"

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

func verifyFn(targetObject types.NamespacedName, expectedObjects []client.Object, err error) bool {
	for _, expectedObject := range expectedObjects {
		if targetObject == client.ObjectKeyFromObject(expectedObject) {
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Expected resource %s/%s to exist", expectedObject.GetNamespace(), expectedObject.GetName()))
			return true
		}
	}
	Expect(err).To(HaveOccurred(), fmt.Sprintf("Expected error since resource %s/%s should not exist", targetObject.Namespace, targetObject.Name))
	return true
}

func verifyBackupFunctionality(ctx context.Context, c client.Client,
	preservedConfigMapList, preservedSecretList []*corev1.Secret,
	expectedPreservedConfigMaps, expectedPreservedSecrets []client.Object,
) {
	for _, obj := range preservedConfigMapList {
		key := client.ObjectKeyFromObject(obj)
		targetObject := &corev1.Secret{}
		Expect(c.Get(ctx, key, targetObject)).
			To(Satisfy(func(err error) bool {
				return verifyFn(key, expectedPreservedConfigMaps, err)
			}))
	}

	for _, obj := range preservedSecretList {
		key := client.ObjectKeyFromObject(obj)
		targetObject := &corev1.Secret{}
		Expect(c.Get(ctx, key, targetObject)).
			To(Satisfy(func(err error) bool {
				return verifyFn(key, expectedPreservedSecrets, err)
			}))
	}
}

func verifyRestoreFunctionality(ctx context.Context, c client.Client,
	configMapList []*corev1.ConfigMap,
	secretList []*corev1.Secret,
	expectedRestoredConfigMaps, expectedRestoredSecrets []client.Object,
) {
	for _, obj := range configMapList {
		key := client.ObjectKeyFromObject(obj)
		targetObject := &corev1.ConfigMap{}
		Expect(c.Get(ctx, key, targetObject)).
			To(Satisfy(func(err error) bool {
				return verifyFn(key, expectedRestoredConfigMaps, err)
			}))
	}

	for _, obj := range secretList {
		key := client.ObjectKeyFromObject(obj)
		targetObject := &corev1.Secret{}
		Expect(c.Get(ctx, key, targetObject)).
			To(Satisfy(func(err error) bool {
				return verifyFn(key, expectedRestoredSecrets, err)
			}))
	}
}

// generateConfigMap function generates and returns originalConfigMap, preservedConfigMap
func generateConfigMap(name, namespace, ownerRef string, mode v1alpha1.PreservationMode) (*corev1.ConfigMap, *corev1.Secret) {

	labels := map[string]string{
		ci.OwnedByLabel: ownerRef,
	}
	switch mode {
	case v1alpha1.PreservationModeAll:
		labels[v1alpha1.PreservationLabelKey] = ""
	case v1alpha1.PreservationModeClusterIdentity:
		labels[v1alpha1.PreservationLabelKey] = v1alpha1.ClusterIdentityLabelValue
	case PreservationModeInternal:
		labels[InternalPreservationLabelKey] = InternalPreservationLabelValue
	}

	// ConfigMaps for testing.
	originalConfigMap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Data: map[string]string{"foo": "bar"},
	}

	originalConfigMapCopied := originalConfigMap.DeepCopy()
	sanitizeResourceMetadata(originalConfigMapCopied)
	dAtA, err := yaml.Marshal(originalConfigMapCopied)
	Expect(err).ToNot(HaveOccurred())

	annotations := map[string]string{
		reinstallGenerationAnnotationKey: testReinstallGeneration,
		resourceTypeAnnotationKey:        string(configMapResourceType),
	}

	if mode != v1alpha1.PreservationModeNone {
		annotations[v1alpha1.PreservationLabelKey] = string(mode)
	}

	if mode == v1alpha1.PreservationModeClusterIdentity {
		annotations[ClusterIdentityDataAnnotationKey] = ""
	}

	labels = map[string]string{
		ci.OwnedByLabel: ownerRef,
	}
	if mode == PreservationModeInternal {
		labels[preservedInternalDataLabelKey] = testTimestamp
	} else {
		labels[preservedDataLabelKey] = testTimestamp
	}

	preservedConfigMap := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        generateBackupName(configMapResourceType, name, testReinstallGeneration),
			Namespace:   namespace,
			Annotations: annotations,
			Labels:      labels,
		},
		Type:      corev1.SecretTypeOpaque,
		Immutable: ptr.To(true),
		Data:      map[string][]byte{preservedDataKey: dAtA},
	}

	return originalConfigMap, preservedConfigMap
}

// generateSecret function generates and returns originalSecret, preservedSecret
func generateSecret(name, namespace, ownerRef string, mode v1alpha1.PreservationMode) (*corev1.Secret, *corev1.Secret) {
	labels := map[string]string{
		ci.OwnedByLabel: ownerRef,
	}
	switch mode {
	case v1alpha1.PreservationModeAll:
		labels[v1alpha1.PreservationLabelKey] = ""
	case v1alpha1.PreservationModeClusterIdentity:
		labels[v1alpha1.PreservationLabelKey] = v1alpha1.ClusterIdentityLabelValue
	case PreservationModeInternal:
		labels[InternalPreservationLabelKey] = InternalPreservationLabelValue
	}

	// ConfigMaps for testing.
	originalSecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"username": []byte("admin"), "password": []byte("password")},
	}

	originalSecretCopied := originalSecret.DeepCopy()
	sanitizeResourceMetadata(originalSecretCopied)
	dAtA, err := yaml.Marshal(originalSecretCopied)
	Expect(err).ToNot(HaveOccurred())

	annotations := map[string]string{
		reinstallGenerationAnnotationKey: testReinstallGeneration,
		resourceTypeAnnotationKey:        string(secretResourceType),
	}

	if mode != v1alpha1.PreservationModeNone {
		annotations[v1alpha1.PreservationLabelKey] = string(mode)
	}

	if mode == v1alpha1.PreservationModeClusterIdentity {
		annotations[ClusterIdentityDataAnnotationKey] = ""
	}

	labels = map[string]string{
		ci.OwnedByLabel: ownerRef,
	}
	if mode == PreservationModeInternal {
		labels[preservedInternalDataLabelKey] = testTimestamp
	} else {
		labels[preservedDataLabelKey] = testTimestamp
	}
	preservedSecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        generateBackupName(secretResourceType, name, testReinstallGeneration),
			Namespace:   namespace,
			Annotations: annotations,
			Labels:      labels,
		},
		Type:      corev1.SecretTypeOpaque,
		Immutable: ptr.To(true),
		Data:      map[string][]byte{preservedDataKey: dAtA},
	}

	return originalSecret, preservedSecret
}

var _ = Describe("Test Backup and Restore functionality", func() {

	var (
		c   client.Client
		ctx context.Context

		testLogger = zap.NewNop().Named("Test")

		ownerRef1 = ci.GenerateOwnedByLabelValue(testNamespace1, testNamespace1)
		ownerRef2 = ci.GenerateOwnedByLabelValue(testNamespace2, testNamespace2)

		// ConfigMap test data
		originalCM1, originalCM2, originalCM3, originalCM4     *corev1.ConfigMap
		preservedCM1, preservedCM2, preservedCM3, preservedCM4 *corev1.Secret

		// Secret test data
		originalSecret1, originalSecret2, originalSecret3, originalSecret4     *corev1.Secret
		preservedSecret1, preservedSecret2, preservedSecret3, preservedSecret4 *corev1.Secret

		originalPullSecret1, preservedPullSecret1 *corev1.Secret
		originalPullSecret2, preservedPullSecret2 *corev1.Secret

		// Collection of original ConfigMaps and Secrets
		originalConfigMaps []*corev1.ConfigMap
		originalSecrets    []*corev1.Secret

		// Collection of preserved ConfigMaps and Secrets
		preservedConfigMaps []*corev1.Secret
		preservedSecrets    []*corev1.Secret
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()
		ctx = context.Background()

		// Generate test data consisting of ConfigMaps and Secrets

		// ConfigMaps for testing.
		originalCM1, preservedCM1 = generateConfigMap("cm1", testNamespace2, ownerRef2, v1alpha1.PreservationModeAll)
		originalCM2, preservedCM2 = generateConfigMap("cm2", testNamespace1, ownerRef1, v1alpha1.PreservationModeAll)
		originalCM3, preservedCM3 = generateConfigMap("cm3", testNamespace2, ownerRef2, v1alpha1.PreservationModeClusterIdentity)
		originalCM4, preservedCM4 = generateConfigMap("cm4", testNamespace1, ownerRef1, v1alpha1.PreservationModeClusterIdentity)

		// Secrets for testing.
		originalSecret1, preservedSecret1 = generateSecret("secret1", testNamespace2, ownerRef2, v1alpha1.PreservationModeAll)
		originalSecret2, preservedSecret2 = generateSecret("secret2", testNamespace1, ownerRef1, v1alpha1.PreservationModeAll)
		originalSecret3, preservedSecret3 = generateSecret("secret3", testNamespace2, ownerRef2, v1alpha1.PreservationModeClusterIdentity)
		originalSecret4, preservedSecret4 = generateSecret("secret4", testNamespace1, ownerRef1, v1alpha1.PreservationModeClusterIdentity)

		// ClusterInstance internal preservation data
		originalPullSecret1, preservedPullSecret1 = generateSecret("pull-secret1", testNamespace1, ownerRef1, PreservationModeInternal)
		originalPullSecret2, preservedPullSecret2 = generateSecret("pull-secret2", testNamespace2, ownerRef2, PreservationModeInternal)

		// Collect ConfigMaps and Secrets
		originalConfigMaps = append([]*corev1.ConfigMap(nil), originalCM1, originalCM2, originalCM3, originalCM4)
		preservedConfigMaps = append([]*corev1.Secret(nil), preservedCM1, preservedCM2, preservedCM3, preservedCM4)
		originalSecrets = append([]*corev1.Secret(nil), originalSecret1, originalSecret2, originalSecret3, originalSecret4, originalPullSecret1, originalPullSecret2)
		preservedSecrets = append([]*corev1.Secret(nil), preservedSecret1, preservedSecret2, preservedSecret3, preservedSecret4, preservedPullSecret1, preservedPullSecret2)
	})

	Describe("Backup", func() {

		BeforeEach(func() {
			// Create the original ConfigMaps and Secrets
			for _, cm := range originalConfigMaps {
				Expect(c.Create(ctx, cm)).To(Succeed())
			}
			for _, secret := range originalSecrets {
				Expect(c.Create(ctx, secret)).To(Succeed())
			}
		})

		It("successfully backups all labeled ConfigMaps and Secrets when preservationMode is All", func() {
			clusterInstance := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testNamespace1,
					Namespace: testNamespace1,
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					Reinstall: &v1alpha1.ReinstallSpec{
						PreservationMode: v1alpha1.PreservationModeAll,
						Generation:       testReinstallGeneration,
					},
				},
			}

			err := Backup(ctx, c, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())

			expectedBackupCMs := []client.Object{preservedCM2, preservedCM4}
			expectedBackupSecrets := []client.Object{preservedSecret2, preservedSecret4, preservedPullSecret1}
			verifyBackupFunctionality(ctx, c, preservedConfigMaps, preservedSecrets, expectedBackupCMs, expectedBackupSecrets)
		})

		It("successfully backups only cluster identity ConfigMaps and Secrets when preservationMode is ClusterIdentity", func() {
			clusterInstance := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testNamespace1,
					Namespace: testNamespace1,
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					Reinstall: &v1alpha1.ReinstallSpec{
						PreservationMode: v1alpha1.PreservationModeClusterIdentity,
						Generation:       testReinstallGeneration,
					},
				},
			}

			err := Backup(ctx, c, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())

			expectedBackupCMs := []client.Object{preservedCM4}
			expectedBackupSecrets := []client.Object{preservedSecret4, preservedPullSecret1}
			verifyBackupFunctionality(ctx, c, preservedConfigMaps, preservedSecrets, expectedBackupCMs, expectedBackupSecrets)
		})

		It("does not backup any data when preservationMode is None", func() {
			clusterInstance := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testNamespace1,
					Namespace: testNamespace1,
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					Reinstall: &v1alpha1.ReinstallSpec{
						PreservationMode: v1alpha1.PreservationModeNone,
						Generation:       testReinstallGeneration,
					},
				},
			}

			err := Backup(ctx, c, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())

			expectedBackupCMs := []client.Object(nil)
			expectedBackupSecrets := []client.Object(nil)
			verifyBackupFunctionality(ctx, c, preservedConfigMaps, preservedSecrets, expectedBackupCMs, expectedBackupSecrets)
		})

	})

	Describe("Restore", func() {

		BeforeEach(func() {
			// Create the preserved ConfigMaps and Secrets
			for _, cm := range preservedConfigMaps {
				Expect(c.Create(ctx, cm)).To(Succeed())
			}
			for _, secret := range preservedSecrets {
				Expect(c.Create(ctx, secret)).To(Succeed())
			}
		})

		It("successfully restore all labeled ConfigMaps and Secrets when preservationMode is All", func() {
			clusterInstance := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testNamespace2,
					Namespace: testNamespace2,
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					Reinstall: &v1alpha1.ReinstallSpec{
						PreservationMode: v1alpha1.PreservationModeAll,
						Generation:       testReinstallGeneration,
					},
				},
			}

			err := Restore(ctx, c, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())

			expectedRestoredCMs := []client.Object{originalCM1, originalCM3}
			expectedRestoredSecrets := []client.Object{originalSecret1, originalSecret3, originalPullSecret2}
			verifyRestoreFunctionality(ctx, c, originalConfigMaps, originalSecrets, expectedRestoredCMs, expectedRestoredSecrets)
		})

		It("successfully restore only cluster identity ConfigMaps and Secrets when preservationMode is ClusterIdentity", func() {
			clusterInstance := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testNamespace1,
					Namespace: testNamespace1,
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					Reinstall: &v1alpha1.ReinstallSpec{
						PreservationMode: v1alpha1.PreservationModeClusterIdentity,
						Generation:       testReinstallGeneration,
					},
				},
			}

			err := Restore(ctx, c, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())

			expectedRestoredCMs := []client.Object{originalCM4}
			expectedRestoredSecrets := []client.Object{originalSecret4, originalPullSecret1}
			verifyRestoreFunctionality(ctx, c, originalConfigMaps, originalSecrets, expectedRestoredCMs, expectedRestoredSecrets)
		})

		It("does not restore any data when preservationMode is None", func() {
			clusterInstance := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testNamespace1,
					Namespace: testNamespace1,
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					Reinstall: &v1alpha1.ReinstallSpec{
						PreservationMode: v1alpha1.PreservationModeNone,
						Generation:       testReinstallGeneration,
					},
				},
			}

			err := Restore(ctx, c, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())

			expectedRestoredCMs := []client.Object(nil)
			expectedRestoredSecrets := []client.Object(nil)
			verifyRestoreFunctionality(ctx, c, originalConfigMaps, originalSecrets, expectedRestoredCMs, expectedRestoredSecrets)
		})

	})
})

var _ = Describe("Cleanup", func() {
	var (
		ctx             context.Context
		c               client.Client
		testLogger      *zap.Logger
		deletionHandler *deletion.DeletionHandler

		// ConfigMap resources
		cm1, cm2, cm3, cm4 *corev1.ConfigMap
		// Secret resources
		secret1, secret2, secret3, secret4 *corev1.Secret
		// Objects
		objects []client.Object

		clusterInstance *v1alpha1.ClusterInstance
	)
	BeforeEach(func() {
		ctx = context.Background()
		testLogger = zap.NewNop().Named("Test")

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: testNamespace1,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				Reinstall: &v1alpha1.ReinstallSpec{
					Generation:       "1",
					PreservationMode: v1alpha1.PreservationModeAll,
				},
			},
		}

		pLabel := map[string]string{
			preservedDataLabelKey: testTimestamp,
			ci.OwnedByLabel:       ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name),
		}
		otherLabel := map[string]string{
			"foo": "bar",
		}

		cm1 = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm1", Namespace: testNamespace1, Labels: pLabel}}
		cm2 = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm2", Namespace: testNamespace1, Labels: otherLabel}}
		cm3 = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm3", Namespace: testNamespace2, Labels: pLabel}}
		cm4 = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm4", Namespace: testNamespace2, Labels: otherLabel}}

		secret1 = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret1", Namespace: testNamespace1, Labels: pLabel}}
		secret2 = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret2", Namespace: testNamespace1, Labels: otherLabel}}
		secret3 = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret3", Namespace: testNamespace2, Labels: pLabel}}
		secret4 = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret4", Namespace: testNamespace2, Labels: otherLabel}}

		objects = []client.Object{cm1, cm2, cm3, cm4, secret1, secret2, secret3, secret4}
	})

	It("successfully deletes all preserved ConfigMaps and Secrets in a given namespace", func() {
		c = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).
			WithObjects(objects...).
			Build()

		deletionHandler = &deletion.DeletionHandler{
			Client: c,
			Logger: testLogger,
		}

		// Ensure resources exist before attempting Cleanup
		for _, obj := range objects {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
		}

		// First call to cleanup should return no errors with cleanup waiting for resources to be deleted
		cleanupCompleted, err := Cleanup(ctx, c, deletionHandler, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(cleanupCompleted).To(BeFalse())

		// Second call to cleanuo should result in confirmation of the deletion of preservation resources
		cleanupCompleted, err = Cleanup(ctx, c, deletionHandler, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(cleanupCompleted).To(BeTrue())

		// Verify correct resources are deleted
		for _, obj := range []client.Object{cm1, secret1} {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), obj)).ToNot(Succeed())
		}
		for _, obj := range []client.Object{cm2, cm3, cm4, secret2, secret3, secret4} {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
		}
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

		deletionHandler = &deletion.DeletionHandler{
			Client: c,
			Logger: testLogger,
		}

		completedDeletion, err := Cleanup(ctx, c, deletionHandler, testLogger, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(fakeErrorMsg))
		Expect(deleteCalled).To(BeTrue())
		Expect(completedDeletion).To(BeFalse())
	})

})

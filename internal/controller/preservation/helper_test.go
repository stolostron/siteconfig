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
	"fmt"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"

	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func ensureEquality(actual, expected client.Object) {
	// Check for equality
	sanitizeResourceMetadata(actual)
	sanitizeResourceMetadata(expected)
	Expect(equality.Semantic.DeepEqual(actual, expected)).To(BeTrue())
}

var _ = Describe("Test LabelSelector builders", func() {

	verifyLabelSelectorFn := func(mode v1alpha1.PreservationMode,
		labelSelectorBuilderFn func(v1alpha1.PreservationMode) (labels.Selector, error),
		expectedOutcome OmegaMatcher) {
		lSelector, err := labelSelectorBuilderFn(mode)
		Expect(err).NotTo(HaveOccurred())
		Expect(lSelector).To(expectedOutcome)
	}

	Describe("buildBackupLabelSelector", func() {

		labelSelectorBuilderFn := buildBackupLabelSelector

		It("errors and returns a nil label.selector for unknown preservationMode", func() {
			var mode v1alpha1.PreservationMode = "foobar"
			lSelector, err := buildBackupLabelSelector(mode)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unknown PreservationMode"))
			Expect(lSelector).To(BeNil())
		})

		It("returns a nil label.selector when preservationMode is None", func() {
			verifyLabelSelectorFn(v1alpha1.PreservationModeNone, labelSelectorBuilderFn, BeNil())
		})

		It("returns a label.selector for retrieving all preservation resources when preservationMode is All", func() {
			expected, err := labels.NewRequirement(v1alpha1.PreservationLabelKey, selection.Exists, nil)
			Expect(err).NotTo(HaveOccurred())
			verifyLabelSelectorFn(v1alpha1.PreservationModeAll, labelSelectorBuilderFn, Equal(labels.NewSelector().Add(*expected)))
		})

		It("returns a label.selector for retrieving all preservation resources when preservationMode is ClusterIdentity", func() {
			expected, err := labels.NewRequirement(v1alpha1.PreservationLabelKey, selection.Equals, []string{v1alpha1.ClusterIdentityLabelValue})
			Expect(err).NotTo(HaveOccurred())
			verifyLabelSelectorFn(v1alpha1.PreservationModeClusterIdentity, labelSelectorBuilderFn, Equal(labels.NewSelector().Add(*expected)))
		})

		It("returns a label.selector for retrieving all preservation resources when preservationMode is Internal", func() {
			expected, err := labels.NewRequirement(InternalPreservationLabelKey, selection.Equals, []string{InternalPreservationLabelValue})
			Expect(err).NotTo(HaveOccurred())
			verifyLabelSelectorFn(PreservationModeInternal, labelSelectorBuilderFn, Equal(labels.NewSelector().Add(*expected)))
		})

	})

	Describe("buildRestoreLabelSelector", func() {

		labelSelectorBuilderFn := buildRestoreLabelSelector

		It("errors and returns a nil label.selector for unknown preservationMode", func() {
			var mode v1alpha1.PreservationMode = "foobar"
			lSelector, err := buildBackupLabelSelector(mode)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unknown PreservationMode"))
			Expect(lSelector).To(BeNil())
		})

		It("returns a nil label.selector when preservationMode is None", func() {
			verifyLabelSelectorFn(v1alpha1.PreservationModeNone, labelSelectorBuilderFn, BeNil())
		})

		It("returns a label.selector for retrieving all preservation resources when preservationMode is All", func() {
			expected, err := labels.NewRequirement(preservedDataLabelKey, selection.Exists, nil)
			Expect(err).NotTo(HaveOccurred())
			verifyLabelSelectorFn(v1alpha1.PreservationModeAll, labelSelectorBuilderFn, Equal(labels.NewSelector().Add(*expected)))
		})

		It("returns a label.selector for retrieving all preservation resources when preservationMode is ClusterIdentity", func() {
			expected, err := labels.NewRequirement(preservedDataLabelKey, selection.Exists, nil)
			Expect(err).NotTo(HaveOccurred())
			verifyLabelSelectorFn(v1alpha1.PreservationModeClusterIdentity, labelSelectorBuilderFn, Equal(labels.NewSelector().Add(*expected)))
		})

		It("returns a label.selector for retrieving all preservation resources when preservationMode is Internal", func() {
			expected, err := labels.NewRequirement(preservedInternalDataLabelKey, selection.Exists, nil)
			Expect(err).NotTo(HaveOccurred())
			verifyLabelSelectorFn(PreservationModeInternal, labelSelectorBuilderFn, Equal(labels.NewSelector().Add(*expected)))
		})

	})

})

var _ = Describe("generateName", func() {
	It("should generate a name by appending the resourceType and generation to the base name", func() {
		Expect(generateBackupName(configMapResourceType, "foobar", "12345")).To(Equal("configmap-foobar-12345"))
		Expect(generateBackupName(secretResourceType, "foobar", "12345")).To(Equal("secret-foobar-12345"))
	})
})

var _ = Describe("resourcesForPreservation", func() {
	var (
		c   client.Client
		ctx context.Context

		cm1, cm2, cm3             *corev1.ConfigMap
		secret1, secret2, secret3 *corev1.Secret

		testLogger = zap.NewNop().Named("Test")

		namespaceUnderTest string
	)

	BeforeEach(func() {
		cm1 = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cm1",
				Namespace: testNamespace1,
				Labels:    map[string]string{"foo": "bar"},
			},
		}
		cm2 = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cm2",
				Namespace: testNamespace1,
				Labels:    map[string]string{"bar": "foo"},
			},
		}
		cm3 = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cm3",
				Namespace: testNamespace2,
				Labels:    map[string]string{"foo": "bar"},
			},
		}

		secret1 = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "secret1",
				Namespace: testNamespace1,
				Labels:    map[string]string{"bar": "foobar"},
			},
		}

		secret2 = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "secret2",
				Namespace: testNamespace1,
				Labels:    map[string]string{"foo": "bar"},
			},
		}

		secret3 = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "secret3",
				Namespace: testNamespace2,
				Labels:    map[string]string{"foo": "bar1"},
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(cm1, cm2, cm3, secret1, secret2, secret3).
			Build()
		ctx = context.Background()
	})

	retrieveDataAndEnsureEquality := func(namespace string, requirement *labels.Requirement, expectedResult *resources) {
		listOptions := &client.ListOptions{
			Namespace:     namespace,
			LabelSelector: labels.NewSelector().Add(*requirement),
		}
		// Retrieve data for restoration
		actual, err := resourcesForPreservation(ctx, c, testLogger, listOptions)
		Expect(err).NotTo(HaveOccurred())
		Expect(actual).To(Equal(expectedResult))
	}

	Context("ConfigMaps and Secrets exist in the given namespace and match the label.selector", func() {

		When("using testNamespace1", func() {

			BeforeEach(func() {
				namespaceUnderTest = testNamespace1
			})

			It("retrieves data with label.selector={foo:bar}", func() {
				requirement, err := labels.NewRequirement("foo", selection.Equals, []string{"bar"})
				Expect(err).NotTo(HaveOccurred())
				expected := &resources{
					configMaps: []string{"cm1"},
					secrets:    []string{"secret2"},
				}
				retrieveDataAndEnsureEquality(namespaceUnderTest, requirement, expected)
			})

			It("retrieves data with label.selector={bar:*}", func() {
				requirement, err := labels.NewRequirement("bar", selection.Exists, nil)
				Expect(err).NotTo(HaveOccurred())
				expected := &resources{
					configMaps: []string{"cm2"},
					secrets:    []string{"secret1"},
				}
				retrieveDataAndEnsureEquality(namespaceUnderTest, requirement, expected)
			})

		})

		When("using testNamespace2", func() {

			BeforeEach(func() {
				namespaceUnderTest = testNamespace2
			})

			It("retrieves data with label.selector={foo:*}", func() {
				requirement, err := labels.NewRequirement("foo", selection.Exists, nil)
				Expect(err).NotTo(HaveOccurred())
				expected := &resources{
					configMaps: []string{"cm3"},
					secrets:    []string{"secret3"},
				}
				retrieveDataAndEnsureEquality(namespaceUnderTest, requirement, expected)
			})
		})
	})

	Context("ConfigMaps and Secrets do not exist in the given namespace or do not match the label.selector", func() {

		When("using a namespace with no ConfigMaps or Secrets but matching label.selector", func() {

			BeforeEach(func() {
				namespaceUnderTest = "non-existent"
			})

			It("returns an empty Data object", func() {
				requirement, err := labels.NewRequirement("foo", selection.Equals, []string{"bar"})
				Expect(err).NotTo(HaveOccurred())
				expected := &resources{
					configMaps: nil,
					secrets:    nil,
				}
				retrieveDataAndEnsureEquality(namespaceUnderTest, requirement, expected)
			})
		})

		When("using a namespace with ConfigMaps and Secrets but no matching label.selector", func() {

			BeforeEach(func() {
				namespaceUnderTest = testNamespace1
			})

			It("returns an empty Data object", func() {
				requirement, err := labels.NewRequirement("foo", selection.Equals, []string{"do-not-match"})
				Expect(err).NotTo(HaveOccurred())
				expected := &resources{
					configMaps: nil,
					secrets:    nil,
				}
				retrieveDataAndEnsureEquality(namespaceUnderTest, requirement, expected)
			})
		})
	})

})

var _ = Describe("fetchAndValidateObjectToRestore", func() {
	var (
		ctx        context.Context
		c          client.Client
		testLogger *zap.Logger
		testConfig config
		object     *corev1.ConfigMap
	)

	BeforeEach(func() {
		ctx = context.Background()
		testLogger = zap.NewNop().Named("Test")
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		testConfig = config{
			resourceKey:         types.NamespacedName{Namespace: testNamespace1, Name: "test-configmap"},
			ownerRef:            "test-owner",
			reinstallGeneration: testReinstallGeneration,
			preservationMode:    v1alpha1.PreservationModeClusterIdentity,
		}

		object = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-configmap",
				Namespace: testNamespace1,
				Labels: map[string]string{
					ci.OwnedByLabel: "test-owner",
				},
				Annotations: map[string]string{
					reinstallGenerationAnnotationKey: testReinstallGeneration,
					v1alpha1.PreservationLabelKey:    "ClusterIdentity",
				},
			},
		}
	})

	It("should return an error when the object is not found", func() {
		skipRestore, err := fetchAndValidateObjectToRestore(ctx, c, testLogger, object, testConfig)
		Expect(skipRestore).To(BeTrue())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to retrieve resource"))
	})

	It("should return an error when ownership verification fails", func() {
		// Create a ConfigMap with a different owner label
		object.Labels = map[string]string{
			ci.OwnedByLabel: "different-owner",
		}
		Expect(c.Create(ctx, object)).To(Succeed())

		skipRestore, err := fetchAndValidateObjectToRestore(ctx, c, testLogger, object, testConfig)
		Expect(skipRestore).To(BeTrue())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("is not the owner of object"))
	})

	It("should return an error when the reinstall generation annotation is missing", func() {
		// Create a ConfigMap without the reinstall generation annotation
		object.Annotations = map[string]string{v1alpha1.PreservationLabelKey: "ClusterIdentity"}
		Expect(c.Create(ctx, object)).To(Succeed())

		skipRestore, err := fetchAndValidateObjectToRestore(ctx, c, testLogger, object, testConfig)
		Expect(skipRestore).To(BeTrue())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("missing the '%s' annotation", reinstallGenerationAnnotationKey)))
	})

	It("should skip restore when the preservation mode is None", func() {
		// Create a ConfigMap with a preservation mode but set config to None
		testConfig.preservationMode = v1alpha1.PreservationModeNone
		Expect(c.Create(ctx, object)).To(Succeed())

		skipRestore, err := fetchAndValidateObjectToRestore(ctx, c, testLogger, object, testConfig)
		Expect(skipRestore).To(BeTrue())
		Expect(err).NotTo(HaveOccurred())
	})

	It("should return nil error and skipRestore=false when validation succeeds", func() {
		// Create a valid ConfigMap that matches the config
		Expect(c.Create(ctx, object)).To(Succeed())

		skipRestore, err := fetchAndValidateObjectToRestore(ctx, c, testLogger, object, testConfig)
		Expect(skipRestore).To(BeFalse())
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("sanitizeResourceMetadata", func() {
	It("should remove CreationTimestamp, OwnerReferences, ResourceVersion, and UID", func() {

		// Create a sample ConfigMap with metadata
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "cm1",
				Namespace:         testNamespace1,
				CreationTimestamp: metav1.Now(),
				ResourceVersion:   "1",
				UID:               "abc-123",
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "v1",
						Kind:       "Foobar",
						Name:       "foobar",
					},
				},
			},
		}
		sanitizeResourceMetadata(obj)

		Expect(obj.GetCreationTimestamp().Time.IsZero()).To(BeTrue(), "CreationTimestamp should be zero")
		Expect(obj.GetOwnerReferences()).To(BeNil(), "OwnerReferences should be nil")
		Expect(obj.GetResourceVersion()).To(BeEmpty(), "ResourceVersion should be empty")
		Expect(obj.GetUID()).To(BeEmpty(), "UID should be empty")
	})
})

var _ = Describe("createResource", func() {
	var (
		ctx        context.Context
		c          client.Client // Assuming a mock client is used
		testConfig config
		obj        *corev1.ConfigMap
	)

	BeforeEach(func() {
		ctx = context.Background()

		testConfig = config{
			resourceKey:         types.NamespacedName{Name: "cm1", Namespace: testNamespace1},
			resourceType:        configMapResourceType,
			ownerRef:            "test-owner",
			reinstallGeneration: testReinstallGeneration,
			preservationMode:    v1alpha1.PreservationModeAll,
		}

		obj = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cm1",
				Namespace: testNamespace1,
			},
		}
	})

	It("should add labels and annotations to the resource and create it", func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		err := createResource(ctx, c, obj, testConfig)
		Expect(err).ToNot(HaveOccurred())

		labels := obj.GetLabels()
		annotations := obj.GetAnnotations()

		Expect(labels).To(HaveKey(preservedDataLabelKey), "preservedDataLabelKey should be set")
		Expect(labels[ci.OwnedByLabel]).To(Equal(testConfig.ownerRef), "OwnedByLabel should match ownerRef")

		Expect(annotations).To(HaveKey(v1alpha1.PreservationLabelKey), "PreservationLabelKey should be set")
		Expect(annotations[v1alpha1.PreservationLabelKey]).To(Equal(string(testConfig.preservationMode)))
		Expect(annotations).To(HaveKey(resourceTypeAnnotationKey), "resourceTypeAnnotationKey should be set")
		Expect(annotations[resourceTypeAnnotationKey]).To(Equal(string(configMapResourceType)))
		Expect(annotations).To(HaveKey(reinstallGenerationAnnotationKey), "reinstallGenerationAnnotationKey should be set")
		Expect(annotations[reinstallGenerationAnnotationKey]).To(Equal(testConfig.reinstallGeneration))
	})

	It("should return an error if the client fails to create the resource", func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					return fmt.Errorf("inject error")
				},
			}).
			Build()

		err := createResource(ctx, c, obj, testConfig)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to create"))
	})
})

var _ = Describe("Backup Restore Functionality", func() {

	var (
		ctx        context.Context
		c          client.Client
		testLogger *zap.Logger

		namespace           = testNamespace1
		originalName        = "original-resource"
		ownerRef            = "foobar"
		reinstallGeneration = testReinstallGeneration
		mode                = v1alpha1.PreservationModeClusterIdentity

		nonExistentKey = types.NamespacedName{Namespace: namespace, Name: "non-existent"}

		secretType = corev1.SecretTypeOpaque

		originalConfigMap  *corev1.ConfigMap
		preservedConfigMap *corev1.Secret

		originalSecret, preservedSecret *corev1.Secret

		testConfig config
	)

	BeforeEach(func() {
		ctx = context.Background()
		testLogger = zap.NewNop().Named("Test")

		originalConfigMap, preservedConfigMap = generateConfigMap(originalName, namespace, ownerRef, mode)
		originalSecret, preservedSecret = generateSecret(originalName, namespace, ownerRef, mode)

	})

	Describe("backupResource", func() {
		BeforeEach(func() {
			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(originalConfigMap, originalSecret).
				Build()
		})

		Context("Test backing-up ConfigMap", func() {

			BeforeEach(func() {
				testConfig = config{
					resourceType:        configMapResourceType,
					ownerRef:            ownerRef,
					reinstallGeneration: reinstallGeneration,
					preservationMode:    mode,
				}
			})

			It("should successfully create a backup ConfigMap", func() {
				testConfig.resourceKey = client.ObjectKeyFromObject(originalConfigMap)

				err := backupResource(ctx, c, testLogger, preservedConfigMap.Name, testConfig)
				Expect(err).ToNot(HaveOccurred())

				backedupConfigMap := &corev1.Secret{}
				err = c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: preservedConfigMap.Name}, backedupConfigMap)
				Expect(err).ToNot(HaveOccurred())
				Expect(backedupConfigMap.GetAnnotations()).To(HaveKeyWithValue(reinstallGenerationAnnotationKey, reinstallGeneration))
				Expect(backedupConfigMap.GetLabels()).To(HaveKeyWithValue(ci.OwnedByLabel, ownerRef))
				Expect(backedupConfigMap.GetLabels()).To(HaveKey(preservedDataLabelKey))
				// Set the preservedLabel value to match the preservedConfigMap value to test for equality
				backedupConfigMap.Labels[preservedDataLabelKey] = testTimestamp
				ensureEquality(backedupConfigMap, preservedConfigMap)
			})

			It("should return an error if the original ConfigMap is not found", func() {
				testConfig.resourceKey = nonExistentKey

				err := backupResource(ctx, c, testLogger, preservedConfigMap.Name, testConfig)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to retrieve ConfigMap"))
			})
		})

		Context("Test backing-up Secret", func() {

			BeforeEach(func() {
				testConfig = config{
					resourceType:        secretResourceType,
					ownerRef:            ownerRef,
					reinstallGeneration: reinstallGeneration,
					preservationMode:    mode,
				}
			})

			It("should successfully create a backup Secret", func() {
				testConfig.resourceKey = client.ObjectKeyFromObject(originalSecret)

				err := backupResource(ctx, c, testLogger, preservedSecret.Name, testConfig)
				Expect(err).ToNot(HaveOccurred())

				backedUpSecret := &corev1.Secret{}
				err = c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: preservedSecret.Name}, backedUpSecret)
				Expect(err).ToNot(HaveOccurred())
				Expect(backedUpSecret.GetAnnotations()).To(HaveKeyWithValue(reinstallGenerationAnnotationKey, reinstallGeneration))
				Expect(backedUpSecret.GetLabels()).To(HaveKeyWithValue(ci.OwnedByLabel, ownerRef))
				Expect(backedUpSecret.GetLabels()).To(HaveKey(preservedDataLabelKey))

				// Set the preservedLabel value to match the preservedSecret value to test for equality
				backedUpSecret.Labels[preservedDataLabelKey] = testTimestamp
				ensureEquality(backedUpSecret, preservedSecret)
			})

			It("should return an error if the original Secret is not found", func() {
				testConfig.resourceKey = nonExistentKey

				err := backupResource(ctx, c, testLogger, preservedSecret.Name, testConfig)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to retrieve Secret"))
			})

		})
	})

	Describe("restoreResource", func() {

		BeforeEach(func() {
			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(preservedConfigMap, preservedSecret).
				Build()
		})

		Context("Test restoring ConfigMap", func() {

			BeforeEach(func() {
				testConfig = config{
					resourceKey:         client.ObjectKeyFromObject(preservedConfigMap),
					ownerRef:            ownerRef,
					reinstallGeneration: reinstallGeneration,
					preservationMode:    mode,
				}
			})

			It("should successfully restore a ConfigMap", func() {
				err := restoreResource(ctx, c, testLogger, testConfig)
				Expect(err).ToNot(HaveOccurred())

				restoredConfigMap := &corev1.ConfigMap{}
				err = c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: originalName}, restoredConfigMap)
				Expect(err).ToNot(HaveOccurred())
				Expect(restoredConfigMap.Annotations).To(HaveKey(RestoredAtAnnotationKey))
				delete(restoredConfigMap.Annotations, RestoredAtAnnotationKey)
				ensureEquality(restoredConfigMap, originalConfigMap)
			})

			It("should not return an error if the ConfigMap to be restored exists, but instead update it", func() {
				// Create an existing "restored" ConfigMap.
				existingConfigMap := &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: corev1.SchemeGroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:            originalName,
						Namespace:       namespace,
						Annotations:     map[string]string{"test": "value"},
						Labels:          map[string]string{"abc": "def"},
						ResourceVersion: "1",
					},
					Data: map[string]string{"random": "foobar"},
				}

				c = fakeclient.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(preservedConfigMap, existingConfigMap).
					Build()

				err := restoreResource(ctx, c, testLogger, testConfig)

				// Validate that no error is returned.
				Expect(err).NotTo(HaveOccurred())

				actual := &corev1.ConfigMap{}
				err = c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: originalName}, actual)
				Expect(err).ToNot(HaveOccurred())
				Expect(actual.Annotations).To(HaveKey(RestoredAtAnnotationKey))
				delete(actual.Annotations, RestoredAtAnnotationKey)
				ensureEquality(actual, originalConfigMap)
			})

			It("should return an error if the preserved ConfigMap is not found", func() {
				testConfig.resourceKey = nonExistentKey

				err := restoreResource(ctx, c, testLogger, testConfig)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to retrieve resource"))
			})

			It("should return an error if the preserved ConfigMap contains invalid data", func() {
				preservedConfigMap.Data[preservedDataKey] = []byte("invalid-yaml:")
				c = fakeclient.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(preservedConfigMap).
					Build()

				err := restoreResource(ctx, c, testLogger, testConfig)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to restore preserved ConfigMap"))
			})
		})

		Context("Test restoring Secret", func() {

			BeforeEach(func() {
				testConfig = config{
					resourceKey:         client.ObjectKeyFromObject(preservedSecret),
					ownerRef:            ownerRef,
					reinstallGeneration: reinstallGeneration,
					preservationMode:    mode,
				}
			})

			It("should successfully restore a Secret", func() {
				err := restoreResource(ctx, c, testLogger, testConfig)
				Expect(err).ToNot(HaveOccurred())

				restoredSecret := &corev1.Secret{}
				err = c.Get(ctx, client.ObjectKeyFromObject(originalSecret), restoredSecret)
				Expect(err).ToNot(HaveOccurred())
				Expect(restoredSecret.Annotations).To(HaveKey(RestoredAtAnnotationKey))
				delete(restoredSecret.Annotations, RestoredAtAnnotationKey)
				ensureEquality(restoredSecret, originalSecret)
			})

			It("should not return an error if the Secret to be restored exists, but instead update it", func() {
				// Create an existing "restored" Secret.
				existingSecret := &corev1.Secret{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Secret",
						APIVersion: corev1.SchemeGroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:            originalName,
						Namespace:       namespace,
						Annotations:     map[string]string{"test": "value"},
						Labels:          map[string]string{"abc": "def"},
						ResourceVersion: "1",
					},
					Type: secretType,
					Data: map[string][]byte{},
				}

				c = fakeclient.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(preservedSecret, existingSecret).
					Build()

				err := restoreResource(ctx, c, testLogger, testConfig)
				// Validate that no error is returned.
				Expect(err).NotTo(HaveOccurred())

				actual := &corev1.Secret{}
				err = c.Get(ctx, client.ObjectKeyFromObject(originalSecret), actual)
				Expect(err).ToNot(HaveOccurred())
				Expect(actual.Annotations).To(HaveKey(RestoredAtAnnotationKey))
				delete(actual.Annotations, RestoredAtAnnotationKey)
				ensureEquality(actual, originalSecret)
			})

			It("should return an error if the preserved Secret is not found", func() {
				testConfig.resourceKey = nonExistentKey

				err := restoreResource(ctx, c, testLogger, testConfig)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to retrieve resource"))
			})

			It("should return an error if the preserved Secret contains invalid data", func() {
				preservedSecret.Data[preservedDataKey] = []byte("invalid-yaml")
				c = fakeclient.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(preservedSecret).Build()

				err := restoreResource(ctx, c, testLogger, testConfig)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to unmarshal"))
			})

		})

	})

})

var _ = Describe("classifyPreservedResource", func() {
	namespace := testNamespace1

	It("should classify a preserved cluster-identity resource correctly", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "preserved-cluster-identity",
				Namespace: namespace,
				Annotations: map[string]string{
					v1alpha1.PreservationLabelKey: string(v1alpha1.PreservationModeClusterIdentity),
				},
			},
		}
		Expect(classifyPreservedResource(secret)).To(Equal(resourceCategoryClusterIdentity))
	})

	It("should classify a preserved non-cluster-identity resource correctly", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "preserved-other",
				Namespace: namespace,
				Annotations: map[string]string{
					v1alpha1.PreservationLabelKey: string(v1alpha1.PreservationModeAll),
				},
			},
		}
		Expect(classifyPreservedResource(secret)).To(Equal(resourceCategoryOther))
	})

	It("should classify an internal resource as unknown", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "internal-resource",
				Namespace: namespace,
			},
		}
		Expect(classifyPreservedResource(secret)).To(Equal(resourceCategoryUnknown))
	})
})

var _ = Describe("classifyRestoredResource", func() {
	namespace := testNamespace1

	It("should classify a restored cluster-identity resource correctly", func() {
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "restored-cluster-identity",
				Namespace: namespace,
				Labels: map[string]string{
					v1alpha1.PreservationLabelKey: string(v1alpha1.ClusterIdentityLabelValue),
				},
				Annotations: map[string]string{
					RestoredAtAnnotationKey: "some-timestamp",
				},
			},
		}
		Expect(classifyRestoredResource(configMap)).To(Equal(resourceCategoryClusterIdentity))
	})

	It("should classify a restored non-cluster-identity resource correctly", func() {
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "restored-other",
				Namespace: namespace,
				Labels: map[string]string{
					v1alpha1.PreservationLabelKey: string(v1alpha1.PreservationModeAll),
				},
				Annotations: map[string]string{
					RestoredAtAnnotationKey: "some-timestamp",
				},
			},
		}
		Expect(classifyRestoredResource(configMap)).To(Equal(resourceCategoryOther))
	})

	It("should classify an internal resource or non-restored resource as unknown", func() {
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "non-restored-resource",
				Namespace: namespace,
			},
		}
		Expect(classifyRestoredResource(configMap)).To(Equal(resourceCategoryUnknown))
	})
})

var _ = Describe("countMatchingResources", func() {

	var (
		ctx           context.Context
		c             client.Client
		namespace     = testNamespace1
		labelSelector labels.Selector
	)

	BeforeEach(func() {
		ctx = context.Background()
		c = fakeclient.
			NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()
		labelSelector = labels.Everything() // Matches all resources
	})

	It("should count preserved resources correctly", func() {
		secret1 := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "preserved-cluster-id",
				Namespace: namespace,
				Annotations: map[string]string{
					v1alpha1.PreservationLabelKey: string(v1alpha1.PreservationModeClusterIdentity),
				},
			},
		}

		secret2 := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "preserved-other",
				Namespace: namespace,
				Annotations: map[string]string{
					v1alpha1.PreservationLabelKey: string(v1alpha1.PreservationModeAll),
				},
			},
		}

		c = fakeclient.
			NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(secret1, secret2).
			Build()

		clusterIDCount, otherCount, err := countMatchingResources(ctx, c, namespace, labelSelector, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterIDCount).To(Equal(1))
		Expect(otherCount).To(Equal(1))
	})

	It("should count restored resources correctly", func() {
		secret1 := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "restored-cluster-id",
				Namespace: namespace,
				Labels: map[string]string{
					v1alpha1.PreservationLabelKey: string(v1alpha1.ClusterIdentityLabelValue),
				},
				Annotations: map[string]string{
					RestoredAtAnnotationKey: "some-timestamp",
				},
			},
		}

		configMap1 := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "restored-cluster-id",
				Namespace: namespace,
				Labels: map[string]string{
					v1alpha1.PreservationLabelKey: string(v1alpha1.ClusterIdentityLabelValue),
				},
				Annotations: map[string]string{
					RestoredAtAnnotationKey: "some-timestamp",
				},
			},
		}

		configMap2 := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "restored-other",
				Namespace: namespace,
				Labels: map[string]string{
					v1alpha1.PreservationLabelKey: string(v1alpha1.PreservationModeAll),
				},
				Annotations: map[string]string{
					RestoredAtAnnotationKey: "some-timestamp",
				},
			},
		}

		c = fakeclient.
			NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(secret1, configMap1, configMap2).
			Build()

		clusterIDCount, otherCount, err := countMatchingResources(ctx, c, namespace, labelSelector, true)
		Expect(err).To(BeNil())
		Expect(clusterIDCount).To(Equal(2))
		Expect(otherCount).To(Equal(1))
	})

	It("should return zero counts if no matching resources exist", func() {
		c = fakeclient.
			NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		clusterIDCount, otherCount, err := countMatchingResources(ctx, c, namespace, labelSelector, false)
		Expect(err).To(BeNil())
		Expect(clusterIDCount).To(Equal(0))
		Expect(otherCount).To(Equal(0))
	})
})

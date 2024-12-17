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

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("retrievePreservationData", func() {
	var (
		c   client.Client
		ctx context.Context

		cm1, cm2, cm3             *corev1.ConfigMap
		secret1, secret2, secret3 *corev1.Secret

		testLogger = zap.NewNop().Named("Test")
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

	retrieveDataAndEnsureEquality := func(namespace string, requirement *labels.Requirement, expectedResult *Data) {
		listOptions := &client.ListOptions{
			Namespace:     namespace,
			LabelSelector: labels.NewSelector().Add(*requirement),
		}
		// Retrieve data for restoration
		actual, err := retrievePreservationData(ctx, c, listOptions, testLogger)
		Expect(err).NotTo(HaveOccurred())
		Expect(actual).To(Equal(expectedResult))
	}

	Context("ConfigMaps and Secrets exist in the given namespace and match the label.selector", func() {

		When("using testNamespace1", func() {
			namespace := testNamespace1
			It("retrieves data with label.selector={foo:bar}", func() {
				requirement, err := labels.NewRequirement("foo", selection.Equals, []string{"bar"})
				Expect(err).NotTo(HaveOccurred())
				expected := &Data{
					ConfigMaps: []string{"cm1"},
					Secrets:    []string{"secret2"},
				}
				retrieveDataAndEnsureEquality(namespace, requirement, expected)
			})

			It("retrieves data with label.selector={bar:*}", func() {
				requirement, err := labels.NewRequirement("bar", selection.Exists, nil)
				Expect(err).NotTo(HaveOccurred())
				expected := &Data{
					ConfigMaps: []string{"cm2"},
					Secrets:    []string{"secret1"},
				}
				retrieveDataAndEnsureEquality(namespace, requirement, expected)
			})

		})

		When("using testNamespace2", func() {
			namespace := testNamespace2
			It("retrieves data with label.selector={foo:*}", func() {
				requirement, err := labels.NewRequirement("foo", selection.Exists, nil)
				Expect(err).NotTo(HaveOccurred())
				expected := &Data{
					ConfigMaps: []string{"cm3"},
					Secrets:    []string{"secret3"},
				}
				retrieveDataAndEnsureEquality(namespace, requirement, expected)
			})
		})
	})

	Context("ConfigMaps and Secrets do not exist in the given namespace or do not match the label.selector", func() {

		When("using a namespace with no ConfigMaps or Secrets but matching label.selector", func() {
			namespace := "non-existent"
			It("returns an empty Data object", func() {
				requirement, err := labels.NewRequirement("foo", selection.Equals, []string{"bar"})
				Expect(err).NotTo(HaveOccurred())
				expected := &Data{
					ConfigMaps: nil,
					Secrets:    nil,
				}
				retrieveDataAndEnsureEquality(namespace, requirement, expected)
			})
		})

		When("using a namespace with ConfigMaps and Secrets but no matching label.selector", func() {
			namespace := testNamespace1
			It("returns an empty Data object", func() {
				requirement, err := labels.NewRequirement("foo", selection.Equals, []string{"do-not-match"})
				Expect(err).NotTo(HaveOccurred())
				expected := &Data{
					ConfigMaps: nil,
					Secrets:    nil,
				}
				retrieveDataAndEnsureEquality(namespace, requirement, expected)
			})
		})
	})

})

var _ = Describe("generateName", func() {
	It("should generate a name by appending the generation to the base name", func() {
		Expect(generateName("configmap", "12345")).To(Equal("configmap-12345"))
	})
})

var _ = Describe("addPreservedLabel", func() {
	It("should add a preserved label to an empty map", func() {
		labels := addPreservedLabel(nil)
		Expect(labels).To(HaveKey(preservedLabel))
		Expect(labels[preservedLabel]).ToNot(BeEmpty())
	})

	It("should add a preserved label to an existing map", func() {
		labels := map[string]string{"existing-label": "value"}
		updatedLabels := addPreservedLabel(labels)
		Expect(updatedLabels).To(HaveKey(preservedLabel))
		Expect(updatedLabels["existing-label"]).To(Equal("value"))
	})
})

var _ = Describe("removePreservedLabel", func() {
	It("should remove the preserved label from a map", func() {
		labels := map[string]string{preservedLabel: "some-timestamp", "key": "value"}
		updatedLabels := removePreservedLabel(labels)
		Expect(updatedLabels).ToNot(HaveKey(preservedLabel))
		Expect(updatedLabels).To(HaveKey("key"))
	})

	It("should return nil if the input map is nil", func() {
		Expect(removePreservedLabel(nil)).To(BeNil())
	})
})

var _ = Describe("Preservation Helper Functions", func() {

	var (
		ctx           context.Context
		client        client.Client
		namespace     = "test-namespace"
		originalName  = "original-resource"
		preservedName = "preserved-resource"
		annotations   map[string]string
		labels        map[string]string
		secretType    = corev1.SecretTypeOpaque
	)

	BeforeEach(func() {
		ctx = context.Background()
		annotations = map[string]string{
			"test-annotation": "test-value",
		}
		labels = map[string]string{
			"test-label": "test-value",
		}
	})

	Describe("Preservation Backup Functions", func() {
		Describe("backupConfigMap", func() {
			var (
				originalConfigMap *corev1.ConfigMap
			)

			BeforeEach(func() {
				originalConfigMap = &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:        originalName,
						Namespace:   namespace,
						Annotations: annotations,
						Labels:      labels,
					},
					Data: map[string]string{
						"foo": "bar",
					},
				}
				client = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(originalConfigMap).Build()
			})

			It("should successfully create a backup ConfigMap", func() {
				err := backupConfigMap(ctx, client, namespace, originalName, preservedName)
				Expect(err).ToNot(HaveOccurred())

				preserved := &corev1.ConfigMap{}
				err = client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: preservedName}, preserved)
				Expect(err).ToNot(HaveOccurred())
				Expect(preserved.Annotations).To(Equal(originalConfigMap.Annotations))
				Expect(preserved.Labels).To(HaveKey(preservedLabel))
				Expect(preserved.Data).To(HaveKey(originalName))

				var restoredData map[string]string
				err = json.Unmarshal([]byte(preserved.Data[originalName]), &restoredData)
				Expect(err).ToNot(HaveOccurred())
				Expect(restoredData).To(Equal(originalConfigMap.Data))
			})

			It("should return an error if the original ConfigMap is not found", func() {
				err := backupConfigMap(ctx, client, namespace, "non-existent", preservedName)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to retrieve ConfigMap"))
			})
		})

		Describe("backupSecret", func() {
			var (
				originalSecret *corev1.Secret
			)

			BeforeEach(func() {
				originalSecret = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:        originalName,
						Namespace:   namespace,
						Annotations: annotations,
						Labels:      labels,
					},
					Type: secretType,
					Data: map[string][]byte{
						"username": []byte("admin"),
						"password": []byte("admin123"),
					},
				}
				client = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(originalSecret).Build()
			})

			It("should successfully create a backup Secret", func() {
				err := backupSecret(ctx, client, namespace, originalName, preservedName)
				Expect(err).ToNot(HaveOccurred())

				preserved := &corev1.Secret{}
				err = client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: preservedName}, preserved)
				Expect(err).ToNot(HaveOccurred())
				Expect(preserved.Annotations).To(Equal(originalSecret.Annotations))
				Expect(preserved.Labels).To(HaveKey(preservedLabel))
				Expect(preserved.Type).To(Equal(secretType))
				Expect(preserved.Data).To(HaveKey(originalName))

				var restoredData map[string][]byte
				err = json.Unmarshal(preserved.Data[originalName], &restoredData)
				Expect(err).ToNot(HaveOccurred())
				Expect(restoredData).To(Equal(originalSecret.Data))
			})

			It("should return an error if the original Secret is not found", func() {
				err := backupSecret(ctx, client, namespace, "non-existent", preservedName)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to retrieve Secret"))
			})
		})
	})

	Describe("Preservation Restore Functions", func() {
		Describe("restoreConfigMap", func() {
			var (
				preservedConfigMap *corev1.ConfigMap
				data               map[string]string
			)

			BeforeEach(func() {
				data = map[string]string{
					"foo": "bar",
				}
				encodedData, _ := json.Marshal(data)
				preservedConfigMap = &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:        preservedName,
						Namespace:   namespace,
						Annotations: annotations,
						Labels:      labels,
					},
					Data: map[string]string{originalName: string(encodedData)},
				}
				client = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(preservedConfigMap).Build()
			})

			It("should successfully restore a ConfigMap", func() {
				err := restoreConfigMap(ctx, client, namespace, preservedName)
				Expect(err).ToNot(HaveOccurred())

				restored := &corev1.ConfigMap{}
				err = client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: originalName}, restored)
				Expect(err).ToNot(HaveOccurred())
				Expect(restored.Annotations).To(Equal(annotations))
				Expect(restored.Labels).To(Equal(labels))
				Expect(restored.Data).To(Equal(data))
			})

			It("should not return an error if the ConfigMap to be restored exists", func() {
				// Mock the existing restored ConfigMap with the same data.
				existingConfigMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:        originalName,
						Namespace:   namespace,
						Annotations: annotations,
						Labels:      labels,
					},
					Data: data,
				}

				client = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(preservedConfigMap, existingConfigMap).Build()

				err := restoreConfigMap(ctx, client, namespace, preservedName)
				// Validate that no error is returned.
				Expect(err).NotTo(HaveOccurred())
			})

			It("should return an error if the preserved ConfigMap is not found", func() {
				err := restoreConfigMap(ctx, client, namespace, "non-existent")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to retrieve ConfigMap"))
			})

			It("should return an error if the preserved ConfigMap contains invalid data", func() {
				preservedConfigMap.Data[originalName] = "invalid-json"
				client = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(preservedConfigMap).Build()

				err := restoreConfigMap(ctx, client, namespace, preservedName)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to unmarshal ConfigMap.Data"))
			})
		})

		Describe("restoreSecret", func() {
			var (
				data            map[string][]byte
				preservedSecret *corev1.Secret
			)

			BeforeEach(func() {
				data = map[string][]byte{
					"username": []byte("admin"),
					"password": []byte("admin123"),
				}
				encodedData, _ := json.Marshal(data)
				preservedSecret = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:        preservedName,
						Namespace:   namespace,
						Annotations: annotations,
						Labels:      labels,
					},
					Type: secretType,
					Data: map[string][]byte{originalName: encodedData},
				}
				client = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(preservedSecret).Build()
				ctx = context.Background()
			})

			It("should successfully restore a Secret", func() {
				err := restoreSecret(ctx, client, namespace, preservedName)
				Expect(err).ToNot(HaveOccurred())

				restored := &corev1.Secret{}
				err = client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: originalName}, restored)
				Expect(err).ToNot(HaveOccurred())
				Expect(restored.Annotations).To(Equal(annotations))
				Expect(restored.Labels).To(Equal(labels))
				Expect(restored.Type).To(Equal(secretType))
				Expect(restored.Data).To(Equal(data))
			})

			It("should not return an error if the Secret to be restored exists", func() {
				// Mock the existing restored Secret with the same data.
				existingSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:        originalName,
						Namespace:   namespace,
						Annotations: annotations,
					},
					Type: secretType,
					Data: data,
				}
				client = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(preservedSecret, existingSecret).Build()

				err := restoreSecret(ctx, client, namespace, preservedName)
				// Validate that no error is returned.
				Expect(err).NotTo(HaveOccurred())
			})

			It("should return an error if the preserved Secret is not found", func() {
				err := restoreSecret(ctx, client, namespace, "non-existent")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to retrieve Secret"))
			})

			It("should return an error if the preserved Secret contains invalid data", func() {
				preservedSecret.Data[originalName] = []byte("invalid-json")
				client = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(preservedSecret).Build()

				err := restoreSecret(ctx, client, namespace, preservedName)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to unmarshal Secret.Data"))
			})
		})

	})
})

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

package deletion

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	cierrors "github.com/stolostron/siteconfig/internal/controller/errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDeletion(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "DeletionSuite")
}

var _ = BeforeSuite(func() {
	Expect(v1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
})

func testFnGenerateObject(ctx context.Context, c client.Client,
	Name, Namespace, ownerRef, finalizer string, gvk schema.GroupVersionKind,
) (u *unstructured.Unstructured, o *ci.RenderedObject) {
	u = &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	u.SetName(Name)
	u.SetNamespace(Namespace)
	u.SetLabels(map[string]string{ci.OwnedByLabel: ownerRef})
	u.SetAnnotations(map[string]string{ci.WaveAnnotation: "0"})
	if finalizer != "" {
		u.SetFinalizers([]string{finalizer})
	}

	o = &ci.RenderedObject{}
	Expect(o.SetObject(u.Object)).To(Succeed())

	Expect(c.Create(ctx, u)).To(Succeed())
	Expect(c.Get(ctx, client.ObjectKeyFromObject(u), u)).To(Succeed())
	return
}

var _ = Describe("DeleteObject", func() {
	var (
		ctx             context.Context
		testLogger      *zap.Logger
		handler         *DeletionHandler
		c               client.Client
		clusterInstance *v1alpha1.ClusterInstance

		Name      = "test"
		Namespace = "default"

		gvk schema.GroupVersionKind
	)

	BeforeEach(func() {

		gvk = schema.GroupVersionKind{
			Group:   "",
			Version: "v1",
			Kind:    "ConfigMap",
		}
		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      Name,
				Namespace: Namespace,
			},
			Status: v1alpha1.ClusterInstanceStatus{
				ManifestsRendered: []v1alpha1.ManifestReference{
					{
						APIGroup:  ptr.To(gvk.Group + "/" + gvk.Version),
						Kind:      gvk.Kind,
						Name:      Name,
						Namespace: Namespace,
						SyncWave:  0,
						Status:    v1alpha1.ManifestRenderedSuccess,
					},
				},
			},
		}

		ctx = context.TODO()
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance).
			Build()
		testLogger = zap.NewNop().Named("Test")

		handler = &DeletionHandler{Client: c, Logger: testLogger}

	})

	It("should return true if the object does not exist", func() {

		ownerRef := ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name)

		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gvk)
		obj.SetName(Name)
		obj.SetNamespace(Namespace)
		obj.SetLabels(map[string]string{ci.OwnedByLabel: ownerRef})

		object := ci.RenderedObject{}
		Expect(object.SetObject(obj.Object)).To(Succeed())

		deleted, err := handler.DeleteObject(ctx, clusterInstance, object, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleted).To(BeTrue())
	})

	It("should return false if the object exists but ownership verification fails", func() {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gvk)
		obj.SetName(Name)
		obj.SetNamespace(Namespace)
		obj.SetLabels(map[string]string{ci.OwnedByLabel: "foobar"})

		object := ci.RenderedObject{}
		Expect(object.SetObject(obj.Object)).To(Succeed())

		Expect(c.Create(ctx, obj)).To(Succeed())

		deleted, err := handler.DeleteObject(ctx, clusterInstance, object, nil)
		Expect(err).To(HaveOccurred())
		Expect(cierrors.IsNotOwnedObject(err)).To(BeTrue())
		Expect(deleted).To(BeFalse())
	})

	It("should initiate deletion if the object exists and ownership is verified", func() {
		ownerRef := ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name)

		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gvk)
		obj.SetName(Name)
		obj.SetNamespace(Namespace)
		obj.SetLabels(map[string]string{ci.OwnedByLabel: ownerRef})

		object := ci.RenderedObject{}
		Expect(object.SetObject(obj.Object)).To(Succeed())

		Expect(c.Create(ctx, obj)).To(Succeed())

		deleted, err := handler.DeleteObject(ctx, clusterInstance, object, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleted).To(BeFalse())
	})

	It("should return an error if deletion times out", func() {

		testClient := fakeclient.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return c.Get(ctx, key, obj, opts...)
			},
			Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				return c.Create(ctx, obj, opts...)
			},
			Delete: func(ctx context.Context, fclient client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				return cierrors.NewDeletionTimeoutError("")
			},
		}).Build()

		handler = &DeletionHandler{Client: testClient, Logger: testLogger}

		ownerRef := ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name)

		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gvk)
		obj.SetName(Name)
		obj.SetNamespace(Namespace)
		obj.SetLabels(map[string]string{ci.OwnedByLabel: ownerRef})

		Expect(testClient.Create(ctx, obj)).To(Succeed())

		object := ci.RenderedObject{}
		Expect(object.SetObject(obj.Object)).To(Succeed())

		deleted, err := handler.DeleteObject(ctx, clusterInstance, object, nil)
		Expect(err).To(HaveOccurred())
		Expect(cierrors.IsDeletionTimeoutError(err)).To(BeTrue())
		Expect(deleted).To(BeFalse())
	})

	It("should return false and wait if the object is marked for deletion but not yet deleted", func() {
		ownerRef := ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name)

		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gvk)
		obj.SetName(Name)
		obj.SetNamespace(Namespace)
		obj.SetLabels(map[string]string{ci.OwnedByLabel: ownerRef})

		Expect(c.Create(ctx, obj)).To(Succeed())

		object := ci.RenderedObject{}
		Expect(object.SetObject(obj.Object)).To(Succeed())

		deleted, err := handler.DeleteObject(ctx, clusterInstance, object, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleted).To(BeFalse())
	})

})

var _ = Describe("DeleteObjects", func() {
	var (
		ctx             context.Context
		testLogger      *zap.Logger
		handler         *DeletionHandler
		c               client.Client
		clusterInstance *v1alpha1.ClusterInstance

		Name1     = "test1"
		Name2     = "test2"
		Namespace = "default"

		gvk schema.GroupVersionKind
	)

	BeforeEach(func() {
		gvk = schema.GroupVersionKind{
			Group:   "apps",
			Version: "v1",
			Kind:    "Deployment",
		}
		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      Name1,
				Namespace: Namespace,
			},
		}

		ctx = context.TODO()
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects().
			Build()
		testLogger = zap.NewNop().Named("Test")
		handler = &DeletionHandler{Client: c, Logger: testLogger}
	})

	It("should return true if all objects are successfully deleted", func() {
		clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
			ManifestsRendered: []v1alpha1.ManifestReference{
				{
					APIGroup:  ptr.To(gvk.Group + "/" + gvk.Version),
					Kind:      gvk.Kind,
					Name:      Name1,
					Namespace: Namespace,
					SyncWave:  0,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
				{
					APIGroup:  ptr.To(gvk.Group + "/" + gvk.Version),
					Kind:      gvk.Kind,
					Name:      Name2,
					Namespace: Namespace,
					SyncWave:  0,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		ownerRef := ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name)

		obj1, renderedObject1 := testFnGenerateObject(ctx, c, Name1, Namespace, ownerRef, "", gvk)
		obj2, renderedObject2 := testFnGenerateObject(ctx, c, Name2, Namespace, ownerRef, "", gvk)

		deleted, err := handler.DeleteObjects(
			ctx, clusterInstance, []ci.RenderedObject{*renderedObject1, *renderedObject2}, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleted).To(BeFalse())

		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
		Expect(clusterInstance.Status.ManifestsRendered).To(HaveLen(2))
		Expect(clusterInstance.Status.ManifestsRendered[0].Status).To(Equal(v1alpha1.ManifestDeletionInProgress))
		Expect(clusterInstance.Status.ManifestsRendered[1].Status).To(Equal(v1alpha1.ManifestDeletionInProgress))

		// Objects should get deleted, but will take another reconcile
		Expect(c.Get(ctx, client.ObjectKeyFromObject(obj1), obj1)).ToNot(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(obj2), obj2)).ToNot(Succeed())

		deleted, err = handler.DeleteObjects(
			ctx, clusterInstance, []ci.RenderedObject{*renderedObject1, *renderedObject2}, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleted).To(BeTrue())

		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
		Expect(clusterInstance.Status.ManifestsRendered).To(HaveLen(0))
	})

	It("should exclude objects specified in the excludeObjects list", func() {
		clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
			ManifestsRendered: []v1alpha1.ManifestReference{
				{
					APIGroup:  ptr.To(gvk.Group + "/" + gvk.Version),
					Kind:      gvk.Kind,
					Name:      Name1,
					Namespace: Namespace,
					SyncWave:  0,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
				{
					APIGroup:  ptr.To(gvk.Group + "/" + gvk.Version),
					Kind:      gvk.Kind,
					Name:      Name2,
					Namespace: Namespace,
					SyncWave:  0,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		ownerRef := ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name)

		obj1, renderedObject1 := testFnGenerateObject(ctx, c, Name1, Namespace, ownerRef, "", gvk)
		excludeObj, excludeRenderedObject := testFnGenerateObject(ctx, c, Name2, Namespace, ownerRef, "", gvk)

		deleted, err := handler.DeleteObjects(
			ctx, clusterInstance, []ci.RenderedObject{*renderedObject1, *excludeRenderedObject},
			[]ci.RenderedObject{*excludeRenderedObject}, nil)

		Expect(err).ToNot(HaveOccurred())
		Expect(deleted).To(BeFalse())

		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
		Expect(clusterInstance.Status.ManifestsRendered).To(HaveLen(2))
		Expect(clusterInstance.Status.ManifestsRendered[0].Name).To(Equal(Name1))
		Expect(clusterInstance.Status.ManifestsRendered[0].Status).To(Equal(v1alpha1.ManifestDeletionInProgress))
		Expect(clusterInstance.Status.ManifestsRendered[1].Name).To(Equal(Name2))
		Expect(clusterInstance.Status.ManifestsRendered[1].Status).To(Equal(v1alpha1.ManifestRenderedSuccess))

		// Object should get deleted, but will take another reconcile
		Expect(c.Get(ctx, client.ObjectKeyFromObject(obj1), obj1)).ToNot(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(excludeObj), excludeObj)).To(Succeed())

		deleted, err = handler.DeleteObjects(
			ctx, clusterInstance, []ci.RenderedObject{*renderedObject1, *excludeRenderedObject},
			[]ci.RenderedObject{*excludeRenderedObject}, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleted).To(BeTrue())

		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
		Expect(clusterInstance.Status.ManifestsRendered).To(HaveLen(1))
		Expect(clusterInstance.Status.ManifestsRendered[0].Name).To(Equal(Name2))
	})

	It("should return false if any object deletion fails", func() {

		testClient := fakeclient.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return c.Get(ctx, key, obj, opts...)
			},
			Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				return c.Create(ctx, obj, opts...)
			},
			Patch: func(ctx context.Context, client client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				return c.Patch(ctx, obj, patch, opts...)
			},
			Delete: func(ctx context.Context, fclient client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				return fmt.Errorf("induced error")
			},
			SubResourcePatch: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				return c.Status().Patch(ctx, obj, patch, opts...)
			},
		}).Build()

		handler = &DeletionHandler{Client: testClient, Logger: testLogger}

		clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
			ManifestsRendered: []v1alpha1.ManifestReference{
				{
					APIGroup:  ptr.To(gvk.Group + "/" + gvk.Version),
					Kind:      gvk.Kind,
					Name:      Name1,
					Namespace: Namespace,
					SyncWave:  0,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		ownerRef := ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name)

		_, renderedObject := testFnGenerateObject(ctx, testClient, Name1, Namespace, ownerRef, "", gvk)

		deleted, err := handler.DeleteObjects(
			ctx, clusterInstance, []ci.RenderedObject{*renderedObject}, nil, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("induced error"))
		Expect(deleted).To(BeFalse())

		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
		Expect(clusterInstance.Status.ManifestsRendered).To(HaveLen(1))
		Expect(clusterInstance.Status.ManifestsRendered[0].Status).To(Equal(v1alpha1.ManifestDeletionFailure))
		Expect(clusterInstance.Status.ManifestsRendered[0].Message).To(ContainSubstring("induced error"))
	})

	When("any object deletion fails and updating the ClusterInstance.Status fails", func() {
		It("should return false due to deletion failure and should reflect both deletion and update errors ", func() {

			testClient := fakeclient.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return c.Get(ctx, key, obj, opts...)
				},
				Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					return c.Create(ctx, obj, opts...)
				},
				Patch: func(ctx context.Context, client client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					return c.Patch(ctx, obj, patch, opts...)
				},
				Delete: func(ctx context.Context, fclient client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					return fmt.Errorf("induced deletion error")
				},
				SubResourcePatch: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					return fmt.Errorf("induced Status patch error")
				},
			}).Build()

			handler = &DeletionHandler{Client: testClient, Logger: testLogger}

			clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
				ManifestsRendered: []v1alpha1.ManifestReference{
					{
						APIGroup:  ptr.To(gvk.Group + "/" + gvk.Version),
						Kind:      gvk.Kind,
						Name:      Name1,
						Namespace: Namespace,
						SyncWave:  0,
						Status:    v1alpha1.ManifestRenderedSuccess,
					},
				},
			}
			Expect(c.Create(ctx, clusterInstance)).To(Succeed())

			ownerRef := ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name)

			_, renderedObject := testFnGenerateObject(ctx, testClient, Name1, Namespace, ownerRef, "", gvk)

			deleted, err := handler.DeleteObjects(ctx, clusterInstance, []ci.RenderedObject{*renderedObject}, nil, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("induced deletion error"))
			Expect(err.Error()).To(ContainSubstring("induced Status patch error"))
			Expect(deleted).To(BeFalse())

			// Verify that ClusterInstance.Status.ManifestsRendered was not updated
			Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
			Expect(clusterInstance.Status.ManifestsRendered).Should(HaveLen(1))
			Expect(clusterInstance.Status.ManifestsRendered[0].Status).Should(Equal(v1alpha1.ManifestRenderedSuccess))
			Expect(clusterInstance.Status.ManifestsRendered[0].Message).Should(Equal(""))
		})
	})

	It("should return false if any object is marked for deletion but not yet deleted", func() {

		clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
			ManifestsRendered: []v1alpha1.ManifestReference{
				{
					APIGroup:  ptr.To(gvk.Group + "/" + gvk.Version),
					Kind:      gvk.Kind,
					Name:      Name1,
					Namespace: Namespace,
					SyncWave:  0,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
				{
					APIGroup:  ptr.To(gvk.Group + "/" + gvk.Version),
					Kind:      gvk.Kind,
					Name:      Name2,
					Namespace: Namespace,
					SyncWave:  0,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		ownerRef := ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name)

		obj1, renderedObject1 := testFnGenerateObject(ctx, c, Name1, Namespace, ownerRef, "", gvk)
		obj2, renderedObject2 := testFnGenerateObject(ctx, c, Name2, Namespace, ownerRef, "do-not-delete", gvk)

		deleted, err := handler.DeleteObjects(
			ctx, clusterInstance, []ci.RenderedObject{*renderedObject1, *renderedObject2}, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleted).To(BeFalse())

		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
		Expect(clusterInstance.Status.ManifestsRendered).To(HaveLen(2))
		Expect(clusterInstance.Status.ManifestsRendered[0].Status).To(Equal(v1alpha1.ManifestDeletionInProgress))
		Expect(clusterInstance.Status.ManifestsRendered[1].Status).To(Equal(v1alpha1.ManifestDeletionInProgress))

		// Verify only Object1 is deleted
		Expect(c.Get(ctx, client.ObjectKeyFromObject(obj1), obj1)).ToNot(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(obj2), obj2)).To(Succeed())

		deleted, err = handler.DeleteObjects(
			ctx, clusterInstance, []ci.RenderedObject{*renderedObject1, *renderedObject2}, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleted).To(BeFalse())

		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
		Expect(clusterInstance.Status.ManifestsRendered).To(HaveLen(1))
		Expect(clusterInstance.Status.ManifestsRendered[0].Status).To(Equal(v1alpha1.ManifestDeletionInProgress))
		Expect(clusterInstance.Status.ManifestsRendered[0].Name).To(Equal(Name2))

	})

	It("should return a TimedOut error if a deletion times out", func() {
		clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
			ManifestsRendered: []v1alpha1.ManifestReference{
				{
					APIGroup:  ptr.To(gvk.Group + "/" + gvk.Version),
					Kind:      gvk.Kind,
					Name:      Name1,
					Namespace: Namespace,
					SyncWave:  0,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		ownerRef := ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name)

		testClient := fakeclient.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return c.Get(ctx, key, obj, opts...)
			},
			Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				return c.Create(ctx, obj, opts...)
			},
			Delete: func(ctx context.Context, fclient client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				return cierrors.NewDeletionTimeoutError("")
			},
			Patch: func(ctx context.Context, client client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				return c.Patch(ctx, obj, patch, opts...)
			},
			SubResourcePatch: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				return c.Status().Patch(ctx, obj, patch, opts...)
			},
		}).Build()

		handler = &DeletionHandler{Client: testClient, Logger: testLogger}

		_, renderedObject := testFnGenerateObject(ctx, c, Name1, Namespace, ownerRef, "", gvk)

		deleted, err := handler.DeleteObjects(ctx, clusterInstance, []ci.RenderedObject{*renderedObject}, nil, nil)
		Expect(err).To(HaveOccurred())
		Expect(cierrors.IsDeletionTimeoutError(err)).To(BeTrue())
		Expect(deleted).To(BeFalse())
	})

})

var _ = Describe("DeleteRenderedObjects", func() {
	var (
		ctx             context.Context
		testLogger      *zap.Logger
		handler         *DeletionHandler
		c               client.Client
		clusterInstance *v1alpha1.ClusterInstance

		Name1     = "test1"
		Name2     = "test2"
		Namespace = "default"

		gvk schema.GroupVersionKind
	)

	BeforeEach(func() {
		gvk = schema.GroupVersionKind{
			Group:   "apps",
			Version: "v1",
			Kind:    "Deployment",
		}
		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      Name1,
				Namespace: Namespace,
			},
		}

		ctx = context.TODO()
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects().
			Build()
		testLogger = zap.NewNop().Named("Test")
		handler = &DeletionHandler{Client: c, Logger: testLogger}
	})

	It("should return true if all rendered objects are successfully deleted", func() {
		clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
			ManifestsRendered: []v1alpha1.ManifestReference{
				{
					APIGroup:  ptr.To(gvk.Group + "/" + gvk.Version),
					Kind:      gvk.Kind,
					Name:      Name1,
					Namespace: Namespace,
					SyncWave:  0,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
				{
					APIGroup:  ptr.To(gvk.Group + "/" + gvk.Version),
					Kind:      gvk.Kind,
					Name:      Name2,
					Namespace: Namespace,
					SyncWave:  0,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		ownerRef := ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name)

		obj1, _ := testFnGenerateObject(ctx, c, Name1, Namespace, ownerRef, "", gvk)
		obj2, _ := testFnGenerateObject(ctx, c, Name2, Namespace, ownerRef, "", gvk)

		deleted, err := handler.DeleteRenderedObjects(ctx, clusterInstance, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleted).To(BeFalse())

		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
		Expect(clusterInstance.Status.ManifestsRendered).To(HaveLen(2))
		Expect(clusterInstance.Status.ManifestsRendered[0].Status).To(Equal(v1alpha1.ManifestDeletionInProgress))
		Expect(clusterInstance.Status.ManifestsRendered[1].Status).To(Equal(v1alpha1.ManifestDeletionInProgress))

		// Objects should get deleted, but will take another reconcile
		Expect(c.Get(ctx, client.ObjectKeyFromObject(obj1), obj1)).ToNot(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(obj2), obj2)).ToNot(Succeed())

		deleted, err = handler.DeleteRenderedObjects(ctx, clusterInstance, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleted).To(BeTrue())

		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
		Expect(clusterInstance.Status.ManifestsRendered).To(HaveLen(0))
	})
})

var _ = Describe("Helper Functions", func() {
	var (
		ctx             context.Context
		c               client.Client
		clusterInstance *v1alpha1.ClusterInstance

		Name      = "test"
		Namespace = "default"

		gvk schema.GroupVersionKind
	)

	BeforeEach(func() {
		ctx = context.TODO()
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		gvk = schema.GroupVersionKind{
			Group:   "",
			Version: "v1",
			Kind:    "ConfigMap",
		}
		clusterInstance = &v1alpha1.ClusterInstance{
			Status: v1alpha1.ClusterInstanceStatus{
				ManifestsRendered: []v1alpha1.ManifestReference{
					{
						APIGroup:  ptr.To(gvk.Group + "/" + gvk.Version),
						Kind:      gvk.Kind,
						Name:      Name,
						Namespace: Namespace,
					},
				},
			},
		}
	})

	Describe("objectsForClusterInstance", func() {

		It("should return an empty list if ManifestsRendered is empty", func() {
			clusterInstance.Status.ManifestsRendered = nil
			objects := objectsForClusterInstance(clusterInstance)
			Expect(objects).To(BeEmpty())
		})

		It("should correctly return the list of objects for the ClusterInstance", func() {
			clusterInstance.Status.ManifestsRendered = []v1alpha1.ManifestReference{
				{
					APIGroup:  ptr.To("v1"),
					Kind:      "ConfigMap",
					Name:      "cm1",
					Namespace: "test",
					SyncWave:  1,
				},
				{
					APIGroup:  ptr.To("apps/v1"),
					Kind:      "Deployment",
					Name:      "deploy1",
					Namespace: "test",
					SyncWave:  3,
				},
			}
			objects := objectsForClusterInstance(clusterInstance)
			Expect(objects).To(HaveLen(2))

			Expect(objects[0].GetName()).To(Equal("cm1"))
			Expect(objects[0].GetNamespace()).To(Equal("test"))
			Expect(objects[0].GetKind()).To(Equal("ConfigMap"))
			Expect(objects[0].GetAnnotations()).To(HaveKeyWithValue(ci.WaveAnnotation, "1"))

			Expect(objects[1].GetName()).To(Equal("deploy1"))
			Expect(objects[1].GetNamespace()).To(Equal("test"))
			Expect(objects[1].GetKind()).To(Equal("Deployment"))
			Expect(objects[1].GetAnnotations()).To(HaveKeyWithValue(ci.WaveAnnotation, "3"))
		})
	})

	Describe("initiateObjectDeletion", func() {

		It("should initiate deletion successfully", func() {

			obj := &unstructured.Unstructured{}
			obj.SetGroupVersionKind(gvk)
			obj.SetName(Name)
			obj.SetNamespace(Namespace)

			// Configure a finalizer to verify deletionTimestamp being set
			obj.SetFinalizers([]string{"hold"})

			Expect(c.Create(ctx, obj)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
			Expect(obj.GetDeletionTimestamp()).To(BeNil())

			// Initiate Deletion
			Expect(initiateObjectDeletion(ctx, c, obj)).To(Succeed())

			Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
			Expect(obj.GetDeletionTimestamp()).NotTo(BeNil())

			// Remove finalizer and expect object to be deleted
			obj.SetFinalizers(nil)
			Expect(c.Update(ctx, obj)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), obj)).NotTo(Succeed())

		})

		It("should handle not found errors gracefully", func() {
			obj := &unstructured.Unstructured{}
			obj.SetGroupVersionKind(gvk)
			obj.SetName("non-existent")
			obj.SetNamespace(Namespace)

			err := initiateObjectDeletion(ctx, c, obj)
			Expect(apierrors.IsNotFound(err)).To(BeFalse())
		})
	})

	Describe("isObjectDeleted", func() {

		It("should return true if the object does not exist", func() {
			obj := &unstructured.Unstructured{}
			obj.SetGroupVersionKind(gvk)
			obj.SetName("non-existent")
			obj.SetNamespace(Namespace)

			deleted, err := isObjectDeleted(ctx, c, obj)
			Expect(err).ToNot(HaveOccurred())
			Expect(deleted).To(BeTrue())
		})

		It("should return false if the object exists", func() {
			obj := &unstructured.Unstructured{}
			obj.SetGroupVersionKind(gvk)
			obj.SetName(Name)
			obj.SetNamespace(Namespace)

			Expect(c.Create(ctx, obj)).To(Succeed())

			deleted, err := isObjectDeleted(ctx, c, obj)
			Expect(err).ToNot(HaveOccurred())
			Expect(deleted).To(BeFalse())
		})
	})

	Describe("timeoutExceeded", func() {
		It("should return false if no timeout is specified", func() {
			exceeded := timeoutExceeded(time.Now(), nil)
			Expect(exceeded).To(BeFalse())
		})

		It("should return true if timeout is exceeded", func() {
			timeout := 1 * time.Second
			exceeded := timeoutExceeded(time.Now().Add(-2*time.Second), &timeout)
			Expect(exceeded).To(BeTrue())
		})

		It("should return false if timeout is not yet exceeded", func() {
			timeout := 1 * time.Second
			exceeded := timeoutExceeded(time.Now(), &timeout)
			Expect(exceeded).To(BeFalse())
		})
	})
})

var _ = Describe("filterOutDeletedManifests", func() {
	var (
		manifestsRendered []v1alpha1.ManifestReference
		manifestsDeleted  []v1alpha1.ManifestReference
	)

	BeforeEach(func() {
		manifestsRendered = []v1alpha1.ManifestReference{
			{APIGroup: ptr.To("apps/v1"), Kind: "Deployment", Name: "test", Namespace: "default"},
			{APIGroup: ptr.To("apps/v1"), Kind: "StatefulSet", Name: "test", Namespace: "default"},
			{APIGroup: ptr.To("v1"), Kind: "ConfigMap", Name: "test", Namespace: "default"},
		}

		manifestsDeleted = []v1alpha1.ManifestReference{}
	})

	It("returns the same list when deleted is empty", func() {
		filtered := filterOutDeletedManifests(manifestsRendered, manifestsDeleted)
		Expect(filtered).To(Equal(manifestsRendered))
	})

	It("removes a single deleted manifest", func() {
		manifestsDeleted = []v1alpha1.ManifestReference{
			{APIGroup: ptr.To("apps/v1"), Kind: "Deployment", Name: "test", Namespace: "default"},
		}
		filtered := filterOutDeletedManifests(manifestsRendered, manifestsDeleted)

		Expect(filtered).To(Equal([]v1alpha1.ManifestReference{
			{APIGroup: ptr.To("apps/v1"), Kind: "StatefulSet", Name: "test", Namespace: "default"},
			{APIGroup: ptr.To("v1"), Kind: "ConfigMap", Name: "test", Namespace: "default"},
		}))
	})

	It("removes multiple deleted manifests", func() {
		manifestsDeleted = []v1alpha1.ManifestReference{
			{APIGroup: ptr.To("apps/v1"), Kind: "Deployment", Name: "test", Namespace: "default"},
			{APIGroup: ptr.To("v1"), Kind: "ConfigMap", Name: "test", Namespace: "default"},
		}
		filtered := filterOutDeletedManifests(manifestsRendered, manifestsDeleted)

		Expect(filtered).To(Equal([]v1alpha1.ManifestReference{
			{APIGroup: ptr.To("apps/v1"), Kind: "StatefulSet", Name: "test", Namespace: "default"},
		}))
	})

	It("handles no manifests", func() {
		manifestsRendered = []v1alpha1.ManifestReference{}
		filtered := filterOutDeletedManifests(manifestsRendered, manifestsDeleted)
		Expect(filtered).To(BeEmpty())
	})

	It("handles no matches between manifests and deleted", func() {
		manifestsDeleted = []v1alpha1.ManifestReference{
			{APIGroup: ptr.To("apps/v1"), Kind: "DaemonSet", Name: "test", Namespace: "default"},
		}
		filtered := filterOutDeletedManifests(manifestsRendered, manifestsDeleted)

		Expect(filtered).To(Equal(manifestsRendered))
	})

	It("handles all manifests being deleted", func() {
		manifestsDeleted = manifestsRendered
		filtered := filterOutDeletedManifests(manifestsRendered, manifestsDeleted)
		Expect(filtered).To(BeEmpty())
	})
})

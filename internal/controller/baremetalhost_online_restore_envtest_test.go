//go:build envtest

/*
Copyright 2026 Red Hat, Inc.

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
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/configuration"
	"github.com/stolostron/siteconfig/internal/controller/deletion"
	"github.com/stolostron/siteconfig/internal/controller/reinstall"
)

func (f *bareMetalHostOnlineEnvtestFixture) testOlderRestoration(t *testing.T) {
	for _, initial := range []bool{false, true} {
		for _, sameUID := range []bool{false, true} {
			t.Run(fmt.Sprintf("restore-older-backup-initial-%t-same-uid-%t", initial, sameUID), func(t *testing.T) {
				g := NewWithT(t)
				desired := f.host(initial)
				original := f.cluster.DeepCopy()
				original.Spec.Reinstall = &v1alpha1.ReinstallSpec{Generation: "newer-than-backup"}
				original.Status.Reinstall = &v1alpha1.ReinstallStatus{ObservedGeneration: "newer-than-backup"}
				apply := func(instance *v1alpha1.ClusterInstance, dryRun bool) {
					t.Helper()
					var writer client.Client = f.c
					if dryRun {
						writer = client.NewDryRunClient(f.c)
					}
					// Reconstruct reconciliation state on each apply to test restart persistence.
					r := &ClusterInstanceReconciler{Client: f.c, APIReader: f.c}
					_, err := r.applyRenderedObject(f.ctx, writer, f.log, instance, *desired, dryRun)
					g.Expect(err).ToNot(HaveOccurred(), "apply host metadata using durable restore and lifecycle records")
				}
				apply(original, false)
				f.power(t, desired, !initial)
				before := f.read(t, desired)
				restored := original.DeepCopy()
				if !sameUID {
					restored.UID = types.UID("restored-uid")
				}
				restored.Spec.Reinstall = &v1alpha1.ReinstallSpec{Generation: "backup-generation"}
				restored.Status.Reinstall = &v1alpha1.ReinstallStatus{ObservedGeneration: "backup-generation"}
				restored.Labels = map[string]string{veleroRestoreNameLabel: "older-backup"}
				restored.Annotations = map[string]string{
					veleroRestoreHandledAnnotation:     "older-backup",
					bmhOnlineRestoreBaselineAnnotation: string(restored.UID) + ":backup-generation",
				}
				apply(restored, true)
				g.Expect(f.read(t, desired).GetResourceVersion()).To(Equal(before.GetResourceVersion()),
					"dry-run restore adoption must not persist a checkpoint or change the surviving host")
				apply(restored, false)
				f.assertOnline(t, desired, !initial)
				g.Expect(f.read(t, desired).GetAnnotations()).To(HaveKeyWithValue(
					bmhOnlineRestoreAnnotation, string(restored.UID)+":older-backup"),
					"the adopted restore must be checkpointed even when the ClusterInstance UID is unchanged")
				g.Expect(f.read(t, desired).GetAnnotations()).To(HaveKeyWithValue(
					bmhOnlineInitializedAnnotation, string(restored.UID)+":backup-generation"),
					"the adopted host must establish a baseline against the restored status without replaying power")
				for _, note := range []string{"first-update", "after-restart"} {
					desired.SetAnnotations(map[string]string{"example.com/note": note})
					apply(restored, false)
					f.assertOnline(t, desired, !initial)
					g.Expect(f.read(t, desired).GetAnnotations()).To(HaveKeyWithValue("example.com/note", note),
						"ordinary metadata must continue to update after adopting a stale restore generation")
				}
				// A second restore with the same CI UID has its own adoption boundary.
				restored.Labels[veleroRestoreNameLabel] = "second-backup"
				restored.Annotations[veleroRestoreHandledAnnotation] = "second-backup"
				restored.Annotations[bmhOnlineRestoreBaselineAnnotation] = string(restored.UID) + ":second-backup-generation"
				restored.Spec.Reinstall = &v1alpha1.ReinstallSpec{Generation: "second-backup-generation"}
				restored.Status.Reinstall = &v1alpha1.ReinstallStatus{ObservedGeneration: "second-backup-generation"}
				apply(restored, false)
				f.assertOnline(t, desired, !initial)
				g.Expect(f.read(t, desired).GetAnnotations()).To(HaveKeyWithValue(
					bmhOnlineRestoreAnnotation, string(restored.UID)+":second-backup"),
					"each distinct handled restore must establish its own durable checkpoint")
				restored.Spec.Reinstall = &v1alpha1.ReinstallSpec{Generation: "intentional-reinstall"}
				restored.Status.Reinstall = &v1alpha1.ReinstallStatus{ObservedGeneration: "intentional-reinstall"}
				apply(restored, false)
				f.assertOnline(t, desired, initial)
				f.power(t, desired, !initial)
				apply(restored, false)
				f.assertOnline(t, desired, !initial)
			})
		}
	}
}

// Exercise the actual restore and reinstall handlers before any host adoption.
func (f *bareMetalHostOnlineEnvtestFixture) testReinstallBeforeRestoreAdoption(t *testing.T) {
	for _, initial := range []bool{false, true} {
		for _, backupGeneration := range []string{"", "backup-install"} {
			t.Run(fmt.Sprintf("initial-%t-backup-%s", initial, backupGeneration), func(t *testing.T) {
				g := NewWithT(t)
				desired := f.host(initial)
				name := desired.GetName()
				instance := &v1alpha1.ClusterInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name: name, Namespace: f.cluster.Namespace, Finalizers: []string{clusterInstanceFinalizer},
						Labels:      map[string]string{veleroRestoreNameLabel: "restore-before-reinstall"},
						Annotations: map[string]string{veleroRestoreStatusAnnotation: "true"},
					},
					Spec: v1alpha1.ClusterInstanceSpec{
						ClusterName: name, BaseDomain: "example.com", ClusterType: v1alpha1.ClusterTypeSNO,
						ClusterImageSetNameRef: "unused", PullSecretRef: corev1.LocalObjectReference{Name: "pull-secret"},
						TemplateRefs: []v1alpha1.TemplateRef{{Name: "cluster-template", Namespace: f.cluster.Namespace}},
						Nodes: []v1alpha1.NodeSpec{{
							HostName: name, Role: "master", BmcAddress: "redfish+https://192.0.2.1/redfish/v1/Systems/1",
							BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: "bmc-credentials"},
							BootMACAddress:     "02:00:00:00:00:01",
							TemplateRefs:       []v1alpha1.TemplateRef{{Name: "node-template", Namespace: f.cluster.Namespace}},
						}},
					},
				}
				createBareMetalHostOnlineEnvtestResource(t, f.c, instance)
				instance.Status.ObservedGeneration = 5
				if backupGeneration != "" {
					instance.Status.Reinstall = &v1alpha1.ReinstallStatus{ObservedGeneration: backupGeneration}
				}
				g.Expect(f.c.Status().Update(f.ctx, instance)).To(Succeed(), "restore status that omits the surviving host")
				g.Expect(instance.Status.ManifestsRendered).To(BeEmpty(), "the backup must not track this host")
				desired.SetLabels(map[string]string{ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(instance.Namespace, name)})
				original := instance.DeepCopy()
				original.UID = types.UID("original-uid")
				original.Status.Reinstall = &v1alpha1.ReinstallStatus{ObservedGeneration: "host-install"}
				_, err := f.r.applyRenderedObject(f.ctx, f.c, f.log, original, *desired, false)
				g.Expect(err).ToNot(HaveOccurred(), "establish the surviving host's pre-restore installation")
				f.power(t, desired, !initial)
				before := f.read(t, desired)

				restorer := &ClusterInstanceReconciler{Client: f.c, APIReader: f.c, Log: f.log}
				_, err = restorer.Reconcile(f.ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(instance)})
				g.Expect(err).ToNot(HaveOccurred(), "handle the restore before any ordinary manifest apply")
				g.Expect(f.c.Get(f.ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed(),
					"read the handled restore from the API server")
				g.Expect(instance.Annotations).To(HaveKeyWithValue(veleroRestoreHandledAnnotation, "restore-before-reinstall"),
					"the controller must acknowledge the restore before reinstall begins")
				g.Expect(f.read(t, desired).GetResourceVersion()).To(Equal(before.GetResourceVersion()),
					"handling the restore must not touch the surviving host")

				instance.Spec.Reinstall = &v1alpha1.ReinstallSpec{
					Generation: "post-restore-install", PreservationMode: v1alpha1.PreservationModeNone,
				}
				g.Expect(f.c.Update(f.ctx, instance)).To(Succeed(), "request reinstall before host adoption")
				store, err := configuration.NewConfigurationStore(configuration.NewDefaultConfiguration())
				g.Expect(err).ToNot(HaveOccurred(), "configure the actual reinstall handler")
				store.SetAllowReinstalls(true)
				handler := &reinstall.ReinstallHandler{Client: f.c, Logger: f.log, ConfigStore: store,
					DeletionHandler: &deletion.DeletionHandler{Client: f.c, Logger: f.log}}
				for range 16 {
					_, err := handler.ProcessRequest(f.ctx, instance)
					g.Expect(err).ToNot(HaveOccurred(), "process teardown and restoration through the real reinstall handler")
					g.Expect(f.c.Get(f.ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed(),
						"read durable reinstall progress")
					if instance.Status.Reinstall.ObservedGeneration == "post-restore-install" {
						break
					}
				}
				g.Expect(instance.Status.Reinstall.ObservedGeneration).To(Equal("post-restore-install"),
					"the reinstall must actually complete before the first host adoption")
				g.Expect(meta.IsStatusConditionTrue(instance.Status.Reinstall.Conditions,
					string(v1alpha1.ReinstallRequestProcessed))).To(BeTrue(), "reinstall completion must be positive")
				g.Expect(f.read(t, desired).GetResourceVersion()).To(Equal(before.GetResourceVersion()),
					"a host absent from manifestsRendered must survive teardown unchanged")
				g.Expect(f.read(t, desired).GetAnnotations()).ToNot(HaveKey(bmhOnlineRestoreAnnotation),
					"no per-host restore checkpoint may exist before the regression's first apply")

				restarted := &ClusterInstanceReconciler{Client: f.c, APIReader: f.c}
				_, err = restarted.applyRenderedObject(f.ctx, client.NewDryRunClient(f.c), f.log, instance, *desired, true)
				g.Expect(err).ToNot(HaveOccurred(), "validate the new installation without mutating the host")
				g.Expect(f.read(t, desired).GetResourceVersion()).To(Equal(before.GetResourceVersion()),
					"a dry run must not consume initialization or acknowledge the host restore")
				_, err = restarted.applyRenderedObject(f.ctx, f.c, f.log, instance, *desired, false)
				g.Expect(err).ToNot(HaveOccurred(), "initialize the completed post-restore installation")
				f.assertOnline(t, desired, initial)
				expectedBaseline := string(instance.UID)
				if backupGeneration != "" {
					expectedBaseline += ":" + backupGeneration
				}
				g.Expect(instance.Annotations).To(HaveKeyWithValue(bmhOnlineRestoreBaselineAnnotation, expectedBaseline),
					"reinstall must not replace the restored lifecycle baseline with its new generation")
				g.Expect(f.read(t, desired).GetUID()).To(Equal(before.GetUID()),
					"the regression must exercise a surviving host rather than a newly created one")
				f.power(t, desired, !initial)
				_, err = restarted.applyRenderedObject(f.ctx, f.c, f.log, instance, *desired, false)
				g.Expect(err).ToNot(HaveOccurred(), "apply later metadata after the new installation was initialized")
				f.assertOnline(t, desired, !initial)
			})
		}
	}
}

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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stolostron/siteconfig/api/v1alpha1"
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
				restored.Annotations = map[string]string{veleroRestoreHandledAnnotation: "older-backup"}
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

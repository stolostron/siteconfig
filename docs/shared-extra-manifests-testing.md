# Shared extra-manifests: QE checklist

Use the operator and matching CRD from the branch under test. Follow the
[two-cluster sample](../config/samples/shared-extra-manifests/README.md) for inputs,
installation prerequisites, and hub/spoke verification commands. See the
[operator guide](configure_siteconfig.md#shared-extra-manifest-configmaps-proposal-implementation)
for recovery behavior and limitations.

## Installation and synchronization

- Verify startup creates `siteconfig-shared-resources` with the managed-by label
  and `pod-security.kubernetes.io/enforce: restricted`.
- Install two clusters referencing the same shared ConfigMap. Verify owned hub
  copies, local installer references, successful installation, and the payload on
  both spokes. Cover Assisted Installer and IBI independently.
- Check that copies contain source data but no source GitOps or backup metadata.
  Referenced sources should receive `cluster.open-cluster-management.io/backup`.
- Update source content. Verify both copies converge while their UIDs and
  `initial-resource-version` stay unchanged. ClusterInstance generation,
  `ObservedGeneration`, and rendered-resource versions must remain unchanged.
  Synchronization alone must not be counted as applying content to the spokes.
- Verify metadata-only source edits do not rewrite copies, unreferenced clusters
  remain untouched, and user-owned destination ConfigMaps are never overwritten.
- With installation held, update source content and release the hold. Verify the
  propagated content is retained and the initial version annotation is preserved.

## Recovery and lifecycle

- Delete a managed copy: verify recreation with a new UID and no rendering.
- Edit copy content without changing the source: verify prompt repair.
- Stop the operator, delete a copy, and restart: verify missing-copy recovery,
  unchanged existing-copy versions, and unchanged rendered-resource versions.
- Verify paused/deleting ClusterInstances are skipped by missing-copy recovery.
- Delete a source: verify existing copies remain and warning events are emitted.
- On a disposable setup, verify shared namespace recreation and copy garbage
  collection when the owning ClusterInstance is deleted.

## Outstanding validation

Post-install reference-name switching failed against the tested Assisted Installer:
SiteConfig admitted the change, but downstream validation reported
`ManifestsConfigMapRefs.Name` immutable after installation starts, leaving
`RenderedTemplatesValidated=False`. IBI switching remains untested. On a dedicated
installed test cluster, save the full reference list before reproducing this case,
record installer versions and conditions, and restore the original references
immediately afterward. Do not bypass the installer webhook.

A real ACM/Velero restore and representative fleet-load testing remain required.
For restore, verify absent copies recover without a generation change or rendering;
existing copies and rendered resources must retain their resource versions. For
scale, measure convergence time, API-server throughput, and provisioning-controller
latency. Fake-client benchmarks do not establish fleet-scale acceptance.

Policy-owner confirmation of missing-copy reporting and must-gather collection of
the shared namespace are external follow-ups. Recovery design approval and the
scope of post-install switching remain subject to
[EP #1056](https://github.com/stolostron/siteconfig/pull/1056).

Attach the tested commit/image, installer versions, inputs without credentials,
conditions, timings, and cleanup results to the test report. Keep run-specific
images and lab histories in that report rather than in this reusable checklist.

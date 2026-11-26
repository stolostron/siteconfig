# BareMetalHost power management

SiteConfig uses the rendered BareMetalHost (BMH) `spec.online` value to initialize
power management for an installation. After initialization, later template
applies preserve the current value instead of reasserting the template default.
The installer and other controllers can then manage power without a metadata
update to the ClusterInstance unexpectedly powering a host off or on.

This behavior applies to every BMH rendered by SiteConfig, including custom
templates. The default IBI template still initializes `online: false`, and the
default assisted-installation template still initializes `online: true`. Other
BMH fields, labels, and annotations continue to use normal server-side apply;
BMHs do not need to be added to `suppressedManifests` to preserve power state.

A custom template cannot use later ClusterInstance changes to continually manage
`spec.online`. Make intentional power changes through the installer or the
controller responsible for the BMH instead.

## Initialization and lifecycle

SiteConfig records the initialization in the internal
`siteconfig.open-cluster-management.io/online-initialized` annotation. The value
identifies the ClusterInstance UID and the last successfully processed reinstall
generation, when present in `status.reinstall.observedGeneration`.
The marker is applied atomically with the initial power value and survives
controller restarts and ordinary ClusterInstance updates.

A new ClusterInstance UID, a successfully processed new reinstall, or a new
association after the SiteConfig ownership label has been removed starts a new
initialization. Changing `spec.reinstall.generation` alone does not reset power.
A rejected or failed request that leaves the BMH in place retains its existing
initialization marker and power state. The reinstall handler confirms a new
generation after deleting rendered manifests and restoring preserved data.
A deletion failure pauses the request; resuming it retries deletion and waits
for finalizers rather than treating the previous failure as completed teardown.
A BMH that is already owned by the same ClusterInstance but has no initialization
marker is adopted with its current power value. This avoids replaying an
installation default on hosts created by older SiteConfig versions. For a
ClusterInstance restored by Velero, the first apply to a surviving owned host
adopts its current power value even if restored status predates the installation
recorded on the BMH. Reinstall generation names are opaque; a mismatch during
restore adoption is not evidence that a new installation should start. This
also covers an in-place restore that retains the ClusterInstance UID.

SiteConfig records the ClusterInstance UID and handled restore name in the
internal `siteconfig.open-cluster-management.io/online-restore` annotation,
atomically with the adopted initialization marker. That checkpoint establishes
the restored ClusterInstance's lifecycle baseline so repeated metadata updates
preserve power. If reinstall status was not restored, the existing BMH marker
supplies the previous initialization generation.

When handling a Velero restore, SiteConfig also records the restored lifecycle in
the ClusterInstance's internal
`clusterinstance.siteconfig.open-cluster-management.io/online-restore-baseline`
annotation, in the same metadata patch that acknowledges the restore. A
subsequently completed reinstall initializes power even if no host apply has
acknowledged the restore yet. This includes a surviving host omitted from the
restored `status.manifestsRendered` list. Hosts still at the restore baseline
retain their current power value. A previously handled restore without this
annotation records its current baseline before processing any new reinstall.
A newly created or unallocated host continues to use its installation default.

Do not edit or remove the initialization/restore annotations or SiteConfig
ownership label to request a power change. Use the supported reinstall workflow
for a new installation. SiteConfig refuses to modify a deleting BMH or one owned by
another ClusterInstance.

## Field ownership

Simply omitting a field previously owned through server-side apply can delete
it or restore its API default. SiteConfig first shares the **current** power
value with the `siteconfig-initial-power` field manager, then omits `online` from
its normal apply. Once that handoff succeeds, SiteConfig never reapplies the
power field through either manager. Historical managed-field entries created
through ordinary updates can remain; they do not cause subsequent applies to
reassert the value.

Both handoff writes use resource-version preconditions, so a concurrent power
change causes a conflict and retry rather than overwriting the new value.
Dry-run validation preserves the current value without persisting initialization
or ownership changes. The BMH read, ownership handoff and final apply share a
30-second operation deadline and retain parent cancellation or an earlier parent
deadline, so a stalled API request cannot occupy the worker indefinitely.

## Regression checks

Run `make unittest` for the controller safety tests and `make test-envtest` for
power-field ownership tests against a local Kubernetes API server. Each behavior
area has its own test entry point and control plane; fixture namespaces and
cluster-scoped image sets have registered cleanup. The target downloads matching
API-server binaries through `setup-envtest`; it does not use or modify a running
cluster. These checks are also included in `make ci-job`.

Run `make test-coverage` to collect unit-test coverage in `test-coverage.out`.
Sonar CI also measures coverage using ordinary Go tests. API-server tests run
separately through `make test-envtest` and are not included in this report.

The API-server tests cover initial values of both `true` and `false`, existing
inventory hosts, adoption of older SiteConfig resources, metadata changes and
removals, lifecycle changes, older-backup restore adoption, dry runs, and
concurrent power writes. They use a minimal structural BMH schema and do not run
BMO or installer controllers.

Lab verification should cover an initial IBI installation, its automatic
installation-to-import transition, and a later ClusterInstance label or
annotation update. Confirm that `spec.online` stays under the installer's power
management while the metadata changes reach the BMH. Repeat with assisted
installation and exercise an intentional power change and reinstallation.

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
identifies the ClusterInstance UID and, when present, its reinstall generation.
The marker is applied atomically with the initial power value and survives
controller restarts and ordinary ClusterInstance updates.

A new ClusterInstance UID, a new reinstall generation, or a new association after
the SiteConfig ownership label has been removed starts a new initialization.
A BMH that is already owned by the same ClusterInstance but has no initialization
marker is adopted with its current power value. This avoids replaying an
installation default on hosts created by older SiteConfig versions. For a
ClusterInstance restored by Velero, a changed UID is adopted without resetting
power when SiteConfig has handled the restore and the reinstall generation is
unchanged. An explicit new reinstall still initializes power normally.

Do not edit or remove the initialization annotation or SiteConfig ownership
label to request a power change. Use the supported reinstall workflow for a
new installation. SiteConfig refuses to modify a deleting BMH or one owned by
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
or ownership changes.

## Regression checks

Run `make unittest` for the controller safety tests and `make test-envtest` for
power-field ownership tests against a local Kubernetes API server. The latter
downloads matching API-server binaries through `setup-envtest`; it does not use
or modify a running cluster. These checks are also included in `make ci-job`.

Run `make test-coverage` to collect both unit and API-server coverage in
`test-coverage.out`, including the ownership paths exercised only by envtest.

The API-server tests cover initial values of both `true` and `false`, existing
inventory hosts, adoption of older SiteConfig resources, metadata changes and
removals, lifecycle changes, dry runs, and concurrent power writes. They use a
minimal structural BMH schema and do not run BMO or installer controllers.

Lab verification should cover an initial IBI installation, its automatic
installation-to-import transition, and a later ClusterInstance label or
annotation update. Confirm that `spec.online` stays under the installer's power
management while the metadata changes reach the BMH. Repeat with assisted
installation and exercise an intentional power change and reinstallation.

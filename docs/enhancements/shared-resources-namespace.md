---
title: shared-resources-namespace
authors:
  - "@sakhoury"
reviewers:
  - "@imiller0"
  - "@sabbir-47"
  - "@carbonin"
approvers:
  - "@imiller0"
  - "@sabbir-47"
  - "@carbonin"
api-approvers:
  - "@imiller0"
  - "@sabbir-47"
  - "@carbonin"
creation-date: 2026-04-30
last-updated: 2026-05-01
status: provisional
tracking-link:
  - https://redhat.atlassian.net/browse/ACM-17129
see-also: []
replaces: []
superseded-by: []
---

# Shared Resources Namespace

## Release Signoff Checklist

- [ ] Enhancement is `implementable`
- [ ] Design details are appropriately documented from clear requirements
- [ ] Test plan is defined
- [ ] Graduation criteria are defined
- [ ] User-facing documentation is updated

## Summary

The SiteConfig operator should create and manage a dedicated namespace called
`siteconfig-shared-resources` for holding ConfigMaps that are referenced as
extra-manifests by many ClusterInstances. Today, extra-manifest ConfigMaps must
reside in the same namespace as each ClusterInstance, forcing users with
thousands of clusters to duplicate the same ConfigMaps into every namespace.
This proposal introduces the shared namespace, extends the ClusterInstance API
to allow cross-namespace extra-manifest references, and has the controller
automatically copy shared ConfigMaps into each ClusterInstance namespace during
reconciliation so that downstream CRDs continue to work unchanged.

## Motivation

Large-scale deployments (1,000+ clusters) need a common set of extra manifests
applied to every cluster — typically MachineConfig and Network CRs, along with
day-2 operator subscriptions and security policies. Under the current model,
each
ClusterInstance's extra-manifest ConfigMaps must exist in the same namespace as
the ClusterInstance itself. This forces administrators to either:

- Duplicate every shared ConfigMap into every cluster namespace, creating a
  maintenance burden that scales linearly with cluster count.
- Build external tooling (GitOps policies, scripts) to keep the copies in sync,
  introducing fragile indirection.

A shared namespace eliminates this duplication by allowing many ClusterInstances
to reference a single source-of-truth ConfigMap. The operator handles the
per-namespace copying automatically.

### User Stories

**As a platform administrator**, I want to define a common set of extra-manifest
ConfigMaps once in a shared namespace so that all ClusterInstances can reference
them, reducing duplication and configuration drift across thousands of clusters.

**As a platform administrator**, I want the shared namespace to be automatically
re-created if it is accidentally deleted so that my cluster provisioning pipeline
is not permanently broken by a single mistake.

**As a platform administrator**, I want the operator to create the shared
namespace automatically so that I do not need to perform manual setup steps
before using the feature.

**Day-2 operations considerations:**

- **Lifecycle**:
  - The shared namespace is created at operator startup and re-created by a
    controller if deleted while the operator is running.
  - On operator uninstall, the namespace is not automatically removed (to
    preserve user-created ConfigMaps); administrators delete it manually if
    desired.
  - Copied ConfigMaps in ClusterInstance namespaces are automatically cleaned
    up via Kubernetes garbage collection when the owning ClusterInstance is
    deleted.
  - During cluster reinstall, the reconciler re-copies shared ConfigMaps as
    part of normal reconciliation; no special handling is needed.
- **Monitoring**: No new status conditions are required. The existing
  `ClusterInstanceValidated` condition already covers validation failures when
  a referenced ConfigMap does not exist. The `RenderedTemplatesApplied`
  condition covers failures during template rendering and manifest application.
- **Remediation**: If the shared namespace is deleted, the `NamespaceMonitor`
  controller re-creates it. Administrators must re-create any ConfigMaps that
  were inside it. If a ClusterInstance references a ConfigMap that does not
  exist, reconciliation fails with a clear error message identifying the
  missing ConfigMap and namespace, and requeues after `DefaultValidationErrorDelay`
  (30 seconds, defined in `clusterinstance_controller.go`).
- **Scale**: No impact on `maxConcurrentReconciles`. Each reconcile adds one
  Get + one Create/Update per shared ConfigMap reference — negligible overhead.

### Goals

- The operator creates the `siteconfig-shared-resources` namespace at startup
  without manual administrator intervention.
- The namespace is automatically re-created if deleted while the operator is
  running.
- ClusterInstance `extraManifestsRefs` can reference ConfigMaps in both the
  ClusterInstance's own namespace and the `siteconfig-shared-resources`
  namespace.
- The controller automatically copies shared ConfigMaps into the ClusterInstance
  namespace during reconciliation so that downstream CRDs
  (`AgentClusterInstall`, `ImageClusterInstall`) work without modification.
- The change is backward-compatible: existing ClusterInstance CRs that do not
  specify a namespace continue to work unchanged.
- Validation rejects references to any namespace other than the two allowed
  ones.
- Copied ConfigMaps are automatically cleaned up when the ClusterInstance is
  deleted, via Kubernetes ownerReference garbage collection.

### Non-Goals

- Auto-deleting the shared namespace on operator uninstall. OLM does not
  currently support cleanup of runtime-created resources, and the namespace may
  contain user data.
- Supporting arbitrary cross-namespace references beyond the shared namespace.
- Extending TemplateRefs to use the shared namespace (TemplateRefs already
  support explicit namespace references).

## Proposal

### Workflow Description

**Operator startup:**

1. The operator creates the `siteconfig-shared-resources` namespace (if it does
   not already exist) during its initialization phase, before the manager and
   webhooks start.
2. If the namespace already exists (e.g., pre-created by an admin or managed
   by another tool), startup proceeds normally — the namespace exists, which
   is all that matters.

**Runtime namespace protection:**

A `NamespaceMonitor` controller watches for deletion events on the
`siteconfig-shared-resources` namespace. If the namespace is deleted, the
controller re-creates it.

**User workflow:**

1. Administrator creates ConfigMaps in the `siteconfig-shared-resources`
   namespace containing shared extra manifests.
2. Administrator creates ClusterInstance CRs referencing those ConfigMaps:

   ```yaml
   apiVersion: siteconfig.open-cluster-management.io/v1alpha1
   kind: ClusterInstance
   metadata:
     name: cluster-1
     namespace: cluster-1-ns
   spec:
     extraManifestsRefs:
       - name: local-config           # Uses cluster-1-ns (default)
       - name: common-day2-manifests
         namespace: siteconfig-shared-resources
   ```

3. The webhook validates that `namespace`, if specified, is either the
   ClusterInstance's own namespace or `siteconfig-shared-resources`.
4. During reconciliation, the controller:

   - Validates that the referenced ConfigMaps exist in their specified
     namespaces.
   - For each reference with `namespace: siteconfig-shared-resources`, copies
     the ConfigMap into the ClusterInstance namespace (same name, same data),
     setting a Kubernetes ownerReference to the ClusterInstance for automatic
     garbage collection. The copy is annotated with provenance metadata
     recording which version of the source ConfigMap it was copied from (see
     [Copy provenance annotations](#copy-provenance-annotations)).
   - Clears the `Namespace` field on the in-memory ExtraManifestsRefs before
     passing them to the template engine, so that `toYaml` renders only the
     `name` field — matching what downstream CRDs expect.
5. Template rendering and manifest application proceed normally. Downstream
   CRDs (`AgentClusterInstall.manifestsConfigMapRefs`,
   `ImageClusterInstall.extraManifestsRefs`) see local ConfigMap references
   with no `namespace` field.

**Downstream CRD compatibility:**

The downstream CRDs that consume `ExtraManifestsRefs` do not support a
`namespace` field:

- `AgentClusterInstall.spec.manifestsConfigMapRefs` uses
  `ManifestsConfigMapReference` (has only `Name`).
- `ImageClusterInstall.spec.extraManifestsRefs` uses
  `corev1.LocalObjectReference` (has only `Name`).

Both expect the referenced ConfigMaps to exist in the same namespace as the
CRD. The local-copy approach ensures this contract is preserved.

**ClusterInstance deletion:**

Copied ConfigMaps have a Kubernetes ownerReference pointing to the
ClusterInstance. When the ClusterInstance is deleted, the Kubernetes garbage
collector automatically deletes the copies. No changes to the existing
`DeletionHandler` or `ManifestsRendered` tracking are needed.

**Operator uninstall:**

The `siteconfig-shared-resources` namespace is not automatically removed. Users
should manually delete it after uninstalling the operator if a clean removal is
desired.

### API Extensions

**New type: `ExtraManifestReference`**

```go
type ExtraManifestReference struct {
    // Name specifies the name of the ConfigMap.
    // +kubebuilder:validation:MinLength=1
    // +required
    Name string `json:"name,omitempty"`

    // Namespace specifies the namespace of the ConfigMap.
    // When omitted, defaults to the namespace of the ClusterInstance.
    // When specified, must be either the ClusterInstance's own namespace
    // or the siteconfig-shared-resources namespace.
    // +optional
    Namespace string `json:"namespace,omitempty"`
}
```

Note: The `Name` field uses `json:"name,omitempty"` to match the serialization
behavior of the existing `corev1.LocalObjectReference.Name` field, which also
uses `omitempty`. This prevents spurious diffs in GitOps tools.

**Modified field in `ClusterInstanceSpec`:**

```go
// Before:
ExtraManifestsRefs []corev1.LocalObjectReference `json:"extraManifestsRefs,omitempty"`

// After:
ExtraManifestsRefs []ExtraManifestReference `json:"extraManifestsRefs,omitempty"`
```

**Backward compatibility:** For each entry, `name` keeps the same JSON meaning
and `omitempty` serialization as `corev1.LocalObjectReference`. Optional
`namespace` uses `omitempty`; when omitted, unmarshaling yields empty and
validation plus reconciliation treat that as the ClusterInstance namespace.
Relative to the current CRD (inlined `LocalObjectReference`: `name` not
required, empty default allowed), the proposed `Name` markers
(`+kubebuilder:validation:MinLength=1`, `+required`) tighten OpenAPI validation:
manifests with an empty or omitted `name` may be rejected at apply time.
Manifests that already set a non-empty `name` and omit `namespace` need no
change. Strongly-typed Go code must use `ExtraManifestReference` instead of
`corev1.LocalObjectReference` for each slice element.

**Webhook validation:** A new `validateExtraManifestReferences` function allows
`namespace` only when it is empty, equals the ClusterInstance namespace, or
equals `siteconfig-shared-resources`; any other value is rejected.

### Siteconfig Impact

- **Controllers**:
  - `ClusterInstanceReconciler`: Updated to copy shared ConfigMaps into the
    ClusterInstance namespace before template rendering. A new
    `copySharedExtraManifests` function handles the copy logic with
    ownerReferences.
  - `NamespaceMonitor` (new): Lightweight controller watching for deletion of
    the shared namespace, inspired by the `ConfigurationMonitor` pattern but
    using a more targeted predicate that only processes delete events (the
    namespace does not carry configuration that needs syncing on updates).
- **Templates**: No changes to template definitions. The controller clears the
  `namespace` field on in-memory ExtraManifestsRefs before template rendering,
  so `toYaml` produces output identical to the current `LocalObjectReference`
  format (only `name`).
- **API fields**: `ExtraManifestsRefs` type changes from
  `[]corev1.LocalObjectReference` to `[]ExtraManifestReference`.
- **Validation**: Webhook validation adds namespace restriction. Controller
  validator updated for cross-namespace ConfigMap lookup with defense-in-depth
  namespace check.

### Implementation Details

**Namespace constant:** Defined as `SharedResourcesNamespace` in
`api/v1alpha1/groupversion_info.go` alongside existing constants.

**Startup initialization:** Uses an uncached client before the manager starts,
similar to `initConfigMapTemplates` in `cmd/main.go`, but with a
create-if-not-exists approach (rather than delete-and-recreate) and
`client.Patch()` for label reconciliation. Uses
`retry.RetryOnConflictOrRetriable` (matching the codebase's existing retry
pattern) for transient error handling.

**NamespaceMonitor controller:** Inspired by the `ConfigurationMonitor` pattern
(predicate-filtered single-resource watch, re-creation on deletion), but differs
in that it watches Namespace objects cluster-wide (vs. ConfigMaps in a single
namespace) and only processes delete events. The existing RBAC marker on the
`ClusterInstanceReconciler` already grants `get`, `list`, `create`, `update`,
`patch`, and `delete` on namespaces. Only the `watch` verb is missing and must
be added to support the `NamespaceMonitor`'s `For(&corev1.Namespace{})` watch.

**ConfigMap copy and ownership:** During reconciliation, shared ConfigMaps are
copied into the ClusterInstance namespace with a Kubernetes ownerReference
pointing to the ClusterInstance. This provides automatic garbage collection
when the ClusterInstance is deleted. A managed-copy label is added so the
controller can distinguish its own copies from user-created ConfigMaps with
the same name (see [Name collision handling](#name-collision-handling)).

#### Name collision handling

If a user-created ConfigMap with the same name already exists in the
ClusterInstance namespace (without the managed-copy label and without an
ownerReference to the ClusterInstance), the controller returns an error that
blocks reconciliation and sets a validation failure condition on the
ClusterInstance. The user-created ConfigMap is never overwritten.

#### Copy provenance annotations

Each copied ConfigMap is annotated with
metadata recording which version of the source it was copied from:

```yaml
annotations:
  siteconfig.open-cluster-management.io/source-namespace: siteconfig-shared-resources
  siteconfig.open-cluster-management.io/source-resource-version: "48291"
```

- `source-namespace`: The namespace the ConfigMap was copied from. Provides a
  direct pointer back to the source.
- `source-resource-version`: The `metadata.resourceVersion` of the source
  ConfigMap at the time of copy. This is an opaque Kubernetes-managed value
  that changes on every update to the source. It provides provenance — a
  support engineer can compare this value to the source ConfigMap's current
  `resourceVersion` to determine whether the copy was made from the latest
  version or an earlier one.

These annotations are set on every copy/update. They serve an auditability
purpose: when a user updates a shared ConfigMap after clusters are provisioned,
the copied ConfigMaps retain the `source-resource-version` from the time they
were copied. This creates a clear trail showing exactly which version of the
shared ConfigMap each cluster was provisioned with — critical for debugging
cases where clusters provisioned at different times may have received
different versions of the same shared extra manifests.

Note: `resourceVersion` is not meaningful for comparison across different
objects, but it is valid for tracking the version of a single object over time
(the source ConfigMap). The annotation answers the question "which version of
the source was this copy made from?" — not "is this copy stale?"

### Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| External Go clients break on type change | `name` JSON field preserved (including `omitempty`); `namespace` is additive. Strongly-typed Go clients importing the field type must update. YAML/kubectl users unaffected. |
| Namespace deleted while operator is running | `NamespaceMonitor` controller re-creates it. ConfigMaps inside are lost and must be re-created by the user (standard Kubernetes behavior). |
| Namespace not deleted on operator uninstall | Documented as manual step. Matches existing behavior where ClusterInstance CRs are not auto-deleted on uninstall either. |
| Multi-replica race on namespace creation | Multiple replicas calling `Create()` concurrently is safe — the first succeeds, others get `AlreadyExists` and proceed. |
| Name collision: shared ConfigMap name matches existing local ConfigMap | Controller detects the conflict (missing managed-copy label / wrong ownerReference) and rejects with a clear validation error instead of overwriting. |
| Downstream CRD incompatibility | Controller copies ConfigMaps locally and strips `namespace` before template rendering, so downstream CRDs see only `name` — identical to current behavior. |
| ownerReference pattern inconsistency | Scoped to this one use case (copied ConfigMaps). Documented in code. Label-based ownership remains the pattern for template-rendered objects. |
| Shared ConfigMap updated after provisioning — copy is stale | Provenance annotations (`source-resource-version`) on the copy record which version it was made from. Support engineers can compare against the source's current `resourceVersion` to identify the discrepancy. The copy is not auto-updated post-provisioning (by design — extra manifests are consumed once during installation). |

### Drawbacks

- Adds a new controller (`NamespaceMonitor`) that must detect deletion of the
  shared namespace. The simplest implementation watches all Namespace objects
  with a client-side predicate, adding one watch to the API server.
- Changing the `ExtraManifestsRefs` type from a core Kubernetes type to a custom
  type is a one-way change. External tooling must be updated.
- The namespace is not cleaned up on operator uninstall, which may surprise
  administrators expecting a clean removal.
- The controller creates copies of shared ConfigMaps in each ClusterInstance
  namespace. While this re-introduces per-namespace copies, the duplication is
  now automated and invisible to the user.

## Design Details

### Open Questions

1. Should the shared namespace support Secrets in addition to ConfigMaps for
   extra manifests? The existing codebase only validates ConfigMaps for extra
   manifests, and this proposal follows that pattern, but the shared-namespace
   approach could be extended to Secrets.

### Test Plan

**Unit tests:**

- `cmd/main_test.go`:
  - Namespace does not exist — created successfully.
  - Namespace already exists — no error, startup proceeds.
  - Client Create error — error propagated.
- `internal/controller/namespace_monitor_test.go`:
  - Namespace deleted triggers re-creation.
  - Namespace exists is a no-op.
  - Reconcile for unrelated namespace is a no-op.
  - Create failure returns error.
  - Non-NotFound Get error returns error.
- `api/v1alpha1/clusterinstance_webhook_test.go`:
  - Empty namespace passes (ValidateCreate and ValidateUpdate).
  - Local namespace passes.
  - `siteconfig-shared-resources` passes.
  - Other namespace rejected with clear error.
  - Multiple refs with mixed valid/invalid namespaces — invalid one rejected.
- `internal/controller/clusterinstance/validator_test.go`:
  - ExtraManifest in local namespace (implicit) passes.
  - ExtraManifest in local namespace (explicit) passes.
  - ExtraManifest in shared namespace passes (ConfigMap exists in shared ns).
  - ExtraManifest in disallowed namespace fails.
  - ExtraManifest does not exist in specified namespace fails.
  - ConfigMap exists locally but referenced as shared — fails.
  - ConfigMap exists in shared namespace but referenced without namespace
    (implicit local) — fails.
- `internal/controller/clusterinstance_controller_test.go` (copy logic):
  - Shared ConfigMap copied to ClusterInstance namespace with correct
    ownerReference, managed-copy label, and provenance annotations.
  - Provenance annotations record source namespace and resourceVersion.
  - Existing managed copy updated with new data and updated provenance
    annotations.
  - Name collision with user-created local ConfigMap — rejected with error.
  - Shared ConfigMap does not exist — error.
  - Namespace field cleared on in-memory ExtraManifestsRefs before rendering.
- Template rendering tests (`internal/controller/clusterinstance/template_engine_test.go`):
  - `toYaml` on ExtraManifestsRefs with empty namespace produces output
    identical to old `LocalObjectReference` format (no `namespace` key in
    YAML output).

**Integration/e2e tests:**

- Operator startup creates the namespace.
- ClusterInstance with shared extra-manifest references reconciles successfully
  (ConfigMap copied locally, downstream CRDs created with local references).
- Deleting the shared namespace while the operator is running triggers
  re-creation.
- Deleting a ClusterInstance garbage-collects its copied ConfigMaps.

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] Implementation merged with adequate test coverage
- [ ] Documented in user-facing operator documentation covering shared
  namespace setup and `ExtraManifestsRefs` namespace field usage
- [ ] Released

### Upgrade / Downgrade Strategy

**Upgrade:** The new `ExtraManifestReference` type is a superset of
`corev1.LocalObjectReference`. Existing CRs are deserialized correctly because
the `name` field is unchanged (including `omitempty`) and `namespace` defaults
to empty (which means local namespace). The CRD schema adds the optional
`namespace` field and a `MinLength=1` validation on `name` (a minor tightening
from the previous schema). No manual migration steps are required.

**Downgrade:** If downgrading to a version that does not support the `namespace`
field, any ClusterInstances using `namespace: siteconfig-shared-resources` will
fail validation. Administrators must remove the `namespace` field from
`extraManifestsRefs` and duplicate the ConfigMaps into local namespaces before
downgrading. Copied ConfigMaps with ownerReferences will be garbage-collected
when the ClusterInstance is deleted or can be manually removed. The
`siteconfig-shared-resources` namespace will remain but will no longer be
managed (harmless orphan).

### Version Skew Strategy

The SiteConfig operator is a standalone component. Version skew with the hub
cluster or managed clusters is not a concern for this change. The only skew
scenario is between the CRD version and the operator version:

- **CRD newer than operator:** The operator ignores the unknown `namespace`
  field (standard Kubernetes behavior for optional fields).
- **Operator newer than CRD:** The operator creates the namespace and registers
  the `NamespaceMonitor`, but the CRD schema does not include `namespace`. Users
  cannot use the feature until the CRD is updated. The operator otherwise
  functions normally.

## Implementation History

- 2026-04-30: Initial proposal created.

## Alternatives

### Alternative A: Validator checks both namespaces without API change

Keep `ExtraManifestsRefs` as `[]corev1.LocalObjectReference` and have the
validator check both the local namespace and `siteconfig-shared-resources` for
each reference.

**Rejected because:** Ambiguous when a ConfigMap with the same name exists in
both namespaces. The user has no control over which one is used. Silent behavior
changes if a ConfigMap is added to or removed from one namespace.

### Alternative B: Use the existing Reference type (like TemplateRef)

Change `ExtraManifestsRefs` to use the existing `Reference` type where
`Namespace` is a required field.

**Rejected because:** The `Namespace` field in `Reference` is required
(`+required`), which would break backward compatibility for existing CRs that
only specify `name`. A new type with an optional `Namespace` field avoids this.

### Alternative C: Protect namespace with a finalizer

Add a custom finalizer to the `siteconfig-shared-resources` namespace to
prevent accidental deletion.

**Rejected because:** If the operator is not running when someone deletes the
namespace (e.g., during operator upgrade or after uninstall), the namespace
enters `Terminating` state and remains stuck indefinitely — no operator
instance exists to remove the finalizer. This blocks access to all ConfigMaps
inside it and breaks the provisioning pipeline permanently until manual
intervention. The `NamespaceMonitor` re-creation approach is more resilient:
deletion completes normally, the controller re-creates the namespace on the
next reconcile, and the namespace can always be cleaned up even without the
operator running.

### Alternative D: Auto-delete namespace on operator uninstall

Add SIGTERM handler or OLM cleanup hook to delete the shared namespace when the
operator is removed.

**Rejected because:** The operator cannot reliably distinguish between pod
restart, upgrade, and uninstall at SIGTERM time. OLM's `spec.cleanup` is not
implemented. Deleting the namespace risks destroying user-created ConfigMaps.
This matches how ClusterInstance CRs are handled — they are not auto-deleted on
uninstall either.

### Alternative E: Pass namespace through to downstream CRDs

Render the `namespace` field in `ExtraManifestsRefs` directly into downstream
CRDs via `toYaml`, relying on them to support cross-namespace references.

**Rejected because:** Downstream CRDs (`AgentClusterInstall` uses
`ManifestsConfigMapReference` with only `Name`;  `ImageClusterInstall` uses
`corev1.LocalObjectReference` with only `Name`) do not support a `namespace`
field. The `namespace` field would either be silently dropped during
deserialization or cause schema validation errors. The local-copy approach
avoids this by ensuring ConfigMaps are always available in the local namespace.

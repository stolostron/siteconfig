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
last-updated: 2026-11-04
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

Those copies are then kept in step with their source: when a shared ConfigMap
is edited, a dedicated controller propagates the change to every cluster
namespace that references it, without re-rendering manifests or touching the
downstream CRDs. This is what makes the shared ConfigMap a genuine single
source of truth — the existing ACM policy that compares a cluster's
configuration against the extra-manifests ConfigMap in its own namespace will
report non-compliance, setting up a TALM-driven rollout to the spoke clusters.

To let administrators stage such a change across a large fleet rather than
applying it everywhere at once, `extraManifestsRefs` also becomes mutable after
installation, so clusters can be repointed to a new shared ConfigMap in
batches.

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

**As a platform administrator**, I want the shared namespace to be
automatically re-created if it is accidentally deleted, so that recovery means
restoring the ConfigMaps from Git rather than also reconstructing the
namespace and its configuration. Deleting the namespace still destroys the
ConfigMaps inside it: re-creation restores the container, not the content.

**As a platform administrator**, I want the operator to create the shared
namespace automatically so that I do not need to perform manual setup steps
before using the feature.

**As a platform administrator**, I want an edit to a shared ConfigMap to be
reflected in every referencing cluster namespace promptly, so that the ACM
policy that compares cluster state against the cluster-namespace copy reports
non-compliance and TALM can drive the change out to the spoke clusters.

**As a platform administrator managing a large fleet**, I want to roll a
manifest change out gradually rather than to all clusters at once, so I want to
create a second shared ConfigMap (`common-manifests-v2`) and repoint batches of
ClusterInstances at it over time — which requires `extraManifestsRefs` to be
editable after a cluster has been installed.

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
  - When a shared ConfigMap is updated, the `SharedManifestSync` controller
    propagates the new content to every cluster-namespace copy that references
    it.
- **Monitoring**: No new status conditions are required on ClusterInstance, but
  the existing ones must be used deliberately. `ClusterInstanceValidated`
  covers a referenced ConfigMap that does not exist and — because the collision
  check runs as part of validation (see
  [Name collision handling](#name-collision-handling)) — a name collision.
  Template failures split across three conditions: `RenderedTemplates` for
  rendering failures, `RenderedTemplatesValidated` for dry-run rejection, and
  `RenderedTemplatesApplied` for apply failures. Propagation health is
  observable by comparing each copy's
  `data`/`binaryData` against its source, by noticing a copy that is absent
  altogether (a third state, distinct from current and lagging), by the
  `provenance` annotations for each copy's lineage, and through
  controller-runtime's standard per-controller metrics
  (`controller_runtime_reconcile_errors_total{controller="SharedManifestSync"}`).
- **Remediation**: If the shared namespace is deleted, the `NamespaceMonitor`
  controller re-creates it. Administrators must re-create any ConfigMaps that
  were inside it. If a ClusterInstance references a ConfigMap that does not
  exist, reconciliation fails with a clear error message identifying the
  missing ConfigMap and namespace. The reconciler passes
  `DefaultValidationErrorDelay` (30 seconds) alongside a non-nil error, so
  controller-runtime discards that delay and retries on its default
  exponential backoff instead.
- **Scale**: Provisioning-time copying adds one Get plus one Create/Update per
  shared ConfigMap reference per reconcile — negligible. Propagation is
  discussed in detail under
  [Shared ConfigMap update propagation](#shared-configmap-update-propagation);
  in short, it costs one write per referencing cluster namespace per shared
  ConfigMap change, is driven by a separate controller with its own
  concurrency limit, and therefore does not consume
  `maxConcurrentReconciles` budget from the `ClusterInstanceReconciler`.

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
- **The controller automatically updates the copied ConfigMaps in each
  ClusterInstance namespace when the shared ConfigMap is updated**, so that the
  cluster-namespace copy converges on the shared single-source-of-truth. This
  holds regardless of whether the referencing cluster has finished
  provisioning. Convergence is on content rather than on edit events; see
  [Change detection and convergence](#change-detection-and-convergence) for
  what it does and does not guarantee.
- `extraManifestsRefs` is mutable after installation, so administrators can
  stage a fleet-wide manifest change by creating a new shared ConfigMap and
  repointing clusters in batches.
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
- Supporting Secrets in the shared namespace. Scope is `extraManifestsRefs`
  and what the backend installers accept there, which is ConfigMaps only.
  This covers both readings of the question: hub-side Secrets such as the
  pull secret, and Secrets intended to land on the spoke cluster. Settled
  during review.
- Reconciling the *spoke cluster's* state against an updated shared ConfigMap.
  Propagation stops at the cluster-namespace copy; driving that content onto an
  already-installed cluster is the job of an ACM policy and TALM (see
  [Relationship to policy-driven day-2 reconciliation](#relationship-to-policy-driven-day-2-reconciliation)).
- Detecting or reporting hand edits to a copy. Because propagation compares
  content, the next propagation overwrites the fields the controller manages —
  but nothing watches the copies as currently specified, so a hand edit is
  repaired when the source next changes or at the next cache resync — by
  default within ten hours — not when the edit happens.
  [Open Question 2](#open-questions) proposes adding that watch, which would
  make repair prompt; it would not add detection or reporting, which stay out
  of scope. See
  [Change detection and convergence](#change-detection-and-convergence).
- Extra manifests for the Hosted Control Plane flow. `ExtraManifestsRefs` is not
  referenced by any template in `internal/templates/hosted-cluster/`, so this
  feature applies only to the Assisted Installer and Image Based Installer
  flows.

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
     the ConfigMap into the ClusterInstance namespace. Exactly three things are
     taken from the source: its `name`, its `data` and its `binaryData`. **No
     label and no annotation on the source is copied** (see
     [Source metadata is not copied](#source-metadata-is-not-copied)). The
     controller then sets a Kubernetes ownerReference to the ClusterInstance
     for automatic garbage collection, and writes its own managed-copy label
     and provenance annotations recording which version of the source
     ConfigMap it was copied from (see
     [Copy provenance annotations](#copy-provenance-annotations)).
   - Clears the `Namespace` field on the in-memory ExtraManifestsRefs before
     passing them to the template engine, so that `toYaml` renders only the
     `name` field — matching what downstream CRDs expect.
5. Template rendering and manifest application proceed normally. Downstream
   CRDs (`AgentClusterInstall.manifestsConfigMapRefs`,
   `ImageClusterInstall.extraManifestsRefs`) see local ConfigMap references
   with no `namespace` field.

**Shared ConfigMap is updated:**

1. Administrator edits a ConfigMap in `siteconfig-shared-resources`.
2. The `SharedManifestSync` controller receives the update event and resolves
   the set of ClusterInstances referencing that ConfigMap via a field index.
3. For each one, it updates the copy in that ClusterInstance's namespace with
   the new `data`/`binaryData` and refreshes the
   `source-resource-version` annotation.
4. No ClusterInstance reconcile is triggered, no templates are re-rendered, and
   no downstream CRD is touched. The ClusterInstance's `ObservedGeneration`
   short-circuit is left intact.

This is the mechanism that lets an ACM policy treat the cluster-namespace copy
as the single source of truth for day-2 compliance — see
[Relationship to policy-driven day-2 reconciliation](#relationship-to-policy-driven-day-2-reconciliation).

**Staged rollout (repointing a batch of clusters):**

1. Administrator creates `common-manifests-v2` in the shared namespace,
   leaving `common-manifests-v1` in place.
2. For a batch of ClusterInstances, the administrator edits
   `extraManifestsRefs` to reference `common-manifests-v2`.
3. Because this is a spec change, `metadata.generation` is incremented, the
   `ObservedGeneration` short-circuit is bypassed, and the full reconcile runs:
   validation, re-render, dry-run, and Server-Side Apply of all manifests. The
   copy for `v2` is created in the cluster namespace and the downstream CRD's
   manifest reference is updated to the new name.
4. The `v1` copy is left in place until the ClusterInstance stops referencing
   it; see [Orphaned copies](#orphaned-copies).

Note the asymmetry between these two workflows, which is central to this
design:

| | Shared ConfigMap edited in place | ClusterInstance repointed to a new name |
|---|---|---|
| Trigger | ConfigMap event | ClusterInstance spec change |
| What changes | Copy `data` only | Copy set, plus downstream CRD manifest reference |
| ClusterInstance reconcile | No | Yes (full re-render + SSA) |
| Downstream CRD updated | No | Yes |
| Risk profile | Low — ConfigMap writes only | Carries the open question in [Post-installation mutability of `extraManifestsRefs`](#post-installation-mutability-of-extramanifestsrefs) |

**Downstream CRD compatibility:**

The downstream CRDs that consume `ExtraManifestsRefs` do not support a
`namespace` field:

- `AgentClusterInstall.spec.manifestsConfigMapRefs` uses
  `ManifestsConfigMapReference` (has only `Name`).
- `ImageClusterInstall.spec.extraManifestsRefs` is understood to use
  `corev1.LocalObjectReference` (only `Name`). `ImageClusterInstall` is not
  vendored in this repository, so this is taken from its upstream API rather
  than verified here.

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

#### Why an explicit field rather than a dual-namespace lookup

The obvious alternative, raised during review, is to change no API at all and
have the controller look in both namespaces
(see [Alternative A](#alternative-a-validator-checks-both-namespaces-without-api-change)).
Its premise is correct: `namespace` can only ever be empty, the ClusterInstance's
own namespace, or `siteconfig-shared-resources`, so the field is effectively
an enum with two distinct behaviours. Two arguments that might appear to favour
the field do not survive scrutiny and are not relied on here:

- **Collision handling is not a differentiator.** A dual-namespace lookup can
  distinguish the controller's own copies from user-created ConfigMaps by the
  managed-copy label and ownerReference, exactly as this design does, and fail
  the same way. Collision behaviour is identical under both.
- **Forward-compatibility is not a differentiator.** If assisted-service and
  IBIO ever accept a namespace on their ConfigMap reference types, the copy
  path still has to stay, so the field buys no migration saving (see
  [Alternative E](#alternative-e-pass-namespace-through-to-downstream-crds)).

What does distinguish them is **opt-in blast radius**. Under a dual-namespace
lookup the shared namespace becomes a hub-wide fallback for every
ClusterInstance on the hub, including every one that never opted in: a
reference whose named ConfigMap is absent locally is a hard validation failure
today, and would instead begin resolving against whatever the shared namespace
happens to contain. Write access to that one namespace would then reach the
install content of any cluster with a missing local ConfigMap. With an explicit
field, a ClusterInstance that does not set `namespace` cannot be influenced by
the shared namespace at all, and opting in is a reviewable change to the
ClusterInstance in Git rather than a side effect of creating a ConfigMap
somewhere else. A local-wins precedence rule narrows that exposure but does not
close it, and it reintroduces resolution order as something an operator has to
reason about during an incident.

The costs of the field are real and are accepted:

- It is permanent API surface. A controller lookup rule could be withdrawn in a
  patch release; a shipped CRD field can only be deprecated.
- It puts an operator-owned namespace name into user CRs, so a future rename
  becomes a fleet-wide CR migration rather than a change to a single constant.
  That name has already changed twice during this review and is now back at
  its original value.
- Adopting the shared namespace for an existing cluster requires editing the
  ClusterInstance, which bumps `metadata.generation` and forces a full
  re-render and Server-Side Apply even though the rendered downstream payload
  is byte-identical. Under the lookup rule, adoption would be a ConfigMap move
  with no ClusterInstance edit at all.

#### Post-installation mutability of `extraManifestsRefs`

Staged rollout requires `extraManifestsRefs` to be editable after a cluster is
installed. Today it is not: `ValidateUpdate` routes post-provisioning edits
through `validatePostProvisioningChanges` →`validateChangesWithJSONDiff`
(`api/v1alpha1/clusterinstance_validation.go`), which rejects any JSON-diff
path not present in `ClusterLevelAllowedPaths` or `NodeLevelAllowedPaths`.
`/extraManifestsRefs` is absent from both.

**Change required:** add `/extraManifestsRefs` to `ClusterLevelAllowedPaths`
alongside the six existing entries (`/extraAnnotations`, `/extraLabels`,
`/suppressedManifests`, `/pruneManifests`, `/clusterImageSetNameRef`,
`/holdInstallation`). This is a one-line change; no new validation machinery is
needed.

**Granularity is all-or-nothing.** `pathMatchesPattern` splits both the diff
path and the pattern on `/` and compares segment-wise, treating `*` as a
single-segment wildcard and succeeding when the pattern is a prefix of the
path. Adding `/extraManifestsRefs` therefore permits every mutation beneath
that subtree — append, remove, reorder, and per-element `name`/`namespace`
edits.

A narrower rule such as "additions only" is **not** expressible with the
existing declarative mechanism: `pathMatchesAnyPattern` is given only
`diff.Path`, never `diff.Type`. Op-aware rules exist but are hardcoded
one-offs (`isCRDUpgradeFieldAddition`, which matches exactly
`/cpuArchitecture` + `OperationAdd`, and the `/nodes/*` switch). More
importantly, an additions-only rule would not serve the use case anyway:
`jsondiff` is configured without LCS, so arrays diff by index, and repointing
`v1`→`v2` in place emits `replace` at `/extraManifestsRefs/0/name`, not `add`.

**What stays blocked.** This change does not loosen anything during
provisioning. While `ClusterProvisioned.Reason == InProgress`, or while a
reinstall is in progress, `determineSpecChangePermission` returns
`SpecChangeBlocked` and the webhook rejects *any* spec change via a whole-spec
`DeepEqual` — the allow-list is never consulted in that branch. The same
applies in two less obvious states that fall through to the default
`SpecChangeBlocked`: when the `ClusterProvisioned` condition is absent
(a freshly created ClusterInstance before its first reconcile), and when
provisioning has **Failed**.

One branch precedes all of those and is easy to miss:
`determineSpecChangePermission` first returns `SpecChangeUnrestricted` when
`holdInstallation` was already set on the old object and provisioning has not
completed. That escape hatch therefore applies during the in-progress states
too, not only in the two fall-through states, and once `/extraManifestsRefs`
is allow-listed it covers that field as well — though by construction the
cluster is not installing while it is held. Repointing without
`holdInstallation` remains available exactly when provisioning has completed,
which is the intended window.

**Precedent, with a caveat.** `/clusterImageSetNameRef` is already day-N
mutable and renders into `AgentClusterInstall.spec.imageSetRef` and
`ImageClusterInstall.spec.imageSetRef`, so the control flow for changing a
downstream install-CR field after provisioning already exists. What this
repository cannot show is that assisted-service or the IBI operator has ever
*accepted* such an update in practice — the same unknown as Open Question 1.
The precedent establishes the mechanism, not the outcome.
Adding `/extraManifestsRefs` introduces no new control flow: a full re-render
and Server-Side Apply of every manifest already happens today for all ten
existing allow-listed paths. The only new thing is that the re-applied
`AgentClusterInstall` / `ImageClusterInstall` payload differs in content.

**Open risk — see [Open Questions](#open-questions).** Whether the downstream
CRDs accept an in-place update to their manifest reference after installation
is not established. At the schema level `AgentClusterInstall.spec.manifestsConfigMapRefs`
carries only `+optional` with no CEL immutability rule, so the CRD itself will
accept the change; but the assisted-service validating webhook is not vendored
in this repository and cannot be inspected here, and there is precedent for
assisted-service enforcing `AgentClusterInstall` immutability elsewhere
(`internal/controller/clusterinstance/helper.go` documents exactly such a
constraint for SNO expansion). `ImageClusterInstall` is not vendored in any
form, so its behaviour is unknown.

If a downstream rejection does occur, the failure mode is severe and must be
understood before implementation: `handleRenderTemplates` dry-runs **every**
rendered object before applying **any** of them, so a single rejected object
means nothing at all is applied for that ClusterInstance, `ObservedGeneration`
is never advanced, and the ClusterInstance retries indefinitely with no pause
annotation and no self-healing. The retry interval is **not** the 30 seconds
of `DefaultTemplateRenderingErrorDelay`: that delay is returned alongside a
non-nil error, and controller-runtime discards the result whenever the error
is non-nil, requeueing on the workqueue rate limiter instead. No custom rate
limiter is configured, so the default exponential backoff applies and the
interval grows towards a ceiling of roughly 16 minutes — the failure is both
permanent and progressively slower to retry. **This must be settled by a live-cluster test before the
enhancement moves to `implementable`.** The guaranteed-safe fallback is
reinstall, which tears the downstream CR down and recreates it rather than
updating in place.

### Siteconfig Impact

- **Controllers**:
  - `ClusterInstanceReconciler`: Updated to copy shared ConfigMaps into the
    ClusterInstance namespace before template rendering. A new
    `copySharedExtraManifests` function handles the copy logic with
    ownerReferences. The read-only name-collision check is added to
    `ci.Validate`, so a collision reports on the `ClusterInstanceValidated`
    condition (see [Name collision handling](#name-collision-handling));
    `ci.Validate` remains read-only. The `ObservedGeneration` pre-empt is
    **not** modified by the copy or propagation paths. If
    [Open Question 2](#open-questions) is settled as proposed, this controller
    also gains a narrow "ensure managed copies" step that runs *ahead* of the
    pre-empt and a startup pass over ClusterInstances carrying shared
    references; both are recovery-only, perform no rendering, and do not
    advance `ObservedGeneration`.
  - `NamespaceMonitor` (new): Lightweight controller watching for deletion of
    the shared namespace, inspired by the `ConfigurationMonitor` pattern but
    using a more targeted predicate that only processes delete events (the
    namespace does not carry configuration that needs syncing on updates).
    It deliberately does **not** watch ConfigMaps; propagation is a separate
    concern with a separate reconcile key (see
    [Shared ConfigMap update propagation](#shared-configmap-update-propagation)).
  - `SharedManifestSync` (new): Watches ConfigMaps in the shared namespace and
    propagates updates to the cluster-namespace copies. If
    [Open Question 2](#open-questions) is settled as proposed, it also watches
    objects carrying the managed-copy label so that deleting or editing a copy
    enqueues the owning ClusterInstance — detection only; the write stays in
    `ClusterInstanceReconciler`.
- **Templates**: No changes to template definitions. The controller clears the
  `namespace` field on in-memory ExtraManifestsRefs before template rendering,
  so `toYaml` produces output identical to the current `LocalObjectReference`
  format (only `name`).
- **API fields**: `ExtraManifestsRefs` type changes from
  `[]corev1.LocalObjectReference` to `[]ExtraManifestReference`.
- **Validation**: Webhook validation adds namespace restriction and adds
  `/extraManifestsRefs` to `ClusterLevelAllowedPaths`. Controller validator
  updated for cross-namespace ConfigMap lookup with defense-in-depth namespace
  check.
- **RBAC**: The existing ClusterRole already grants cluster-wide
  `get;list;watch;create;update;patch;delete` on ConfigMaps, so
  `SharedManifestSync` needs **no RBAC change**. `NamespaceMonitor` still
  requires adding the missing `watch` verb on namespaces.

### Implementation Details

#### Namespace naming

The namespace is named `siteconfig-shared-resources`, matching the
`siteconfig-` prefix the operator already uses for its own resources
(`siteconfig-operator-configuration`, `siteconfig-controller-manager`).

An earlier revision renamed it to `openshift-siteconfig-resources`, for
consistency with components delivered under the OpenShift umbrella. That rename
has been reverted on user-experience grounds. The console classes
`openshift-`-prefixed namespaces as system projects and hides them behind the
"Show default projects" toggle — a UI default rather than a permission
boundary, but one that applies to administrators as well as ordinary users.
(Console behaviour is external to this repository and is taken from the
review discussion rather than verified here.)
This namespace exists precisely so that administrators can put shared
ConfigMaps in it and edit them, so hiding it by default works against the
primary workflow. The prefix also left two questions open that could not be
answered from this repository: whether the managed-cluster validating webhook
on ROSA/OSD would permit the operator's ServiceAccount to create a privileged
namespace, and — more importantly — whether administrators could still create
and edit ConfigMaps inside one. An ordinary `siteconfig-` name carries no
special treatment, so it retires both questions rather than leaving them to a
live-cluster check.

The other reason originally given for the `openshift-` prefix was that
reserved prefixes avoid collision with user-created namespaces. That holds,
and the revert gives it up: `siteconfig-shared-resources` is an ordinary name
a user could already have created. The operator's create is
create-if-not-exists, so it adopts whatever is there rather than failing,
which means a pre-existing namespace of that name is used as-is. This is
accepted on the grounds that the name is specific enough for collision to be
unlikely, and that the consequence is shared rather than destructive — the
operator adds its labels and the namespace continues to hold whatever else it
held. An administrator who wants certainty can check for the namespace before
installing the operator.

Two points are independent of the name and survive the revert:

- **Pod Security Admission.** The operator sets
  `pod-security.kubernetes.io/enforce: restricted` on the namespace at
  creation, so it fails closed if anyone later tries to run a pod there. The
  namespace holds only ConfigMaps, so this is hardening rather than a
  requirement. The limit of that guarantee is worth stating:
  `NamespaceMonitor` processes delete events only and label reconciliation
  happens at startup, so a label stripped at runtime is not restored until the
  operator restarts. Having the monitor correct label drift as well is a small
  change and should be settled during implementation.
- **must-gather does not collect the namespace automatically.** A previous
  revision stated flatly that `openshift/must-gather` and
  `stolostron/must-gather` select namespaces by explicit name lists and
  ClusterOperator `relatedObjects` rather than by prefix. Neither repository is
  vendored here and this one contains no must-gather artefact, so that claim
  cannot be checked from this repository and is recorded as unverified. The
  design does not rely on the namespace name for collection either way;
  coverage is handled as an explicit follow-up, so that a support bundle
  containing ClusterInstance CRs also contains the shared ConfigMaps they
  reference. This is tracked as a graduation criterion rather than an open
  question, because the change lands in the must-gather repository.

**Namespace constant:** Defined as `SharedResourcesNamespace` in
`api/v1alpha1/groupversion_info.go`, alongside the existing `Group`, `Version`,
and `ClusterInstanceKind` constants.

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

**Required invariant: `spec.clusterName` equals the ClusterInstance
namespace.** The copy is placed in `clusterInstance.Namespace`, but every
install CR is rendered into `{{ .Spec.ClusterName }}`, and
`AgentClusterInstall.spec.manifestsConfigMapRefs` is resolved by
assisted-service within the AgentClusterInstall's own namespace. The two are
equal by established convention, and the existing validator already assumes it
when it looks up extra manifests, but nothing enforces it: `ci.Validate` only
checks that `clusterName` is non-empty and the CRD carries no CEL rule.

This enhancement makes that latent assumption load-bearing for a write and for
garbage collection, so it must be enforced rather than assumed. If the two
differ, both placements are wrong: a copy in `clusterInstance.Namespace` sits
where no downstream CR will look and the install fails, while a copy in
`spec.clusterName` would carry a cross-namespace ownerReference, which
Kubernetes treats as an absent owner — the garbage collector would remove the
copy almost immediately. `validateExtraManifestReferences` therefore rejects a
ClusterInstance that references the shared namespace while
`spec.clusterName != metadata.namespace`. The check is scoped to references
that actually use the shared namespace, so no existing CR changes behaviour.

#### Name collision handling

A ConfigMap in the ClusterInstance namespace is treated as the controller's
own copy only if it carries **both** the managed-copy label and an
ownerReference whose UID matches the current ClusterInstance. Anything else —
no label, no ownerReference, or an ownerReference carrying a stale UID left
behind by a `--cascade=orphan` deletion and re-create — is treated as a
user-created object and is never overwritten.

Because this check is read-only, it runs as part of the validation phase
(`ci.Validate`) rather than as part of the copy. The placement is load-bearing:
the copy runs after `handleValidate` and before template rendering, and nothing
in that gap writes `Status.Conditions`. A collision detected there would leave
`ClusterInstanceValidated=True`, no failing condition anywhere, no Event, and
an `ObservedGeneration` that silently stops advancing — the failure would be
invisible to the GitOps operators this feature targets. Detecting it during
validation means it surfaces on the existing `ClusterInstanceValidated`
condition, with a message naming the conflicting ConfigMap and namespace, and
blocks reconciliation before any copy is attempted.

The user-created ConfigMap is never silently used in place of the shared one:
installing a cluster with manifests the spec did not ask for would be worse
than failing.

Collisions are also reachable **after** a successful copy — a user can delete
the managed copy and create their own object under the same name. The
propagation controller therefore repeats the same ownership check before every
write; see
[Shared ConfigMap update propagation](#shared-configmap-update-propagation).

#### Source metadata is not copied

A copy is constructed from its source, never deep-copied from it. Exactly
three things are taken from the source ConfigMap: its `name`, its `data` and
its `binaryData`. **No label and no annotation on the source is copied onto
the copy** — not at creation, not during propagation, and not on the re-copy
that happens during a reinstall. Every label and annotation a copy carries is
written by the controller: the managed-copy label, and the provenance
annotations below.

The rule is deliberately a blanket one rather than a list of keys to strip,
because the hazards are open-ended and three are already visible.

**GitOps ownership metadata.** The shared ConfigMaps are expected to be
managed from Git — that is the premise of the feature — and this project
documents deploying from an ArgoCD `Application` with
`syncPolicy.automated.prune: true` (`docs/argocd.md`). GitOps engines mark the
objects they own with labels and annotations and use those marks to decide
what to prune. Copying such a mark onto a controller-created object in a
ClusterInstance namespace, which no Git manifest renders, presents that object
to the engine as Git-owned with no source behind it. The likely outcomes are
that the engine prunes the copy on its next sync, or reports it permanently as
out-of-sync or orphaned. The exact keys and pruning semantics are properties
of the GitOps engine and the user's `Application`, not of siteconfig, and are
not asserted here; a blanket rule holds without depending on them.

**Reinstall preservation labels.** If a source ConfigMap carried
`siteconfig.open-cluster-management.io/preserve`, a copy inheriting it would
be swept into the reinstall backup/restore/cleanup path whenever the
referencing ClusterInstance used `PreservationMode: All`, producing two
controllers writing the same object. The `.internal` variant is worse: the
controller runs an internal preservation pass alongside the user's mode on
every reinstall where the mode is not `None`, so a copy carrying that label
would be swept in regardless of which mode the user chose.

**ACM hub backup label.** Labelling the shared ConfigMaps with
`cluster.open-cluster-management.io/backup` so the inputs survive a hub
restore is contemplated elsewhere in this proposal. The copies must not
inherit it: they are reconstructible from the source, so backing up one copy
per cluster namespace adds bulk to the backup and restores objects the
reconciler would recreate anyway.

#### Copy provenance annotations

Each copied ConfigMap carries three provenance annotations:

```yaml
annotations:
  siteconfig.open-cluster-management.io/source-namespace: siteconfig-shared-resources
  siteconfig.open-cluster-management.io/source-resource-version: "91823"
  siteconfig.open-cluster-management.io/initial-resource-version: "48291"
```

- `source-namespace`: The namespace the ConfigMap was copied from. Provides a
  direct pointer back to the source. Constant in the current design, but
  recorded so that a future design supporting additional source namespaces does
  not need an annotation change.
- `source-resource-version`: The `metadata.resourceVersion` of the source at
  the time of the most recent successful propagation. **Updated on every
  propagation**, by the `ClusterInstanceReconciler` on the initial copy and by
  `SharedManifestSync` thereafter. It is recorded for provenance and is **not**
  the change-detection key; see
  [Change detection and convergence](#change-detection-and-convergence).
- `initial-resource-version`: The source's `resourceVersion` at the time
  **that copy** was first created, never updated thereafter. The name is
  deliberately not `provisioned-resource-version`: the version a copy was
  created from is frequently not the version the cluster installed from.
  Three cases make them diverge, and all three are accepted rather than
  engineered around:
  - A copy created by a post-installation repoint records the version at
    repoint time, not at install.
  - A ClusterInstance created with `spec.holdInstallation: true` has its
    copies created on the first reconcile, because nothing in
    `ClusterInstanceReconciler` is gated on `holdInstallation` — within the
    reconciler the field is only rendered, into
    `AgentClusterInstall.spec.holdInstallation`, and only by the Assisted
    Installer templates; the webhook consults it, but no reconcile branch
    does. A source edit made before the hold is released is therefore
    propagated into the copy, and the cluster installs from the edited
    content while this annotation still names the pre-edit version. This is
    not a race; it is the deterministic form of the same gap.
  - Because propagation is not gated on provisioning state, a cluster caught
    by the mid-install race below may consume content matching neither
    recorded version.

  Where the install-time version is needed precisely, it must be answered from
  the ClusterInstance status rather than from this annotation.

The two version annotations answer different questions, and the split exists
precisely because propagation makes them diverge:

| Question | Annotation |
|---|---|
| Is this copy up to date with the shared ConfigMap? | Compare the copy's `data`/`binaryData` against the source's — the same test propagation uses |
| Which version was this copy first created from? | `initial-resource-version` — equal to the install-time version only if the copy predates the install and the reference has not been repointed since |
| Has the source been written since this copy was created? | `initial-resource-version` vs. the source's **live** `resourceVersion` |

Freshness is a content question, which is why the first row compares content
rather than annotations. `source-resource-version` only advances when
propagation writes, and propagation deliberately does not write when a source
edit touched metadata alone, so comparing that annotation against the source's
live `resourceVersion` reports staleness that does not exist. It also fails the
other way: when propagation is lagging, an existing copy still carries the
annotation the controller last wrote while its content sits behind the source.

A missing copy is a third state rather than a stale one. There is no
annotation and no content to compare, so neither test applies; it is unhealthy
by definition, and only the referencing ClusterInstance's `extraManifestsRefs`
reveals that the copy should exist at all. Health reporting must therefore
distinguish three cases — present and current, present and lagging, and
absent — rather than folding the last into staleness.

The annotations remain useful as provenance, and for the coarser question in
the third row — whether the source object has been written at all since this
copy was created — which the live comparison answers correctly because
`initial-resource-version` never moves.

Note: `resourceVersion` is opaque and is not comparable across different
objects, but it is valid for tracking a single object (the source ConfigMap)
over time, which is all that is required here. It is used for equality
comparison only, never ordering.

#### Shared ConfigMap update propagation

A dedicated `SharedManifestSync` controller watches ConfigMaps in the shared
namespace and propagates updates to the cluster-namespace copies.

**Why a separate controller rather than extending `ClusterInstanceReconciler`.**
Three reasons, all structural:

1. `ClusterInstanceReconciler.SetupWithManager` applies its predicates via
   `WithEventFilter`, which is controller-wide and would therefore also filter
   any `Watches()` source added to that controller. Its predicate set is
   `GenerationChangedPredicate | LabelChangedPredicate | holdAnnotationPredicate
   | provisionedChangedPredicate`. ConfigMaps do not have a meaningful
   `metadata.generation`, so `GenerationChangedPredicate` compares `0 != 0` and
   a data-only ConfigMap update would be **silently dropped**. Supporting the
   watch there would mean restructuring the existing event filter into
   per-watch predicates — a change to the hot path of the main reconciler, for
   no benefit.
2. Propagation and provisioning have different reconcile keys. Propagation is
   keyed on the shared ConfigMap and fans out to N namespaces; provisioning is
   keyed on one ClusterInstance. Conflating them means either reconciling the
   ClusterInstance (which the `ObservedGeneration` short-circuit exists to
   avoid) or threading a second code path through the main loop.
3. Keeping them separate means propagation **cannot** trigger a re-render.
   Nothing downstream of `handleRenderTemplates` runs, so a shared ConfigMap
   edit carries none of the Server-Side Apply risk described under
   [Post-installation mutability](#post-installation-mutability-of-extramanifestsrefs).

**Resolving which ClusterInstances reference a ConfigMap.** A field index is
registered on `ClusterInstance` before the manager starts, mapping each
`spec.extraManifestsRefs` entry whose namespace is the shared namespace to the
referenced ConfigMap name. On a ConfigMap event the controller does a single
indexed `List` rather than listing and filtering every ClusterInstance in the
cluster. The repository currently registers no field indexes, so this is the
first; `IndexField` must be called before `mgr.Start`, and the idiomatic
placement is inside the new controller's `SetupWithManager` ahead of the
builder chain, mirroring how `ConfigurationMonitor.SetupWithManager` validates
its own configuration before building.

**Write path.** For each referencing ClusterInstance the controller updates the
copy's `data`, `binaryData`, and `source-resource-version` annotation, and
nothing else. It does **not** touch `initial-resource-version`, the
ownerReference, or the managed-copy label, and it adds, removes or alters no
other label or annotation on the copy — in particular it never syncs the
source's own labels or annotations across.

Every write is gated on the same ownership check used at copy time (see
[Name collision handling](#name-collision-handling)), and there are two
distinct skip cases:

- **No object with that name.** Propagation skips it and leaves creation to the
  `ClusterInstanceReconciler` — propagation never creates a copy for a
  ClusterInstance that has not yet been reconciled, which keeps creation and
  ownership in exactly one place.
- **An object exists but is not a managed copy.** This is a collision that
  appeared after the initial copy succeeded. Propagation must not overwrite it
  and skips it, emitting a warning Event against the ClusterInstance. An
  `EventRecorder` is already wired on the reconciler but currently unused
  anywhere in the operator, so this is a new pattern and is called out
  deliberately; the alternative — a silent successful write over user data —
  would not surface on any condition, on `ObservedGeneration`, or on
  `controller_runtime_reconcile_errors_total`, because the write succeeds.

Writes therefore use an explicit `Get`-then-`Update`, **not**
`controllerutil.CreateOrUpdate`. That helper creates the object when the `Get`
returns `NotFound`, which would bypass both the skip-if-missing rule and the
ownership guard and would produce copies with no ownerReference, no
managed-copy label and no `initial-resource-version` — ConfigMaps that are
never garbage-collected when the ClusterInstance is deleted.

This also deliberately avoids Server-Side Apply. Both SSA call sites in the
codebase (`internal/controller/clusterinstance_controller.go` and
`internal/controller/reinstall/reimport.go`) use field manager
`siteconfig-controller` with `client.ForceOwnership`. Two appliers sharing one
field-manager name do not conflict — the second apply's field set replaces the
first's under that manager, so fields present only in the earlier apply are
pruned, which is a quieter failure than a conflict. If SSA is preferred during
implementation, it must use a distinct field manager.

#### Change detection and convergence

Propagation compares the copy's `data` and `binaryData` against the source's
and writes only when they differ. It deliberately does **not** key on the
copy's `source-resource-version` annotation. Comparing that annotation against
the source's live `resourceVersion` would be marginally cheaper and is wrong in
both directions:

- Any write to the source that leaves its content untouched — a label, an
  annotation, an ACM backup label, a field-manager change — bumps
  `resourceVersion` and would fan out one no-op write per referencing
  namespace. Content comparison makes that case genuinely free, which matters
  because the cost model below assumes only intentional content edits.
- A copy whose `data` was edited by hand still carries whatever annotation the
  controller last wrote, so an annotation-keyed check would skip it
  permanently. Content comparison repairs it on the next propagation.

The copy is read anyway for the ownership check, so comparing content costs
nothing extra.

**What convergence does not cover.** Propagation is edge-triggered on source
events, so two cases are not self-healing:

- **A deleted copy is not recreated.** Propagation skips a missing object by
  design, and the `ClusterInstanceReconciler` cannot recreate it for an
  already-provisioned cluster because of the `ObservedGeneration` pre-empt. The
  copy stays missing until something bumps the ClusterInstance generation. A
  missing copy must therefore also register as non-compliance on the
  extra-manifests policy rather than being silently skipped, or the failure is
  invisible end to end. That is external to this repository and is recorded as
  [Open Question 3](#open-questions).
- **A hand-edited copy is repaired only when the source next changes, or at
  the next cache resync**, because nothing watches the copies as currently
  specified. The manager's informer resync defaults to ten hours and
  redelivers every cached object as an update, which content comparison turns
  into a drift check — so drift does converge, but on a bound measured in
  hours rather than promptly.

Both holes have the same shape and the same candidate fix: a watch on objects
carrying the managed-copy label, so that deleting or editing a copy enqueues
the owning ClusterInstance.

**The runtime cost of that watch is close to zero.** `ConfigurationMonitor`
already declares `For(&corev1.ConfigMap{})`, and `cmd/main.go` passes no
`Cache` option to `ctrl.NewManager`, so `DefaultNamespaces`,
`DefaultLabelSelector` and `ByObject` are all unset: the ConfigMap informer is
cluster-wide and unfiltered today. controller-runtime keys informers by GVK
and wires a watch by fetching that informer and adding an event handler, so a
second ConfigMap watch adds one handler to an informer that already exists —
no second list/watch against the API server and no second copy of the objects
in memory. The ClusterRole already grants `watch` on ConfigMaps cluster-wide,
so no RBAC change is needed either. The marginal cost is one predicate
evaluation per ConfigMap event the process already receives, plus the recovery
writes, which are zero in steady state. A cache-level label selector cannot
reduce it further: `cache.Options.ByObject` is keyed by object type, so a
managed-copy selector on ConfigMaps would apply to every ConfigMap the manager
reads and would break both `ConfigurationMonitor`'s watch on the unlabelled
operator ConfigMap and the validator's reads of user extra-manifest
ConfigMaps. The label filter belongs in a per-watch predicate, after delivery.

**The real cost is structural**, which is why this remains an open question
rather than specified behaviour. Recreating a copy means either letting
`SharedManifestSync` create objects, contradicting the single-ownership rule
above, or adding a narrow "ensure managed copies" step to
`ClusterInstanceReconciler` that runs *before* the `ObservedGeneration`
pre-empt — a new early branch in the main reconciler's hot path, which must
return immediately for ClusterInstances holding no shared references. The
watch must also not be attached to `ClusterInstanceReconciler` with
`Watches()`: that controller filters every source through a controller-wide
`WithEventFilter`, and its behaviour on ConfigMaps is erratic rather than
uniformly closed. Of the four event kinds only `Update` is filtered at all —
the four predicates override `Update` and leave `Create`, `Delete` and
`Generic` defaulting to true — and even an `Update` passes when the object's
labels change, because `LabelChangedPredicate` is type-agnostic. A data-only
edit is dropped for the `GenerationChangedPredicate` reason given above, but
stripping the managed-copy label off a copy would be delivered. Depending on
that mixture is fragile. (`WatchesRawSource` is documented as bypassing
`WithEventFilter` entirely and would be sound, if the watch has to live
there.) See [Open Question 2](#open-questions).

**Immutable copies were considered and rejected.** The proposal was to set
`immutable: true` on the copies and replace them whenever the source changes,
on the grounds that a hand edit is user error. It does block hand edits to
`data` and `binaryData`, which is what it is meant to do.

The decisive objection is the replacement step. An immutable ConfigMap's
`data` and `binaryData` cannot be updated in place — only its metadata can —
so every source change becomes delete-then-create — the pattern
`initConfigMapTemplates` is already forced into for the immutable
installation template ConfigMaps in `cmd/main.go`. That opens a window on
every propagation in which the copy does not exist. The extra-manifests
policy should be reporting a missing copy as real loss, so routine
propagation would manufacture exactly the non-compliance signal that needs to
stay meaningful, once per copy per edit, across the fleet.

Two further costs are smaller. `initial-resource-version` is reset on each
recreate unless the controller reads the old copy and carries the value
forward, which turns provenance into state that any delete-recreate cycle can
silently lose — a cost already anticipated when the option was raised.
Recreation would also move creation into `SharedManifestSync` unless the
replacement is driven from `ClusterInstanceReconciler` instead. Set against
blocking an edit that content comparison already repairs, the trade does not
pay. If hand edits need preventing outright, namespace RBAC is the direct
lever and does not disturb the propagation path.

**Manual recovery of a missing copy.** Until [Open Question
2](#open-questions) is settled there is a manual path, but it is narrower than
it looks, because the two intuitive nudges both fail silently:

- **Editing the source ConfigMap does not work**, for two independent reasons.
  A metadata-only edit — a meaningless annotation or label — bumps the
  source's `resourceVersion` and delivers an event, but propagation compares
  `data` and `binaryData`, finds them identical, and writes nothing. Even a
  real content edit would not help: propagation skips a name that has no
  object, by design, because creation belongs solely to
  `ClusterInstanceReconciler`.
- **Editing the ClusterInstance's metadata does not work either.**
  the API server bumps `metadata.generation` only on a spec write, so an
  annotation or label edit leaves it unchanged. (The status subresource
  `ClusterInstance` declares matters here for a related reason: it keeps the
  reconciler's own `Status.ObservedGeneration` write from bumping
  `generation`, without which the pre-empt would never settle.) An annotation edit is dropped outright by the
  controller's event filter, which admits only generation changes, label
  changes, the paused annotation, and the reinstall `Provisioned` transition.
  A label edit *is* admitted and does enter `Reconcile`, but it then hits the
  `ObservedGeneration` pre-empt and returns before validation, the copy step
  or rendering. The reconcile happens; the copy is not recreated.
- **Only an allow-listed spec edit recovers the copy**, because only a spec
  write advances `metadata.generation` past `Status.ObservedGeneration` and so
  clears the pre-empt. Post-provisioning the webhook restricts spec writes to
  `ClusterLevelAllowedPaths` and `NodeLevelAllowedPaths` in
  `api/v1alpha1/clusterinstance_validation.go`, plus `/extraManifestsRefs`
  once this enhancement adds it. The least invasive is `spec.extraAnnotations`:
  it is keyed by manifest kind, nothing validates the keys, and an entry under
  a kind that no template renders is never matched by `ExtraAnnotationSearch`,
  so it bumps the generation without changing a single rendered manifest.
  `/holdInstallation` is not usable as a lever, since only `true` → `false` is
  accepted.

This recovery is not free: the reconcile it triggers re-renders and re-applies
**every** manifest with Server-Side Apply and `client.ForceOwnership`, so any
drift in a siteconfig-owned field is reverted. It is a blunt escape hatch
rather than a targeted repair, which is the main argument for settling Open
Question 2 instead of relying on it.

**Source metadata is never propagated either.** Propagation writes `data`,
`binaryData` and `source-resource-version` and nothing else. A label or
annotation added to the source, whether alongside a content change or on its
own, is not carried onto the copy, and the labels and annotations already on
the copy are left untouched. This is the same rule that governs the initial
copy, applied on the update path — see
[Source metadata is not copied](#source-metadata-is-not-copied) for why GitOps
ownership marks, reinstall preservation labels and the ACM backup label must
all stay off the copies.

**Deletion of a source ConfigMap** does not delete the copies. Copies remain
so that already-provisioned clusters keep a valid policy source.

The dangling reference is **not** surfaced automatically on an
already-provisioned ClusterInstance. The extra-manifest existence check lives
in `ci.Validate`, which runs after the `ObservedGeneration` pre-empt, so for a
cluster whose generation has not changed there is no reconcile in which that
check can run. The reference is reported only when something next bumps the
generation — a repoint or a reinstall. For a ClusterInstance that has not yet
been provisioned, today's behaviour applies unchanged and validation fails as
it does for any missing extra manifest.

So that deletion is not silent, `SharedManifestSync` emits a warning Event
against each ClusterInstance that the field index still maps to a shared
ConfigMap when it observes that ConfigMap being deleted. This does not
reconcile the ClusterInstance and does not set a condition; it exists so the
deletion is visible to an operator rather than discoverable only by inspecting
annotations.

**Propagation during provisioning.** Propagation is not gated on provisioning
state. A shared ConfigMap edit that lands while a referencing cluster is
mid-install changes the content the installer is about to read, so that
cluster may install with a mix of old and new manifests depending on timing.
The same ungated path has a deterministic variant: a ClusterInstance created
with `spec.holdInstallation: true` has its copies created on the first
reconcile, so an edit to the source made before the hold is released is
propagated into the copy and the cluster installs from the edited content —
no race required.

This is **accepted, documented behaviour**, not a defect. It is recorded in
the risks table, and the user-facing documentation shipped with the feature
must state it, so that an administrator who sees a cluster installed from
content newer than `initial-resource-version` records does not report it as a
propagation bug. Edits are expected to be rare (on the order of a few times
per release), and the policy-driven day-2 reconcile converges the cluster
afterwards regardless. Administrators staging a change across a fleet should
prefer creating a new ConfigMap and repointing batches, which has neither the
race nor the held-install variant.

**Cost.** The manager's cache is cluster-wide and unrestricted, and
`ConfigurationMonitor` already establishes a cluster-wide ConfigMap informer,
so this controller adds **no new informer**. The steady-state cost is zero: no
ClusterInstance reconciles, no renders, no API writes. The transient cost of a
single shared ConfigMap edit is one indexed list plus one ConfigMap write per
referencing cluster namespace — for a 3,500-cluster fleet, 3,500 writes as a
one-time burst, bounded by the controller's own `MaxConcurrentReconciles` and
on a separate budget from the `ClusterInstanceReconciler`'s. That value must be
chosen deliberately rather than left to the default: the codebase default is 1
(`DefaultMaxConcurrentReconciles`) and `ConfigurationMonitor` hardcodes 1, so
an unset value would serialise the entire burst. It should be read from the
operator ConfigMap the same way the `ClusterInstanceReconciler` reads its own,
with a default above 1.

#### Orphaned copies

When a ClusterInstance stops referencing a shared ConfigMap — the staged-rollout
case, where `v1` is replaced by `v2` — the `v1` copy is left in the cluster
namespace.

How harmful that is depends on something this design does not control. If the
extra-manifests policy correlates from the ClusterInstance's
`extraManifestsRefs` to the ConfigMap those references name — which is what it
should do, and is recorded as [Open Question 3](#open-questions) — then an
orphan is never read by the policy, because the current reference set does not
name it. If the policy instead selects ConfigMaps in the cluster namespace
more broadly, the orphan becomes a live second source of truth for that
cluster.

It is worth reclaiming either way, for reasons that do not depend on that
answer: the copies accumulate without bound, an unowned copy is exactly the
"second source of truth with no owner" that this design otherwise rejects, and
the name-masquerade case below makes a stale copy actively misleading to an
administrator reading the namespace.

The copies are not reclaimed by any existing mechanism: the `DeletionHandler`
only ever deletes objects listed in `Status.ManifestsRendered` that also carry
the `owned-by` label, and the one existing namespace sweep —
`preservation.Cleanup` — selects only objects carrying the preservation
labels. Reclaiming them requires the `ClusterInstanceReconciler`, during the
same reconcile that processes the new reference set, to list ConfigMaps in the
ClusterInstance namespace carrying the managed-copy label and delete any that
the current `extraManifestsRefs` no longer names.

Reclamation must key on owner identity and source, not on name alone. Two
cases break a name-only sweep: nothing forbids two ClusterInstances in one
namespace, and each would delete the other's copies; and repointing a
reference from the shared namespace to a local ConfigMap of the same name
leaves the managed copy in place, still matching the name, where it would
masquerade as the user's own ConfigMap. The sweep therefore matches on the
managed-copy label, an ownerReference UID belonging to the ClusterInstance
being reconciled, and the `source-namespace` annotation. This is a deliberate
addition rather than a side effect, and is listed in the test plan.

#### Relationship to policy-driven day-2 reconciliation

This design draws the boundary at the cluster-namespace copy.

The SiteConfig operator owns the 1:N mapping expressed by
`extraManifestsRefs`, so it owns keeping the copies faithful to the source.
That is the whole of its responsibility here. It does not compare spoke cluster
state to the ConfigMap, and it does not drive manifests onto a running cluster.

Nothing below it does either. Neither assisted-service nor the IBI operator
re-reads a changed extra-manifests ConfigMap on an already-installed cluster,
so neither propagates the change to the spoke (confirmed during review; this
is behaviour of components outside this repository and cannot be verified
from it). The policy/TALM path is therefore not one option among several for
day-2 convergence — it is the only one, and the cluster-namespace copy has no
other consumer after installation.

That remaining step is already solved by an ACM policy that compares the
cluster's configuration against the extra-manifests ConfigMap in the cluster
namespace (see the `extra-manifests-policy` reference CR in
`openshift-kni/telco-reference`). Because propagation keeps that ConfigMap
current, editing the shared ConfigMap makes the policy report non-compliance,
which is what sets up a TALM-driven rollout. By design that policy runs in
**inform** mode: it reports the drift, it does not remediate it. Enforcement
happens only under a progressive rollout driven by TALM, so an administrator
decides when each batch of clusters takes the change. How the policy is
constructed is outside the scope of this enhancement and is described here
only for reference. The result is a single source of truth spanning both
installation and day-N state.

What that policy must do to make this work — report a missing copy as
non-compliant, and correlate from the ClusterInstance's references rather
than reading the namespace broadly — is recorded as
[Open Question 3](#open-questions), because it is outside this repository.

The alternative — leaving the copies stale and having the policy resolve the
shared ConfigMap itself — was rejected; see
[Alternative F](#alternative-f-defer-change-detection-to-talm--policies).

### Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| External Go clients break on type change | `name` JSON field preserved (including `omitempty`); `namespace` is additive. Strongly-typed Go clients importing the field type must update. YAML/kubectl users unaffected. |
| Namespace deleted while operator is running | `NamespaceMonitor` controller re-creates it. ConfigMaps inside are lost and must be re-created by the user (standard Kubernetes behavior). |
| Namespace not deleted on operator uninstall | Documented as manual step. Matches existing behavior where ClusterInstance CRs are not auto-deleted on uninstall either. |
| Multi-replica race on namespace creation | Multiple replicas calling `Create()` concurrently is safe — the first succeeds, others get `AlreadyExists` and proceed. |
| Name collision: shared ConfigMap name matches existing local ConfigMap | Detected read-only during validation — the object lacks the managed-copy label, or its ownerReference UID does not match — so it fails on `ClusterInstanceValidated` instead of overwriting. Propagation repeats the same check before every write and skips on mismatch. |
| Updated copy does not match the running spoke cluster | After propagation the cluster-namespace ConfigMap holds the new content while the installed cluster still runs the old manifests until the ACM policy and TALM reconcile it. Documented so it is not mistaken for a siteconfig bug. The provenance annotations record the copy's lineage, not what the spoke installed. |
| Copies not garbage-collected when deletion is paused | ownerReference GC only runs once the ClusterInstance object is removed, which the finalizer gates. On deletion timeout the reconciler applies the pause annotation and the object persists, so its copies persist with it until an administrator intervenes. |
| Shared ConfigMaps and copies are absent from ACM hub backup | `applyACMBackupLabelToInstallTemplates` labels only `TemplateRefs` ConfigMaps. After a restore the copies are missing and the `ObservedGeneration` pre-empt stops the reconciler recreating them. Labelling the shared ConfigMaps restores the inputs but not the copies, so it is necessary and not sufficient; see Open Question 2. |
| Extra-manifests policy does not treat a missing copy as non-compliance | A deleted copy is not recreated for an already-provisioned cluster, so the policy is the only thing that would surface it. If the policy skips a missing ConfigMap rather than reporting non-compliance, the failure is invisible end to end. External to this repository; see Open Question 3. |
| Downstream CRD incompatibility | Controller copies ConfigMaps locally and strips `namespace` before template rendering, so downstream CRDs see only `name` — identical to current behavior. |
| ownerReference pattern inconsistency | Scoped to this one use case (copied ConfigMaps). Documented in code. Label-based ownership remains the pattern for template-rendered objects. |
| Shared ConfigMap updated after provisioning — copy is stale | `SharedManifestSync` propagates the update to every referencing cluster namespace. `source-resource-version` records the version last propagated; `initial-resource-version` preserves the version the copy was first created from. |
| Propagation burst at fleet scale (3,500 writes from one edit) | Separate controller with its own `MaxConcurrentReconciles`, so it cannot starve `ClusterInstanceReconciler`. No new informer is added (a cluster-wide ConfigMap informer already exists). Steady-state cost is zero; bursts are expected a few times per release. |
| Shared ConfigMap edited while a referencing cluster is mid-install, or before a held installation is released | Accepted and documented behaviour. The cluster may install with mixed content, or with content newer than `initial-resource-version` records. The day-2 policy reconcile converges it afterwards. Staged rollout via a new ConfigMap name avoids both. |
| Downstream CRD rejects an in-place manifest-reference update on a repoint | **Unresolved** — see [Open Questions](#open-questions). Nothing is applied at all, because the dry-run gate precedes every apply, and retries back off towards ~16 minutes. Must be settled by live-cluster test before `implementable`; reinstall is the safe fallback. |
| Repoint re-applies all manifests with `ForceOwnership`, reverting drift | Pre-existing behaviour for all ten currently-allowed day-N paths, not introduced here. Fields siteconfig does not render are not owned and so are not affected; `BareMetalHost.spec.online` and `automatedCleaningMode` are re-asserted. |
| Copy inherits a GitOps ownership label or annotation and is pruned by the GitOps engine | No source label or annotation is copied. A copy is built from the source's `name`, `data` and `binaryData` only; every label and annotation it carries is written by the controller. See [Source metadata is not copied](#source-metadata-is-not-copied). |
| Copy inherits a preservation label and collides with the reinstall preservation flow | Same rule — no source metadata is copied, so `siteconfig.open-cluster-management.io/preserve` never reaches a copy and the copy cannot be swept into the reinstall backup/restore/cleanup path. |
| Orphaned `v1` copies accumulate after a staged rollout | Reconciler deletes managed copies in the ClusterInstance namespace that the current `extraManifestsRefs` no longer names. |
| Two controllers writing the same ConfigMap | Creation and ownership belong solely to `ClusterInstanceReconciler`. `SharedManifestSync` only updates objects that exist and pass the ownership check, and only `data`/`binaryData`/`source-resource-version`. It uses `Get`-then-`Update`, not `CreateOrUpdate`, and avoids SSA, so it cannot replace the `siteconfig-controller` field set. |

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
- Adds a second new controller (`SharedManifestSync`) and the repository's
  first field index. Neither is complex, but both are new machinery to
  maintain.
- Making `/extraManifestsRefs` day-N mutable is all-or-nothing: the existing
  allow-list mechanism cannot express a narrower rule, so administrators can
  also remove or reorder references post-install, not only repoint them.
- A shared ConfigMap edit now reaches every referencing cluster namespace on
  the hub by construction, and it does so immediately. That is the point of
  the feature, but it means a mistaken edit lands in every cluster-namespace
  copy at once, with no staging step on the hub side. It does **not** follow
  that the mistake reaches the spoke clusters: how the new content rolls out
  to the fleet is determined by the construction of the ACM policy that
  watches the copy, which is outside the scope of this enhancement and
  described here only for reference. By design that policy runs in inform
  mode, so a mistaken edit surfaces fleet-wide as reported non-compliance
  rather than being applied fleet-wide; enforcement happens only under a
  progressive rollout driven by TALM. (Policy and TALM behaviour is external
  to this repository and cannot be verified from it.) See
  [Relationship to policy-driven day-2 reconciliation](#relationship-to-policy-driven-day-2-reconciliation).

## Design Details

### Open Questions

1. **Do the downstream CRDs accept an in-place manifest-reference update after
   installation?** `AgentClusterInstall.spec.manifestsConfigMapRefs` carries no
   CEL immutability rule at the schema level, but the assisted-service
   validating webhook is not vendored in this repository and cannot be
   inspected here, and assisted-service is known to enforce
   `AgentClusterInstall` immutability elsewhere. `ImageClusterInstall` is not
   vendored in any form, so nothing can be said about it from this repository.
   This must be answered by a live-cluster test before the enhancement moves to
   `implementable`, because a rejection blocks *all* manifest application for
   the affected ClusterInstance, not just the changed object. If either CRD
   rejects the update, the staged-rollout workflow must fall back to
   reinstall-based repointing, which recreates the downstream CR instead of
   updating it.
2. **How should a missing or drifted copy be recovered?** Today a deleted
   copy is never recreated for an already-provisioned cluster, and a
   hand-edited copy is repaired only when the source next changes (see
   [Change detection and convergence](#change-detection-and-convergence)).

   The proposed answer is **a startup pass plus a watch on the copies, and no
   periodic resync**.

   A watch on objects carrying the managed-copy label covers deletion and hand
   edits while the operator is running, and it is close to free: it adds an
   event handler to the cluster-wide ConfigMap informer that already exists,
   with no new informer, no new API-server watch and no RBAC change. The cost
   is worked through under
   [Change detection and convergence](#change-detection-and-convergence). It
   also covers events missed during a watch gap, because on relist client-go's
   delta queue emits a delete for objects that are no longer present, so a
   deletion that happened while the connection was broken is still delivered.

   What the watch cannot cover is a copy that is already absent when the
   operator starts: the object is not in the informer's initial list, so there
   is no delta and no event. That is the ACM hub restore case, where a
   restored ClusterInstance comes back with `ObservedGeneration ==
   Generation`, nothing recreates its copies, and the day-2 policy has nothing
   to read. That depends on the hub backup restoring the CR's `status`, which
   is cluster-backup-operator behaviour and cannot be confirmed from this
   repository; if a restore cleared `status`, the ordinary reconcile would
   recreate the copies and only the drift case would remain. A startup pass closes it — after cache sync, list the
   ClusterInstances carrying shared references through the field index and
   verify each copy exists. That is one cached list plus one cached read per
   reference, once per leader term, with writes only for the copies that are
   genuinely missing. It is a boot-time check, not a sweep. It must be cheap
   and idempotent, because `LeaderElectionReleaseOnCancel` is enabled and a
   leader handover re-runs it.

   A **purpose-built periodic sweep is not proposed**, which is a narrower
   claim than "no periodic behaviour at all". One already exists for free: the
   manager's informer resync defaults to ten hours and redelivers every cached
   object as an update, which content comparison turns into a drift check for
   every copy that exists. That is the backstop referred to elsewhere in this
   document, and it is why drift converges on a ten-hour bound even today. A
   watch replaces that bound with prompt repair. An *additional* sweep over
   ClusterInstances would re-check only what the watch already observes; it
   reads from the cache so it costs no API traffic, but at fleet scale it is a
   recurring walk over every ClusterInstance and every copy, and a recurring
   load that scale testing would then have to characterise — for a failure
   mode that only arises while the operator is not running, which is precisely
   what the startup pass covers.

   What still has to be settled before `implementable` is not whether to
   recover but where the write lives. Recreation must not move into
   `SharedManifestSync`, so it needs a narrow "ensure managed copies" step in
   `ClusterInstanceReconciler` ahead of the `ObservedGeneration` pre-empt, and
   the watch itself must not be added with `Watches()` on a controller that
   applies a controller-wide `WithEventFilter` — though `WatchesRawSource` is
   documented as bypassing that filter and would work. Making the copies
   immutable is not an
   alternative; see
   [Change detection and convergence](#change-detection-and-convergence).
3. **How does the extra-manifests ACM policy select and evaluate the
   cluster-namespace copy?** Two sub-questions, neither answerable from this
   repository — no policy is defined or vendored here, and the only reference
   is the `extra-manifests-policy` CR in `openshift-kni/telco-reference`.
   First, does the policy report a **missing** copy as non-compliant? The
   design relies on it: a deleted copy is not recreated for an
   already-provisioned cluster, so until Open Question 2 is implemented the
   policy is the only thing that would surface the loss, and a policy that
   skips a missing ConfigMap would make it silent. Second, does the policy
   **correlate from the ClusterInstance's `extraManifestsRefs` to the
   ConfigMap those references name**, rather than selecting ConfigMaps in the
   cluster namespace more broadly? The answer decides whether an orphaned copy
   is inert clutter or a live second source of truth; see
   [Orphaned copies](#orphaned-copies). If either answer is no, a story to
   change the policy belongs on this epic.

### Test Plan

**Unit tests:**

- `cmd/main_test.go`:
  - Namespace does not exist — created successfully.
  - Namespace already exists — no error, startup proceeds.
  - Namespace is created carrying the
    `pod-security.kubernetes.io/enforce: restricted` label.
  - Client Create error — error propagated.
- `internal/controller/namespace_monitor_test.go`:
  - Namespace deleted triggers re-creation.
  - Namespace exists is a no-op.
  - Namespace exists but is `Terminating` — `Create` returns `AlreadyExists`,
    which must be retried rather than swallowed as success.
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
  - Provenance annotations record source namespace, `source-resource-version`,
    and `initial-resource-version`.
  - Existing managed copy updated with new data and updated
    `source-resource-version`, while `initial-resource-version` is left
    unchanged.
  - No source label or annotation reaches the copy. Given a source carrying a
    preservation label, an arbitrary GitOps ownership label and annotation,
    and the ACM backup label, the copy's labels are exactly the managed-copy
    label and its annotations are exactly the three provenance annotations —
    asserted as set equality rather than as the absence of named keys, so the
    test fails on any inherited metadata.
  - Name collision with user-created local ConfigMap — rejected with error.
  - Shared ConfigMap does not exist — error.
  - Namespace field cleared on in-memory ExtraManifestsRefs before rendering.
  - Managed copies no longer named by `extraManifestsRefs` are deleted
    (orphan reclamation after a repoint).
  - Copy deleted while `Status.ObservedGeneration == Generation` — a
    label-only update on the ClusterInstance enters `Reconcile` but is
    pre-empted, and the copy is **not** recreated. This asserts the behaviour
    as currently specified; it is superseded by the ensure-copies case below
    if [Open Question 2](#open-questions) is settled as proposed.
  - Copy deleted, then an allow-listed spec path is edited
    (`spec.extraAnnotations` under a kind no template renders) — the
    generation advances, the reconcile runs in full, and the copy is
    recreated with a fresh `initial-resource-version`.
  - **Conditional on [Open Question 2](#open-questions) being settled as
    proposed**, and superseding the previous case once it is: copy absent on
    an already-provisioned ClusterInstance — the ensure-copies step runs ahead
    of the pre-empt and recreates it, without rendering templates and without
    advancing `ObservedGeneration`.
  - Copy present and current on an already-provisioned ClusterInstance — the
    ensure-copies step performs no write.
  - ClusterInstance with no shared references — the ensure-copies step
    returns without listing or reading anything.
- `internal/controller/sharedmanifestsync_controller_test.go` (new; mirrors the
  fake-client Ginkgo pattern in `configuration_monitor_test.go`, since the
  repository uses no envtest):
  - Source ConfigMap updated — every referencing copy receives the new data and
    a refreshed `source-resource-version`.
  - `initial-resource-version` is not modified by propagation.
  - `holdInstallation: true` on create, source ConfigMap edited, then
    `holdInstallation: false` — the release is accepted and the copy carries
    the propagated content while `initial-resource-version` still names the
    pre-edit version.
  - ClusterInstances that do not reference the ConfigMap are untouched.
  - Copy absent — propagation skips it and does not create one.
  - Source edited in metadata only while the copy is absent — no write and no
    create; an absent copy is not recoverable from the source side.
  - Copy present but not a managed copy (no label, or an ownerReference UID
    that does not match) — skipped, never overwritten, warning Event emitted.
  - Copy hand-edited — content comparison detects the drift and the next
    propagation repairs it, even though the copy's `source-resource-version`
    annotation still matches the source.
  - Source changed in metadata only (a label added, content identical) — no
    write to any namespace.
  - ConfigMap outside the shared namespace — ignored.
  - Source deleted — copies are left in place and a warning Event is emitted
    against each referencing ClusterInstance.
  - Propagation does not alter the copy's ownerReference or managed-copy label.
  - A label and an annotation added to the source alongside a content change
    are not propagated onto the copy, and the copy's own labels and
    annotations are left byte-identical across the write.
  - Propagation does not modify the ClusterInstance or its
    `Status.ObservedGeneration`.
  - Field index returns the correct ClusterInstance set, including the case
    where one ClusterInstance references several shared ConfigMaps and the
    case where a reference omits `namespace` (must not match).
  - Partial failure — one namespace write fails; the remaining namespaces are
    still updated and an aggregate error is returned.
- `api/v1alpha1/clusterinstance_validation_test.go` (day-N mutability):
  - `/extraManifestsRefs` present in `SpecChangeBaseOnly.GetAllowedPaths()` and
    in `SpecChangeReinstall.GetAllowedPaths()` (extending the existing
    `ContainElement` assertions).
  - Post-provisioning repoint `v1`→`v2` accepted, mirroring the existing
    `clusterImageSetNameRef` case.
  - Post-provisioning append, removal, and reorder all accepted.
  - All of the above still rejected while provisioning is in progress and
    while a reinstall is in progress.
  - Still rejected when the `ClusterProvisioned` condition is absent or its
    reason is `Failed`, unless `holdInstallation` is true.
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
- Editing a shared ConfigMap propagates to all referencing cluster namespaces,
  and does **not** cause the referencing ClusterInstances to re-render
  (assert `Status.ObservedGeneration` and the downstream CRD's
  `resourceVersion` are unchanged).
- **Blocking test for [Open Question 1](#open-questions):** on a provisioned
  cluster, repoint `extraManifestsRefs` from `v1` to `v2` and confirm that
  assisted-service accepts the `AgentClusterInstall.spec.manifestsConfigMapRefs`
  update, and that the IBI operator accepts the equivalent
  `ImageClusterInstall.spec.extraManifestsRefs` update. Assert that the dry-run
  gate passes and the ClusterInstance reaches `ObservedGeneration ==
  Generation` rather than failing the dry-run gate and backing off
  indefinitely.
- Staged rollout across a batch: repoint a subset of ClusterInstances to `v2`,
  confirm the unrepointed ones still reference `v1`, and confirm orphaned `v1`
  copies are reclaimed in the repointed namespaces only.
- **Hub restore ([Open Question 2](#open-questions)):** restore a hub whose
  ClusterInstances reference shared ConfigMaps but whose cluster-namespace
  copies are absent, and confirm the copies are recreated **without** any
  ClusterInstance generation change — that is, with
  `Status.ObservedGeneration == Generation` throughout. Generation equality
  is not sufficient on its own: it shows only that the spec did not change,
  not that template rendering and Server-Side Apply were skipped. Also assert
  that every object listed in `Status.ManifestsRendered` keeps its
  `resourceVersion`, and that only the absent copies are created while copies
  that still exist keep theirs. A copy watch alone cannot satisfy this test,
  since an absent object generates no event.
- **Drift with no source event ([Open Question 2](#open-questions)):**
  hand-edit a cluster-namespace copy, leave the shared ConfigMap untouched,
  and confirm the chosen recovery path repairs it. The propagation tests above
  only cover repair triggered by a later source change, which is a different
  path and does not exercise this one.

**Scale testing:**

- With a large number of ClusterInstances referencing one shared ConfigMap,
  edit the source once and measure propagation wall-clock time, API server
  write throughput, and the effect on `ClusterInstanceReconciler` latency,
  confirming the two controllers' concurrency budgets are in fact independent.

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] **[Open Question 1](#open-questions) resolved by live-cluster test**:
  confirmed whether `AgentClusterInstall` and `ImageClusterInstall` accept an
  in-place manifest-reference update post-installation. Required before this
  proposal moves from `provisional` to `implementable`, since a rejection
  changes the staged-rollout design. A negative answer does not block
  propagation, which is independently shippable; it blocks only the repoint
  capability built on top of it.
- [ ] Implementation merged with adequate test coverage
- [ ] Propagation validated at fleet scale
- [ ] **Must-gather coverage aligned**: confirm what the ACM/MCE must-gather
  collects for the SiteConfig operator today and, if ClusterInstance CRs are
  collected, add the shared namespace to the same collection so a bundle is
  self-contained. The change lands in the must-gather repository, so it is
  tracked as a separate story on the epic.
- [ ] **[Open Question 3](#open-questions) confirmed or tracked**: the
  extra-manifests policy reports a missing copy as non-compliant and
  correlates from the ClusterInstance to the ConfigMap its references name —
  or a story exists on this epic to make it do both
- [ ] **[Open Question 2](#open-questions) resolved**: a recovery path chosen
  that covers both failure modes it names — a hub-restore test confirming
  absent copies return without a ClusterInstance generation change, and a
  test confirming a hand-edited copy is repaired with no source-ConfigMap
  event
- [ ] Documented in user-facing operator documentation covering shared
  namespace setup, `ExtraManifestsRefs` namespace field usage, the staged
  rollout workflow, the interaction with the extra-manifests ACM policy, and
  the accepted mid-install and held-install behaviour
- [ ] Released

### Upgrade / Downgrade Strategy

**Upgrade:** The new `ExtraManifestReference` type is a superset of
`corev1.LocalObjectReference`. Existing CRs are deserialized correctly because
the `name` field is unchanged (including `omitempty`) and `namespace` defaults
to empty (which means local namespace). The CRD schema adds the optional
`namespace` field and a `MinLength=1` validation on `name`. That last part is a
real tightening, not a cosmetic one: the current CRD inlines
`corev1.LocalObjectReference`, which defaults `name` to `""` and does not
require it, so an entry with an empty or omitted `name` is valid today and
would be rejected afterwards. Such an entry is already meaningless — it cannot
resolve to a ConfigMap — but any CR carrying one must be corrected before
upgrade. No other manual migration steps are required.

Adding `/extraManifestsRefs` to `ClusterLevelAllowedPaths` is a pure relaxation
of webhook validation: edits that were previously rejected are now accepted.
Apart from the empty-`name` case above, no existing ClusterInstance changes
behaviour on upgrade.

**Downgrade:** If downgrading to a version that does not support the `namespace`
field, any ClusterInstances using `namespace: siteconfig-shared-resources` will
fail validation. Administrators must remove the `namespace` field from
`extraManifestsRefs` and duplicate the ConfigMaps into local namespaces before
downgrading. Copied ConfigMaps with ownerReferences will be garbage-collected
when the ClusterInstance is deleted or can be manually removed. The
`siteconfig-shared-resources` namespace will remain but will no longer be
managed (harmless orphan).

After a downgrade, `/extraManifestsRefs` reverts to being immutable
post-install and propagation stops. Copies are left at whatever content they
last received; they are not reverted. Clusters already repointed to a `v2`
ConfigMap keep that reference, because the downgraded webhook rejects
*changes* to the field rather than re-validating existing values, and further
staged-rollout steps are blocked until the operator is upgraded again.

The downgrade is therefore non-breaking only for ClusterInstances that carry
no `namespace`. Any CR that does reference the shared namespace fails
validation against the older schema, which is why the migration above is a
prerequisite rather than an optional tidy-up: it must be completed *before*
the downgrade, not after.

### Version Skew Strategy

The SiteConfig operator is a standalone component. Version skew with the hub
cluster or managed clusters is not a concern for this change. The only skew
scenario is between the CRD version and the operator version:

- **CRD newer than operator:** The operator ignores the unknown `namespace`
  field (standard Kubernetes behavior for optional fields).
- **Operator newer than CRD:** The operator creates the namespace and registers
  the `NamespaceMonitor` and `SharedManifestSync` controllers, but the CRD
  schema does not include `namespace`. No ClusterInstance can reference a
  shared ConfigMap, so the field index stays empty and `SharedManifestSync`
  never has work to do. Users cannot use the feature until the CRD is updated.
  The operator otherwise functions normally.

## Implementation History

- 2026-04-30: Initial proposal created.
- 2026-10-29: Revised following review. Namespace renamed from
  `siteconfig-shared-resources` to `openshift-siteconfig-resources`; added
  propagation of shared ConfigMap updates to cluster-namespace copies via a
  dedicated `SharedManifestSync` controller; made `extraManifestsRefs` mutable
  after installation to support staged fleet rollout; split provenance into
  live and install-time annotations.
- 2026-11-04: Second review pass folded in. Namespace rename reverted to
  `siteconfig-shared-resources`, because the `openshift-` prefix hides the
  namespace behind the console's "Show default projects" toggle and left the
  managed-OpenShift question unresolved. `provisioned-resource-version`
  renamed to `initial-resource-version`, which is what it actually records.
  Confirmed that neither assisted-service nor the IBI operator re-reads a
  changed ConfigMap post-install; scoped Secrets out as a Non-Goal; recorded
  that no source label or annotation is copied onto a copy; corrected the
  Drawbacks claim about fleet blast radius; and settled the copy-recovery
  open question on a startup pass plus a copy watch, with no additional
  periodic sweep; where the write lives is still open. Must-gather coverage
  moved from an open question to a graduation criterion.
  Open Questions renumbered: the downstream-CRD mutability question stays 1,
  copy recovery becomes 2, and a new question records the extra-manifests
  policy dependency as 3.

## Alternatives

### Alternative A: Validator checks both namespaces without API change

Keep `ExtraManifestsRefs` as `[]corev1.LocalObjectReference` and have the
validator check both the local namespace and `siteconfig-shared-resources`
for each reference. In its strongest form the controller tells its own copies
from user-created ConfigMaps by the managed-copy label and fails on a genuine
collision, exactly as the proposed design does; a local-wins variant would
resolve to the local ConfigMap whenever one exists and fall back to the shared
namespace otherwise.

**Rejected because:** it makes the shared namespace a hub-wide fallback for
every ClusterInstance, including those that never opted in. A reference whose
ConfigMap is absent locally fails validation today; under this alternative it
would resolve against the shared namespace instead, so write access there would
reach clusters whose specs never mention it. The explicit field keeps opt-in
visible in the ClusterInstance and reviewable in Git.

Collision handling and forward-compatibility are **not** reasons to prefer the
explicit field — both designs behave the same on collisions, and neither avoids
the copy path. See
[Why an explicit field rather than a dual-namespace lookup](#why-an-explicit-field-rather-than-a-dual-namespace-lookup)
for the full comparison, including the costs of the field.

### Alternative B: Use the existing Reference type (like TemplateRef)

Change `ExtraManifestsRefs` to use the existing `Reference` type where
`Namespace` is a required field.

**Rejected because:** The `Namespace` field in `Reference` is required
(`+required`), which would break backward compatibility for existing CRs that
only specify `name`. A new type with an optional `Namespace` field avoids this.

### Alternative C: Protect namespace with a finalizer

Add a custom finalizer to the `siteconfig-shared-resources` namespace to
prevent accidental deletion.

**Rejected because:** a finalizer does not protect the thing worth protecting.
A custom entry in a Namespace's `metadata.finalizers` does not stop the
namespace controller from deleting the namespace's contents: on delete the
namespace moves to `Terminating`, its contents are removed, and only the final
removal of the now-empty Namespace object is held up. The shared ConfigMaps
are destroyed either way, so a finalizer buys an empty shell that cannot
accept new ConfigMaps rather than a preserved namespace.

The usual secondary objection — that an uninstalled operator leaves the
namespace stuck in `Terminating` — is weaker than it first appears and is not
the reason for rejecting this option. The operator already ships a finalizer
with exactly that property: `clusterInstanceFinalizer` is removed only from
inside `Reconcile`, there is no drain on shutdown, and on deletion timeout the
handler deliberately pauses and leaves the ClusterInstance finalized pending
manual intervention. What genuinely differs is blast radius: a stuck
ClusterInstance affects one cluster, while a stuck shared namespace blocks
provisioning for every cluster that references it.

Since neither option preserves the ConfigMaps, the real choice is between a
namespace that cannot be deleted while the operator runs and one that is
re-created empty after deletion. `NamespaceMonitor` takes the latter: deletion
completes normally, the namespace is re-created, and it can still be cleaned
up when the operator is not running. Either way administrators must keep the
ConfigMaps in Git. Two variants are deliberately out of scope: removing the
finalizer on graceful shutdown, which does not help on an abrupt one; and a
validating webhook on Namespace `DELETE`, which would prevent the deletion
rather than undo it and would reuse the webhook server the operator already
runs. The latter is the stronger form of this alternative if protection is
ever revisited.

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

If the downstream CRDs do gain a `namespace` field on their ConfigMap
reference types, the namespace could be passed through instead of cleared — but
the copy path would still have to stay, so the rollback is much smaller than it
first appears. The ACM policy that keeps day-N cluster state aligned reads the
ConfigMap in the **cluster namespace** (see
[Relationship to policy-driven day-2 reconciliation](#relationship-to-policy-driven-day-2-reconciliation)),
so dropping the copy would leave that policy with nothing per-cluster to
compare against and would reintroduce exactly the second-source-of-truth
problem that Alternative F rejects. `copySharedExtraManifests` and
`SharedManifestSync` would both remain; only the namespace-clearing step before
rendering would go away.

Consequently this proposal does **not** offer forward-compatibility with such a
field as a reason to add `namespace` to the API; see
[Why an explicit field rather than a dual-namespace lookup](#why-an-explicit-field-rather-than-a-dual-namespace-lookup).
Whether the downstream change is worth pursuing in assisted-service and IBIO
depends on demand outside SiteConfig users, which is a question for PM.

### Alternative F: Defer change detection to TALM / policies

Leave the cluster-namespace copies at the content they had when the cluster was
provisioned, and have the day-2 tooling resolve the shared ConfigMap itself —
either by giving TALM its own controller to detect source changes and
distribute them, or by having the ACM policy traverse from the copy back to the
source when evaluating compliance.

**Rejected because:** SiteConfig owns the 1:N mapping that `extraManifestsRefs`
expresses, so keeping the copies faithful to the source is its responsibility;
pushing that elsewhere splits one relationship across two owners. The first
two objections below were raised by @imiller0 during review and are reproduced
here because they are the decisive ones:

- A TALM-side controller would have to watch ClusterInstances and the shared
  ConfigMaps — watches the SiteConfig operator already holds — and would
  duplicate the copy logic in a second codebase, with the two implementations
  free to drift.
- Policy-side traversal moves the cost from rare to continuous: the mapping
  would be unwound on every compliance evaluation, typically every 10 minutes,
  for every cluster. At 3,500 clusters that is a permanent load, against a
  propagation burst that occurs a few times per release. (The evaluation
  interval is quoted from that review discussion and is not established in
  this repository.)
- A third variant avoids both costs: have the ACM policy reference the shared
  ConfigMap directly through hub-side templating, needing neither a second
  controller nor per-cluster traversal. It is rejected for a different reason.
  It leaves the cluster-namespace copy permanently stale, so each cluster
  namespace still holds content that does not match what the policy evaluates
  and the reinstall path would reuse that stale copy; and it moves the place
  where the 1:N mapping is expressed from the ClusterInstance into the policy.

Leaving the copies stale also defeats the purpose of the feature. The goal is a
single source of truth spanning installation and day-N state; a copy that no
longer matches its source is a second source of truth with no owner.

The earlier revision of this proposal argued the opposite — that extra
manifests are consumed once at provisioning, so propagation would be overhead
with no consumer. That reasoning was wrong in its premise: the consumer is not
the installer but the ACM policy that treats the cluster-namespace ConfigMap as
the desired day-N state.

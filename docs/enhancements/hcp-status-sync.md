---
title: hcp-status-sync
authors:
  - "@cwilkers"
reviewers:
  - "@sakhoury"
approvers:
  - "@sakhoury"
api-approvers:
  - "None"
creation-date: 2026-04-30
last-updated: 2026-07-21
status: provisional
tracking-link:
  - TBD
see-also:
  - "/docs/enhancements/related-proposal.md"
replaces:
  - "/docs/enhancements/old-proposal.md"
superseded-by:
  - "/docs/enhancements/new-proposal.md"
---

# Hosted Control Plane cluster status sync

## Release Signoff Checklist

- [ ] Enhancement is `implementable`
- [ ] Design details are appropriately documented from clear requirements
- [ ] Test plan is defined
- [ ] Graduation criteria are defined
- [ ] User-facing documentation is updated

## Summary

SiteConfig Operator should update the `Provisioned` status of `ClusterInstances`
that deploy HCP clusters by watching the associated `ManagedCluster` resource.
This is the same signal that will eventually be used for all cluster types (HCP,
Assisted Installer, and Image Based Installer), unifying how ZTP workflows detect
deployment completion.

## Motivation

The `Provisioned` `ClusterInstance.status.conditions[]` field is used in ZTP
workflows to signal completion of the deployment and clears automation to
proceed to the next phase of a multi-part deployment.

Today, AI and IBI clusters derive this status from `ClusterDeployment`
conditions via the `ClusterDeploymentReconciler`. HCP clusters do not report
`Provisioned` status in the same way, and this enhancement proposal seeks to
find a unified method to provide those updates with the same symantic meaning
across all cluster types.

Every installation flow already renders a `ManagedCluster` manifest. Using
`ManagedCluster.status` as the single source of truth for deployment completion
across cluster types reduces controller complexity and aligns with ACM's view of
cluster readiness.

### User Stories

- As a ZTP workflow author, I want HCP clusters' status reflected in their owner
  `ClusterInstances` so that my workflows can monitor the successful deployment of
  the clusters.

- As a ZTP workflow author, I want an HCP cluster's status to remain `Provisioned`
  when it reaches `Provisioned` so that there are no side effects in automation if
  that status changes.

- As a ZTP workflow author, I want deployment completion to be determined the
  same way regardless of cluster type, so that my automation does not need
  cluster-type-specific logic.

Other stories that may need to be filled out:

- **Remediation**: How do admins recover when the feature fails? Does it use the
  pause annotation pattern? What requeue delays apply? Is there troubleshooting
  guidance?
- **Scale**: Does it affect `maxConcurrentReconciles`? How does it behave with
  many ClusterInstances?

### Goals

* A `ClusterInstance` should report deployment completion via the `Provisioned`
  condition based on the associated `ManagedCluster` status, regardless of the
  underlying cluster type.
* Respecting the previous goal, the `ClusterInstance` should pass through useful
  information about a hosted cluster's deployment progress while provisioning is
  in progress.
* The implementation should be delivered in two stages: HCP first, then AI and
  IBI migration.

### Non-Goals

* `ClusterInstance` does not need to be involved in day-two life-cycle events of a
  hosted cluster, and should not revert the `Provisioned` status once achieved.
* This enhancement does not require changes to the `ManagedCluster` API itself,
  though upstream OCM behavior may need verification (see Open Questions).

## Proposal

### Workflow Description

Each installation flow (HCP, AI, IBI) renders a `ManagedCluster` resource as
part of its manifest templates. When the spoke cluster joins the hub and becomes
available, OCM sets `ManagedCluster.status.conditions[]`, including
`ManagedClusterConditionAvailable`.

SiteConfig Operator watches the `ManagedCluster` associated with each
`ClusterInstance` and maps its conditions to the `ClusterInstance`'s
`Provisioned` condition:

1. **Provisioning not started**: `Provisioned` is initialized to `Unknown` when
   the `ClusterInstance` is created and no `ManagedCluster` exists yet.
2. **Provisioning in progress**: `ManagedCluster` exists but
   `ManagedClusterConditionAvailable` is not `True` (or prerequisite conditions
   such as `ManagedClusterJoined` are not yet satisfied). `Provisioned` is set
   to `False` with reason `InProgress`.
3. **Provisioning failed**: Relevant `ManagedCluster` conditions indicate a
   terminal failure. `Provisioned` is set to `False` with reason `Failed`.
4. **Provisioning complete**: `ManagedClusterConditionAvailable` is `True`.
   `Provisioned` is set to `True` with reason `Completed`. Once set, this
   status is not reverted (see Non-Goals).

**Monitoring**: This feature adds or extends status condition updates on
`ClusterInstance` (`Provisioned`). Structured controller logs should record
`ManagedCluster` name, condition transitions, and any lookup errors.

**Error handling**: If the `ManagedCluster` is temporarily unavailable, the
reconciler requeues with backoff. If the `ManagedCluster` is deleted after
`Provisioned` was reached, the status is not reverted.

### Staged Rollout

#### Stage 1: HCP clusters only

Add a new `ManagedClusterReconciler` (or equivalent) that:

- Watches `ManagedCluster` resources labeled with
  `siteconfig.open-cluster-management.io/owned-by` (reverse lookup to
  `ClusterInstance`, matching `ClusterDeploymentReconciler` today).
- Also watches `ClusterInstance` resources so status sync can proceed via
  forward lookup (`manifestsRendered` / `spec.clusterName`) even when the
  `owned-by` label is not yet present (see **ManagedCluster create race**
  below).
- Resolves the `ManagedCluster` for a `ClusterInstance` using the association
  contract below (shared with the reinstall helpers). Forward lookup is
  authoritative for status mapping; reverse label lookup is an optimization
  for event-driven updates.
- Maps `ManagedCluster.status.conditions[]` to `ClusterInstance.status.conditions[]`
  (`Provisioned`).
- Uses event predicates that enqueue on `ManagedCluster.status.conditions`
  changes (see **Event predicates** below); must not apply
  `GenerationChangedPredicate` or other spec-only filters to the primary
  `ManagedCluster` watch.
- Does not modify the existing `ClusterDeploymentReconciler` behavior for AI
  and IBI clusters.

This stage unblocks HCP ZTP workflows without requiring changes to the
existing AI/IBI status path.

#### Stage 2: AI and IBI clusters

Migrate AI and IBI `ClusterInstances` to the same `ManagedCluster`-based status
path:

- Extend the `ManagedClusterReconciler` to handle all cluster types, or
  consolidate status logic into a shared helper used by all flows.
- Refactor or replace the `ClusterDeploymentReconciler`'s
  `Provisioned`-condition logic, which currently reads
  `ClusterDeployment.status.conditions[]` and `Spec.Installed`.
- Preserve existing `ClusterInstance.status.deploymentConditions` behavior for
  AI/IBI if still needed for detailed install progress, or evaluate whether
  `ManagedCluster` conditions alone are sufficient.

Stage 2 should be a separate change set from Stage 1 to limit review scope and
allow HCP workflows to benefit sooner.

### API Extensions

None.

### Siteconfig Impact

Summarize which siteconfig components are affected:

- **Controllers**:
  - **Stage 1**: New or extended reconciler watching `ManagedCluster` for HCP
    `ClusterInstances`, plus a mandatory secondary `ClusterInstance` watch for
    forward-lookup status sync and metadata heal when Hypershift/ACM creates
    the `ManagedCluster` first.
  - **Stage 2**: `ClusterDeploymentReconciler` retired and AI/IBI clusters added to ManagedCluster reconciler from stage 1.
  - `ClusterInstanceReconciler` continues to SSA-apply manifests (including
    `ManagedCluster` label/annotation heal). `ConfigurationMonitor` is
    unaffected.

- **Templates**: No template changes are required. HCP, AI, and IBI templates
  already render `ManagedCluster` resources. Apply semantics must remain
  ensure/SSA (not create-only) so competing creators cannot permanently drop
  SiteConfig metadata.

- **API fields**: No type changes are expected.

- **Validation**: No webhook or spec validation rules are affected.

### Implementation Details/Notes/Constraints [optional]

**ManagedCluster as the unified signal**

All three installation flows already include a `ManagedCluster` in their
rendered manifests. OCM manages this resource on the hub and updates its
status as the spoke cluster joins and becomes available. SiteConfig Operator
should treat `ManagedCluster.status.conditions[]` as the canonical source for
the `Provisioned` condition across all cluster types.

**Key ManagedCluster conditions**

| ManagedCluster condition | Relevance |
|---|---|
| `ManagedClusterJoined` | Spoke agent has registered with the hub |
| `HubAcceptedManagedCluster` | Hub has accepted the join request |
| `ManagedClusterConditionAvailable` | Cluster is available; primary signal for `Provisioned=Completed` |

**Association between ClusterInstance and ManagedCluster**

All lookup paths (`ManagedClusterReconciler`, reinstall, and any shared helper)
use one contract.

**ClusterInstance → ManagedCluster**

| Field | Value |
|---|---|
| Source | `clusterInstance.status.manifestsRendered[]` |
| Selector | `kind == "ManagedCluster"` and `status == "rendered"` (`ManifestRenderedSuccess`) |
| Lookup key | `manifest.name` (cluster-scoped; `ManagedCluster` has no namespace) |
| API scope | `client.Get` on `ManagedCluster/{manifest.name}` in the hub cluster |

Uniqueness: each installation flow renders exactly one `ManagedCluster` named
`spec.clusterName`. `manifestsRendered` must contain at most one matching
entry. If multiple entries match the selector, use the entry whose `name` equals
`spec.clusterName`; if that yields zero or more than one entry, log an error,
do not update `Provisioned`, and requeue.

This matches `getManagedClusterManifest` and `getManagedClusterResource` in
`internal/controller/reinstall/helper.go` and `reimport.go` today, except that
today's helper returns the first matching entry when multiple exist; Stage 1
should tighten that to the `spec.clusterName` rule above.

**ManagedCluster → ClusterInstance (reverse / event path)**

| Field | Value |
|---|---|
| Label key | `siteconfig.open-cluster-management.io/owned-by` |
| Label value | `{clusterInstance.namespace}_{clusterInstance.name}` |
| Lookup | Parse label value with `GetNamespacedNameFromOwnedByLabel` |

Rendered manifests receive this label from `appendManifestLabels` in
`template_engine.go`. `ClusterDeploymentReconciler` uses the same label for
reverse association today.

Reverse association is **best-effort for enqueueing**. Status mapping itself
must not require the label: the reconciler always resolves
`ClusterInstance` → `ManagedCluster` via the forward contract above. If a
`ManagedCluster` event arrives without a valid `owned-by` label, prefer
enqueueing the matching `ClusterInstance` by name when
`manifestsRendered` (or `spec.clusterName`) identifies that object, rather
than dropping the event.

**ManagedCluster create race (Hypershift / ACM vs SiteConfig)**

For HCP, more than one controller may create or mutate the same
`ManagedCluster` named `spec.clusterName`:

- SiteConfig Operator renders and applies the HCP `ManagedCluster` template
  (including SiteConfig labels/annotations and `owned-by`).
- Hypershift / ACM hosted-cluster import paths may also create a
  `ManagedCluster` when the `HostedCluster` appears, often **before**
  SiteConfig's apply lands.

Observed failure mode: SiteConfig attempts to create the object, but another
controller created it first. If SiteConfig treats that as a hard create-only
failure (or otherwise does not merge metadata), SiteConfig labels and
annotations—including `owned-by` and import annotations such as
`import.open-cluster-management.io/klusterlet-deploy-mode: Hosted`—are never
applied. Reverse watches that filter on `owned-by` then silently skip the
object, and status sync stalls.

**Required mitigations (Stage 1)**

1. **Ensure, do not create-only.** `ManagedCluster` application must continue
   to use Server-Side Apply with a SiteConfig field manager and
   `ForceOwnership` (as `applyObject` does today), so a pre-existing
   `ManagedCluster` is patched rather than rejected. Create-only /
   ignore-`AlreadyExists`-without-merge behavior is explicitly out of scope
   and must not be reintroduced for this resource.
2. **Heal SiteConfig metadata on every apply.** Each successful
   `ClusterInstance` reconcile that reapplies manifests must re-assert
   SiteConfig-owned labels and annotations on the live `ManagedCluster`,
   including at least:
   - `siteconfig.open-cluster-management.io/owned-by`
   - HCP template import annotations (`hosting-cluster-name`,
     `klusterlet-deploy-mode`, `created-via`, sync-wave)
   - User `extraLabels` / `extraAnnotations` for `ManagedCluster`
3. **Forward lookup is authoritative for status.** `ManagedClusterReconciler`
   must update `Provisioned` from the live object found by
   `manifestsRendered` / `spec.clusterName` even when `owned-by` is missing
   or invalid. Missing `owned-by` is a healable inconsistency, not a reason
   to skip status sync.
4. **Secondary `ClusterInstance` watch is mandatory for Stage 1 HCP**, not
   optional. It closes the window where the `ManagedCluster` exists and its
   conditions change, but reverse enqueue cannot fire because `owned-by` is
   absent. Reconcile from the `ClusterInstance` side, Get the
   `ManagedCluster` by name, map status, and rely on manifest re-apply (or an
   explicit metadata heal) to restore labels.
5. **Do not require Hypershift to create first or second.** Correctness must
   not depend on controller ordering. Either creator may win the initial
   create; SiteConfig must converge metadata and status afterward.

**Behavior when lookup fails**

| Condition | Status-sync reconciler | Reinstall path |
|---|---|---|
| No matching `manifestsRendered` entry | Keep `Provisioned` at `Unknown`; requeue | `getManagedCluster` returns nil (wait); `getManagedClusterResource` returns `ManagedClusterNotInManifestError` (wait) |
| Manifest exists, API object not found | Keep `Provisioned` at `InProgress`; requeue | Wait for provisioning (same as reimport today) |
| Multiple ambiguous matches | Log error; do not update `Provisioned`; requeue | Same (after `spec.clusterName` disambiguation is added) |
| `owned-by` label missing or invalid on `ManagedCluster` | Still reconcile via forward lookup when the object name matches the rendered `ManagedCluster`; log a warning; rely on SSA heal to restore the label. Do **not** ignore the object for status mapping. Reverse-watch-only handlers may skip enqueue if they cannot resolve a `ClusterInstance`, but the `ClusterInstance` watch covers this case. | N/A (reinstall uses forward lookup) |
| SSA / metadata heal fails (field conflict, RBAC, validation) | Keep mapping status if the object is readable; requeue with backoff until SiteConfig labels/annotations are applied; surface the error in controller logs | Same apply path as provisioning |

**Event predicates**

`ManagedCluster` status changes do not bump `metadata.generation`. The primary
`ManagedCluster` watch must therefore enqueue reconciliation on
`status.conditions` updates (including `ManagedClusterConditionAvailable`
becoming `True`), not only on spec changes.

Match `ClusterDeploymentReconciler` today for the labeled reverse path:

- `CreateFunc` / `UpdateFunc`: return `true` for objects with a valid
  `siteconfig.open-cluster-management.io/owned-by` label (any owned create or
  update, including condition-only status updates). Optionally also return
  `true` when the `ManagedCluster` name matches a known HCP
  `ClusterInstance.spec.clusterName` even without `owned-by`, if that can be
  resolved cheaply; otherwise depend on the `ClusterInstance` watch.
- `GenericFunc`: return `false` so periodic cache resync does not enqueue
  reconciles.
- `DeleteFunc`: return `false` (status is not reverted once `Provisioned` is
  reached; see Non-Goals).

Do **not** mirror `ClusterInstanceReconciler`'s `GenerationChangedPredicate` on
the `ManagedCluster` watch; that filter drops condition-only updates.

A secondary `ClusterInstance` watch is **required** for Stage 1 (HCP) so that
status sync and label healing are not gated on `owned-by` being present at
create time. That watch should omit generation-only filtering, matching
`ClusterDeploymentReconciler`'s `WatchesRawSource` setup.

**One-way `Provisioned` transition**

Once `Provisioned` reaches `Completed`, it must not revert. This matches
existing AI/IBI behavior and the Non-Goals above.

**Rejected alternative: HostedCluster.status**

A PoC that followed `HostedCluster.status.conditions[]` or
`HostedCluster.status.version[]` was rejected because it couples SiteConfig
Operator to Hypershift-specific status fields and increases maintenance burden
as those fields evolve.

### Risks and Mitigations

| Risk | Mitigation |
|---|---|
| `ManagedClusterConditionAvailable` may not reflect full provisioning progress for HCP (for example, it currently only indicates API health) | Verify OCM/Hypershift behavior for HCP clusters during Stage 1 implementation; coordinate with OCM if richer conditions are needed |
| Stage 1 and Stage 2 reconcilers could conflict on AI/IBI clusters | Stage 1 scoped strictly to HCP; Stage 2 removes `Provisioned` logic from `ClusterDeploymentReconciler` before enabling `ManagedCluster` path for AI/IBI |
| Hypershift vendoring may be required for other reasons | Status observation does not require Hypershift types; vendoring is independent of this design decision |
| Hypershift / ACM creates `ManagedCluster` before SiteConfig; SiteConfig labels/annotations (including `owned-by`) are missing | Treat `ManagedCluster` as a shared object: SSA + `ForceOwnership` ensure/heal metadata; forward lookup via `manifestsRendered` / `spec.clusterName` for status; mandatory `ClusterInstance` watch so status sync does not depend on `owned-by` at create time (see **ManagedCluster create race**) |
| Another field manager repeatedly overwrites SiteConfig labels/annotations | SiteConfig re-asserts metadata on each manifest apply; log conflicts; requeue until SiteConfig-owned fields converge. If a persistent conflict remains, escalate as an interoperability bug with the competing controller |
| Reverse watch drops unlabeled `ManagedCluster` events | Acceptable for reverse-only enqueue; forward path + `ClusterInstance` watch must still drive reconciles and heal labels |

### Drawbacks

- **Stage 2 migration cost**: Refactoring AI/IBI away from `ClusterDeployment`
  status requires careful testing to avoid regressions in existing ZTP workflows.
- **ManagedCluster granularity**: `ManagedCluster` conditions may be less
  descriptive than `ClusterDeployment` install conditions for in-progress
  status. Detailed deployment progress for AI/IBI may still require
  `deploymentConditions` from `ClusterDeployment`.
- **Dependency on OCM**: Status accuracy depends on OCM correctly updating
  `ManagedCluster` for all cluster types, including HCP.
- **Shared `ManagedCluster` ownership**: HCP `ManagedCluster` objects may be
  created or mutated by Hypershift / ACM as well as SiteConfig. Status sync
  and metadata healing must tolerate that shared ownership without depending
  on create ordering.

## Design Details

### Open Questions [optional]

* Does `ManagedClusterConditionAvailable` accurately reflect HCP provisioning
  completion, or does OCM work need to happen first to expose richer status?
* For Stage 2, should `ClusterInstance.status.deploymentConditions` continue to
  be populated from `ClusterDeployment`, or can that be deprecated?
* Which Hypershift / ACM component creates the competing `ManagedCluster` in
  the observed race (hypershift-addon, cluster-import controller, or another
  path), and does it continue to mutate labels/annotations after create? If it
  owns conflicting fields under SSA, SiteConfig may need a documented field
  ownership split with that controller.

### Test Plan

Describe how this enhancement will be tested. Consider:

- **Unit tests**: Mock `ManagedCluster` objects with various condition
  combinations and verify `ClusterInstance` `Provisioned` status mapping.
  Cover initialization, in-progress, completed, failed, and one-way
  `Provisioned` transitions. Include a predicate test that a condition-only
  `ManagedCluster` update (for example, `ManagedClusterConditionAvailable`
  becoming `True` with unchanged `metadata.generation`) enqueues reconciliation.
- **Create-race / metadata heal tests**:
  - Pre-create a `ManagedCluster` with the expected name but **without**
    SiteConfig `owned-by` / import annotations. Run SiteConfig apply and
    assert SSA adds the required labels and annotations (not
    `AlreadyExists` without merge).
  - Assert status sync still maps `Provisioned` via forward lookup while
    `owned-by` is absent, and that a subsequent reconcile restores the label.
  - Assert a `ClusterInstance` watch-driven reconcile updates `Provisioned`
    when only the unlabeled `ManagedCluster` exists.
- **Stage 1 integration**: E2E test with a hub/hosted cluster pair to
  demonstrate HCP `ClusterInstance` reaching `Provisioned=Completed` when the
  `ManagedCluster` becomes available. Prefer a scenario where Hypershift /
  ACM may create the `ManagedCluster` first. May be expensive or flaky in CI.
- **Stage 2 regression**: Existing AI/IBI unit and integration tests must pass
  after migration to `ManagedCluster`-based status.
- **Downgrade test**: Verify that a `ClusterInstance` that reached
  `Provisioned=Completed` retains that status if the status-sync code is
  removed (see Upgrade / Downgrade Strategy).

### Graduation Criteria

Define the milestones for this enhancement:

**Stage 1 (HCP)**

- [ ] Design reviewed and approved by maintainers
- [ ] `ManagedClusterReconciler` implemented for HCP clusters
- [ ] Forward lookup + mandatory `ClusterInstance` watch; status sync does not
      require `owned-by` at create time
- [ ] SSA metadata heal verified when `ManagedCluster` pre-exists without
      SiteConfig labels/annotations
- [ ] Unit tests with adequate coverage (including create-race cases)
- [ ] E2E demonstration on hub/hosted cluster pair
- [ ] Released in version X.Y.Z

**Stage 2 (AI and IBI)**

- [ ] AI/IBI `Provisioned` status migrated to `ManagedCluster` path
- [ ] `ClusterDeploymentReconciler` refactored; no duplicate status updates
- [ ] Regression tests pass for existing AI/IBI workflows
- [ ] Documented in user-facing docs
- [ ] Released in version X.Y.Z

### Upgrade / Downgrade Strategy

**Status sync after upgrade**

Existing HCP deployments do not update at merge time. `Provisioned` is
refreshed only after the upgraded operator is running and the new
`ManagedClusterReconciler` has synced its watches.

- **Initial refresh**: On manager startup, controller-runtime enqueues a
  reconcile for each watched `ManagedCluster` once the informer cache has
  synced. A secondary `ClusterInstance` watch (required for Stage 1; matching
  the pattern used by `ClusterDeploymentReconciler` today) enqueues reconciles
  for `ClusterInstances` that already reference a `ManagedCluster` but still
  lack `Provisioned`, and for cases where the `ManagedCluster` exists without
  `owned-by` yet. The first successful reconcile reads the current
  `ManagedCluster.status.conditions[]` and patches
  `ClusterInstance.status.conditions[]` as needed. Manifest re-apply / SSA
  heal restores SiteConfig labels and annotations if they are missing.
- **Ongoing updates**: After that initial reconcile, updates are event-driven.
  `ManagedCluster` update events enqueue reconciles when the controller
  predicates allow them. In particular, condition-only updates (for example,
  `ManagedClusterConditionAvailable` becoming `True` with an unchanged
  `metadata.generation`) must pass `UpdateFunc` and enqueue reconciliation; see
  **Event predicates** above. Transient API or patch errors use the existing
  `requeueWithError` helper. Steady-state reconciles return `doNotRequeue()`;
  there is no active polling loop.
- **Resync**: The manager does not set a custom `SyncPeriod` in `cmd/main.go`,
  so the cache uses controller-runtime's default resync interval (10 hours).
  `ManagedClusterReconciler` must set `GenericFunc` to `false`, matching
  `ClusterDeploymentReconciler`, so these periodic resync events do not enqueue
  reconciles.

It would be good to test whether a downgrade will affect the status of a
`ClusterInstance` that has reached `Provisioned` when the code to update it is
gone. Because `Provisioned=Completed` is a one-way transition, downgrade should
not revert an already-provisioned cluster.

### Version Skew Strategy

It is unlikely that a version will be advanced during a hosted cluster's
deployment, but in that case, status updates should proceed normally.

#### Failure Modes

The primary failure mode will be a missing or unknown `Provisioned` status,
which is the current state for HCP clusters. During Stage 1, AI/IBI clusters
continue to use the existing `ClusterDeployment` path and are unaffected.

#### Support Procedures

SiteConfig Controller logs should show if there is an error while reading
`ManagedCluster` status. The original manifests will also still be unaltered
for debugging.

## Implementation History

- **2026-04-30**: Initial proposal; implementation path undecided
  (`HostedCluster` vs `ManagedCluster`).
- **2026-07-10**: Methodology decided. `ManagedCluster` will be used for all
  cluster types. Two-stage rollout: Stage 1 (HCP only), Stage 2 (AI and IBI
  migration).
- **2026-07-21**: Documented Hypershift/ACM vs SiteConfig `ManagedCluster`
  create race. Status sync must use forward lookup and a mandatory
  `ClusterInstance` watch; SiteConfig must SSA-heal labels/annotations rather
  than create-only; missing `owned-by` must not skip `Provisioned` updates.
- If OCM changes are required to make `ManagedCluster` status comprehensive
  enough for HCP, that work must complete before or in parallel with Stage 1.
- After Stage 1 is deployed, both existing and new HCP deployments can report
  `Provisioned` status via `ManagedCluster`. Existing deployments are updated on
  the operator's initial post-upgrade reconcile (see Upgrade / Downgrade
  Strategy), not immediately at merge time.

## Alternatives

**HostedCluster.status (rejected)**

Following `HostedCluster.status.conditions[]` was attempted in a PoC but
rejected as requiring too much maintenance burden on SiteConfig Operator due to
Hypershift-specific status fields.

**ClusterDeployment.status (current AI/IBI path; to be superseded in Stage 2)**

The existing `ClusterDeploymentReconciler` reads Hive `ClusterDeployment`
conditions. This works for AI/IBI but does not apply to HCP clusters, which do
not use `ClusterDeployment`. Keeping separate status paths per cluster type
increases complexity and is inconsistent with the goal of unified ZTP workflows.

**No change (rejected)**

Without this enhancement, HCP `ClusterInstances` never reach `Provisioned`,
blocking ZTP multi-phase deployments that include hosted clusters.

---
title: revert-patch-restoration
authors:
  - "@alegacy"
reviewers:
  - "@sakhoury"
approvers:
  - "@sakhoury"
api-approvers:
  - "@sakhoury"
creation-date: 2026-10-28
last-updated: 2026-10-29
status: provisional
tracking-link:
  - https://issues.redhat.com/browse/CNF-22913
see-also:
  - "https://github.com/stolostron/siteconfig/pull/1076"
---

# Revert-Patch Restoration for Pre-existing Resources

## Release Signoff Checklist

- [ ] Enhancement is `implementable`
- [ ] Design details are appropriately documented from clear requirements
- [ ] Test plan is defined
- [ ] Graduation criteria are defined
- [ ] User-facing documentation is updated

## Summary

This enhancement lets SiteConfig adopt eligible pre-existing rendered resources
and restore their original field values during ClusterInstance (CI) deletion,
manifest pruning, and reinstall cleanup. A durable, versioned recovery record
is persisted atomically with each apply; cleanup generates a guarded JSON Patch
that restores tracked fields, preserves unrelated changes, and releases
SiteConfig ownership. Resources created by SiteConfig retain their normal
deletion behavior. The mechanism applies to every rendered resource kind.

## Motivation

SiteConfig currently creates or updates rendered resources and deletes them
during cleanup. Inventory resources such as BareMetalHosts (BMHs) supplied
through `hostRef` exist before cluster deployment and should survive for reuse. Simply
deleting them loses inventory identity, while removing Server-Side Apply (SSA)
ownership cannot recover values that SiteConfig overwrote.

Administrators need a durable record of the original values and an atomic
restoration operation that survives retries, controller restarts, and unrelated
writes. The same lifecycle problem applies to any pre-existing rendered object.

### User Stories

**As a cluster administrator**, I want a pre-existing inventory BMH to retain
its UID and recover its original configuration when a cluster is deleted so
that it can return to the available pool.

**As a template author**, I want eligible pre-existing resources of any kind
to be restored on pruning or reinstall without writing resource-specific
cleanup logic.

**As an administrator performing day-2 operations**, I want later changes to
untracked fields to survive cleanup and failures to retain recovery data so
that I can correct the cause and retry safely.

**As an administrator restoring a CI from backup**, I want a CI with the same
namespace/name to resume recovery even when its Kubernetes UID has changed.

**Day-2 operations considerations:**

- **Lifecycle**: Restoration runs through the shared deletion handler for
  finalization, pruning, and eligible reinstall cleanup. Normal descending
  sync waves and existing reinstall exclusions remain. A failed restoration
  retains the CI finalizer; reinstall cannot advance until cleanup completes.
- **Monitoring**: Manifest restoration errors use the new `restoration-failed`
  status and an error message. Restoration conflicts pause reconciliation;
  reinstall reports `RestorationFailed` through its cleanup condition.
  Structured logs identify the resource and failed operation.
- **Remediation**: Administrators repair ownership, schema, admission, or
  recovery-data problems and remove the pause annotation. Concurrent-write
  conflicts and transient API errors use normal retries without pausing.
  Pending deletions retain the existing deletion requeue behavior.
- **Scale**: No change to `maxConcurrentReconciles`. Adoption adds live reads,
  SSA previews, and recovery annotation bytes; restoration adds reads and a
  dry-run before each conditional patch. Records are bounded by Kubernetes'
  per-object annotation limit. Measure API request overhead with many CIs.

### Goals

1. Preserve eligible pre-existing resource identity through cleanup and restore
   all tracked fields to their saved baselines while preserving untracked data.
2. Persist original values atomically with the changes they describe and keep
   them stable across repeated apply, retries, and controller restarts.
3. Prevent competing CI claims and create races from overwriting another
   owner's resource or losing its baseline.
4. Make finalization, pruning, field retirement, and reinstall cleanup
   repeatable after partial progress or status-write failures.
5. Fail visibly and retain recovery data when safe restoration cannot complete.

### Non-Goals

1. Restoring values for legacy owned resources without an original baseline.
2. Restoring an entire historical object or undoing unrelated writers' changes.
3. Preserving later changes to tracked fields: their saved baselines win on
   restoration, including entire tracked lists and atomic maps.
4. Cooperative ownership of rendered fields during active reconciliation;
   existing forced SSA behavior remains.
5. Transactional cleanup across resources, protection from external deletion,
   or automatic translation of incompatible API schemas.
6. Adding a new external backup store.

## Proposal

### Workflow Description

SiteConfig adopts an eligible pre-existing rendered resource and restores its
own changes when the CI is deleted, the resource is pruned, or reinstall cleans
it up. This applies to every resource kind, including an inventory BMH rendered
through `hostRef`. Merely referencing an external resource does not adopt it.
SiteConfig-created resources retain their normal deletion behavior.

#### Claims and Lifecycle Disposition

Before applying, SiteConfig checks the live ownership label. A resource claimed
by a different CI is rejected; the shared SSA field manager does not establish
exclusive ownership between CIs. Adoption also validates any existing recovery
record's owner against the same live resource version used by the apply.

* **Absent resource:** use `Create`, not an SSA upsert. If another creator wins
  the race, `AlreadyExists` ends the attempt without modifying that resource.
  The next reconciliation reads it again and performs ownership/adoption
  checks. A successful create uses no recovery annotations and retains normal
  SSA field-removal behavior on subsequent applies.
* **Unclaimed pre-existing resource:** capture a recovery record and store it,
  an `adopted: "true"` annotation, the ownership label, and the desired changes
  together in the real SSA request, guarded by the observed resource version.
* **Existing adopted resource:** preserve its original field values and include
  recovery metadata in subsequent SSA requests. The annotation must not be
  dropped through SSA omission. Missing or invalid recovery data with an
  adopted marker blocks processing rather than authorizing deletion.
* **Matching ownership label without recovery annotations:** retain the existing
  created/legacy lifecycle behavior. There is no reliable baseline to reconstruct
  for resources already managed before this feature was installed.

Recovery records identify the CI by namespace/name, encoded as `namespace_name`
like the ownership label. They do not bind recovery to the CI UID: a CI restored
from backup with the same namespace/name can resume even if its UID changes.

#### Field Scope and Capture

An SSA dry-run computes the actual changes after schema-aware merging,
defaulting, and normalization. The recovery record stores each changed field's
value before its first actual modification. A later apply preserves the original
baseline and captures newly changed fields at that time. The real SSA request
persists the recovery record and desired changes atomically under a
resource-version guard. A stale write retries from a fresh read; an oversized
annotation rejects that request without persisting either part.

The scope includes all actual SiteConfig changes:

* Granular maps, labels, and annotations are tracked by entry. Unrelated entries
  survive restoration. Lists, including associative/set lists, and schema-defined
  atomic maps are tracked as whole values. Restoration overwrites later changes
  to tracked fields, including changes made by collaborating controllers;
  already-restored fields need no further change.
* Status and system metadata are excluded. Templates adopting existing resources
  must not explicitly manage status, finalizers, or owner references. Secret
  `stringData` is normalized to the persisted `data` representation.
* Active reconciliation retains forced SSA: rendered values win while the CI
  is active. Recovery data supplies the original values SSA ownership alone
  cannot recover; it does not change active reconciliation into cooperative
  ownership of rendered fields.
* Template omission restores the removed field's baseline immediately and
  releases its recovery tracking. The SSA request first marks that field as
  retiring, so an interrupted reconciliation completes restoration before the
  next apply. This partial restoration retains the claim and recovery metadata
  for remaining fields. A field changed again later gets a new baseline.
  Initially equal fields are also preserved if SSA omission would otherwise
  remove their pre-existing value.

#### Recovery Annotation and Examples

The two reserved annotations are:

* `siteconfig.open-cluster-management.io/revert-patch`: a compact JSON recovery
  record, **not an executable JSON Patch**.
* `siteconfig.open-cluster-management.io/adopted: "true"`: an independent marker
  that prevents a lost record from turning an adopted resource into a deletable
  created resource.

Templates cannot supply these annotations. Format version 1 stores the CI owner,
the API representation most recently accepted, and original field values.
An omitted `original` means the field was absent; explicit `null`,
`false`, `0`, and empty values remain present values. Paths are JSON Pointers,
escaping `~` as `~0` and `/` as `~1`. Optional `retiring` marks pending field
restoration; optional `parents` lists maps introduced by SiteConfig, which are
removed only if empty so other writers' entries survive.

**Example: change power and cleaning intent.** An inventory BMH initially
has `spec.online: false` and `spec.automatedCleaningMode: disabled`. CI
`example-cluster/example-cluster` renders `online: true` and
`automatedCleaningMode: metadata`. Its recovery annotation contains the following
JSON, shown expanded for readability (the stored annotation is compact):

```json
{
  "version": 1,
  "owner": "example-cluster_example-cluster",
  "apiVersion": "metal3.io/v1alpha1",
  "fields": [
    {
      "path": "/metadata/labels/siteconfig.open-cluster-management.io~1owned-by"
    },
    {"path": "/spec/automatedCleaningMode", "original": "disabled"},
    {"path": "/spec/online", "original": false}
  ]
}
```

This example assumes the metadata maps already existed and these were the only
changed fields. The missing `original` on the ownership label means restoration
removes that label; `online` is restored to `false` and `automatedCleaningMode`
to `disabled`.

**Example: generated restoration request.** For the example above, assume the
live values are still those rendered, the resource version is `12345`, and
unrelated labels/annotations keep those maps nonempty. The controller generates
a JSON Patch with the following operations. `<current BMH UID>` stands for the
actual UID, not a literal value sent to the API. Operation order may vary after
the guards.

```json
[
  {"op": "replace", "path": "/metadata/resourceVersion", "value": "12345"},
  {"op": "test", "path": "/metadata/uid", "value": "<current BMH UID>"},
  {"op": "replace", "path": "/spec/online", "value": false},
  {"op": "replace", "path": "/spec/automatedCleaningMode", "value": "disabled"},
  {"op": "remove", "path": "/metadata/labels/siteconfig.open-cluster-management.io~1owned-by"},
  {"op": "remove", "path": "/metadata/annotations/siteconfig.open-cluster-management.io~1adopted"},
  {"op": "remove", "path": "/metadata/annotations/siteconfig.open-cluster-management.io~1revert-patch"}
]
```

The UID is the only value tested. Later changes to the tracked fields are
overwritten with their baselines. An unrelated annotation or BMO status update
survives. A write between observation and patch submission invalidates the
resource-version guard and causes a fresh-read retry.

During interrupted field retirement,
`{"path":"/spec/online","original":false,"retiring":true}` means the
template omitted `online` and its original value must now be restored.

#### Atomic Restoration and Completion

Cleanup validates the recovery owner and current ownership label, then constructs
the intended restored object from the saved original values regardless of later
changes to tracked fields. It generates a JSON Patch with resource-version and
UID guards. Before committing it, SiteConfig dry-runs that exact patch and
verifies the returned content matches the intended restoration.
Schema pruning, defaulting, or admission that changes the intended result blocks
restoration while preserving the recovery data. Admission must behave consistently
between the preview and real request.

Final restoration removes `owned-by`, `adopted`, and `revert-patch` in the same
persisted patch as the field changes. If updating `ManifestsRendered` afterward
fails, the retry encounters an unowned resource and completes bookkeeping without
deleting it. No permanent completion marker is needed; later adoption starts a
new baseline. Restoration is atomic per resource, not across all resources;
normal descending sync waves still apply.

If the ownership label has been removed, cleanup leaves field values unchanged
and releases recovery annotations belonging to this CI. Invalid recovery data
blocks that release. A valid record and matching label belonging to another CI
are left alone; inconsistent ownership blocks restoration.

#### Failure and Recovery

A restoration conflict immediately pauses reconciliation, reports the failing
manifest with `restoration-failed` and an error, and retains the CI finalizer
during deletion. Reinstall reports `RestorationFailed`. The resource and recovery
data remain unchanged; already completed resources are not rolled back. This does
not depend on the 30-minute deletion timeout, which requires a target deletion
timestamp that a rejected restoration never acquires.

For restoration-triggered pauses, `status.paused.reason` must identify restoration
as the cause, name the affected resource, and explain the failure. This applies
to finalization, pruning, and reinstall cleanup.

Concurrent-write conflicts and transient restoration API errors retry normally.
Other generic reinstall cleanup failures can still pause, but correction and
unpause now retry the failed cleanup phase instead of skipping it. Reinstall
advances only after cleanup completes.

To recover from a restoration conflict:

1. Inspect the CI's manifest messages and paused status for the failed validation
   or API operation.
2. Resolve ownership, schema, or admission problems that prevent restoration.
   Keep recovery annotations intact. Invalid or missing records require repair
   from trusted data; SiteConfig does not guess a baseline. Later changes to
   tracked values do not require correction.
3. Remove the pause annotation to retry:

   ```sh
   oc annotate clusterinstance <name> -n <namespace> \
     clusterinstance.siteconfig.open-cluster-management.io/paused-
   ```

#### API Representation Changes

SiteConfig can preserve and restore adopted resources when their templates move
between compatible, served versions of the same API resource. The API server
must preserve the fields needed for restoration; SiteConfig does not translate
renamed or reshaped fields, and compatibility is not guaranteed for every
version pair.

If a template targets an unserved version, apply fails. Cleanup requires the API
version in the CI's manifest status to remain served; it does not discover or
fall back to another version. Removing that endpoint before cleanup is
unsupported and blocks cleanup. An unavailable endpoint alone does not mean
the resource is absent.

#### Limitations

* The annotation-size limit (256 KiB total per object) bounds recovery data.
  Large records reject the apply atomically. Annotations inherit the resource's
  access controls and backup lifecycle and may contain original sensitive values.
* Restoring a tracked list or atomic map overwrites later changes anywhere in
  that value, including different elements changed by another writer.
* Parent namespace deletion or external owner deletion can still remove a
  resource independently of SiteConfig restoration.
* Downgrading to a controller without restoration support while adopted resources
  remain active is unsupported. An older controller may treat the ownership label
  as permission to delete them. Restore and release adopted resources before
  downgrading.
* Resources already owned by SiteConfig before this feature was enabled have no
  saved original values. SiteConfig cannot reconstruct values it previously
  overwrote, so these resources retain their existing deletion behavior.

### API Extensions

No new CRD fields, admission/conversion webhooks, aggregated API servers, or
finalizers are introduced. The existing CI finalizer remains responsible for
cleanup. The externally visible additions are the reserved `adopted` and
`revert-patch` resource annotations, the manifest status `restoration-failed`,
and the reinstall cleanup reason `RestorationFailed` described above. Templates
cannot set the reserved annotations. The versioned recovery format is an
on-resource compatibility contract; an unknown format must block processing
rather than permit deletion.

### Siteconfig Impact

- **Controllers**: `ClusterInstanceReconciler` checks claims and uses guarded
  create/adopt/apply paths. The shared deletion handler restores adopted
  resources during finalization, pruning, and reinstall. Reinstall retries failed
  cleanup after correction and unpause and advances only when cleanup succeeds.
  No changes to `ClusterDeploymentReconciler` or `ConfigurationMonitor`.
- **Templates**: No shipped template changes are required. All flows use the
  same lifecycle handling. Templates adopting resources must avoid status,
  finalizers, owner references, and reserved recovery annotations.
- **API fields**: No new ClusterInstance spec fields. Existing manifest status,
  pause annotation, finalizer, and reinstall cleanup condition carry results.
- **Validation**: Validate recovery format, logical owner, resource identity,
  field paths, API representation, and exact dry-run restoration result.
  Reject missing/invalid recovery metadata instead of falling back to delete.
- **RBAC**: No new resource kinds or backup-store permissions. Reuse existing
  target-resource read/create/patch/delete permissions.

### Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Concurrent writes invalidate a capture or restoration | Guard apply with the observed resource version; guard restoration with resource version and UID. Fresh-read retries recompute the request. |
| A competing CI or creator claims the same object | Check the ownership label and recovery owner against the same live version; create absent objects without SSA upsert. |
| Recovery metadata is lost, corrupt, too large, or unsupported | Keep an independent adopted marker; reject invalid records and oversized atomic applies. Pause unsafe restoration and preserve available recovery data. |
| Another writer changes a tracked field | Document that restoration deliberately returns tracked fields, lists, and atomic maps to their baselines; untracked fields survive. |
| API conversion or admission changes the intended restored values | Dry-run the exact patch and compare the returned content with the intended result; block incompatible restoration. Admission must be consistent between preview and commit. |
| Original values include sensitive data | Keep recovery data under the resource's own access controls and backup lifecycle; avoid logging record contents and document annotation exposure. |
| A status-write failure follows successful restoration | Release ownership and recovery metadata in the same patch as restored fields; retries complete bookkeeping for the now-unowned object without deleting it. |
| An older controller deletes adopted resources | Require restoration and release of ownership before downgrade. Annotations alone cannot protect resources from an older controller. |

### Drawbacks

- Adds generic schema-aware change tracking, recovery format maintenance,
  served-version handling, and cleanup state transitions to controller code.
- Requires additional API calls and stores original values in annotations,
  bounded by the total annotation-size limit.
- Baseline restoration can overwrite later edits to tracked fields made by
  other controllers; whole-list and atomic-map tracking broadens that effect.
- Unsafe cleanup blocks CI finalization or reinstall until an administrator
  repairs the cause; downgrade requires advance cleanup.

## Design Details

### Test Plan

The focused API-server integration suite uses generic resource and BMH-shaped
schemas. It must not be treated as proof of BMO behavior, switch configuration,
hardware provisioning, or cluster lifecycle manager behavior.

**Unit and controller coverage:**

- Competing CI claims, create-only races, logical owner identity after CI
  recreation, and resource-version guards.
- Missing, invalid, unknown-version, or mismatched-owner recovery data; reserved
  template annotations; and created/legacy versus adopted disposition.
- Initial capture, repeat apply without baseline overwrite, and interrupted
  field retirement.
- Per-resource conflicts, `restoration-failed` status for restoration errors,
  restoration-specific pause reasons identifying the resource and cause,
  correction/unpause, finalizer retention, and transient retries.
- Successful restoration followed by CI status-write failure during
  finalization, pruning, and reinstall; the restored object must not be deleted.
- Generic failed reinstall cleanup retry, pending finalizers, existing reinstall
  exclusions, and completion gating.
- Distinguish an unavailable API endpoint from an absent resource.

**API-server integration coverage:**

- Real SSA merging/ownership and atomic persistence of originals and changes.
- Initially equal fields, defaults, absent versus explicit null values, false,
  atomic maps, associative lists, JSON Pointer escaping, and Secret normalization.
- Template omission, interrupted retirement, readoption, annotation size
  rejection without partial writes, and dry-run validation without persistence.
- Capture and restoration races, including writes after restoration preview;
  preserve status and unrelated fields while overwriting tracked-field drift.
- Compatible alpha-to-beta BMH-shaped transitions, repeat reconciliation,
  omission across versions, and cleanup without an intervening new apply.
- Incompatible/pruned originals and rejection of saved values without changing
  the resource or recovery data.

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] Claim checks and create-only race handling implemented with tests
- [ ] Capture, retirement, atomic restoration, and recovery format implemented
      with adequate unit and API-server integration coverage
- [ ] Finalization, pruning, and reinstall retries qualified, including
      status-write failures and compatible API-version changes
- [ ] Documented in user-facing docs, including baseline semantics, sensitive
      annotation data, recovery procedures, and downgrade restrictions
- [ ] Released in version 2.18.0

### Upgrade / Downgrade Strategy

**Upgrade**: No CRD migration or new CI field is needed. Unclaimed pre-existing
rendered resources begin adoption on their first guarded apply by the new
controller. Already owned legacy resources without recovery annotations keep
their existing deletion behavior: there is no trustworthy retrospective
baseline. Administrators must inventory these resources and plan any explicit
migration before expecting restoration protection. Existing recovery records
must be recognized and validated; an unknown format blocks processing.

**Downgrade**: Do not downgrade to a controller without restoration support
while adopted resources remain active. Older controllers can interpret the
ownership label as permission to delete them even when recovery annotations
remain. Complete restoration and release ownership/recovery metadata with the
capable controller first. Saving annotations alone is not a safe downgrade.

### Version Skew Strategy

Controllers reading a recovery record must support its format version; do not
silently discard unknown or invalid records. A rollout must avoid an older
active reconciler applying legacy deletion semantics to adopted resources.

The recovery record's API version describes the representation most recently
accepted. Apply requires the template's version to be served; cleanup requires
the version in manifest status to remain served, as described in
[API Representation Changes](#api-representation-changes). An unavailable
endpoint or rejected original value blocks cleanup. Conversion must preserve
the fields being restored; SiteConfig does not translate renamed or reshaped
fields.

### Operational Aspects of API Extensions

Monitor existing CI conditions, per-manifest errors, pause state, and finalizer
duration. Use structured logs and API request/error rates to investigate
adoption and restoration failures. Measure extra dry-run/read/patch traffic and
annotation growth under representative CI concurrency; no new service or
webhook availability dependency is introduced.

#### Failure Modes

Invalid ownership or recovery data, incompatible schemas, and admission changes
pause restoration and block deletion or reinstall for the affected CI.
Concurrent writes and transient API failures retry. Oversized recovery records
reject adoption without applying its changes. External deletion, namespace
deletion, or garbage collection can still remove a resource independently.
Escalation starts with the SiteConfig administrator and involves the target API
or admission owner when compatibility or policy prevents restoration.

#### Support Procedures

Follow [Failure and Recovery](#failure-and-recovery): inspect manifest errors,
repair the cause while retaining recovery data, and explicitly unpause. Do not
remove the finalizer or recovery annotations to force progress. If recovery
data must be repaired, use trusted original data; SiteConfig cannot reconstruct
a lost baseline. Disabling restoration by rolling back the operator is unsafe
while adopted resources remain active; restore and release them first.

## Implementation History

- 2026-10-28: Created this proposal for baseline-only resource restoration.

## Alternatives

### Server-Side Apply Field Ownership Alone for Revert

SSA remains useful for schema-aware merging, defaulting, field omission, and the
preview used to capture actual changes. Omitting SiteConfig-managed fields at
cleanup, however, can remove fields or leave values owned by other managers; it
cannot recover overwritten original values. The shared manager also does not
establish exclusive ownership between CIs, and active forced SSA can reclaim
fields. The chosen design therefore combines SSA with explicit claim checks and
durable original values, then uses a conditional JSON Patch to restore.

### Snapshot Backup/Restore for Revert

SiteConfig could snapshot the entire pre-existing resource into a ConfigMap
before modification and restore it on deprovisioning. Replacing the object with
that snapshot would also overwrite unrelated changes by other writers. It
would require external-state lifecycle management and still face API schema
changes between capture and restoration. A guarded, field-selective restore
could address concurrent writes, but would still need the original-value
tracking and conditional updates described in the chosen approach.

### ConfigMap Storage for Revert Patches

The recovery record (used to generate the restoration JSON Patch) could be stored
in a ConfigMap rather than as an annotation on the modified resource. This was
not selected because:

- **Lifecycle complexity** — ConfigMap-based storage would require creating a
  ConfigMap per modified resource (or a shared ConfigMap with keyed entries),
  handling cleanup on successful revert, orphan detection if the resource is
  deleted externally, and preservation during reinstall workflows. Annotations
  are created and removed as part of the resource's own patch operations.
- **Referential integrity** — the recovery record and the resource it describes
  can get out of sync (e.g., ConfigMap deleted accidentally, resource recreated
  without the corresponding ConfigMap entry). With annotations, the record is
  co-located with the resource and follows its backup lifecycle. Restoring a CI
  under a different namespace/name still requires explicit handling of identity.
- **RBAC expansion** — ConfigMap storage would require additional RBAC
  permissions and coordination with the existing PreservationHandler to avoid
  conflicts. Annotations are part of the resource metadata and require no
  additional permissions.

## Infrastructure Needed

No new repository or backup service. Use an API-server integration environment
with structural test CRDs and compatible/incompatible served versions, plus
existing lab infrastructure for inventory BMH and reinstall qualification.

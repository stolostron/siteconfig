---
title: skip-cluster-secrets
authors:
  - "@dav1x"
reviewers:
  - ""
approvers:
  - ""
api-approvers:
  - "None"
creation-date: 2026-04-30
last-updated: 2026-05-19
status: provisional
tracking-link:
  - TBD
see-also:
  - docs/skipping_secret_verification.md
replaces:
  - N/A
superseded-by:
  - N/A
---

# Skip ClusterInstance pull secret and BMC secret presence validation

## Release Signoff Checklist

- [ ] Enhancement is `implementable`
- [x] Design details are appropriately documented from clear requirements
- [x] Test plan is defined
- [ ] Graduation criteria are defined
- [x] User-facing documentation is updated

## Summary

Some deployments manage pull secrets and BMC credentials outside the GitOps
objects that siteconfig reconciles first—for example with HashiCorp Vault,
External Secrets Operator (ESO), or a similar controller that materializes
`Secret` resources after the `ClusterInstance` exists. Today the ClusterInstance
controller fails early in `validateResources` if those secrets are not already
present. This enhancement adds **optional ClusterInstance metadata annotations**
that mark secrets as externally provisioned so integrators can skip only the
checks they need (pull secret, BMC secrets, or both) while leaving other
validation unchanged.

## Motivation

Customers already run secret lifecycle in a separate control plane or namespace
workflow. Blocking reconciliation until secrets exist in the same way as a
fully GitOps-managed cluster prevents those patterns from working with
siteconfig without fragile ordering hacks.

### User Stories

As an **integrator**, I want siteconfig to proceed with rendering and
reconciliation when my secret operator will create the pull secret and/or BMC
secrets shortly after `ClusterInstance` admission, so that I can use existing
secret infrastructure with Assisted-style clusters.

- **Lifecycle**: Skipping presence checks does not change finalizers, sync
  waves, or reinstall behavior. If secrets never appear, downstream manifests
  that reference them may still fail; that remains the responsibility of the
  secret controller and the integrator. Namespace cleanup ordering is unchanged;
  integrators should ensure their GitOps or secret CRs align with namespace
  deletion policies.
- **Monitoring**: No new status conditions are required for this enhancement.
  The controller emits an **info** log once per reconciliation when one or more
  allowed skip annotations are set. Existing reconciliation errors from missing
  refs in rendered objects remain the primary signal when secrets are absent for
  too long.
- **Remediation**: Admins remove the annotation(s) once secrets exist to
  restore strict validation, or use the existing pause annotation if they need
  to stop reconciliation entirely. No new requeue semantics.
- **Scale**: Annotations are per `ClusterInstance`. Cluster templates can set
  them consistently for fleets; there is no change to `maxConcurrentReconciles`.

### Goals

- Allow optional bypass of **pull secret existence** validation in the controller.
- Allow optional bypass of **BMC credential secret existence** validation per node.
- Keep bypass **explicit** (opt-in per annotation) and **independent** (either
  or both checks may be skipped).
- Accept only **empty** or **`"true"`** annotation values; reject other values
  during validation.
- Leave ClusterImageSet, extra manifests, template refs, and CRD/webhook
  validation unchanged.

### Non-Goals

- Removing or weakening validation of secret **contents** or types.
- Skipping validation for other resource types (e.g. SSH keys, image sets).
- Adding new fields to `ClusterInstanceSpec`; this design uses annotations only.
- Guaranteeing that installation succeeds without secrets; only presence checks
  are optional.

## Proposal

### Workflow Description

1. An integrator creates a `ClusterInstance` with one or both annotations (see
   below) set to `""` or `"true"`, typically via a cluster template or GitOps
   overlay. The `ClusterInstance` may reference a cluster template that creates
   a custom resource resulting in one or more secrets.
2. External tooling (ESO, Vault Agent, etc.) creates or syncs the pull secret
   and/or BMC `Secret` objects in the expected namespace(s), possibly after the
   `ClusterInstance` is applied.
3. The siteconfig controller runs `handleValidate` → `Validate` →
   `validateResources`. When an annotation is set to an allowed value, it
   **does not** perform the corresponding `client.Get` for that secret class;
   other checks in `validateResources` still run. Invalid annotation values fail
   validation before presence checks run.
4. Once secrets exist, the integrator may remove the annotation(s) so future
   reconciles enforce presence again (optional operational hardening).

### API Extensions

**None** for `ClusterInstance` spec or CRD schema.

**Metadata annotations** (full keys are constants in `api/v1alpha1`;
`ExternallyProvisionedPullSecretEnabled` and `ExternallyProvisionedBmcSecretEnabled`
parse and validate values):

| Annotation key (suffix after `clusterinstance.siteconfig.open-cluster-management.io/`) | Effect |
| --- | --- |
| `externally-provisioned-pull-secret` | Skip validating that `spec.pullSecretRef` exists in the ClusterInstance namespace. |
| `externally-provisioned-bmc-secret` | Skip validating that each node’s `bmcCredentialsName` secret exists (respecting `HostRef` namespace when set). |

**Allowed values** for each annotation:

| Value | Behavior |
| --- | --- |
| *(annotation absent)* | Presence check runs as today. |
| `""` (empty) | Skip the corresponding presence check. |
| `"true"` | Skip the corresponding presence check. |
| Any other value | Validation fails with an error naming the annotation and allowed values. |

### Siteconfig Impact

- **Controllers**: `ClusterInstance` reconciliation path only.
  - `handleValidate` calls `LogObservedExternallyProvisionedSecretAnnotations`
    once per reconciliation before `Validate` when any allowed skip annotation
    is set (info log lists active annotation keys).
  - `validateResources` in the clusterinstance package gates the bypass via
    `ExternallyProvisionedPullSecretEnabled` / `ExternallyProvisionedBmcSecretEnabled`.
- **Templates**: None required; integrators may add annotations via templates.
- **API fields**: None; annotations and helpers in `clusterinstance_info.go` only.
- **Validation**: Controller-time resource validation only. Webhook / OpenAPI
  validation for `ClusterInstance` spec is unchanged.

### Implementation Details/Notes/Constraints

- Bypass applies only to **existence** checks performed before rendering
  proceeds; it does not inject secrets or change template output.
- Invalid annotation values return wrapped errors from `validateResources`
  (for example `failed to validate "<key>" annotation: ...`).
- Operators must ensure secrets exist before any consumer (e.g. Agent install
  flow) strictly needs them, or accept install failures.
- Annotations are a **workaround-level** escape hatch; document clearly for
  support and security review.

### Risks and Mitigations

| Risk | Mitigation |
| --- | --- |
| Typo or misuse leaves clusters without secrets | Explicit annotation names; user docs; optional removal after secrets land. |
| Invalid annotation value silently ignored | Reject values other than `""` or `"true"` during validation. |
| Broader bypass than intended | Separate annotations for pull vs BMC so callers skip only what they need. |
| Security / compliance | Call out in docs that validation is relaxed only for presence, not for RBAC on secrets. |

### Drawbacks

- Divergence between “secrets exist” and “validation passed” can confuse
  operators who expect the controller to always preflight secrets.
- Support must recognize annotation-driven skips when triaging failures.

## Design Details

### Open Questions

- Whether a future phase should deprecate annotations in favor of a spec field.

### Test Plan

- Unit tests in the clusterinstance validator package:
  - With each annotation set to `""` or `"true"`, missing pull or BMC secret must
    **not** fail validation.
  - With each annotation set to an invalid value (for example `"false"`), validation
    must fail before presence checks.
  - Without annotations, behavior matches today; other `validateResources` checks
    still fail when appropriate.
- `handleValidate` exercises logging via the reconciler path (logger may be no-op
  in unit tests).

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] Implementation merged with adequate test coverage
- [x] Documented in user-facing docs (`docs/skipping_secret_verification.md`)
- [ ] Released in a tagged siteconfig version (TBD)

### Upgrade / Downgrade Strategy

- **Upgrade**: New controller honors new annotations; existing clusters without
  annotations behave identically.
- **Downgrade**: Older controllers ignore unknown annotations; validation
  becomes strict again—secrets must exist before downgrade if the cluster relied
  on skips.

### Version Skew Strategy

Older siteconfig releases ignore the annotations. Newer releases on the
management cluster with this feature can reconcile `ClusterInstance` objects
created by newer GitOps while spoke content is unchanged.

## Implementation History

- 2026-04-30: Proposal drafted.
- 2026-05-19: Updated to match implementation on `skipCIValidation`: renamed
  annotations to `externally-provisioned-*`, restricted values to `""` or
  `"true"`, added reconciliation logging, and linked user-facing docs.

## Alternatives

1. **Spec fields** (e.g. `externallyProvisionedSecrets: true`): clearer in CRD
   but requires API and conversion churn; annotations match existing pause
   pattern and ship faster.
2. **Global operator config**: one flag for all clusters; too coarse for
   multi-tenant or mixed fleets.
3. **Reorder GitOps only**: sometimes insufficient when secret operators lag
   `ClusterInstance` creation.
4. **Presence-only annotations (any value)**: rejected in favor of an explicit
   `""` / `"true"` enumeration to catch typos and misconfiguration early.

## Infrastructure Needed

None.

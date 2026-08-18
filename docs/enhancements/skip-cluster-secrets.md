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
last-updated: 2026-08-12
status: provisional
tracking-link: [TBD]
see-also: [N/A]
replaces: [N/A]
superseded-by: [N/A]
---

# Soften ClusterInstance pull secret and BMC secret presence validation

## Release Signoff Checklist

- [ ] Enhancement is `implementable`
- [x] Design details are appropriately documented from clear requirements
- [x] Test plan is defined
- [x] Graduation criteria are defined
- [ ] User-facing documentation is updated

## Summary

Some deployments manage pull secrets and BMC credentials outside the GitOps
objects that siteconfig reconciles first—for example with HashiCorp Vault,
External Secrets Operator (ESO), or a similar controller that materializes
`Secret` resources after the `ClusterInstance` exists. Today the ClusterInstance
controller fails early in `validateResources` if those secrets are not already
present. This enhancement changes pull secret and BMC credential **presence**
checks so a **not found** pull secret or BMC credential emits a structured info
log and validation continues; all other `client.Get` errors still fail
validation. Other resource validation remains strict, including ConfigMaps
used as extra manifests and install templates.

## Motivation

Customers already run secret lifecycle in a separate control plane or namespace
workflow. Blocking reconciliation until secrets exist in the same way as a
fully GitOps-managed cluster prevents those patterns from working with
siteconfig without fragile ordering hacks. A global soft-fail for these two
presence checks removes the chicken-and-egg problem without requiring new
annotations or API fields on every `ClusterInstance`.

### User Stories

As an **integrator**, I want siteconfig to proceed with rendering and
reconciliation when my secret operator will create the pull secret and/or BMC
secrets shortly after `ClusterInstance` admission, so that I can use existing
secret infrastructure with Assisted-style clusters.

- **Lifecycle**: Softening presence checks does not change finalizers, sync
  waves, or reinstall behavior. If secrets never appear, downstream manifests
  that reference them may still fail; that remains the responsibility of the
  secret controller, the integrator, and the controllers for those downstream
  manifests (for example metal3 and Assisted Service). Namespace cleanup
  ordering is unchanged. Those downstream controllers must similarly not fail
  cleanup if the referenced secret does not exist. Integrators should ensure
  their GitOps or secret CRs align with namespace deletion policies.
- **Monitoring**: No new status conditions are required. When a pull secret or
  BMC credential `client.Get` returns `apierrors.IsNotFound`, the siteconfig controller
  emits a structured **info** log (`log.Info`) and continues validation. Forbidden,
  timeout, transient API, and other non-`NotFound` errors still fail validation.
  Use the log-search rule below to find clusters reconciling without secrets yet.
  Existing reconciliation errors from missing refs in rendered objects remain the
  primary signal when secrets are absent for too long.
- **Remediation**: Recovery when pull or BMC secrets are created later does **not**
  depend on a new `ClusterInstance` reconcile. `ClusterInstanceReconciler`
  watches only `ClusterInstance` events (spec generation, labels, pause
  annotation, reinstall `Provisioned` condition changes); it does not watch
  `Secret` objects and adds no `RequeueAfter` for missing secrets. After a
  successful reconcile, `status.observedGeneration` matches `metadata.generation`
  and the controller pre-empts before re-running validation, so presence checks
  do not run again until the spec changes. **Automatic recovery** is handled by
  downstream controllers on the already-applied manifests (for example
  metal3-baremetal-controller for BMC credentials and Assisted Service for the
  pull secret). **Manual siteconfig re-validation** (optional): bump
  `ClusterInstance` spec generation or toggle the pause annotation to force a
  full reconcile—only if operators want siteconfig to re-check presence, not to
  unblock install. Use the pause annotation to stop reconciliation entirely when
  needed.
- **Scale**: Soft-fail applies uniformly to all `ClusterInstance` objects.

### Goals

- Stop failing ClusterInstance validation solely because the **pull secret** is
  not found (`apierrors.IsNotFound`); emit an info log and continue instead.
- Stop failing ClusterInstance validation solely because a node’s **BMC
  credential secret** is not found; emit an info log and continue instead.
- Propagate forbidden, timeout, transient, and other non-`NotFound` secret lookup
  errors unchanged.
- Keep ClusterImageSet, extra manifests, template refs (ConfigMaps), JSON
  override checks, control-plane agent counts, and CRD/webhook validation
  unchanged.
- Avoid new annotations or `ClusterInstanceSpec` fields for this escape hatch.

### Non-Goals

- Removing or weakening validation of secret **contents** or types.
- Skipping validation for other resource types (e.g. SSH keys, image sets).
- Applying the same soft-fail to **ConfigMaps** (`extraManifestsRefs`, cluster-
  and node-level `TemplateRefs`). Those objects are GitOps inputs required to
  render manifests; they are not secrets materialized after admission by
  ESO/Vault, so a missing ConfigMap remains a hard validation failure.
- Guaranteeing that installation succeeds without secrets; only presence checks
  become non-blocking.
- Adding per-cluster opt-in/opt-out for these two checks.

## Proposal

### Workflow Description

1. An integrator creates a `ClusterInstance` that references a pull secret and
   BMC credential secret names as today. The secrets may not exist yet—for
   example because a cluster template creates a custom resource that later
   materializes those secrets, or because ESO/Vault syncs them asynchronously.
2. External tooling creates or syncs the pull secret and/or BMC `Secret` objects
   in the expected namespace(s), possibly after the `ClusterInstance` is applied.
3. The siteconfig controller runs `handleValidate` → `Validate` →
   `validateResources`. For pull secret and BMC credential `client.Get` calls,
   only `apierrors.IsNotFound` results in an info-level structured log and
   **does not** return an error. Forbidden (`403`), timeout, transient API,
   invalid-key, and other non-`NotFound` errors are wrapped and returned as
   validation failures. All other checks in `validateResources` still fail hard
   when appropriate.
4. Rendering and further reconciliation proceed. Rendered manifests are applied
   once; when secrets appear later, downstream controllers reconcile
   independently (siteconfig does not watch `Secret` creation). If consumers
   still need the secrets at apply time, install may fail later until they
   appear; that failure mode is unchanged.

### API Extensions

**None.** No CRD schema, webhook, or annotation changes are required.

### Siteconfig Impact

- **Controllers**: `ClusterInstance` reconciliation path only.
  - `Validate` / `validateResources` accept a logger so missing pull/BMC secrets
    can be reported without failing the call.
  - Pull secret and per-node BMC credential lookups soft-fail **only** on
    `apierrors.IsNotFound` (info log, continue); all other `client.Get` errors
    propagate. Other resource checks retain current error behavior.
- **Templates**: None.
- **API fields**: None.
- **Validation**: Controller-time resource validation only. Webhook / OpenAPI
  validation for `ClusterInstance` spec is unchanged.

### Implementation Details/Notes/Constraints

- Soft-fail applies only when `client.Get` for pull secret or BMC credentials
  returns `apierrors.IsNotFound`. Any other error (for example
  `apierrors.IsForbidden`, `apierrors.IsTimeout`, `apierrors.IsServerTimeout`,
  `apierrors.IsServiceUnavailable`, or invalid request) must fail validation with
  the existing wrapped error messages.
- Reference implementation / prototype:
  branch `optional-pull-bmc-secret-validation` (validator logs and continues on
  missing pull/BMC secrets; **must add `apierrors.IsNotFound` guard**—prototype
  currently logs on any `Get` error).
- Operators must ensure secrets exist before any consumer (e.g. Agent install
  flow) strictly needs them, or accept install failures.
- **Log level**: `Info` only (`log.Info` in the validator). Do not use `Warn` or
  `Error` for missing pull/BMC secret presence; validation must still succeed.
- **Logger context**: Use the `ClusterInstanceReconciler` logger already bound
  with ClusterInstance identity (`name`, `namespace`, and `version` =
  resourceVersion) so a multi-tenant hub can attribute the message. The
  missing-secret log adds the looked-up Secret identity and the `Get` error:
  - Pull secret: Secret `name`, `namespace`, `error`
  - BMC credentials: Secret `name`, `namespace`, node `hostname`, `error`
- **Log messages** (exact strings for search and alerting):
  - `Pull Secret not detected; continuing validation`
  - `BMC credentials Secret not detected; continuing validation`
- **Log-search rule**: In siteconfig controller manager logs, match
  `level=info` (or JSON `"level":"info"`) and either message above. Filter on
  ClusterInstance `name` / `namespace` for a specific cluster. Example
  queries:
  - OpenShift Logging / Loki:
    `{kubernetes_container_name="manager"} |= "not detected; continuing validation"`
  - `oc logs` / grep:
    `oc logs -n open-cluster-management deploy/siteconfig-controller-manager -c manager | grep 'not detected; continuing validation'`
- **Alert guidance** (optional): Prefer alerting on sustained downstream
  missing-secret errors (BMH `BMCCredentialError`, ACI `SpecSynced` pull-secret
  failures). If monitoring siteconfig logs directly, note that missing-secret
  info messages are typically emitted only during the initial reconcile that
  reaches validation, not on every periodic loop.

### Risks and Mitigations

| Risk | Mitigation |
| --- | --- |
| Clusters proceed without secrets and fail later in install | Info-level logs on every missing lookup; log-search rule above; downstream errors remain until secrets appear. |
| Operators lose an early preflight signal | Keep other validations strict; call out the behavior change in release notes and troubleshooting docs. |
| Security / compliance | Soft-fail only on `NotFound`; forbidden and other API errors still block validation; secret contents are never weakened. |

### Drawbacks

- Divergence between “secrets exist” and “validation passed” can confuse
  operators who expect the controller to always preflight secrets.
- Support must recognize soft-fail logs when triaging late install failures.

## Design Details

### Open Questions

- Whether user-facing docs need a short troubleshooting note beyond the
  log-search rule in this enhancement.

### Test Plan

- Unit tests in the clusterinstance validator package:
  - Pull secret `client.Get` returning `NotFound` must **not** fail validation
    (info log path exercised with a test logger).
  - BMC credential `client.Get` returning `NotFound` (ClusterInstance namespace
    or `HostRef` namespace) must **not** fail validation.
  - Pull secret `client.Get` returning `Forbidden` must fail validation (for
    example wrapped `failed to validate Pull Secret`).
  - BMC credential `client.Get` returning `Forbidden` must fail validation.
  - Pull secret or BMC credential `client.Get` returning a transient error (for
    example `Timeout` or `ServiceUnavailable` from a fake client) must fail
    validation.
  - Other `validateResources` checks still fail when appropriate (empty
    ClusterImageSet, missing extra manifests, template refs, etc.).
- `handleValidate` continues to surface real validation errors via existing
  conditions; soft-fail cases leave validation successful.

#### Manual / integration verification (PR [#1051](https://github.com/stolostron/siteconfig/pull/1051))

Soft-fail was exercised on a live hub with image
`quay.io/dphillip/siteconfig-manager:latest` built from
`optional-pull-bmc-secret-validation`
([a1754679](https://github.com/dav1x/siteconfig/commit/a1754679cc9e8bbcfd748d410314dacbd8fd9a52)).
Results below summarize the delayed-secret runs reported on the proposal PR.

**While secrets are absent**

- Controller emits info logs `Pull Secret not detected; continuing validation`
  and `BMC credentials Secret not detected; continuing validation`, then
  `Validation succeeded` / `Finished validation`.
- `ClusterInstance` reaches `ClusterInstanceValidated`,
  `RenderedTemplates`, `RenderedTemplatesValidated`, and
  `RenderedTemplatesApplied` (`Completed` / `True`) even though referenced
  secrets do not exist yet; manifests (ACI, ClusterDeployment, InfraEnv,
  NMStateConfig, BMH, ManagedCluster, etc.) are rendered and applied.
- `ClusterDeployment` is created and present (`PROVISIONSTATUS=Initialized` on
  `zt-sno3`); Hive install conditions are not yet met. Mirrored
  `ClusterInstance.status.deploymentConditions` show `Initialized` /
  `Unknown` for `ClusterInstallRequirementsMet`, `ClusterInstallCompleted`,
  `ClusterInstallFailed`, and `ClusterInstallStopped`. `ClusterInstance`
  `Provisioned` remains `Unknown` (“Waiting for provisioning to start”).
- Downstream consumers surface missing secrets more directly than
  `ClusterDeployment`: BMH `BMCCredentialError` / registration error; ACI
  `SpecSynced=False` with Assisted pull-secret lookup failure; Assisted Service
  logs/errors referencing pull-secret validation on the `ClusterDeployment`.
  Siteconfig itself does not block on presence.
- **ClusterDeployment signal limitation**: Hive `ClusterDeployment` has no
  condition that names a missing pull or BMC secret. Install conditions stay
  `Unknown`/`Initialized` until Assisted/Hive advance, so CD status alone is a
  weak early indicator—prefer ACI, BMH, and Assisted logs while secrets are
  absent.

**After secrets are created (delayed restore)**

- [Comment 5094867936](https://github.com/stolostron/siteconfig/pull/1051#issuecomment-5094867936)
  (`zt-sno3`): secrets created days after initial apply. BMH moved to
  `registering` / `OK` with `Good Credentials`, `BMCAccessValidated`, and
  provisioning started; `ClusterInstanceValidated` remained succeeded from the
  earlier soft-fail path. ACI `SpecSynced=True` but `Validated=False`
  (`ValidationsFailing`) and `RequirementsMet=False` (`ClusterNotReady`) at
  that checkpoint—install not complete yet. `ClusterDeployment` still showed
  `PROVISIONSTATUS=Initialized`; install had not finished.
- [Comment 5133054284](https://github.com/stolostron/siteconfig/pull/1051#issuecomment-5133054284)
  (`zt-sno4`): secrets restored after ~24 hours. Cluster installed successfully:
  `ClusterDeployment` `ClusterInstallStopped=True` /
  `InstallationCompleted` (`Reason=InstallationCompleted`); ACI state
  `adding-hosts` / “Cluster is installed”. `ManagedCluster` joined and
  available on the hub (`HUB ACCEPTED=true`, `JOINED=True`, `AVAILABLE=True`).

**Acceptance checks for implementers**

- [ ] Apply a `ClusterInstance` with missing pull and BMC secrets; confirm
      validation succeeds with the soft-fail logs and templates apply.
- [ ] Confirm BMH/ACI (or equivalent consumers) report missing-secret errors
      while secrets are absent—not a siteconfig validation failure; do not rely
      on `ClusterDeployment` install conditions alone (see limitation above).
- [ ] Create the secrets after a delay; confirm BMH credentials validate and
      the cluster can complete install and become a managed cluster on the hub.

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] Implementation merged with adequate test coverage
- [ ] Documented in release notes / troubleshooting (log strings and behavior)
- [ ] Released in a tagged siteconfig version (TBD)

### Upgrade / Downgrade Strategy

- **Upgrade**: New controller soft-fails pull/BMC presence checks for all
  clusters; existing clusters that already had secrets see no behavior change
  beyond the absence of hard failures when secrets are briefly missing.
- **Downgrade**: Older controllers restore hard failure when secrets are
  missing; ensure secrets exist before downgrade if clusters relied on the
  soft-fail window.

### Version Skew Strategy

Spoke content is unchanged. Only the management-cluster siteconfig controller
behavior changes.

## Implementation History

- 2026-04-30: Proposal drafted (annotation-based opt-in).
- 2026-05-19: Updated to match annotation implementation on `skipCIValidation`:
  `externally-provisioned-*` keys, `""` / `"true"` values, reconciliation
  logging, and user-facing docs.
- 2026-07-24–2026-07-30: Soft-fail prototype verified on hub clusters
  `zt-sno3` / `zt-sno4` (see Test Plan / PR #1051 comments).
- 2026-08-10: Retargeted proposal to the tested soft-fail approach on
  `optional-pull-bmc-secret-validation`: always log and continue when pull or
  BMC secrets are missing; recorded manual verification results from PR #1051.
- 2026-08-12: Standardized on info-level logging with log-search guidance;
  documented recovery triggers aligned with `ClusterInstanceReconciler` (no
  Secret watch; downstream controller recovery); restricted soft-fail to
  `apierrors.IsNotFound` only.

## Alternatives

1. **Opt-in annotations** (e.g. `externally-provisioned-pull-secret` /
   `externally-provisioned-bmc-secret` with `""` or `"true"`): preserves strict
   default validation and was prototyped on `skipCIValidation`. Rejected as the
   primary design because it requires every affected cluster (or template) to
   carry escape-hatch metadata for a common ESO/Vault ordering problem, and the
   soft-fail approach was tested successfully without annotations.
2. **Spec fields** (e.g. `externallyProvisionedSecrets: true`): clearer in CRD
   but requires API and conversion churn for the same outcome as soft-fail.
3. **Global operator config**: one flag to restore strict presence checks; may
   be revisited if mixed fleets need a kill-switch, but adds config surface the
   soft-fail prototype did not need.
4. **Reorder GitOps only**: sometimes insufficient when secret operators lag
   `ClusterInstance` creation.

## Infrastructure Needed

None.

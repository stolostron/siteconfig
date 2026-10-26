---
title: skip-cluster-secrets
authors:
  - "@dav1x"
reviewers:
  - "@sabbir-47"
  - "@sakhoury"
  - "@imiller0"
  - "@cwilkers"
  - "@fontivan"
approvers:
  - "@sabbir-47"
  - "@sakhoury"
api-approvers:
  - "None"
creation-date: 2026-04-30
last-updated: 2026-10-13
status: provisional
tracking-link:
  - https://issues.redhat.com/browse/ACM-15720
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
checks so only `apierrors.IsNotFound` emits a structured warn log and
validation continues. Forbidden, timeout, `ServiceUnavailable`, and other
non-`NotFound` `client.Get` errors still fail validation. Other resource
validation remains strict, including ConfigMaps used as extra manifests and
install templates. Soft-fail is the default. Operators can restore strict
presence checks globally via a `siteconfig-operator-configuration` ConfigMap
key (`strictSecretPresenceValidation`).

## Motivation

Customers already run secret lifecycle in a separate control plane or namespace
workflow. Blocking reconciliation until secrets exist in the same way as a
fully GitOps-managed cluster prevents those patterns from working with
siteconfig without fragile ordering hacks. Soft-fail for these two presence
checks is the default so ESO/Vault-backed clusters do not need per-cluster
annotations. A global ConfigMap kill-switch restores today’s hard-fail for
fleets that still want the preflight.

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
  BMC credential `client.Get` returns `NotFound`, the siteconfig controller
  emits a structured **warn** log (`log.Warn`) and continues validation. Use
  the log-search rule below to find clusters reconciling without secrets yet.
  If secrets never appear, `ClusterInstance` status is the same as a successful
  render (`ClusterInstanceValidated` / `RenderedTemplates` `Completed` /
  `True`); look at BMH, ACI, and Assisted for the missing-secret signal.
  Forbidden, timeout, and other non-`NotFound` lookup errors still fail
  validation and surface through existing conditions.
- **Remediation**: Recovery when pull or BMC secrets are created later does **not**
  depend on a new `ClusterInstance` reconcile and is **not** blocked on
  siteconfig re-validation. There is no stuck validation status to clear:
  `ClusterInstanceValidated` is already `True` after the soft-fail path.
  `ClusterInstanceReconciler` watches only `ClusterInstance` events (spec
  generation, labels, pause annotation, reinstall `Provisioned` condition
  changes); it does not watch `Secret` objects. **After this change**, a
  missing pull or BMC secret no longer fails `handleValidate`, so the
  controller does not requeue with `DefaultValidationErrorDelay` (30s) for
  that miss—today a hard validation failure *does* requeue after that delay.
  After a successful reconcile, `status.observedGeneration` matches
  `metadata.generation` and the controller pre-empts before re-running
  validation, so presence checks do not run again until the spec changes.
  **Automatic recovery** is handled by
  downstream controllers on the already-applied manifests (for example
  metal3-baremetal-controller for BMC credentials and Assisted Service for the
  pull secret). The pause annotation
  (`clusterinstance.siteconfig.open-cluster-management.io/paused`) remains
  available to stop reconciliation entirely; it is not required to unblock
  install after secrets appear.
- **Scale**: Soft-fail applies uniformly to all `ClusterInstance` objects
  unless `strictSecretPresenceValidation` is set (see Implementation Details).

### Goals

- Stop failing ClusterInstance validation solely because the **pull secret**
  is `NotFound`; emit a warn log and continue instead.
- Stop failing ClusterInstance validation solely because a node’s **BMC
  credential secret** is `NotFound`; emit a warn log and continue instead.
- Fail validation on forbidden, timeout, `ServiceUnavailable`, and other
  non-`NotFound` pull/BMC `client.Get` errors in the same implementation PR.
- Keep ClusterImageSet, extra manifests, template refs (ConfigMaps), JSON
  override checks, control-plane agent counts, and CRD/webhook validation
  unchanged.
- Avoid new annotations or `ClusterInstanceSpec` fields for this escape hatch.
- Default to soft-fail, with a global opt-out on
  `siteconfig-operator-configuration` (`strictSecretPresenceValidation`) to
  restore strict pull/BMC presence checks for mixed fleets.

### Non-Goals

- Removing or weakening validation of secret **contents** or types.
- Skipping validation for other resource types (e.g. SSH keys, image sets).
- Applying the same soft-fail to **ConfigMaps** (`extraManifestsRefs`, cluster-
  and node-level `TemplateRefs`). Those objects are GitOps inputs required to
  render manifests; they are not secrets materialized after admission by
  ESO/Vault, so a missing ConfigMap remains a hard validation failure.
- Guaranteeing that installation succeeds without secrets; only `NotFound`
  presence checks become non-blocking.
- Adding per-cluster opt-in/opt-out for these two checks.
- Watching `Secret` objects or adding `RequeueAfter` so siteconfig re-runs
  presence checks when secrets appear. Recovery stays with downstream
  controllers.

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
   `apierrors.IsNotFound` results in a warn-level structured log and **does
   not** return an error. Forbidden, timeout, and other Get errors still fail
   validation. All other checks in `validateResources` still fail hard when
   appropriate. If `strictSecretPresenceValidation` is true, `NotFound` also
   fails validation (today’s behavior).
4. Rendering and further reconciliation proceed. Rendered manifests are applied
   once; when secrets appear later, downstream controllers reconcile
   independently (siteconfig does not watch `Secret` creation). If consumers
   still need the secrets at apply time, install may fail later until they
   appear; that failure mode is unchanged.

### API Extensions

**No ClusterInstance CRD, webhook, or annotation changes.** One new key on the
existing `siteconfig-operator-configuration` ConfigMap (same namespace as the
operator):

| Key | Default | Effect |
| --- | --- | --- |
| `strictSecretPresenceValidation` | `false` | When `false` (default), pull/BMC `NotFound` is a warn-and-continue. When `true`, those presence checks fail validation as they do on main today. |

`ConfigurationMonitor` already watches this ConfigMap; the new key is read from
`ConfigurationStore` at validation time and does not require a manager-pod
restart (unlike `maxConcurrentReconciles`). Existing ConfigMaps that omit the
key keep the default (`false`). Toggling the key does **not** re-validate
ClusterInstances that already passed validation until their spec generation
changes (same `observedGeneration` pre-empt as missing secrets).

### Siteconfig Impact

- **Controllers**: `ClusterInstance` reconciliation path only.
  - `Validate` / `validateResources` accept a logger so missing pull/BMC secrets
    can be reported without failing the call, and read
    `strictSecretPresenceValidation` from `ConfigurationStore`.
  - Pull secret and per-node BMC credential `client.Get` `NotFound` emits a
    warn log and continues unless the kill-switch is set. Other Get errors and
    other resource checks retain current error behavior.
- **Templates**: None.
- **API fields**: None on `ClusterInstance`. ConfigMap key as above.
- **Validation**: Controller-time resource validation only. Webhook / OpenAPI
  validation for `ClusterInstance` spec is unchanged.

### Implementation Details/Notes/Constraints

- Soft-fail **only** on `apierrors.IsNotFound` for pull secret and BMC
  credential `client.Get`. Log at warn and continue. Forbidden, timeout,
  `ServiceUnavailable`, invalid-key, and other errors still fail validation
  with the existing messages.
- The hub prototype on `optional-pull-bmc-secret-validation` continued on
  **any** Get error. The implementation PR must add the `IsNotFound` guard and
  must **not** ship that broader swallow.
- When `strictSecretPresenceValidation` is `true`, `NotFound` fails validation
  as on main today (no warn-and-continue). Default is `false`.
- Operators must ensure secrets exist before any consumer (e.g. Agent install
  flow) strictly needs them, or accept install failures.
- **Log level**: `Warn` (`log.Warn` in the validator). Delayed secrets from
  ESO/Vault are an expected workflow, but the miss is still operator-visible.
  Do not use `Error` (validation must succeed) or `Info` (too easy to miss on
  a multi-tenant hub).
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
  `level=warn` (or JSON `"level":"warn"`) and either message above. Filter on
  ClusterInstance `name` / `namespace` for a specific cluster. Example
  queries:
  - OpenShift Logging / Loki:
    `{kubernetes_container_name="manager"} |= "not detected; continuing validation"`
  - `oc logs` / grep:
    `oc logs -n open-cluster-management deploy/siteconfig-controller-manager -c manager | grep 'not detected; continuing validation'`
- **Alert guidance** (optional): Prefer alerting on sustained downstream
  missing-secret errors (BMH `BMCCredentialError`, ACI `SpecSynced` pull-secret
  failures). If monitoring siteconfig logs directly, note that missing-secret
  warn messages are typically emitted only during the initial reconcile that
  reaches validation, not on every periodic loop.

### Risks and Mitigations

| Risk | Mitigation |
| --- | --- |
| Clusters proceed without secrets and fail later in install | Warn-level logs on every `NotFound` lookup; log-search rule above; downstream errors remain until secrets appear. |
| Operators lose an early preflight signal | Default still logs the miss; `strictSecretPresenceValidation` restores hard-fail globally; keep other validations strict; call out the behavior change in release notes and troubleshooting docs. |
| Security / compliance | Only `NotFound` is non-blocking; secret contents are never weakened. Forbidden and timeout still fail validation. |

### Drawbacks

- Divergence between “secrets exist” and “validation passed” can confuse
  operators who expect the controller to always preflight secrets.
- Support must recognize soft-fail logs when triaging late install failures.

## Design Details

### Open Questions

None. User-facing troubleshooting will include the log-search rule from this
enhancement (how to find missing pull/BMC secrets in controller logs).

### Test Plan

- Unit tests in the clusterinstance validator package:
  - Missing pull secret (`apierrors.IsNotFound`) must **not** fail validation
    (warn log path exercised with a test logger).
  - Missing BMC credential secret (ClusterInstance namespace or `HostRef`
    namespace) must **not** fail validation.
  - Forbidden, timeout, and other non-`NotFound` `client.Get` errors **must**
    fail validation.
  - With `strictSecretPresenceValidation=true`, `NotFound` **must** fail
    validation (legacy preflight).
  - Other `validateResources` checks still fail when appropriate (empty
    ClusterImageSet, missing extra manifests, template refs, etc.).
- `handleValidate` continues to surface real validation errors via existing
  conditions; `NotFound` soft-fail cases leave validation successful.
- System / regression coverage for supported install flows, at least
  **Assisted** and **IBI**: apply a ClusterInstance with delayed pull and BMC
  secrets, confirm validation succeeds and templates apply, then create the
  secrets and confirm downstream recovery. Confirm with Telco QE
  (`@yliu127`, `@klaskosk`) that this change does not break existing
  regression tests before the implementation PR merges.

#### Manual / integration verification (PR [#1051](https://github.com/stolostron/siteconfig/pull/1051))

Soft-fail was exercised on a live hub with image
`quay.io/dphillip/siteconfig-manager:latest` built from
`optional-pull-bmc-secret-validation`
([a1754679](https://github.com/dav1x/siteconfig/commit/a1754679cc9e8bbcfd748d410314dacbd8fd9a52)).
That prototype continued on any Get error and had no ConfigMap kill-switch;
implementation must use `IsNotFound` and honor
`strictSecretPresenceValidation`. Results below summarize the delayed-secret
runs reported on the proposal PR.

**While secrets are absent**

- Controller emits `Pull Secret not detected; continuing validation` and
  `BMC credentials Secret not detected; continuing validation`, then
  `Validation succeeded` / `Finished validation`. The hub run logged those
  strings at info; the prototype now uses `log.Warn` (this enhancement's
  intended level).
- `ClusterInstance` status is the same as when secrets exist and templates
  apply successfully. Observed hub listing (`zt-sno3`,
  [comment 5074025224](https://github.com/stolostron/siteconfig/pull/1051#issuecomment-5074025224)
  and
  [comment 5094867936](https://github.com/stolostron/siteconfig/pull/1051#issuecomment-5094867936)):

```json
  "conditions": [
    {
      "lastTransitionTime": "2026-07-24T20:17:45Z",
      "message": "Validation succeeded",
      "reason": "Completed",
      "status": "True",
      "type": "ClusterInstanceValidated"
    },
    {
      "lastTransitionTime": "2026-07-24T20:17:45Z",
      "message": "Rendered templates successfully",
      "reason": "Completed",
      "status": "True",
      "type": "RenderedTemplates"
    }
  ]
```

  Also `RenderedTemplatesValidated` and `RenderedTemplatesApplied`
  (`Completed` / `True`). If secrets **never** appear, these conditions stay
  `True`; siteconfig does not add a missing-secret condition, event, or
  annotation. Operators must inspect BMH/ACI (or Assisted logs), not
  `ClusterInstance` validation status.
- Manifests (ACI, ClusterDeployment, InfraEnv, NMStateConfig, BMH,
  ManagedCluster, etc.) are rendered and applied.
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
- [ ] Documented in release notes / troubleshooting (log strings, log-search
      rule, and `strictSecretPresenceValidation`)
- [ ] Released in a tagged siteconfig version (TBD)

### Upgrade / Downgrade Strategy

- **Upgrade**: New controller soft-fails pull/BMC `NotFound` for all clusters
  unless `strictSecretPresenceValidation` is `true`. Existing clusters that
  already had secrets see no behavior change beyond the absence of hard
  failures when secrets are briefly missing. The default ConfigMap gains the
  new key; ConfigMaps that omit it keep the default (`false`).
- **Downgrade**: Older controllers restore hard failure when secrets are
  missing. `FromMap` / `UpdateConfiguration` currently reject unknown keys, so
  remove `strictSecretPresenceValidation` from the ConfigMap before
  downgrade or the configuration monitor will fail to load it. Ensure secrets
  exist before downgrade if clusters relied on the soft-fail window.

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
  Secret watch; downstream controller recovery).
- 2026-09-02: Deferred the `apierrors.IsNotFound` guard to a follow-up PR;
  this enhancement matches the prototype (log and continue on any pull/BMC
  `client.Get` error).
- 2026-09-16: Missing-secret logs changed from `Info` to `Warn` to match the
  prototype (`log.Warn`).
- 2026-10-13: Review feedback: soft-fail only on `NotFound` in the
  implementation PR (not deferred); add
  `strictSecretPresenceValidation` on `siteconfig-operator-configuration` as a
  global kill-switch (default off); drop manual re-validation as a recovery
  step; commit to Assisted/IBI system tests and Telco QE check; fill reviewers,
  approvers, and ACM-15720 tracking link.

## Alternatives

1. **Opt-in annotations** (e.g. `externally-provisioned-pull-secret` /
   `externally-provisioned-bmc-secret` with `""` or `"true"`): preserves strict
   default validation and was prototyped on `skipCIValidation`. Rejected as the
   primary design because it requires every affected cluster (or template) to
   carry escape-hatch metadata for a common ESO/Vault ordering problem, and the
   soft-fail approach was tested successfully without annotations.
2. **Spec fields** (e.g. `externallyProvisionedSecrets: true`): clearer in CRD
   but requires API and conversion churn for the same outcome as soft-fail.
3. **Global operator config** (accepted as a kill-switch, not the default):
   `strictSecretPresenceValidation` on `siteconfig-operator-configuration`
   restores today’s hard-fail. Soft-fail remains the default so ESO/Vault
   ordering works without per-cluster annotations. Per-cluster annotations
   stay rejected (alternative 1).
4. **Reorder GitOps only**: sometimes insufficient when secret operators lag
   `ClusterInstance` creation.

## Infrastructure Needed

None.

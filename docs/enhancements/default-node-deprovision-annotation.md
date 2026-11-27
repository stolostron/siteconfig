---
title: default-node-deprovision-annotation
authors:
  - "@sakhoury"
reviewers:
  - "@carbonin"
  - "@imiller0"
  - "@cwilkers"
approvers:
  - "@carbonin"
  - "@imiller0"
api-approvers:
  - "None"
creation-date: 2026-05-06
last-updated: 2026-05-06
status: provisional
tracking-link:
  - https://redhat.atlassian.net/browse/ACM-33648
see-also:
  - https://redhat.atlassian.net/browse/RFE-6417
replaces:
  - "None"
superseded-by:
  - "None"
---

# Default Node Deprovision Annotation on BareMetalHost

## Release Signoff Checklist

- [ ] Enhancement is `implementable`
- [ ] Design details are appropriately documented from clear requirements
- [ ] Test plan is defined
- [ ] Graduation criteria are defined
- [ ] User-facing documentation is updated

## Summary

Add the `bmac.agent-install.openshift.io/remove-agent-and-node-on-delete: "true"`
annotation to the default BareMetalHost templates for the Assisted Installer and
Hosted Control Plane installation flows. This annotation instructs the BMAC
controller in assisted-service to clean up the corresponding Agent and Node
resources on the spoke cluster when a BareMetalHost is deleted. Including it in
the default templates eliminates the manual annotation step from the documented
node scale-in procedure, reducing it from three git pushes to two.

## Motivation

The [documented procedure for scaling in worker nodes][scale-in-docs] requires
three separate git pushes (or `oc apply` steps):

1. Add the `bmac.agent-install.openshift.io/remove-agent-and-node-on-delete:
   "true"` annotation to the target node via `extraAnnotations.BareMetalHost` in
   the `ClusterInstance` CR. Wait for sync. Verify the annotation is applied on
   the BMH with `oc get bmh`.
2. Add `pruneManifests` entries to the node (BareMetalHost, InfraEnv,
   NMStateConfig) to trigger deletion. Wait for the BMH to deprovision. Verify
   BMH, Agent, and Node resources are removed.
3. Remove the worker node definition from `spec.nodes` to clean up the CR.

Step 1 exists solely because the annotation is not present by default. Including
it in the default templates eliminates this step entirely, reducing the
procedure from three git pushes to two.

[scale-in-docs]: https://docs.redhat.com/en/documentation/red_hat_advanced_cluster_management_for_kubernetes/2.16/html/multicluster_engine_operator_with_red_hat_advanced_cluster_management/siteconfig-intro#scale-in-worker-nodes

### User Stories

As a platform operator managing spoke clusters via ZTP and GitOps, I want
BareMetalHost resources to include the node deprovision annotation by default so
that I can scale in worker nodes without a separate git push to annotate each
BMH before pruning it.

**Day-2 operations considerations:**

- **Lifecycle**: The annotation causes the BMAC controller (in assisted-service)
  to add a finalizer (`bmac.agent-install.openshift.io/deprovision`) to the BMH.
  When the BMH is deleted, BMAC drains the spoke node, removes the Node and
  Machine resources from the spoke cluster, and waits for the host to deprovision
  before removing the finalizer. This interacts with the SiteConfig operator's
  own deletion flow via sync-wave ordering. See [Risks and
  Mitigations](#risks-and-mitigations) for the cluster-level deletion
  consideration.
- **Monitoring**: No new status conditions are introduced. The annotation is
  purely a pass-through to the BMAC controller. Existing `RenderedTemplatesApplied`
  conditions remain sufficient for debugging template rendering.
- **Remediation**: If the BMAC finalizer blocks BMH deletion (e.g., due to spoke
  API unavailability), administrators can manually remove the finalizer from the
  BMH. The SiteConfig operator's existing 30-minute deletion timeout and pause
  annotation pattern apply.
- **Scale**: No impact on `maxConcurrentReconciles`. The annotation is a static
  string in the template and does not affect rendering performance.

### Goals

- Include `bmac.agent-install.openshift.io/remove-agent-and-node-on-delete:
  "true"` in the default Assisted Installer and Hosted Control Plane
  BareMetalHost templates.
- Reduce the documented worker node scale-in procedure from three git pushes to
  two by eliminating the manual annotation step.

### Non-Goals

- Modifying the Image-Based Installer BareMetalHost template (it does not use
  BMAC annotations).
- Making the annotation value configurable via a new CRD field. Users who need
  different behaviour can use custom templates.

## Proposal

### Workflow Description

**Implementation**: Add a single annotation line to the BareMetalHost template
constant in two files, immediately after the existing
`bmac.agent-install.openshift.io/role` annotation:

- `internal/templates/assisted-installer/template.go`
- `internal/templates/hosted-cluster/template.go`

The annotation is unconditionally included (not gated by a template conditional)
since the RFE requests it as a default for all BMH resources.

**User workflow after this change** (compared to the [current documented
procedure][scale-in-docs]):

1. ~~Add the annotation via `extraAnnotations` and wait for sync.~~ (Eliminated
   — the annotation is already present from provisioning time.)
2. Add `pruneManifests` entries to the node (BareMetalHost, InfraEnv,
   NMStateConfig) and push. The BMAC controller detects the BMH deletion, drains
   the spoke node, removes the Agent and Node resources, and deprovisions the
   host.
3. After deprovisioning completes, remove the worker node definition from
   `spec.nodes` and push.

**Opting out**: Users who do not want this behaviour must use custom templates
(their own ConfigMap referenced via `TemplateRefs`) that omit the annotation.
`ExtraAnnotations` cannot be used to opt out: the SiteConfig merge logic skips
keys already defined in the template, and the BMAC controller only checks for
annotation presence — setting the value to `"false"` has no effect.

### API Extensions

None. No CRD, webhook, or API changes are required. This is a template-only
change.

### Siteconfig Impact

- **Controllers**: No controller changes required.
- **Templates**: The default BareMetalHost templates for Assisted Installer and
  Hosted Control Plane are modified. One annotation line is added to each. The
  Image-Based Installer template is not affected.
- **API fields**: No new fields.
- **Validation**: No validation changes.

### Implementation Details/Notes/Constraints

The annotation is added as a static string, consistent with how
`bmac.agent-install.openshift.io/hostname` and
`bmac.agent-install.openshift.io/role` are already included unconditionally in
the same templates.

The default templates are embedded as Go string constants and deployed as
immutable ConfigMaps (`ai-node-templates-v1`, `hcp-node-templates-v1`) at
controller startup. Because the ConfigMaps are immutable, the operator deletes
and recreates them on each deployment (see `cmd/main.go`,
`initConfigMapTemplates`). Updating the Go constants causes the new annotation
to appear in the recreated ConfigMaps automatically.

### Risks and Mitigations

**Risk: Cluster-level deletion may be delayed by BMAC finalizer.**

When the annotation is present, the BMAC controller adds a finalizer to the BMH.
During individual node removal, this works as designed — BMAC connects to the
spoke cluster API to clean up resources before removing the finalizer. However,
during full cluster deletion, the spoke API may become unreachable while the
BMAC finalizer is still pending.

The SiteConfig operator's existing 30-minute deletion timeout
(`DefaultDeletionTimeout`) provides a safety net — if the BMAC finalizer blocks
deletion beyond this threshold, the operator pauses reconciliation and surfaces
the issue for manual intervention.

**Risk: After upgrade, existing clusters gain new behaviour on next spec change.**

The operator upgrade itself does not re-render existing clusters. The controller
skips reconciliation when `ObservedGeneration` matches `Generation` and does not
watch template ConfigMaps for changes. As a result, existing BMHs remain
unchanged until the next spec modification triggers a reconciliation (e.g., a
node scale-in, configuration update, or any other change that bumps the
ClusterInstance generation). At that point, templates are re-rendered with the
new annotation and the BMHs are updated via server-side apply. This means the
annotation — and the BMAC finalizer it triggers — may appear on BMHs at a time
the user did not explicitly request it, potentially catching operators unaware
during unrelated spec changes.

*Mitigation*: The annotation only changes behaviour when a BMH is deleted; it
has no effect on running workloads. New clusters provisioned after the upgrade
always include the annotation from the start. For existing clusters, the gradual
rollout via spec changes is preferable to retroactively altering deletion
semantics for all clusters at once.

**Risk: No opt-out via ExtraAnnotations.**

Users cannot override this annotation through `ExtraAnnotations` for two
independent reasons:

1. The SiteConfig merge logic (`appendToManifestMetadata` in `helper.go`) skips
   keys that already exist in the rendered template. A user setting the same key
   to `"false"` via `ExtraAnnotations` would be silently discarded.
2. Even if the value could be overridden, it would not help. The BMAC controller
   in assisted-service checks for annotation **presence only** — the value is
   discarded (`_, has_annotation := bmh.GetAnnotations()[BMH_DELETE_ANNOTATION]`
   in `bmh_agent_controller.go`). Setting the annotation to `"false"` is
   functionally identical to `"true"`.

The only way to disable this behaviour is to **remove the annotation entirely**
from the BareMetalHost, which requires custom templates.

*Mitigation*: Users who need to opt out can provide their own BareMetalHost
template via a custom ConfigMap referenced in `TemplateRefs`. This is consistent
with how other template-level customisations are handled in the SiteConfig
operator.

### Drawbacks

- Adds an opinionated default that may not suit all deployment scenarios,
  particularly those where spoke cluster teardown ordering is sensitive to BMAC
  finalizer behaviour.
- Users who need to opt out must create and maintain custom templates rather
  than using a simpler mechanism like `ExtraAnnotations`.

## Design Details

### Test Plan

- **Unit tests**: Add test cases in
  `internal/controller/clusterinstance/template_engine_test.go` that render the
  actual default BareMetalHost template constants
  (`assistedinstaller.BareMetalHost` and `hostedcluster.BareMetalHost`) and
  verify the `bmac.agent-install.openshift.io/remove-agent-and-node-on-delete`
  annotation is present with value `"true"` in the rendered output.
- **No new e2e tests required**: The annotation is a static template change with
  no conditional logic. Integration tests in
  `internal/controller/clusterinstance_controller_test.go` track manifests by
  identity (APIVersion, Kind, Name, Namespace), not annotation content, so they
  do not require updates.

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] Implementation merged with adequate test coverage
- [ ] Documented in user-facing docs (labelled `doc-required` on ACM-33648)
- [ ] Released in ACM 5.0.0

### Upgrade / Downgrade Strategy

**Upgrade**: On upgrade, the operator deploys updated immutable ConfigMaps with
the new annotation. However, existing ClusterInstances are not immediately
re-rendered — the controller pre-empts reconciliation when `ObservedGeneration`
matches `Generation`. The annotation is applied to existing BMHs only when the
ClusterInstance spec is next modified (e.g., during a node scale-in or any other
spec change that bumps the generation). New clusters provisioned after the
upgrade always include the annotation. No manual steps are required.

**Downgrade**: On downgrade, the operator deploys the previous ConfigMaps without
the annotation. At the next reconciliation, BMH manifests are re-rendered
without the annotation and applied via server-side apply (with `ForceOwnership`),
which removes the annotation from the BMHs. The BMAC finalizer (if previously
added by assisted-service) remains on the BMH but is harmless on running
clusters — it is only evaluated when the BMH is deleted. If a BMH is
subsequently deleted, BMAC will not find the annotation and will skip the spoke
cleanup, removing its finalizer without blocking. No manual steps are required.

### Version Skew Strategy

The annotation is consumed by the BMAC controller in assisted-service, not by
the SiteConfig operator itself. Version skew between the SiteConfig operator and
assisted-service is not a concern because:

- If the SiteConfig operator is newer (has the annotation) but assisted-service
  is older (does not recognise it): the annotation is ignored. No harm done.
- If the SiteConfig operator is older (no annotation) but assisted-service is
  newer: existing behaviour is preserved. The annotation can be added manually
  via `ExtraAnnotations` (this works because the older template does not define
  the key, so the merge logic adds it).

## Implementation History

- 2026-05-06: Enhancement proposal created.

## Alternatives

**Add a new CRD field to control the annotation.**

Add a boolean field (e.g., `removeAgentAndNodeOnDelete`) to `NodeSpec`, default
it to `true`, and template it into the BMH annotation. This would allow users to
opt out per-node via the ClusterInstance spec rather than requiring custom
templates.

*Rejected because*: The added complexity (CRD change, API review, validation,
documentation) is disproportionate to the value. The annotation is a
pass-through to assisted-service with a clear default. Users who need different
behaviour are already expected to use custom templates for other annotation
overrides.


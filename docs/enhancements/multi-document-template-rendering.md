---
title: multi-document-template-rendering
authors:
  - "@alegacy"
reviewers:
  - "@sakhoury"
approvers:
  - "@sakhoury"
api-approvers:
  - "None"
creation-date: 2026-10-28
last-updated: 2026-11-02
status: provisional
tracking-link:
  - https://issues.redhat.com/browse/CNF-22905
see-also:
  - https://github.com/stolostron/siteconfig/pull/1076
  - https://github.com/stolostron/siteconfig/pull/1415
---

# Multi-Document YAML Template Rendering

## Release Signoff Checklist

- [ ] Enhancement is `implementable`
- [ ] Design details are appropriately documented from clear requirements
- [ ] Test plan is defined
- [ ] Graduation criteria are defined
- [ ] User-facing documentation is updated

## Summary

This enhancement allows one SiteConfig Go template to render zero, one, or
multiple Kubernetes resources as a YAML document stream. Empty, comment-only,
and null documents produce no resources. Every other document must be a valid
resource mapping; otherwise rendering fails. Each valid mapping becomes its own
RenderedObject with the existing metadata, suppression/pruning, sync-wave, and
lifecycle handling. No ClusterInstance (CI) API fields are introduced.

## Motivation

The current template engine unmarshals template output into one manifest map.
A template author who needs several resources must use separate ConfigMap keys,
even when the resources naturally arise from iterating a list. A single Go
`range` loop should be able to emit a resource per entry with YAML `---`
document separators, without losing later documents or bypassing normal
per-resource processing.

### User Stories

**As a template author**, I want a single ConfigMap template value to emit one
resource per list entry so that I can express repeated resources without
duplicating template keys.

**As a template author**, I want to wrap an entire template in a Go `if`
conditional so that it emits resources only when the condition is true and
produces no resources or rendering error when the condition is false.

**As a cluster administrator**, I want every emitted resource to receive
normal validation, ownership metadata, and sync-wave handling so that a stream
behaves like equivalent separate templates.

**As an administrator performing day-2 operations**, I want every generated
resource to be tracked for pruning, CI deletion, and reinstall so that grouping
resources in one template does not leave unmanaged objects or change cleanup
order.

**Day-2 operations considerations:**

- **Lifecycle**: Each object enters existing rendered-manifest status and the
  shared cleanup paths independently. Existing finalizers, reinstall exclusions,
  suppression/pruning rules, and descending cleanup waves remain. Document
  position does not establish application or deletion order.
  Resources omitted on a later render remain live and tracked for CI deletion
  or reinstall, but are no longer applied. Automatic cleanup of omitted
  resources is outside this change. Explicit pruning targets only still-emitted
  resources by API version and kind, not individual names.
  See [Resource Removal and Renaming](#resource-removal-and-renaming) for the
  rationale and administrator workflow.
- **Monitoring**: Use existing `RenderedTemplates`,
  `RenderedTemplatesValidated`, and `RenderedTemplatesApplied` conditions.
  No new status condition or metric is required.
- **Remediation**: Correct the template or input and use the existing retry
  flow. Parsing failures do not introduce a pause or require explicit unpause.
- **Scale**: No change to `maxConcurrentReconciles`. Rendering and downstream
  work grow with output bytes and resource count; list expansion can increase
  API traffic, rendered-manifest status size, and reconcile duration.

### Goals

1. Render each valid resource mapping as an independent resource and fail
   rendering on invalid documents. Empty ranges or conditionals produce no
   resources.
2. Preserve valid single-resource template behavior and per-resource
   metadata, validation, action selection, and lifecycle handling.
3. Fail the render phase if any document is malformed or lacks required
   Kubernetes resource fields, preventing partial application from that render.
4. Support both cluster-level and node-level templates across all installation
   flows without adding CI spec fields.
5. Preserve backward compatibility with existing valid single-resource
   templates, including SiteConfig-provided defaults and custom templates.

### Non-Goals

1. Flattening Kubernetes `List` objects, inventing per-document template
   references, or changing existing duplicate-resource behavior.
2. Guaranteeing apply order from document order or adding readiness gates.
3. Adding a new templating language, external template service, or configurable
   output quota in this change.
4. Automatically pruning resources omitted from later renders or adding
   name-based prune/suppress selectors.

## Proposal

### Workflow Description

1. A template author stores a Go template in a ConfigMap referenced by the CI's
   cluster-level or node-level `templateRefs`, as today.
2. The template emits Kubernetes objects separated by YAML document markers.
   It can emit a leading separator, several documents, or no resource at all.
3. The administrator submits the CI with the template reference. No new API
   fields are needed.
4. The engine executes the Go template once with the existing ClusterData.
   It decodes the complete output stream and validates each nonempty document
   with the existing required `apiVersion`, `kind`, and `metadata.name` checks.
5. Each manifest becomes an independent RenderedObject. The existing pipeline
   determines its render/prune/suppress action. Objects selected for rendering
   receive the applicable cluster/node extra labels, annotations, and SiteConfig
   ownership label; pruned and suppressed objects retain existing behavior.
6. After the full render succeeds, the controller processes pruning and
   suppression, then runs server-side dry-run validation on objects selected
   for rendering before applying them. A rendering error prevents these steps;
   a dry-run failure prevents application but does not undo earlier pruning.
7. Valid objects enter the existing sync-wave and kind ordering. Each is
   recorded and later cleaned up using its own resource identity. Related
   objects must use appropriate waves; writing one document first does not
   create a dependency guarantee.

#### Example Template

Multi-document rendering is a dependency for completing the
[HostNetworkAttachment (HNA) work](https://github.com/stolostron/siteconfig/pull/1415).
This example comes from that work and requires its separate
ClusterInstance API additions, including `spec.hostNetworkAttachmentTemplates`,
which are outside this proposal's scope. The template emits one resource per
entry, or none when the list is empty.

```gotemplate
{{ range .Spec.HostNetworkAttachmentTemplates }}
---
apiVersion: metal3.io/v1alpha1
kind: HostNetworkAttachment
metadata:
  name: "{{ .Name }}"
  namespace: "{{ $.Spec.ClusterName }}"
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
spec:
{{ .Spec | toYaml | indent 2 }}
{{ end }}
```

#### Empty Documents and Error Handling

Whitespace-only output, comment-only documents, and empty documents between
leading, consecutive, or trailing separators produce no RenderedObject. An
empty Go range therefore produces an empty result without a placeholder.
Null documents also represent no resource. A nonempty scalar, sequence, empty
mapping, or mapping without required resource fields is an error, rather than
an object to silently ignore.

Whole-template Go `if` conditionals are in scope. When a false condition leaves
only empty or whitespace-only output, rendering succeeds with no resources.
When the condition is true, emitted documents follow normal validation and
per-resource processing. Changing the condition from true to false does not
delete previously emitted resources; the lifecycle behavior described above
still applies.

Decode until EOF or the first error. A malformed or invalid document fails the
template and propagates a rendering error; callers must not apply an
already-decoded prefix. Include the template key and a one-based document index
in YAML decoding and resource-validation errors. Count each decoded document,
including empty, comment-only, and null documents that produce no resource.
Resource-validation errors identify the decoded document exactly. For decoding
errors, the index identifies the failing Decode call and is best effort:
`yaml.v3` can read ahead into the next document and report its syntax error
while decoding the preceding document. Preserve the original decoder diagnostic,
including any YAML line number. Retain per-resource API validation after parsing.

### Resource Removal and Renaming

This proposal preserves the existing retention behavior when a template stops
emitting a resource. Removing a list entry, removing a template reference, or
making a whole-template condition false does not delete previously emitted
resources. Resources no longer emitted by any referenced template remain live
and tracked in `status.manifestsRendered` for the existing CI deletion and
reinstall cleanup paths, but are no longer applied.  If earlier deletion is
required then manual cleanup by an admin is required.

For example, if a template previously emitted resources named `attachment-a`
and `attachment-b`, removing the second entry leaves `attachment-b` in place.
Renaming that entry to `attachment-c` causes the next successful application to
create or update `attachment-c` while retaining `attachment-b`. There is no
linkage between the old and new identities, and no automatic replacement or
dependency ordering between them.

Existing prune/suppress selectors cannot target individual resource names.
An omitted resource therefore requires explicit cleanup.

Automatic cleanup of omitted resources is deferred to a separate lifecycle
enhancement. Retention preserves existing behavior for single-resource templates
as well as document streams. 

### API Extensions

None. Template ConfigMap values remain strings.

### Siteconfig Impact

- **Controllers**: Update the template engine used by
  `ClusterInstanceReconciler` to collect every rendered document. Existing
  validation, application, status, and cleanup paths consume the resulting
  flat object list.
- **Templates**: Existing single-document AI, IBI, HCP, and custom templates
  remain valid. This proposal adds no default multi-document template.
- **API fields**: None.
- **Validation**: Run existing local manifest checks on each nonempty document;
  preserve full-set server-side validation before apply.
- **RBAC**: None. Emitting more objects does not grant permission for new kinds.

### Implementation Details/Notes/Constraints

- Change `render()` from returning one manifest map to returning a slice of
  manifest maps. Use the existing `gopkg.in/yaml.v3` library's `yaml.Decoder`
  for document boundaries, content on marker lines, and YAML diagnostics.
- Decode each document into a fresh map destination so earlier fields cannot
  carry over. Skip only nil decoded maps; empty mappings must fail validation.
  Preserve the whitespace-only check, including tabs, before decoding. Return
  immediately on any non-EOF decoding or resource-validation error.
- Change `renderManifestFromTemplate()` to return `[]RenderedObject` and run its
  existing action-selection and metadata logic per document. Update
  `renderTemplates()` to append the returned collection with slice expansion.
- Preserve document order within the helper result. Downstream sync-wave/kind
  sorting and ConfigMap key iteration provide no guarantee that application or
  status order matches document order.
- Do not append nil/zero RenderedObjects for empty output. Callers must discard
  any partial result returned alongside an error.
- Each emitted resource must have an appropriate distinct identity. This
  proposal does not redefine collision handling across templates or documents.
- Keep parsing in memory like the current executed-template buffer. No aggregate
  rendered-output limit is introduced.

### Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| A later document is malformed or silently dropped | Decode until EOF, validate every document, and fail the render before any actual apply. |
| Empty documents become invalid placeholder objects | Explicitly skip absent/null documents and test leading, trailing, consecutive, comment-only, and empty-range cases. |
| Multi-document processing bypasses metadata or action rules | Reuse existing per-manifest processing for each object, including cluster/node annotations and prune/suppress selection. |
| Incorrect string splitting loses YAML content or boundaries | Use the YAML decoder to handle marker-line content and syntax errors; test quoted and multiline scalar content. |
| A template expands to many resources | Accepted risk; aggregate limits are deferred. User-facing guidance must cover expansion costs and CI status growth. Kubernetes object limits do not bound total stream size or resource count. |
| Users infer dependency order from document order | Document that sync waves and existing sorting govern application and cleanup. |
| Removing or renaming a list entry leaves an old resource live | Document retention and explicit cleanup by resource identity; automatic pruning of omitted resources is deferred. |
| Existing malformed trailing output starts failing | Require valid streams and document the stricter behavior instead of silently accepting only the first resource. |

### Drawbacks

- Changes internal helper signatures and requires updating their callers and
  tests throughout the template engine.
- One template can emit many resources, increasing reconciliation and debugging
  cost even though the ConfigMap has only one key.
- Existing templates that accidentally emitted additional documents may begin
  creating those resources or failing validation instead of ignoring them.
  Administrators must review custom template output before upgrade.
- Comment-only, null, and separator-only output that previously failed
  required-field validation now produces no resources. An accidentally empty
  template result may therefore succeed silently.

## Design Details

### Test Plan

**Planned unit and fake-client controller tests:**

- Existing single-document YAML and JSON, with unchanged metadata and fields.
- Two or more valid documents, a range loop with zero/one/many entries, and
  both cluster-level and node-level data contexts.
- Whole-template `if` conditionals with true and false conditions; false
  conditions producing empty or whitespace-only output succeed with no objects.
- Empty, whitespace-only (including tabs), and comment-only output; null and
  separator-only documents; leading/trailing/consecutive separators; and mixtures
  with real resources.
- Malformed first, middle, and final documents; valid scalars/sequences that
  are not resource maps; empty mappings; and missing required resource fields.
  Assert that render errors identify the template key, resource-validation
  errors identify the exact document index including skipped empty documents,
  and decoding errors include a best-effort index and the original decoder
  diagnostic. Include a syntax error at the start of a later document that
  triggers decoder read-ahead. Assert that callers do not process partial results.
  Later mappings must not inherit required fields from earlier documents.
- Quoted and multiline scalar values containing `---`, nested lists/maps,
  and ordinary YAML comments.
- Per-object extra labels/annotations, ownership, sync waves, suppression,
  pruning, and no empty RenderedObject placeholders.
- Duplicate resource identities within one template retain existing processing
  behavior; do not introduce a new rejection rule or rely on which copy wins.
- At controller level, a malformed later document fails `RenderedTemplates`
  before pruning or applying any resources from that reconciliation.
- Track each resource from a valid stream independently in status and verify
  CI deletion and reinstall use the existing cleanup rules.

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] Stream decoding and collection-based rendering merged with adequate tests
- [ ] Single-document compatibility and failure-before-apply behavior verified
- [ ] Per-resource tracking, metadata, pruning, and cleanup verified
- [ ] User-facing template docs updated with examples and ordering/size caveats

### Upgrade / Downgrade Strategy

**Upgrade**: No CRD or stored-state migration is needed. Existing valid
single-document templates continue to work. Review custom templates for
previously ignored trailing documents: the new controller processes the whole
stream and may render more objects or report errors for invalid output.
Comment-only, null, and separator-only output now succeeds without producing
resources; leading empty documents no longer hide subsequent resources.
Do not enable multi-document templates until the capable controller is active.
Searching custom template ConfigMaps for `---` can help identify affected
templates, but does not replace reviewing their rendered output. Template
ConfigMap edits alone do not trigger rendering. For a CI whose current
generation is already observed, the new parsing takes effect on its next
eligible spec change, potentially well after the upgrade. After provisioning
completes and while no reinstall is in progress, adding, for example,
`example.com/template-revision: "2"` under `spec.extraLabels.ManagedCluster`
is one way to trigger rendering; changing only CI metadata does not advance
its spec generation.

**Downgrade**: Before using an older controller, replace each multi-document
ConfigMap value with equivalent single-document template keys in the same
referenced ConfigMap, preserving resource identities, metadata, and sync waves.
The webhook rejects cluster-level and node-level `templateRefs` changes after
provisioning. Trigger an eligible CI spec change to re-render, such as adding
the `extraLabels` entry described above, then verify that the same objects are
rendered and tracked. The older engine reads only the first document and can
silently ignore even malformed trailing output. Omitting a resource from
template output does not automatically prune it.

### Version Skew Strategy

There is no Kubernetes API or CRD version dependency specific to this change.
Every emitted kind still requires its own served API and existing SiteConfig
permissions. Avoid enabling multi-document templates while an older active
SiteConfig reconciler can process the same CIs. During rollback, first convert
streams to single-document templates as described above. Single-document
ConfigMaps can be used by either controller version.

## Implementation History

- 2026-10-28: Independent multi-document rendering proposal drafted.

## Alternatives

### One Resource per ConfigMap Key

This remains supported and is adequate for fixed resources, but cannot naturally
produce a variable number of resources from one collection. Stream rendering
adds that capability without replacing the current template-reference model.

### Kubernetes List Output

A `List` wrapper would require explicit flattening and per-item metadata and
action handling. It also requires a different template shape. Standard YAML
streams preserve each resource's normal manifest form and reuse common tooling.

### Split Output on Separator Strings

A naive substring split can corrupt scalar values. A line-anchored splitter
avoids that issue but must still handle content on document-marker lines and
YAML syntax errors. The existing decoder provides these semantics and
diagnostics without a separate parsing implementation.

## Infrastructure Needed

None beyond the existing unit and fake-client controller test infrastructure.

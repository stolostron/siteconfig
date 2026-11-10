# Rendering multiple resources from a template

A ConfigMap value referenced by cluster-level or node-level `templateRefs` can
contain a Go template that emits zero, one, or many Kubernetes resources. Separate
resources with YAML document markers (`---`). Each resource must have its own
identity and the required `apiVersion`, `kind`, and `metadata.name` fields.

For example, this cluster-level template emits a ConfigMap for each node:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: node-settings-templates
  namespace: siteconfig-operator
data:
  NodeSettings: |
    {{ range .Spec.Nodes }}
    ---
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: "{{ $.Spec.ClusterName }}-{{ .HostName }}-settings"
      namespace: "{{ $.Spec.ClusterName }}"
      annotations:
        siteconfig.open-cluster-management.io/sync-wave: "1"
    data:
      role: "{{ .Role }}"
    {{ end }}
```

Reference this ConfigMap from `spec.templateRefs`. For node-level templates, use
the node's `templateRefs` and `.SpecialVars.CurrentNode` to access the current
node. A range over a nil or empty list emits no resources. Whole-template `if`
conditions are also supported: when false, they can emit nothing or whitespace.

Empty, comment-only, and null documents are ignored, including documents between
leading, consecutive, or trailing separators. Empty mappings (`{}`), nonempty
scalars, sequences, missing resource fields, and malformed YAML fail rendering.
Kubernetes `List` resources are not expanded into individual resources.

An invalid document fails `RenderedTemplates` before pruning, dry-run validation,
or application starts. Error messages include the template key and a one-based
document index, counting skipped documents. For YAML syntax errors the index is
best effort because the decoder can read ahead. Decoder diagnostics are omitted
to prevent rendered values, including secrets, from appearing in logs or status.
Inspect the template output securely to locate the problem. Fix the input or
template and retry; rendering errors do not require unpausing the ClusterInstance.

Each emitted resource receives normal ownership labels, applicable cluster/node
extra labels and annotations, validation, status tracking, and lifecycle handling.
All resources selected for rendering undergo server-side dry-run validation
before application. A dry-run failure prevents application but does not undo
pruning that already ran. Existing valid single-resource YAML and JSON templates
continue to work.

## Ordering, selectors, and resource retention

Use sync-wave annotations for ordering. Resources are applied in ascending waves
and cleaned up in descending waves, with existing kind sorting within a wave.
Document position and ConfigMap key order do not guarantee dependency or readiness
ordering. Avoid duplicate identities: they retain existing processing behavior,
with a single status entry, and there is no new duplicate-rejection rule.

`pruneManifests` selects every emitted resource matching its API version and kind.
`suppressedManifests` selects every emitted resource of a matching kind. Neither
selector targets a resource name; several resources of one kind are acted on as
a group. Node selectors supplement cluster selectors, and pruning takes precedence
over suppression. Suppression does not delete an existing resource.

Removing a list entry, renaming a resource, removing a template reference where
permitted, or making a whole-template condition false does not delete the old
resource. It remains live and tracked in `status.manifestsRendered` for CI deletion
or reinstall, but is no longer applied. Reinstall retains its existing exclusions,
including the ManagedCluster. An administrator must manually delete an unwanted
omitted resource by its identity if earlier cleanup is needed. Automatic pruning
of omitted resources and selectors for individual resource names are deferred.

## Capacity and version changes

There is no aggregate resource-count or rendered-byte limit. Expansion increases
memory use, API traffic, reconciliation time, and the size of
`status.manifestsRendered`. Kubernetes per-object limits do not bound the total
stream; keep output sizes manageable.

Before upgrading, review custom template output for previously ignored trailing
documents. Searching ConfigMap values for `---` helps screen for affected
templates but does not replace reviewing their rendered output. Later documents
will now be processed or cause an error. Comment-only, null, and separator-only
output that previously failed validation now succeeds without creating resources.
Enable multi-document templates only after the capable controller is active.

Template ConfigMap edits alone do not trigger rendering. Once a CI's generation
is observed, rendering waits for an eligible spec change. After provisioning and
while no reinstall is in progress, adding an entry under
`spec.extraLabels.ManagedCluster`, for example
`example.com/template-revision: "2"`, can trigger rendering. Changing only CI
metadata does not advance the spec generation.

Before downgrading, split each stream into equivalent single-document keys in the
same referenced ConfigMap, preserving identities, metadata, and waves. The webhook
rejects `templateRefs` changes after provisioning. Trigger an eligible spec change
and verify the same resources are rendered and tracked. Older controllers can
silently ignore trailing documents, even when they are malformed.

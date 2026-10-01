---
title: custom-node-network-templates
authors:
  - "@jmontesi"
reviewers:
  - "@sakhoury"
  - "@imiller0"
  - "@sabbir-47"
  - "@carbonin"
approvers:
  - "@sakhoury"
  - "@imiller0"
  - "@sabbir-47"
  - "@carbonin"
api-approvers:
  - "@sakhoury"
  - "@imiller0"
  - "@sabbir-47"
  - "@carbonin"
creation-date: 2026-09-28
last-updated: 2026-10-06
status: provisional
tracking-link:
  - https://redhat.atlassian.net/browse/CNF-27390
see-also: []
replaces: []
superseded-by: []
---

# Custom Node Network Templates for NMStateConfig

## Release Signoff Checklist

- [ ] Enhancement is `implementable`
- [ ] Design details are appropriately documented from clear requirements
- [ ] Test plan is defined
- [ ] Graduation criteria are defined
- [ ] User-facing documentation is updated

## Summary

Enable custom node-level installation templates to reference arbitrary
per-node keys under `spec.nodes[].templateData` in the ClusterInstance CR.
The motivating use case is shared NMStateConfig templates with different
per-node IP addresses, gateways, and interface names. The parameter map belongs
to `NodeSpec`, so any node-level installation template can consume it, including
templates for non-network resources. The existing `nodeNetwork` field retains
its upstream `NMStateConfigSpec` type, `interfaces` MAC mappings, and raw nmstate
`config`; no replacement network type or upstream API change is required.

## Motivation

In large-scale deployments, nodes share a common network topology (same
interface layout, same routing structure) but differ in specific values
(IP addresses, MAC addresses, gateway addresses). The current model forces
users into several unsatisfying choices:

- **Full inline config per node.** Each node carries a complete nmstate YAML
  blob in `nodeNetwork.config`, duplicating the entire network structure
  across hundreds or thousands of nodes. At scale, this makes the
  ClusterInstance CR unwieldy and error-prone, and changes to the common
  structure must be applied to every node individually.
- **Per-node template ConfigMaps.** A user can supply a separate node-template
  ConfigMap containing the complete NMStateConfig for each node and reference
  it only from that node. This works with today's template references, but
  duplicates the network structure and increases the number of ConfigMaps to
  maintain. Exactly one selected template must emit that node's NMStateConfig;
  adding another ConfigMap does not override the default entry.
- **Other workarounds.** Users manage NMStateConfig resources outside the
  template system (e.g., separate hub-side resource creation) or apply
  network changes after installation via policies. Note that extra-manifests
  are not applicable here: they supply installer manifests via
  AgentClusterInstall, not hub-side NMStateConfig CRs for InfraEnv discovery
  networking. These workarounds add operational complexity and duplication
  overhead, and increase provisioning time.

There is no dedicated field for arbitrary per-node template parameters.
`nodeNetwork` is a strongly-typed Go struct (`NMStateConfigSpec`) that only
preserves `interfaces` and `config`, but its upstream schema need not be
expanded: a sibling `templateData` field on the node provides the appropriate
scope for parameters used by network and other node-level templates.

As a rough measure: for 100 nodes sharing a common network topology, inline
`config` adds ~20 lines of nmstate YAML per node (~2,000 lines total). With
a shared template and per-node parameters in `templateData`, each node adds
~5 lines of parameters (~500 lines plus one shared template ConfigMap).

### User Stories

**As a platform administrator**, I want to define a single NMStateConfig
template that is shared across all nodes and pass per-node network parameters
(IPs, gateways, MACs) through the ClusterInstance CR so that I do not have
to duplicate the entire nmstate config for each node.

**As a platform administrator**, I want existing ClusterInstance CRs that use
the default `nodeNetwork` schema to continue working without modification so
that this feature does not break my current deployments.

**As a template author**, I want the same per-node parameters to be available
to any template referenced by that node, including when `nodeNetwork` is
omitted, so that non-network templates do not need a separate parameter API.

**Day-2 operations considerations:**

- **Lifecycle**: Admission, reconciliation, and the effect on installed nodes
  are separate concerns:

  1. **Updating template data while held:** While `holdInstallation: true`
     permits spec edits, changing `spec.nodes[].templateData` is stored and
     increments the ClusterInstance generation. The controller re-renders the
     selected templates using the updated template data. If rendering and
     validation succeed, it applies the output, including changes to NMStateConfig.
     A final template-data edit may be included in the update that releases hold.
  2. **Updating template data after hold release:** The webhook must reject
     changes to an existing node's `templateData`, including after provisioning
     completes and in a new reinstall request. The rejected template data is
     not stored, generation does not change, and that request causes no
     reconciliation or NMStateConfig update. Adding a new node with its initial
     template data remains subject to existing scaling rules.
  3. **Successful and later eligible reconciliations:** The controller records
     observed generation after success and skips rendering for that generation.
     Later accepted spec changes, including scaling or a valid reinstall
     request, can trigger rendering with the stored `templateData` and templates.
     Reinstall therefore uses the existing nodes' unchanged template data.
  4. **Template ConfigMap updates:** This reconciler does not watch template
     ConfigMaps. Updating a template ConfigMap alone does not trigger
     re-rendering. An existing failure retry or later eligible
     reconciliation can pick up the new contents.

  Updating template data and rendering a hub-side NMStateConfig do not
  reconfigure an already-installed node. NMStateConfig supplies installation
  and discovery networking; this feature adds no day-2 network-update mechanism.
  For reproducible provisioning, versioned immutable template ConfigMaps are
  recommended. Changes to `templateRefs` on an existing ClusterInstance are
  subject to existing update restrictions.
- **Monitoring**: No new status conditions are required. Schema-invalid
  `nodeNetwork` data (e.g., missing required fields, malformed MAC addresses)
  is rejected at CRD admission before the controller processes it. The
  existing `ClusterInstanceValidated` condition covers controller-level
  validation (template refs, JSON fields, agent count). Template rendering
  failures are reported through the `RenderedTemplates` condition; dry-run
  validation failures through `RenderedTemplatesValidated`; and application
  failures through `RenderedTemplatesApplied`.
- **Remediation**: If a custom template references a missing key via the
  `templateVar` helper, the helper returns an error that identifies the
  missing key. The template engine aborts, the controller sets the
  `RenderedTemplates` condition to `False` with `reason: Failed`, and requeues.
  The specific missing key and its template/node context belong in `message`,
  not in the machine-readable reason. If `holdInstallation` is enabled
  (pre-provisioning), users can correct
  `templateData` values or the template and reconciliation retries
  automatically. After installation is released, `templateData` is immutable;
  template ConfigMap corrections are picked up on the next eligible reconciliation.
  For templates with optional parameters, the `hasTemplateVar` helper allows
  presence checks without triggering errors.
- **Scale**: Moving repeated configuration into a shared template reduces the
  serialized size of ClusterInstance objects when parameters are smaller than
  the inline configuration they replace. This reduces storage and request/watch
  payloads per object and helps avoid object-size pressure at high node counts.
  `maxConcurrentReconciles`, watches, and the number of rendered resources are
  unchanged.

### Goals

- Users can define arbitrary keys under
  `spec.nodes[].templateData` in the ClusterInstance CR.
- Custom node-level templates (via `templateRefs`) can reference these keys
  to render NMStateConfig or other node-level resources.
- The change is fully backward-compatible: existing ClusterInstance CRs using
  only `interfaces` and `config` continue to work unchanged.
- Default installation templates (Assisted Installer, Image Based Installer)
  continue to produce identical output.
- The feature is GA-ready with no feature gates or tech-preview flags.

### Non-Goals

- Modifying the upstream `NMStateConfigSpec` type in the `assisted-service`
  repository.
- Changing how the Assisted Installer consumes `NMStateConfig` CRs or how
  the Image Based Installer consumes `NetworkSecret` resources.
- Adding new default templates that use custom keys. Users provide their own
  custom templates.
- Validating the semantic correctness of custom key values. The operator
  preserves and passes them through; the template is responsible for using
  them correctly.
- Adding cluster-level template parameters, template ConfigMap watches, or
  new day-2 mutation permissions.
- Adding per-entry replacement or merge semantics for template ConfigMaps.
  That is a separate enhancement and is not a prerequisite for this feature.

## Proposal

This proposal presents two approaches. We recommend **Approach B**. Approach
A is presented first because it demonstrates the capability using the
existing API and explains the limitations that motivate a dedicated
`templateData` field. Additional implementation options (B2, B3, B4) were
considered and rejected; they are documented in the
[Alternatives](#alternatives) section.

### Approach A: Use the existing `config` field with additional template functions

The `nodeNetwork.config` field already carries the
`+kubebuilder:validation:XPreserveUnknownFields` marker and is backed by a
raw byte type (`NetConfig`). This means arbitrary keys placed inside `config`
are accepted by the API server and preserved through deserialization.

A user can place per-node template parameters inside `config` alongside (or
instead of) the standard nmstate YAML, and write a custom template that
extracts them using `get(fromJson(toJson ...))` chains.

**Prerequisite for the example below:** The operator currently uses
`slim-sprig` v2, which provides `toJson` but lacks `fromJson` and `get`.
The example needs `fromJson` to decode the serialized `NetConfig` into a map;
Go's builtin `index` can replace `get`. This affects template access, not the
API's ability to store arbitrary keys in `config`. Providing these functions
requires either:

- Upgrading to `slim-sprig/v3`, which adds `fromJson` and `get` but also
  **removes** several functions present in v2 (`merge`, `mergeOverwrite`,
  `snakecase`, `camelcase`, `kebabcase`, `semverCompare`, `abbrev`,
  `nospace`, `initials`, and others). This would break any existing custom
  template using those functions. A safe upgrade requires a full compatibility
  analysis and is treated as separate work outside this EP.
- Alternatively, registering custom `fromJson` and `get` equivalents in
  `funcMap()` while retaining the v2 base. This preserves backward
  compatibility; registering only `fromJson` and using builtin `index` is
  sufficient. Neither option is required for Approach B.

This approach has been tested using `slim-sprig/v3` and confirmed working.
The rendered NMStateConfig CR passed server-side dry-run validation (admission
compatibility). Note that dry-run does not test InfraEnv selector matching or
actual provisioning.

**ClusterInstance (per node):**

```yaml
nodeNetwork:
  config:
    eno1: "02:00:00:00:00:01"
    eno2: "02:00:00:00:00:02"
    gwIp: "192.0.2.1"
    nodeIp:
      - ip: "192.0.2.10"
        prefix-length: 24
```

**Custom NMStateConfig template (in a user-provided ConfigMap):**

```yaml
{{ if .SpecialVars.CurrentNode.NodeNetwork }}
apiVersion: agent-install.openshift.io/v1beta1
kind: NMStateConfig
metadata:
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
{{ if .SpecialVars.CurrentNode.HostRef }}
  name: "{{ .SpecialVars.CurrentNode.HostRef.Name }}"
  namespace: "{{ .SpecialVars.CurrentNode.HostRef.Namespace }}"
{{ else }}
  name: "{{ .SpecialVars.CurrentNode.HostName }}"
  namespace: "{{ .Spec.ClusterName }}"
{{ end }}
  labels:
{{ if .SpecialVars.CurrentNode.HostRef }}
    nmstate-label: "{{ .SpecialVars.CurrentNode.HostRef.Name }}"
{{ else }}
    nmstate-label: "{{ .SpecialVars.CurrentNode.HostName }}"
{{ end }}
spec:
  config:
    interfaces:
    - name: eno1
      type: ethernet
      state: up
      ipv4:
        enabled: true
        # get(fromJson(toJson ...)) converts NetConfig (a Go struct) to a map so individual keys can be accessed
        address: {{- get (fromJson (toJson .SpecialVars.CurrentNode.NodeNetwork.NetConfig)) "nodeIp" | toYaml | nindent 8 }}
        dhcp: false
    - name: eno2
      type: ethernet
      state: up
      ipv4:
        enabled: false
        dhcp: false
      ipv6:
        enabled: false
        dhcp: false
    dns-resolver:
      config:
        server:
        - 192.0.2.53
    routes:
      config:
        - destination: '0.0.0.0/0'
          next-hop-address: '{{ get (fromJson (toJson .SpecialVars.CurrentNode.NodeNetwork.NetConfig)) "gwIp" }}'
          next-hop-interface: eno1
          metric: 123
  interfaces:
    - name: "eno1"
      macAddress: '{{ get (fromJson (toJson .SpecialVars.CurrentNode.NodeNetwork.NetConfig)) "eno1" }}'
    - name: "eno2"
      macAddress: '{{ get (fromJson (toJson .SpecialVars.CurrentNode.NodeNetwork.NetConfig)) "eno2" }}'
{{ end }}
```

**Advantages:**

- No API changes required.
- Fully backward-compatible for existing CRs.
- Tested and validated: the rendered NMStateConfig CR passes server-side
  dry-run validation.

**Drawbacks:**

- Requires `fromJson` (not present in the current `slim-sprig` v2), with a
  compatibility risk if upgrading to v3 (see prerequisite above).

- Semantically misleading: `config` is documented as "nmstate YAML", but is
  being used to carry template parameters that are not nmstate data.
- Template syntax is verbose: accessing custom keys requires
  `get (fromJson (toJson ...))` chains because `NetConfig` is a custom Go
  type, not a plain map. A template variable assignment can avoid repeating
  the roundtrip (`$net := fromJson (toJson .NetConfig)`), but the initial
  conversion is still required and the pattern is non-obvious.
- Validation tools or linters that inspect `config` for valid nmstate content
  would flag these custom keys as invalid.
- Not self-documenting: a reader of the ClusterInstance CR cannot distinguish
  template parameters from actual nmstate configuration without also reading
  the custom template.

### Approach B: Native support for node-level template parameters

Add `spec.nodes[].templateData` directly to `NodeSpec`, alongside
`nodeNetwork` and `templateRefs`. Any template selected for that node can
consume the parameters through `.SpecialVars.CurrentNode`; `nodeNetwork`
does not need to be present for non-network templates.

This solves both API-server preservation and typed Go deserialization by
declaring an explicit parameter field. Putting it under `nodeNetwork` would
unnecessarily couple a general template facility to the network API and
require replacing the upstream network type. The node-level placement keeps
`NodeSpec.NodeNetwork` as `*aiv1beta1.NMStateConfigSpec` and avoids that Go API
change. This remains Approach B's explicit-field design, revised to node scope.

#### Proposed type and API

Add the following field to the existing `NodeSpec`. The network field shown
for context keeps its existing type and validation markers:

```go
type NodeSpec struct {
    // Other existing node fields are omitted from this excerpt.
    // NodeNetwork retains the existing upstream type and schema.
    // +optional
    NodeNetwork *aiv1beta1.NMStateConfigSpec `json:"nodeNetwork,omitempty"`

    // TemplateData holds arbitrary user-defined key-value pairs
    // accessible in any custom node-level installation template.
    // +kubebuilder:validation:Schemaless
    // +kubebuilder:validation:Type=object
    // +kubebuilder:validation:XPreserveUnknownFields
    // +optional
    TemplateData map[string]apiextensionsv1.JSON `json:"templateData,omitempty"`
}
```

The `Schemaless` and explicit object-type markers prevent generation of a
non-nullable `additionalProperties` schema, which would prune parameter entries
set to null. Local API-server tests (Kubernetes 1.35.0, envtest) with the generated
CRD verified create/update/read preservation at `spec.nodes[].templateData`,
including explicit null and nested values, with and without `nodeNetwork`.
Strings, numbers, booleans, and arrays were rejected in place of the map;
existing network validation was unchanged.

The relevant generated CRD fragment would be:

```yaml
nodes:
  type: array
  items:
    type: object
    properties:
      # Other existing node properties are omitted from this excerpt.
      nodeNetwork:
        type: object
        properties:
          config:
            type: object
            x-kubernetes-preserve-unknown-fields: true
          interfaces:
            type: array
            minItems: 1
            items:
              type: object
              properties:
                macAddress:
                  pattern: ^([0-9A-Fa-f]{2}[:]){5}([0-9A-Fa-f]{2})$
                  type: string
                name:
                  type: string
              required: [macAddress, name]
      templateData:
        type: object
        x-kubernetes-preserve-unknown-fields: true
```

The existing `nodeNetwork.config` and `nodeNetwork.interfaces` constraints
are preserved. The new sibling `templateData` is an optional object whose
values accept arbitrary JSON. Arbitrary unknown fields at the node or
`nodeNetwork` root are not an additional extension mechanism.

Template access is provided through dedicated helper functions registered in
`funcMap()`, which decode the raw JSON values into template-usable Go types:

```go
// templateVar retrieves and decodes a parameter from NodeSpec.TemplateData.
// Returns an error if the parameter map is undefined, the key is missing,
// or the JSON is invalid.
// Uses json.Decoder.UseNumber to preserve numeric precision.
func templateVar(node v1alpha1.NodeSpec, key string) (interface{}, error) {
    if node.TemplateData == nil {
        return nil, fmt.Errorf("templateData is not defined")
    }
    val, ok := node.TemplateData[key]
    if !ok {
        return nil, fmt.Errorf("templateData key %q is missing", key)
    }
    if len(val.Raw) == 0 {
        return nil, nil
    }
    if !json.Valid(val.Raw) {
        return nil, fmt.Errorf("templateData key %q contains invalid JSON", key)
    }
    dec := json.NewDecoder(bytes.NewReader(val.Raw))
    dec.UseNumber()
    var result interface{}
    if err := dec.Decode(&result); err != nil {
        return nil, fmt.Errorf("failed to decode templateData key %q: %w", key, err)
    }
    return result, nil
}

// hasTemplateVar checks whether a key exists in NodeSpec.TemplateData.
// Returns false for nil templateData, without error.
// Distinguishes absent keys from present keys with null/false/0 values.
func hasTemplateVar(node v1alpha1.NodeSpec, key string) bool {
    _, ok := node.TemplateData[key]
    return ok
}
```

The helpers accept a `NodeSpec` value because `SpecialVars.CurrentNode` is
already a value, not a pointer. They only read its map and JSON bytes. No nil
`nodeNetwork` check is needed; parameters are independent of networking.

**Behavior:**

- Strings, booleans, arrays, and objects are decoded into their corresponding
  Go types (`string`, `bool`, `[]interface{}`, `map[string]interface{}`).
- Numbers are preserved as `json.Number` (renders as the original string
  representation, avoiding `float64` precision issues).
  Emit numeric values with `toJson` or direct interpolation. For explicit integer
  conversion with the retained slim-sprig v2, use `toString | int`; direct `int`
  conversion of `json.Number` silently yields zero. Destination numeric limits
  still apply, so exact identifiers should be stored as strings.
- An undefined parameter map returns `templateData is not defined`. A defined
  map lacking a requested key returns, for example,
  `templateData key "nodeIp" is missing`. These are distinct diagnostics.
  Both abort template execution. The rendering pipeline must wrap the error
  with the node hostname, template ConfigMap namespace/name, and template key
  before the controller records it in the condition's `message`. The reason
  remains `Failed`.
- An explicitly supplied JSON `null` is a present value (`hasTemplateVar`
  returns `true`) and `templateVar` returns `nil, nil`. Note: the vendored
  `apiextensionsv1.JSON` stores an empty `Raw` for null values, so the helper
  checks for empty `Raw` after confirming the key exists. Null handling must
  be tested through actual JSON unmarshalling into `NodeSpec`, not by
  constructing `Raw: []byte("null")` directly.
- For optional parameters, use `hasTemplateVar` before `templateVar`:

```gotemplate
{{ if hasTemplateVar .SpecialVars.CurrentNode "optionalDns" }}
  dns: {{ templateVar .SpecialVars.CurrentNode "optionalDns" | toJson }}
{{ end }}
```

Here, `toJson` emits a value that is valid inline YAML, preserving its JSON type.

**Required parameter access:**

```gotemplate
{{ templateVar .SpecialVars.CurrentNode "gwIp" }}
```

No changes to `buildClusterData()` are needed: the existing implementation
copies the typed `NodeSpec` into `SpecialVars.CurrentNode`, which already
provides the node's fields and will carry its added `TemplateData` map.

An illustrative failure condition (with template-engine line detail omitted)
is:

```yaml
type: RenderedTemplates
status: "False"
reason: Failed
message: >-
  Failed to render templates, err= node "node1.example.com", template ConfigMap
  "siteconfig-operator/custom-node-templates", key "NMStateConfig":
  templateData key "nodeIp" is missing
```

Parse errors, execution errors, and invalid rendered YAML must retain this
same source context. Missing-key errors must also include the requested key;
undefined-map errors must identify the absent map. Include key names and
locations without deliberately dumping parameter values.

**Complete example:**

The default node-template ConfigMap (`ai-node-templates-v1`) contains
templates for `InfraEnv`, `BareMetalHost`, and `NMStateConfig`. The template
engine renders every key from every referenced ConfigMap and appends all
results; it does not merge or override keys across ConfigMaps. If two
templates both emit the same NMStateConfig, their outputs are duplicated or
conflicting; a later reference does not replace an earlier template.

For the example below, the user creates a **single** ConfigMap
that contains the default `InfraEnv` and `BareMetalHost` templates alongside
a custom `NMStateConfig` template, and references **only** that ConfigMap in
`templateRefs`. Supporting per-entry overrides of the default maps requires
a separate enhancement.

To build the custom bundle, copy the default ConfigMap data into a new
ConfigMap and replace its `NMStateConfig` entry. Use the operator namespace
for the actual installation; `siteconfig-operator` is illustrative:

```bash
# Export the default node-template bundle
oc get configmap ai-node-templates-v1 -n siteconfig-operator -o yaml > custom-node-templates.yaml
# Edit: retain apiVersion, kind and data, and construct fresh metadata with
# name: custom-node-templates and the intended namespace. Remove server-assigned
# metadata, owner references and operator-managed labels/annotations.
# Replace the NMStateConfig data entry with the template below and apply:
oc apply -f custom-node-templates.yaml
```

1. **Custom NMStateConfig template** (the entry that replaces `NMStateConfig`
   in the copied bundle; `InfraEnv` and `BareMetalHost` entries remain
   unchanged from the default):

   ```yaml
   NMStateConfig: |-
     {{ if .SpecialVars.CurrentNode.NodeNetwork }}
     apiVersion: agent-install.openshift.io/v1beta1
     kind: NMStateConfig
     metadata:
       annotations:
         siteconfig.open-cluster-management.io/sync-wave: "1"
     {{ if .SpecialVars.CurrentNode.HostRef }}
       name: "{{ .SpecialVars.CurrentNode.HostRef.Name }}"
       namespace: "{{ .SpecialVars.CurrentNode.HostRef.Namespace }}"
     {{ else }}
       name: "{{ .SpecialVars.CurrentNode.HostName }}"
       namespace: "{{ .Spec.ClusterName }}"
     {{ end }}
       labels:
     {{ if .SpecialVars.CurrentNode.HostRef }}
         nmstate-label: "{{ .SpecialVars.CurrentNode.HostRef.Name }}"
     {{ else }}
         nmstate-label: "{{ .SpecialVars.CurrentNode.HostName }}"
     {{ end }}
     spec:
       config:
         interfaces:
         - name: eno1
           type: ethernet
           state: up
           ipv4:
             enabled: true
             address:
             - ip: {{ templateVar .SpecialVars.CurrentNode "nodeIp" | quote }}
               prefix-length: {{ templateVar .SpecialVars.CurrentNode "prefixLength" | toJson }}
             dhcp: false
         dns-resolver:
           config:
             server:
             - {{ templateVar .SpecialVars.CurrentNode "dnsServer" | quote }}
         routes:
           config:
             - destination: '0.0.0.0/0'
               next-hop-address: {{ templateVar .SpecialVars.CurrentNode "gwIp" | quote }}
               next-hop-interface: eno1
       interfaces:
     {{ .SpecialVars.CurrentNode.NodeNetwork.Interfaces | toYaml | indent 4 }}
     {{ end }}
   ```

   Note: MAC addresses remain in `nodeNetwork.interfaces` (the typed field),
   not in `templateData`. The template accesses them via
   `.NodeNetwork.Interfaces`, same as the default template.

2. **ClusterInstance node spec** (with `templateRefs` and `templateData`):

   ```yaml
   nodes:
     - hostName: node1.example.com
       templateRefs:
         - name: custom-node-templates
           namespace: siteconfig-operator
       nodeNetwork:
         interfaces:
           - name: eno1
             macAddress: "02:00:00:00:00:01"
           - name: eno2
             macAddress: "02:00:00:00:00:02"
       templateData:
         nodeIp: "192.0.2.10"
         prefixLength: 24
         gwIp: "192.0.2.1"
         dnsServer: "192.0.2.53"
   ```

3. **Expected rendered NMStateConfig:**

   ```yaml
   apiVersion: agent-install.openshift.io/v1beta1
   kind: NMStateConfig
   metadata:
     annotations:
       siteconfig.open-cluster-management.io/sync-wave: "1"
     name: node1.example.com
     namespace: my-cluster
     labels:
       nmstate-label: node1.example.com
   spec:
     config:
       interfaces:
       - name: eno1
         type: ethernet
         state: up
         ipv4:
           enabled: true
           address:
           - ip: 192.0.2.10
             prefix-length: 24
           dhcp: false
       dns-resolver:
         config:
           server:
           - 192.0.2.53
       routes:
         config:
           - destination: '0.0.0.0/0'
             next-hop-address: '192.0.2.1'
             next-hop-interface: eno1
     interfaces:
       - name: eno1
         macAddress: 02:00:00:00:00:01
       - name: eno2
         macAddress: 02:00:00:00:00:02
   ```

### Workflow Description

1. User adds per-node template parameters under
   `spec.nodes[].templateData` in the ClusterInstance CR. For networking,
   MAC addresses remain in `nodeNetwork.interfaces`.
2. The CRD schema validates the known fields (`interfaces`, `config`) as
   today. `templateData` values are accepted as arbitrary JSON.
3. During reconciliation, the template engine renders the custom template.
   The `templateVar` helper decodes values from `TemplateData` on access.
4. Default templates continue to access only `.NodeNetwork.NetConfig` and
   `.NodeNetwork.Interfaces`, producing identical output.

### Recommendation

We recommend **Approach B** with the explicit `templateData` field on `NodeSpec`.
This supports all node-level templates, separates parameters from installer
configuration, avoids changing the upstream network type, and preserves existing
JSON serialization, CRD validation, and generated `DeepCopy` support. The node's
deep-copy code must still be regenerated to copy the new map and its raw bytes.
Template access through the `templateVar` and `hasTemplateVar`
helpers provides JSON-decoded values with explicit error handling for missing
keys, without requiring any changes to the existing `slim-sprig` dependency.

A broader `slim-sprig` migration (v2 to v3) is separate work. The v3 module
removes several functions present in v2, so a safe migration requires a full
compatibility analysis of existing custom templates across the user base.

### API Extensions

The recommended API change is an optional field on each node.

- **Approach A**: No API changes. Requires `fromJson` (not present in the
  current `slim-sprig` v2); `get` can be replaced by Go's builtin `index`.
- **Approach B**: Adds `TemplateData map[string]apiextensionsv1.JSON` to
  `NodeSpec`, serialized as `spec.nodes[].templateData`. `NodeSpec.NodeNetwork`
  remains `*aiv1beta1.NMStateConfigSpec`. Existing network fields and typed Go
  assignments retain their behavior. No `nodeNetwork.templateData` alias or
  cluster-level parameter map is introduced.

Approach B maintains full backward compatibility for existing CRs that
only use `interfaces` and `config`.

### Siteconfig Impact

- **Controllers**: No changes to `buildClusterData()` are needed: the existing
  implementation copies `NodeSpec` into `SpecialVars.CurrentNode`, carrying the
  added `TemplateData` field. Register `templateVar` and `hasTemplateVar` in
  `funcMap()` in `helper.go`. Extend template-engine error wrapping so condition
  messages identify the node, ConfigMap namespace/name, template key, and error.
  Existing condition types, reason values, retries, and generation handling remain.
- **Templates**: No changes to default templates. The upstream network
  type retains its `Interfaces` and `NetConfig` fields, so the
  default NMStateConfig template continues to access `.NodeNetwork.NetConfig`
  and `.NodeNetwork.Interfaces` and produce identical output. The Image Based
  Installer `NetworkSecret` template also uses `.NodeNetwork.NetConfig` and
  is equally unaffected. Users provide their own custom templates via
  `templateRefs` to use custom keys.
- **API fields**: Add only the optional node-level `templateData` field in
  Approach B, with corresponding generated CRD and `NodeSpec.DeepCopyInto`
  updates. No production RBAC permissions or new resources are required.
- **Validation**: CRD schema validation for `interfaces` (MinItems, MAC regex)
  is preserved since the typed fields and their kubebuilder annotations
  remain. CRD-level constraints should be tested through an API server
  (envtest), not through direct webhook unit tests.

### Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Custom template references a key not present in `templateData` | The `templateVar` helper returns an error identifying the missing key. The template engine aborts and the controller sets `RenderedTemplates=False` with a diagnostic message. For optional keys, `hasTemplateVar` provides a safe presence check. |
| Generic parameters are used to bypass existing mutation restrictions | Enforce immutability for the entire `templateData` map after hold release. Parameter names matching mutable field names must not inherit those fields' permissions. |
| Multiple nodes or ConfigMaps have the same template key | Include node hostname, ConfigMap namespace/name, and template key in user-visible condition messages; key-specific failures also identify the parameter name. |
| Users confuse template parameters with nmstate config (Approach A) | Mitigated by documentation clearly separating template parameters from nmstate content. |

### Drawbacks

- **Approach A** requires additional template functions not present in the
  current operator and establishes a pattern (overloading `config`) that may
  confuse users and complicate future API evolution.
- **Approach B** adds a general parameter API whose values have no predefined
  schema or automatic relationship to installer fields. Template authors must
  document their parameter contracts. Existing-node parameters remain immutable
  after hold release, even if some future consumer would prefer mutable values.
- Custom templates with arbitrary keys are harder to validate statically.
  Template errors surface only at reconciliation time.

## Design Details

### Design Decision

Proceed with Approach B and place `templateData` directly on `NodeSpec`, as
recommended in review. Approach A remains first in the narrative to explain
the existing-field experiment and why an explicit parameter API is preferred.
This recommendation remains subject to the repository's maintainer approval;
the EP stays provisional until that process is complete.

### Implementation Steps (Approach B)

1. Add `TemplateData` to `NodeSpec` in
   `api/v1alpha1/clusterinstance_types.go`, retaining the upstream network type.
2. Regenerate `NodeSpec` deep-copy methods and source CRDs with
   `make generate manifests`; regenerate the shipped bundle CRD as well.
3. Register `templateVar` and `hasTemplateVar` in `funcMap()` in `helper.go`,
   accepting the existing `CurrentNode` value and retaining slim-sprig v2.
4. Wrap template parsing, execution, and rendered-YAML errors with node and
   ConfigMap/template context before they reach the controller condition message.
5. Verify admission rejects changes under `/nodes/*/templateData` outside the
   existing hold correction window. Match permitted fields by path, not by
   substring, so parameter names such as `extraLabels` cannot bypass this rule.
   Preserve the exact reinstall MAC path
   `/nodes/*/nodeNetwork/interfaces/*/macAddress`; do not allow all interface
   fields or arbitrary parameters to change on a reinstall request.
6. Update user documentation, samples, and the tests below for the node-level
   path and contextual errors.

### Mutability and Reinstall

The node-level `templateData` field does not gain special mutation permissions.
Once hold has been released, an existing node's parameters are immutable. This
is consistent with existing treatment of inline network configuration and
prevents general template inputs from bypassing restrictions on other fields.

**Design constraint:** Network MAC mappings belong in the typed
`nodeNetwork.interfaces` field, not in `templateData`. `ReinstallPermissions`
allows changes specifically at `/nodes/*/nodeNetwork/interfaces/*/macAddress`
when a valid new reinstall is requested, not across the entire interface object.
Custom templates should access MACs via `.NodeNetwork.Interfaces`, and use
`templateData` for values such as IP addresses and gateways that follow
the existing immutability expectations.

**Correction window:** While `holdInstallation` is set to `true` and
provisioning has not completed, all spec changes are permitted. This is the
window for correcting errors in `templateData` values before provisioning
proceeds. The permission check uses the old object's hold value, so the update
that releases hold may contain a final parameter correction. Later updates are
restricted: during active provisioning all spec changes are blocked, and after
completion only permitted field changes are allowed. Unknown provisioning state
with hold already released also blocks spec changes.

**Reinstall:** A valid new reinstall request may change the permitted typed
MAC paths while retaining `templateData`. Templates are rendered from that
accepted spec. Once reinstall is in progress, further parameter and MAC changes
are blocked. Moving the parameter map to node scope does not alter these rules.

**Scaling:** A newly added node may carry its own initial `templateData` when
the existing scaling rules allow the addition. Existing nodes' parameters
remain protected in that same update.

### Test Plan

**Approach A (additional template functions + documentation):**

- If using `slim-sprig` v3: verify that existing default and custom template
  rendering is not broken by removed functions (full compatibility audit).
- Add an e2e or integration test that provisions a ClusterInstance using a
  custom NMStateConfig template with per-node parameters stored in the
  `config` field, validating that the rendered NMStateConfig CR contains the
  expected values.

**Approach B:**

Unit tests:

- `api/v1alpha1/clusterinstance_types_test.go`:
  - Existing nodes with only `nodeNetwork.interfaces` and `config` deserialize
    identically with the unchanged upstream type.
  - `NodeSpec.TemplateData` preserves arbitrary JSON values through
    marshal/unmarshal, including when `NodeNetwork` is nil.
  - Generated `NodeSpec.DeepCopyInto` independently copies the parameter map
    and each JSON value's raw bytes; mutating a copy does not alter the source.
- `api/v1alpha1/clusterinstance_webhook_test.go`:
  - Validation passes for existing nodes and nodes with `templateData` alongside
    the known fields. Parameters do not require `nodeNetwork` to be present.
- `internal/controller/clusterinstance/helper_test.go`:
  - `templateVar` decodes string, boolean, numeric (`json.Number`), array,
    object, and null values correctly. Null must be tested via actual JSON
    unmarshalling into `NodeSpec` (the vendored `apiextensionsv1.JSON`
    stores empty `Raw` for null, not `[]byte("null")`).
  - `templateVar` distinguishes an undefined map from a defined map missing a
    key. Assert the exact absent-map diagnostic and the missing key's name.
  - Invalid JSON and trailing JSON values return key-specific errors.
  - `hasTemplateVar` returns `true` for present keys (including null/false/0
    values) and `false` for absent keys.
  - Custom keys are accessible via the template engine through `templateVar`.
- `internal/controller/clusterinstance/template_engine_test.go`:
  - Default NMStateConfig template renders identically with old and new nodes
    when `templateData` is absent (backward compatibility).
  - Default NMStateConfig template renders identically when `templateData`
    is present alongside ordinary inline `config` (no data leak from
    `templateData` into default rendered resources).
  - Default IBI `NetworkSecret` template renders identically with old and
    new nodes (IBI regression); hosted-cluster network defaults also retain
    their output.
  - Custom template referencing custom keys renders expected output.
  - A non-network node template consumes the same parameter map with
    `nodeNetwork` omitted. Two nodes with different parameters remain isolated.
  - Parameter errors, template parse/execution errors, and rendered-YAML
    errors identify node hostname, ConfigMap namespace/name, and template key.
    Include duplicate template keys in distinct ConfigMaps to verify that the
    diagnostic uniquely identifies the source.
- `internal/controller/clusterinstance_controller_test.go`:
  - Failures surface through the actual `RenderedTemplates=False` condition
    with `reason: Failed` and a source-specific `message`. For a missing key,
    assert the key name; for an absent map, assert that distinct diagnostic.
  - Failed rendering does not advance observed generation or partially apply
    resources. A permitted correction recovers through the existing retry path.
- `api/v1alpha1/clusterinstance_validation_test.go`:
  - The exact reinstall MAC path remains allowed for a valid new request.
  - `templateData` changes are rejected on provisioned clusters
    (immutability).
  - Parameter keys matching permitted field names, such as `extraLabels`,
    cannot bypass immutability after provisioning or in a new reinstall request.
  - `templateData` changes are accepted while `holdInstallation` is enabled
    (correction window).
  - `templateData` changes are rejected after releasing `holdInstallation`
    but before provisioning completes (active provisioning blocks all spec
    changes).
  - A final parameter change in the update releasing an existing hold is accepted.
  - A valid new reinstall request accepts permitted typed MAC changes and
    rejects parameter changes; an active reinstall rejects both.
  - Scale-out permits initial parameters for a new node and rejects changes
    to existing-node parameters.

Integration/e2e tests:

- ClusterInstance with default `nodeNetwork` (only `interfaces` + `config`)
  provisions successfully with default templates (regression).
- ClusterInstance with node-level `templateData` and a custom NMStateConfig
  template provisions successfully and the rendered NMStateConfig CR contains
  the expected values.
- CRD schema constraints (`minItems`, MAC regex, rejection of invalid MAC
  addresses) remain unchanged. An API-server test verifies arbitrary JSON and
  explicit null persist at `spec.nodes[].templateData`, including without
  `nodeNetwork`, and rejects non-object parameter maps. Unknown siblings must
  be rejected or pruned rather than preserved as another parameter namespace.
- Rendered InfraEnv `nmStateConfigLabelSelector` matches the NMStateConfig
  label produced by the custom template (interface-selector integration).
- Verify live API condition messages for missing parameters and broken templates
  contain the node, ConfigMap namespace/name, template key, and specific cause.

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] Implementation merged with adequate test coverage, including:
  - API-server persistence of `templateData` (envtest)
  - `templateVar`/`hasTemplateVar` decoding and error-path coverage
  - Node-scoped parameters usable by non-network templates without `nodeNetwork`
  - Source-specific error details in user-visible condition messages
  - `DeepCopy` independence for `templateData` values
  - AI and IBI default template output regression (with and without
    `templateData` present)
  - `templateData` immutability post-provisioning and `holdInstallation`
    correction window
- [ ] Documented in user-facing operator documentation with examples of custom
  NMStateConfig templates using per-node parameters
- [ ] Released

### Upgrade / Downgrade Strategy

**Approach A**: No upgrade or downgrade considerations for the CRD. The
`config` field already accepts arbitrary content. If `slim-sprig` v3 is used,
a compatibility audit of existing custom templates is required before upgrade.

**Approach B**:

**Upgrade:** The CRD adds the optional `spec.nodes[].templateData` field and
the operator's `NodeSpec` gains its corresponding Go field. The existing
`nodeNetwork` type and schema are unchanged, so existing CRs require no manual
migration. Install both the updated CRD and operator before using the new field
or helpers.

**Downgrade:** In-place downgrade of provisioned ClusterInstances that use
`templateData` is unsupported under this design. The webhook blocks spec
changes outside the permitted paths on provisioned clusters, so removing
`templateData` and replacing custom templates with inline `config` blobs
would be rejected.

For ClusterInstances that still have `holdInstallation: true` and whose
provisioning has not completed, rollback is possible: materialize inline
network configuration in `nodeNetwork.config`, switch `templateRefs` to
the default template bundle, and remove `templateData` while spec edits
are still permitted. This procedure covers the network example. Any other
custom node templates using parameters must also be migrated to equivalent
inputs/templates compatible with the older operator before downgrading; there
is no automatic general conversion for arbitrary consumers.

Existing CRs that only use `interfaces` and `config` are unaffected by
downgrade.

### Version Skew Strategy

The SiteConfig operator is a standalone component. Version skew with the hub
cluster or managed clusters is not a concern for this change. The relevant
skew scenarios are between the CRD version, the operator version, and
template ConfigMaps:

- **CRD newer than operator:** The operator ignores unknown fields in
  `NodeSpec` during typed deserialization. Custom keys are stored in etcd
  but not used by the older operator. Default templates continue to work.
- **Operator newer than CRD:** The operator supports custom keys but the CRD
  schema does not include them. API requests can reject or prune the unknown
  field depending on field-validation settings. Users cannot use the feature
  until the CRD is updated; an operator upgrade does not restore pruned data.

**Upgrade ordering:** Install the updated CRD and operator before creating
or applying template ConfigMaps or ClusterInstances that reference
`templateData` or the `templateVar`/`hasTemplateVar` helpers. A custom
template that calls `templateVar` on an older operator (without the helper
registered in `funcMap()`) will fail at render time with a function-not-found
error, setting `RenderedTemplates=False`. Similarly, an Approach A template
that calls `fromJson` or `get` on an operator without those functions will
fail in the same way. A missing-function error is recoverable by upgrading
the operator and retrying if the required inputs were stored. Inputs pruned by
an old CRD must be reapplied from source while admission permits the changes.

## Implementation History

- 2025-05-20: PoC template demonstrating custom keys inside `config` posted
  on CNF-27592.
- 2026-09-28: Initial enhancement proposal created.
- 2026-09-29: Approach A validated with `slim-sprig` v3: custom NMStateConfig
  template rendered successfully and passed server-side dry-run validation
  (admission compatibility). Identified that v3 removes several v2 functions,
  requiring compatibility analysis for a safe upgrade.
- 2026-10-05: Revised Approach B in response to PR #1360 review: place
  `templateData` on `NodeSpec`, retain the upstream network type, require
  source-specific condition messages, clarify lifecycle and scale behavior,
  and document the existing per-node template ConfigMap workaround.
- 2026-10-06: Validated the relocated field's generated CRD in an isolated local
  prototype and verified the EP's `toJson` examples by rendering and parsing YAML.

## Alternatives

### Alternative: Approach A (config field workaround)

See [Approach A](#approach-a-use-the-existing-config-field-with-additional-template-functions)
above. Approach A achieves the same functional goal without an API change,
but at the cost of verbose template syntax, semantic misuse of the `config`
field, and a dependency on template functions not present in the current
operator. If Approach B is approved, Approach A remains documented as context
for how the feature was validated.

### Alternative: Option B2 - Custom unmarshaling with root-level keys

Define a `NodeNetworkSpec` with custom `UnmarshalJSON` that captures unknown
keys into an `Extra map[string]interface{}` field (`json:"-"`), making custom
keys accessible at the root level of the template data
(`{{ .NodeNetwork.gwIp }}`).

**Not pursued because:** High implementation complexity: custom
marshal/unmarshal, manual `DeepCopy`, and potential issues with
`controller-gen` CRD schema generation when combined with custom
`UnmarshalJSON`. The interplay between `+kubebuilder:validation:XPreserveUnknownFields`
on the outer struct and the schemas of declared child properties (e.g.,
`config`) is difficult to verify and may produce unexpected pruning behavior.
Root-level access via custom unmarshaling alone does
not create Go struct fields; a separate template-facing representation would
be required. Custom keys at the root level also risk collision with future
upstream field additions. Finally, `map[string]interface{}` deserializes
numbers as `float64`, causing potential formatting issues (e.g.,
`prefix-length: 24` becomes `float64(24)`). Note that Approach A's
`fromJson` has the same `float64` behavior; Approach B's `templateVar` avoids it
by using `json.Decoder.UseNumber`.

### Alternative: Option B3 - Replace `NodeNetwork` with raw JSON

Replace the typed field with a fully unstructured type
(`*apiextensionsv1.JSON`). The CRD would mark `nodeNetwork` with
`x-kubernetes-preserve-unknown-fields: true` and the Go side would store
everything as raw bytes.

**Not pursued because:** This is a breaking change. The default NMStateConfig
templates access `.NodeNetwork.NetConfig` and `.NodeNetwork.Interfaces` as
typed fields; replacing the type with raw JSON would break them. All existing
code accessing `NodeNetwork` fields would need rewriting to work with
unstructured data. The current CRD schema validation for `interfaces`
(MinItems, MAC regex) would not carry over automatically and would need to
be reconstructed, either through explicit schema definitions or webhook
logic.

### Alternative: Option B4 - Unstructured wrapper with typed accessors

Use a fully unstructured backing store (`map[string]interface{}`) with typed
accessor methods (`GetInterfaces()`, `GetNetConfig()`) for the known fields.

**Not pursued because:** This shares the same breaking-change problems as B3.
Default templates would break because field access syntax changes. The
current CRD schema validation would not carry over automatically. Additionally, runtime type assertion errors become
possible when the accessor methods extract typed data from the unstructured
map, introducing a class of bugs that does not exist with typed fields.

### Alternative: Modify upstream `NMStateConfigSpec`

Add `XPreserveUnknownFields` to the `NMStateConfigSpec` struct in the
`assisted-service` repository.

**Not pursued because:** Even if the upstream type were annotated with
`XPreserveUnknownFields`, this only affects the CRD schema (API server
acceptance). The Go struct would still have only `Interfaces` and `NetConfig`
fields, so Go's JSON unmarshaling would still discard unknown keys. Solving
the Go-side preservation would require the same custom unmarshaling or extra
field approach proposed here, making the upstream change insufficient on its
own. Additionally, the upstream type is owned by a different team and serves
a different purpose (defining the NMStateConfig CRD schema for the Assisted
Installer). Modifying it there would affect all consumers, not just SiteConfig.

### Alternative: Put `templateData` under `nodeNetwork`

The earlier draft introduced a SiteConfig `NodeNetworkSpec` containing
`interfaces`, `config`, and `templateData`. It preserved existing network JSON
but changed the exported Go type and located general template parameters under
a network-specific field.

**Not pursued because:** Adding the parameter map directly to `NodeSpec`
supports the same network use case and other node templates, including nodes
without `nodeNetwork`. It also retains the upstream type, avoiding a duplicate
network schema to maintain and a Go type migration for existing consumers.

### Alternative: A separate NMStateConfig template ConfigMap for each node

The existing workaround uses a separate ConfigMap containing each node's
complete NMStateConfig template, referenced only by that node's `templateRefs`.
The selected templates must emit exactly one NMStateConfig for that node.

**Not preferred:** It duplicates network structure and creates a ConfigMap per
node to maintain. Approach B provides one reusable template and per-node values,
reducing that duplication and keeping node inputs in the ClusterInstance.

### Alternative: Use extra-manifests instead of templates

Supply per-node NMStateConfig YAML through installer extra-manifest ConfigMaps,
or create the hub-side NMStateConfig resources separately from SiteConfig.

**Not pursued because:** Extra-manifest references supply installer manifests
(via AgentClusterInstall); they do not create the hub-side NMStateConfig CRs
selected by InfraEnv for discovery networking. They therefore do not solve
this provisioning requirement. Separately managing those hub-side resources
outside the template system is possible but retains the per-node duplication
this proposal seeks to reduce.

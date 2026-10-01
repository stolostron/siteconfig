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
last-updated: 2026-09-30
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
per-node keys under `spec.nodes[].nodeNetwork.templateData` in the
ClusterInstance CR.
Today, `nodeNetwork` is typed as `NMStateConfigSpec` from the
`assisted-service` API, which only exposes two fields: `interfaces` (MAC
address mappings) and `config` (raw nmstate YAML). Users who need to
templatize NMStateConfig rendering with per-node parameters (different IPs,
gateways, interface names) have no clean way to pass those parameters through
the ClusterInstance API. This proposal introduces support for user-defined
keys under `nodeNetwork.templateData` that custom node-level templates can
reference, enabling flexible NMStateConfig rendering without being locked to
the fixed upstream schema.

## Motivation

In large-scale deployments, nodes share a common network topology (same
interface layout, same routing structure) but differ in specific values
(IP addresses, MAC addresses, gateway addresses). The current model forces
users into one of two unsatisfying choices:

- **Full inline config per node.** Each node carries a complete nmstate YAML
  blob in `nodeNetwork.config`, duplicating the entire network structure
  across hundreds or thousands of nodes. At scale, this makes the
  ClusterInstance CR unwieldy and error-prone, and changes to the common
  structure must be applied to every node individually.
- **Per-node workarounds.** Users manage NMStateConfig resources outside the
  template system (e.g., separate hub-side resource creation) or apply
  network changes after installation via policies. Note that extra-manifests
  are not applicable here: they supply installer manifests via
  AgentClusterInstall, not hub-side NMStateConfig CRs for InfraEnv discovery
  networking. These workarounds add operational complexity and duplication
  overhead, and increase provisioning time.

The root cause is that `nodeNetwork` is a strongly-typed Go struct
(`NMStateConfigSpec`) that only preserves `interfaces` and `config`. There
is no dedicated field for arbitrary per-node template parameters. Custom
templates that render NMStateConfig have no semantically appropriate way to
receive node-specific values through the ClusterInstance API.

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

**Day-2 operations considerations:**

- **Lifecycle**: The reconciliation contract for `nodeNetwork` data and
  templates has three distinct behaviors:

  1. **Successful reconciliation:** The controller records the observed
     generation and stops further rendering for that generation. Template
     data and templates are not re-read until a new reconciliation is
     triggered.
  2. **Later eligible reconciliations:** Spec changes that bump the object
     generation (including scaling and cluster reinstall) trigger a new
     reconciliation that re-reads `nodeNetwork` data and templates.
  3. **Template ConfigMap updates:** This reconciler does not watch template
     ConfigMaps. Updating a template ConfigMap alone does not trigger
     re-rendering. An existing failure retry or later eligible
     reconciliation can pick up the new contents.

  This feature adds no template watches or day-2 network-management behavior.
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
  `RenderedTemplates` condition to `False`, and requeues. If
  `holdInstallation` is enabled (pre-provisioning), users can correct
  `templateData` values or the template and reconciliation retries
  automatically. After installation is released, `templateData` is immutable; template
  ConfigMap corrections are picked up on the next eligible reconciliation.
  For templates with optional parameters, the `hasTemplateVar` helper allows
  presence checks without triggering errors.
- **Scale**: No impact on `maxConcurrentReconciles` or API server load.
  The change affects only how data is deserialized and passed to the template
  engine within a single reconcile loop.

### Goals

- Users can define arbitrary keys under
  `spec.nodes[].nodeNetwork.templateData` in the ClusterInstance CR.
- Custom node-level templates (via `templateRefs`) can reference these keys
  to render flexible NMStateConfig manifests.
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

**Prerequisite:** The operator currently uses `slim-sprig` v2, which provides
`toJson` but lacks `fromJson`. The `fromJson` function is needed to convert
the `NetConfig` Go struct into a map so individual keys can be looked up
(Go's builtin `index` can then replace `get`). Providing `fromJson` requires
either:

- Upgrading to `slim-sprig/v3`, which adds `fromJson` and `get` but also
  **removes** several functions present in v2 (`merge`, `mergeOverwrite`,
  `snakecase`, `camelcase`, `kebabcase`, `semverCompare`, `abbrev`,
  `nospace`, `initials`, and others). This would break any existing custom
  template using those functions. A safe upgrade requires a full compatibility
  analysis and is treated as separate work outside this EP.
- Alternatively, registering custom `fromJson` and `get` equivalents in
  `funcMap()` while retaining the v2 base. This preserves backward
  compatibility but adds implementation scope to what is otherwise a
  documentation-only approach.

This approach has been tested using `slim-sprig/v3` and confirmed working.
The rendered NMStateConfig CR passed server-side dry-run validation (admission
compatibility). Note that dry-run does not test InfraEnv selector matching or
actual provisioning.

**ClusterInstance (per node):**

```yaml
nodeNetwork:
  config:
    eno1: "B4:96:91:A3:EF:60"
    eno2: "B4:96:91:A3:EF:61"
    gwIp: "10.8.34.254"
    nodeIp:
      - ip: "10.8.34.42"
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
        - 10.11.5.19
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
  compatibility risk if upgrading to v3 (see Prerequisite above).

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

### Approach B: Native support for arbitrary keys under `nodeNetwork`

Modify the Go type backing `nodeNetwork` so that user-defined template
parameters under `nodeNetwork.templateData` are preserved through
deserialization and made available to the template engine.

The core technical challenge: the current Go type (`*aiv1beta1.NMStateConfigSpec`)
only has `Interfaces` and `NetConfig` fields. Even if the CRD is marked with
`x-kubernetes-preserve-unknown-fields: true`, Go's JSON unmarshaling discards
keys that do not match a struct field. Approach B solves both the CRD
acceptance and the Go preservation problems by defining a new
`NodeNetworkSpec` type with a dedicated `templateData` field.

#### Proposed type and API

Create a new type that preserves the known fields and adds an explicit field
for custom data:

```go
type NodeNetworkSpec struct {
    // Interfaces is an array of interface objects containing the name and
    // MAC address for interfaces referenced in the raw nmstate config YAML.
    // +kubebuilder:validation:MinItems=1
    // +optional
    Interfaces []*aiv1beta1.Interface `json:"interfaces,omitempty"`

    // NetConfig is the nmstate YAML configuration for this node.
    // +kubebuilder:validation:XPreserveUnknownFields
    // +optional
    NetConfig aiv1beta1.NetConfig `json:"config,omitempty"`

    // TemplateData holds arbitrary user-defined key-value pairs
    // accessible in custom node-level templates for flexible
    // NMStateConfig rendering.
    // +kubebuilder:validation:Schemaless
    // +kubebuilder:validation:Type=object
    // +kubebuilder:validation:XPreserveUnknownFields
    // +optional
    TemplateData map[string]apiextensionsv1.JSON `json:"templateData,omitempty"`
}
```

The `Schemaless` and explicit object-type markers prevent generation of a
non-nullable `additionalProperties` schema, which would prune parameter entries
set to null. Local API-server tests verify preservation of null and rejection of non-object parameter maps.

The generated CRD schema for `nodeNetwork` would be:

```yaml
nodeNetwork:
  properties:
    config:
      type: object
      x-kubernetes-preserve-unknown-fields: true
    interfaces:
      items:
        properties:
          macAddress:
            pattern: ^([0-9A-Fa-f]{2}[:]){5}([0-9A-Fa-f]{2})$
            type: string
          name:
            type: string
        required: [macAddress, name]
        type: object
      minItems: 1
      type: array
    templateData:
      type: object
      x-kubernetes-preserve-unknown-fields: true
  type: object
```

The existing `config` and `interfaces` constraints are preserved. The new
`templateData` is an optional object whose values accept arbitrary JSON.

Template access is provided through dedicated helper functions registered in
`funcMap()`, which decode the raw JSON values into template-usable Go types:

```go
// templateVar retrieves and decodes a template parameter from NodeNetwork.TemplateData.
// Returns an error if the key is missing, the nodeNetwork is nil, or the JSON is invalid.
// Uses json.Decoder.UseNumber to preserve numeric precision.
func templateVar(nn *NodeNetworkSpec, key string) (interface{}, error) {
    if nn == nil {
        return nil, fmt.Errorf("nodeNetwork is nil")
    }
    if nn.TemplateData == nil {
        return nil, fmt.Errorf("templateData is nil, key %q not found", key)
    }
    val, ok := nn.TemplateData[key]
    if !ok {
        return nil, fmt.Errorf("key %q not found in templateData", key)
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

// hasTemplateVar checks whether a key exists in NodeNetwork.TemplateData.
// Returns false for nil nodeNetwork or nil templateData, without error.
// Distinguishes absent keys from present keys with null/false/0 values.
func hasTemplateVar(nn *NodeNetworkSpec, key string) bool {
    if nn == nil || nn.TemplateData == nil {
        return false
    }
    _, ok := nn.TemplateData[key]
    return ok
}
```

**Behavior:**

- Strings, booleans, arrays, and objects are decoded into their corresponding
  Go types (`string`, `bool`, `[]interface{}`, `map[string]interface{}`).
- Numbers are preserved as `json.Number` (renders as the original string
  representation, avoiding `float64` precision issues).
  Emit numeric values with `toJson` or direct interpolation. For explicit integer
  conversion with the retained slim-sprig v2, use `toString | int`; direct `int`
  conversion of `json.Number` silently yields zero. Destination numeric limits
  still apply, so exact identifiers should be stored as strings.
- A missing key returns an error that aborts template execution with a
  diagnostic identifying the key. The condition message includes the template
  name and error detail; node identity is available in the controller logs.
- An explicitly supplied JSON `null` is a present value (`hasTemplateVar`
  returns `true`) and `templateVar` returns `nil, nil`. Note: the vendored
  `apiextensionsv1.JSON` stores an empty `Raw` for null values, so the helper
  checks for empty `Raw` after confirming the key exists. Null handling must
  be tested through actual JSON unmarshalling into `NodeNetworkSpec`, not by
  constructing `Raw: []byte("null")` directly.
- For optional parameters, use `hasTemplateVar` before `templateVar`:

```
{{ if hasTemplateVar .SpecialVars.CurrentNode.NodeNetwork "optionalDns" }}
  dns: {{ templateVar .SpecialVars.CurrentNode.NodeNetwork "optionalDns" }}
{{ end }}
```

**Required parameter access:**

```
{{ templateVar .SpecialVars.CurrentNode.NodeNetwork "gwIp" }}
```

No changes to `buildClusterData()` are needed: the existing implementation
copies the typed `NodeSpec` into `SpecialVars.CurrentNode`, which already
includes `NodeNetwork` and its `TemplateData` field.

**Complete example:**

The default node-template ConfigMap (`ai-node-templates-v1`) contains
templates for `InfraEnv`, `BareMetalHost`, and `NMStateConfig`. The template
engine renders every key from every referenced ConfigMap and appends all
results; it does not merge or override keys across ConfigMaps. If two
ConfigMaps both contain an `NMStateConfig` key, two NMStateConfig resources
are rendered, causing a conflict.

To use custom network parameters, the user creates a **single** ConfigMap
that contains the default `InfraEnv` and `BareMetalHost` templates alongside
a custom `NMStateConfig` template, and references **only** that ConfigMap in
`templateRefs`.

To build the custom bundle, copy the default ConfigMap and replace its
`NMStateConfig` entry:

```bash
# Export the default node-template bundle
oc get configmap ai-node-templates-v1 -n siteconfig-operator -o yaml > custom-node-templates.yaml
# Edit: rename the ConfigMap, replace the NMStateConfig data entry
# with the custom template below, and apply:
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
             - ip: {{ templateVar .SpecialVars.CurrentNode.NodeNetwork "nodeIp" }}
               prefix-length: {{ templateVar .SpecialVars.CurrentNode.NodeNetwork "prefixLength" }}
             dhcp: false
         dns-resolver:
           config:
             server:
             - {{ templateVar .SpecialVars.CurrentNode.NodeNetwork "dnsServer" }}
         routes:
           config:
             - destination: '0.0.0.0/0'
               next-hop-address: '{{ templateVar .SpecialVars.CurrentNode.NodeNetwork "gwIp" }}'
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
             macAddress: "B4:96:91:A3:EF:60"
           - name: eno2
             macAddress: "B4:96:91:A3:EF:61"
         templateData:
           nodeIp: "10.8.34.42"
           prefixLength: 24
           gwIp: "10.8.34.254"
           dnsServer: "10.11.5.19"
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
           - ip: 10.8.34.42
             prefix-length: 24
           dhcp: false
       dns-resolver:
         config:
           server:
           - 10.11.5.19
       routes:
         config:
           - destination: '0.0.0.0/0'
             next-hop-address: '10.8.34.254'
             next-hop-interface: eno1
     interfaces:
       - name: eno1
         macAddress: B4:96:91:A3:EF:60
       - name: eno2
         macAddress: B4:96:91:A3:EF:61
   ```

### Workflow Description

1. User adds per-node template parameters under
   `spec.nodes[].nodeNetwork.templateData` in the ClusterInstance CR. MAC
   addresses remain in `interfaces`.
2. The CRD schema validates the known fields (`interfaces`, `config`) as
   today. `templateData` values are accepted as arbitrary JSON.
3. During reconciliation, the template engine renders the custom template.
   The `templateVar` helper decodes values from `TemplateData` on access.
4. Default templates continue to access only `.NodeNetwork.NetConfig` and
   `.NodeNetwork.Interfaces`, producing identical output.

### Recommendation

We recommend **Approach B** (explicit `templateData` field). This approach
cleanly separates known fields from user-defined template parameters, avoids
key-name collisions with future upstream fields, requires low code complexity,
and preserves full backward compatibility, CRD validation, and auto-generated
`DeepCopy`. Template access through the `templateVar` and `hasTemplateVar`
helpers provides JSON-decoded values with explicit error handling for missing
keys, without requiring any changes to the existing `slim-sprig` dependency.

A broader `slim-sprig` modernization (v2 to v3) is desirable for the
general template function set but is out of scope for this EP. The v3 module
removes several functions present in v2, so a safe migration requires a full
compatibility analysis of existing custom templates across the user base.

### API Extensions

The specific API change depends on the chosen approach.

- **Approach A**: No API changes. Requires `fromJson` (not present in the
  current `slim-sprig` v2); `get` can be replaced by Go's builtin `index`.
- **Approach B**: Adds `templateData` field to a new `NodeNetworkSpec` type.
  The `NodeNetwork` field in `NodeSpec` changes from
  `*aiv1beta1.NMStateConfigSpec` to `*NodeNetworkSpec`. The new type preserves
  the same `interfaces` and `config` fields with identical JSON serialization.

Approach B maintains full backward compatibility for existing CRs that
only use `interfaces` and `config`.

### Siteconfig Impact

- **Controllers**: `ClusterInstanceReconciler` is unaffected in Approach A.
  In Approach B, no changes to `buildClusterData()` are needed: the existing
  implementation copies the typed `NodeSpec` into `SpecialVars.CurrentNode`,
  which already includes `NodeNetwork` and its new `TemplateData` field. The
  `templateVar` and `hasTemplateVar` helper functions are registered in
  `funcMap()` in `helper.go`.
- **Templates**: No changes to default templates in any approach. The new
  type preserves the existing `Interfaces` and `NetConfig` fields, so the
  default NMStateConfig template continues to access `.NodeNetwork.NetConfig`
  and `.NodeNetwork.Interfaces` and produce identical output. The Image Based
  Installer `NetworkSecret` template also uses `.NodeNetwork.NetConfig` and
  is equally unaffected. Users provide their own custom templates via
  `templateRefs` to use custom keys.
- **API fields**: No changes in Approach A. In Approach B, the `NodeNetwork`
  field type changes from `*aiv1beta1.NMStateConfigSpec` to the new
  `*NodeNetworkSpec`. The JSON serialization of `interfaces` and `config` is
  identical.
- **Validation**: CRD schema validation for `interfaces` (MinItems, MAC regex)
  is preserved since the typed fields and their kubebuilder annotations
  remain. CRD-level constraints should be tested through an API server
  (envtest), not through direct webhook unit tests.

### Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Custom template references a key not present in `templateData` | The `templateVar` helper returns an error identifying the missing key. The template engine aborts and the controller sets `RenderedTemplates=False` with a diagnostic message. For optional keys, `hasTemplateVar` provides a safe presence check. |
| Approach B changes the Go type backing `NodeNetwork` in `NodeSpec` | The `interfaces` and `config` JSON fields are preserved with identical serialization. Only strongly-typed Go code referencing `aiv1beta1.NMStateConfigSpec` directly must update. YAML/kubectl users are unaffected. |
| Users confuse template parameters with nmstate config (Approach A) | Mitigated by documentation clearly separating template parameters from nmstate content. |

### Drawbacks

- **Approach A** requires additional template functions not present in the
  current operator and establishes a pattern (overloading `config`) that may
  confuse users and complicate future API evolution.
- **Approach B** introduces a type change that decouples `NodeNetwork` from the
  upstream `NMStateConfigSpec`. Future upstream changes must be manually
  synchronized into the new `NodeNetworkSpec` type.
- Custom templates with arbitrary keys are harder to validate statically.
  Template errors surface only at reconciliation time.

## Design Details

### Open Questions

1. Should we pursue Approach A (additional template functions + documentation)
   or Approach B (explicit `templateData` field)? Approach B is recommended;
   see [Recommendation](#recommendation).

### Implementation Steps (Approach B)

1. Define the new `NodeNetworkSpec` type in
   `api/v1alpha1/clusterinstance_types.go` with `Interfaces`, `NetConfig`,
   and `TemplateData` fields.
2. Update `NodeSpec.NodeNetwork` from `*aiv1beta1.NMStateConfigSpec` to
   `*NodeNetworkSpec`.
3. Regenerate CRD manifests (`make generate manifests`).
4. Verify the reinstall validation path in
   `clusterinstance_validation.go` (line 132). The hardcoded path
   `"nodeNetwork/interfaces"` must be confirmed to still resolve correctly
   with the new type.
5. Register the `templateVar` and `hasTemplateVar` helper functions in
   `funcMap()` in `helper.go`.
6. Update tests as described in the Test Plan.

### Mutability and Reinstall

The `templateData` field follows the same immutability rules as the rest of
`nodeNetwork`. After installation is released (i.e., `holdInstallation` is
disabled and provisioning proceeds), `templateData` is immutable. This is
consistent with existing treatment of inline network configuration and avoids
making arbitrary template inputs a new way to change otherwise restricted
settings.

**Design constraint:** MAC addresses belong in the typed `interfaces` field,
not in `templateData`. The `interfaces` field is already in
`ReinstallPermissions` (MAC address changes are allowed during reinstall).
Custom templates should access MACs via `.NodeNetwork.Interfaces`, and use
`templateData` for values such as IP addresses and gateways that follow
the existing immutability expectations.

**Correction window:** While `holdInstallation` is set to `true` and
provisioning has not completed, all spec changes are permitted. This is the
window for correcting errors in `templateData` values before provisioning
proceeds. Once `holdInstallation` is disabled, the webhook transitions to
restricted mode: during active provisioning all spec changes are blocked,
and after provisioning completes only permitted field changes are allowed.

**Reinstall:** During cluster reinstall, templates are re-rendered from the
current spec. Since `templateData` is immutable, the same values are used.
MAC addresses in `interfaces` may change (allowed by `ReinstallPermissions`)
and the custom template reads them from the typed field.

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
  - `NodeNetwork` with only `interfaces` and `config` deserializes correctly
    (backward compatibility).
  - `NodeNetwork` with custom keys preserves those keys through
    marshal/unmarshal round-trip.
  - `DeepCopy` preserves custom keys.
- `api/v1alpha1/clusterinstance_webhook_test.go`:
  - Validation passes for `nodeNetwork` with only known fields.
  - Validation passes for `nodeNetwork` with known fields + custom keys.
- `internal/controller/clusterinstance/helper_test.go`:
  - `templateVar` decodes string, boolean, numeric (`json.Number`), array,
    object, and null values correctly. Null must be tested via actual JSON
    unmarshalling into `NodeNetworkSpec` (the vendored `apiextensionsv1.JSON`
    stores empty `Raw` for null, not `[]byte("null")`).
  - `templateVar` returns an error for a missing key, nil `nodeNetwork`,
    and nil `templateData`.
  - `hasTemplateVar` returns `true` for present keys (including null/false/0
    values) and `false` for absent keys.
  - Custom keys are accessible via the template engine through `templateVar`.
- `internal/controller/clusterinstance/template_engine_test.go`:
  - Default NMStateConfig template renders identically with old and new type
    when `templateData` is absent (backward compatibility).
  - Default NMStateConfig template renders identically when `templateData`
    is present alongside ordinary inline `config` (no data leak from
    `templateData` into default rendered resources).
  - Default IBI `NetworkSecret` template renders identically with old and
    new type (IBI regression).
  - Custom template referencing custom keys renders expected output.
  - Custom template referencing a missing key produces a clear error.
- `api/v1alpha1/clusterinstance_validation_test.go`:
  - Reinstall validation for `nodeNetwork/interfaces` path still works.
  - `templateData` changes are rejected on provisioned clusters
    (immutability).
  - `templateData` changes are accepted while `holdInstallation` is enabled
    (correction window).
  - `templateData` changes are rejected after releasing `holdInstallation`
    but before provisioning completes (active provisioning blocks all spec
    changes).
  - During reinstall, `templateData` changes are rejected while permitted
    `interfaces` MAC address changes remain accepted.

Integration/e2e tests:

- ClusterInstance with default `nodeNetwork` (only `interfaces` + `config`)
  provisions successfully with default templates (regression).
- ClusterInstance with custom `nodeNetwork` keys and a custom NMStateConfig
  template provisions successfully and the rendered NMStateConfig CR contains
  the expected values.
- CRD schema constraints (`minItems`, MAC regex, rejection of invalid MAC
  addresses, `templateData` acceptance) verified through an envtest-based
  test that submits CRs to a real API server.
- Rendered InfraEnv `nmStateConfigLabelSelector` matches the NMStateConfig
  label produced by the custom template (interface-selector integration).

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] Implementation merged with adequate test coverage, including:
  - API-server persistence of `templateData` (envtest)
  - `templateVar`/`hasTemplateVar` decoding and error-path coverage
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

**Upgrade:** The new `NodeNetworkSpec` type is a superset of
`NMStateConfigSpec`. Existing CRs are deserialized correctly because
`interfaces` and `config` retain the same JSON field names and serialization
behavior. The CRD schema adds the new field(s) or relaxes validation (depending
on chosen option). No manual migration steps are required.

**Downgrade:** In-place downgrade of provisioned ClusterInstances that use
`templateData` is unsupported under this design. The webhook blocks spec
changes outside the permitted paths on provisioned clusters, so removing
`templateData` and replacing custom templates with inline `config` blobs
would be rejected.

For ClusterInstances that still have `holdInstallation: true` and whose
provisioning has not completed, rollback is possible: materialize inline
network configuration in `nodeNetwork.config`, switch `templateRefs` to
the default template bundle, and remove `templateData` while spec edits
are still permitted.

Existing CRs that only use `interfaces` and `config` are unaffected by
downgrade.

### Version Skew Strategy

The SiteConfig operator is a standalone component. Version skew with the hub
cluster or managed clusters is not a concern for this change. The relevant
skew scenarios are between the CRD version, the operator version, and
template ConfigMaps:

- **CRD newer than operator:** The operator ignores unknown fields in
  `nodeNetwork` (standard Kubernetes behavior). Custom keys are stored in etcd
  but not used by the older operator. Default templates continue to work.
- **Operator newer than CRD:** The operator supports custom keys but the CRD
  schema does not include them. Users cannot use the feature until the CRD is
  updated. The operator otherwise functions normally.

**Upgrade ordering:** Install the updated CRD and operator before creating
or applying template ConfigMaps or ClusterInstances that reference
`templateData` or the `templateVar`/`hasTemplateVar` helpers. A custom
template that calls `templateVar` on an older operator (without the helper
registered in `funcMap()`) will fail at render time with a function-not-found
error, setting `RenderedTemplates=False`. Similarly, an Approach A template
that calls `fromJson` or `get` on an operator without those functions will
fail in the same way. These are recoverable: upgrading the operator and
retrying reconciliation resolves the error without data loss.

## Implementation History

- 2025-05-20: PoC template demonstrating custom keys inside `config` posted
  on CNF-27592.
- 2026-09-28: Initial enhancement proposal created.
- 2026-09-29: Approach A validated with `slim-sprig` v3: custom NMStateConfig
  template rendered successfully and passed server-side dry-run validation
  (admission compatibility). Identified that v3 removes several v2 functions,
  requiring compatibility analysis for a safe upgrade.

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
`fromJson` has the same `float64` behavior; B1's `templateVar` avoids it
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

### Alternative: Use extra-manifests instead of templates

Users manage per-node NMStateConfig resources outside of the SiteConfig
template system, for example via extra-manifest ConfigMaps or separate
hub-side resource creation.

**Not pursued because:** Extra-manifest references supply installer manifests
(via AgentClusterInstall); they do not create the hub-side NMStateConfig CRs
selected by InfraEnv for discovery networking. They therefore do not solve
this provisioning requirement. Separately managing those hub-side resources
outside the template system is possible but retains the per-node duplication
this proposal seeks to reduce.

# Custom node network templates

Use `spec.nodes[].templateData` to pass per-node JSON parameters to a
shared custom node-level installation template. Parameters also work without
`nodeNetwork`; networking is one use case. Keep interface names and MAC mappings in
`nodeNetwork.interfaces`. Existing inline NMState configurations in
`nodeNetwork.config` continue to work with the default templates.

The operator does not select a custom template automatically. Default Assisted
Installer, Image Based Installer and hosted-cluster templates ignore `templateData`.
Custom parameters are consumed only by the template referenced in `templateRefs`.
They are not copied into downstream resources unless the template emits them.

## Assisted Installer example

The [sample directory](../config/samples/assisted-installer/custom-node-network/README.md)
contains a complete ClusterInstance example, the custom NMStateConfig template and
its expected output, plus commands to assemble a complete custom node bundle from
the installed defaults. The same ConfigMap can serve many nodes with different
parameter values:

```yaml
nodeNetwork:
  interfaces:
  - name: eno1
    macAddress: "00:11:22:33:44:55"
templateData:
  nodeIp: "192.0.2.10"
  prefixLength: 24
  gwIp: "192.0.2.1"
  dnsServer: "192.0.2.53"
templateRefs:
- name: custom-node-templates-v1
  namespace: siteconfig-operator # Use your hub's operator namespace.
```

Select exactly one template per intended resource. Referenced ConfigMaps are
processed independently; entries do not override the defaults, and duplicate
resource identities are not rejected automatically. Follow the sample's bundle
assembly instructions to retain the other required node templates and the
NMStateConfig/InfraEnv association.

## Accessing values

`templateVar node key` decodes a value from the node's `templateData`. An undefined
map fails with `templateData is not defined`; a defined map without the requested
key fails with `templateData key "<key>" is missing`. The condition message includes
the node hostname, template ConfigMap namespace/name, and template key.
`hasTemplateVar node key` reports presence, even when the value is `null`, `false`,
zero, an empty string, an empty array or an empty object. It returns false for a
nil map or an absent key. Neither helper requires `nodeNetwork`.

```gotemplate
{{- $node := .SpecialVars.CurrentNode }}
ip: {{ templateVar $node "nodeIp" | quote }}
prefix-length: {{ templateVar $node "prefixLength" | toJson }}
{{ if hasTemplateVar $node "optionalSettings" }}
settings: {{ templateVar $node "optionalSettings" | toJson }}
{{ end }}
```

`templateData` must be an object. Values can be strings, numbers, booleans, arrays,
objects or null, including nested combinations. Arbitrary keys are allowed inside
this object; unknown fields elsewhere on the node or under `nodeNetwork`
are still subject to normal CRD pruning or strict field validation. The generated
schema preserves the parameter object's contents without a schema for each value,
so explicit null entries remain present through API writes and reads.

JSON values support DNS-server lists, address objects and boolean switches without
encoding structured values as strings and parsing them inside the template. The
helpers already decode these types; the numeric conversion rules below still apply.
Use native YAML lists, mappings and scalars in a YAML manifest; Kubernetes converts
them to JSON. A quoted JSON array remains a string: the helper does not decode
JSON embedded inside strings.

For example:

```yaml
templateData:
  dnsServers: ["192.0.2.53", "192.0.2.54"]
  addresses:
  - ip: "192.0.2.10"
    prefix-length: 24
  - ip: "192.0.2.11"
    prefix-length: 24
  ipv4Enabled: true
  dhcp: false
```

Under the NMStateConfig's `spec.config`:

```gotemplate
{{- $node := .SpecialVars.CurrentNode }}
dns-resolver:
  config:
    server: {{ templateVar $node "dnsServers" | toJson }}
interfaces:
- name: eno1
  type: ethernet
  state: up
  ipv4:
    enabled: {{ templateVar $node "ipv4Enabled" | toJson }}
    dhcp: {{ templateVar $node "dhcp" | toJson }}
    address: {{ templateVar $node "addresses" | toJson }}
```

Document required and optional keys, their types and defaults in a Go template
comment (`{{/* ... */}}`) inside the ConfigMap's template data, as shown in the
sample. It stays in the template source and is omitted from rendered output.
Unknown or unused keys are not validated: a misspelled optional key can silently
skip a conditional block or select the template's default.

Use `quote` for strings that must stay strings in YAML. Use `toJson` to emit a JSON
value (also valid YAML), including explicit null. Bare interpolation of nil is not
a reliable way to emit YAML null. For a nested YAML block, use `toYaml` and
`nindent` at the appropriate indentation:

```gotemplate
settings:{{ templateVar .SpecialVars.CurrentNode "settings" | toYaml | nindent 2 }}
```

Reject null in templates requiring a non-null value. Alternatively, a template
may interpret an omitted `gateway` as its default, null as omitting that static
route, and a string as an explicit gateway. The template defines these meanings.

Numbers are decoded as `json.Number` to avoid an intermediate float64 conversion.
Emit them directly or with `toJson`. Before integer arithmetic or comparison, use
`toString | int64`: slim-sprig v2 recognizes plain strings, not `json.Number`.
For example, `add (templateVar $node "prefixLength" | toString | int64) 1`.
Direct numeric conversions and arithmetic can silently use zero; comparison with
an integer literal fails. Integer conversion requires integer text within range;
decimal/exponent forms and overflow yield zero. Validate types and ranges.
This does not promise arbitrary precision throughout Kubernetes and YAML
processing; store exact identifiers or decimal values as strings.

Use `hasTemplateVar` when defaults depend on presence. Unconverted numeric zero
is truthy and nonempty for `if`/`default`; boolean false is empty. The existing
slim-sprig v2 function set is unchanged. Parameters are stored in a ClusterInstance,
not in a Secret; use them for non-secret configuration.

## Validation, updates and recovery

The CRD still validates typed interface entries, including required names/MACs,
MAC format and the minimum list length when `interfaces` is supplied. It validates
that `templateData` is an object, but not the network meaning of its values.
Templates and downstream installers validate the resulting configuration.

Start with `holdInstallation: true`. Inspect the rendered resources and correct
parameters before releasing installation. Hold controls installation progress;
the operator still renders and applies resources while the hold is active.

| Cluster state | Existing-node parameter changes | Typed interface changes |
|---|---|---|
| Old spec has hold enabled and provisioning has not completed | Allowed, including final edits in the update releasing hold | Allowed |
| Hold disabled, provisioning active or status unknown | Blocked | Blocked |
| Provisioning completed, no new reinstall request | Blocked | Blocked |
| Valid new reinstall request | Blocked | Existing MAC, name, addition and removal permissions retained |
| Reinstall actively in progress | Blocked | Blocked |

An allowed scale-out can provide initial parameters for a new node; it cannot
change existing nodes' parameters. Keep MACs in `interfaces` so the reinstall
exception remains usable. Reinstall uses the existing immutable parameter values.
These parameter restrictions apply regardless of key names and also when
`nodeNetwork` is absent. Existing field update permissions remain unchanged.

A missing required parameter produces `RenderedTemplates=False`, prevents applying
that reconciliation's partially rendered resources and leaves the generation
unobserved for retry. It does not remove previously applied resources. Check the
condition message for the node, ConfigMap namespace/name, template key, and
parameter diagnostic. Parse, execution, and invalid-YAML errors carry the same
source context.
ConfigMap entries are rendered in sorted key order, stopping at the first error.
Downstream dry-run errors use `RenderedTemplatesValidated`; apply errors use
`RenderedTemplatesApplied`. Schema rejection occurs before reconciliation and
does not create a controller condition.

Template ConfigMap changes alone do not trigger reconciliation. A retry or an
eligible spec-generation change can read the updated ConfigMap, as can reinstall.
Use versioned template ConfigMaps and treat them as immutable after use to keep
renders reproducible. Changing a shared ConfigMap can affect every referencing
ClusterInstance on a later render; this feature does not implement day-2 network
reconfiguration.

## Compatibility and rollback

Install the updated operator and CRD together before submitting parameters.
ClusterInstances using only `interfaces` and `config` require no YAML changes.
Go clients retain the upstream `*aiv1beta1.NMStateConfigSpec` network type.
Existing reinstall workflows retain their interface update permissions.
Generated `NodeSpec` deep-copy code independently copies the new parameter map
and its JSON bytes; no manual deep-copy methods are needed.

Before downgrading a feature-using ClusterInstance, and only while its old spec
still has hold enabled and provisioning is incomplete, materialize each rendered
network configuration into `nodeNetwork.config`, switch to the matching default
node templates, and remove `templateData`. Verify the generated network resources
before downgrading. Downgrade after installation release or provisioning completion
is unsupported for feature-using instances. An older CRD can prune the parameters,
and an older operator cannot parse the new helper functions.

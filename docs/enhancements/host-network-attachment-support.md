---
title: host-network-attachment-support
authors:
  - "@alegacy"
reviewers:
  - "@sakhoury"
approvers:
  - "@sakhoury"
api-approvers:
  - "@sakhoury"
creation-date: 2026-05-08
last-updated: 2026-10-29
status: provisional
tracking-link:
  - https://issues.redhat.com/browse/CNF-22904
see-also:
  - "https://github.com/stolostron/siteconfig/pull/1076"
  - "https://github.com/metal3-io/metal3-docs/pull/586"
  - "https://issues.redhat.com/browse/CNF-22902"
---

# HostNetworkAttachment Support for BareMetalHost TOR Switch Configuration

## Release Signoff Checklist

- [ ] Enhancement is `implementable`
- [ ] Design details are appropriately documented from clear requirements
- [ ] Test plan is defined
- [ ] Graduation criteria are defined
- [ ] User-facing documentation is updated

## Summary

This enhancement adds declarative TOR (Top-of-Rack) switch port configuration
through the Metal3 HostNetworkAttachment (HNA) API. New ClusterInstance fields
and templates define managed HNAs and bind BareMetalHost interfaces to managed
or existing external HNAs across Assisted Installer (AI), Image Based Installer
(IBI), and Hosted Control Plane (HCP) flows. It defines HNA validation, reference
resolution, and lifecycle ordering.

IBI support is planned but remains TBD due to technical issues in BMO. If
deferred, the HNA additions and related ordering changes will be removed from
the implemented IBI templates. IBI behavior described below is conditional on
resolving those issues.

## Motivation

The Metal3 project is introducing a networking feature that allows the Baremetal
Operator (BMO) to configure switch ports associated with BareMetalHost (BMH)
network interfaces. A HostNetworkAttachment defines switch port parameters
(mode, VLANs, MTU), and BareMetalHost `spec.networkInterfaces` references those
attachments.
SiteConfig needs a cluster-level template and node-level binding interface in
ClusterInstance (CI) so administrators can express this configuration as part
of cluster deployment.

Entries in `spec.hostNetworkAttachmentTemplates` are rendered and managed by
SiteConfig. Nodes may reuse those entries or reference existing HNAs shared
with other ClusterInstances. An external reference requires an explicit
namespace and does not cause SiteConfig to create, modify, or delete the HNA.

### User Stories

**As a cluster administrator**, I want to define VLAN assignments, trunk/access
modes, and MTU in my ClusterInstance so that BMO can configure switch ports
when bare-metal hosts are provisioned.

**As a cluster administrator**, I want multiple nodes to share a managed or
existing HNA so that I do not duplicate the same network configuration.

**As a cluster administrator**, I want HNA cleanup to follow BMH cleanup so
that hosts no longer reference an HNA when SiteConfig removes or restores it.

**As a cluster administrator performing day-2 operations**, I want to change
bindings during an eligible reinstall, and add new workers using existing HNAs,
while ordinary changes to a provisioned host's bindings remain rejected.

**Day-2 operations considerations:**

- **Lifecycle**: Distinct sync waves establish the dependency chain
  `HostNetworkAttachment → BareMetalHost → ImageClusterInstall` for IBI. HNA
  uses wave 1; BMH uses wave 2 for IBI and wave 3 for AI/HCP; IBI
  ImageClusterInstall moves to wave 3. Same-wave kind sorting and template
  scope do not guarantee dependency order. Cleanup uses descending waves and
  the existing CI finalizer. Reinstall cleans up eligible resources before
  reapplying the current spec; existing exclusions such as ManagedCluster
  remain. These waves order resources, not switch readiness.
- **Monitoring**: Existing `RenderedTemplatesValidated` and
  `RenderedTemplatesApplied` conditions report validation and application
  failures. Logs identify the failing HNA. This proposal adds no readiness
  condition; BMO remains responsible for networking readiness.
- **Remediation**: Rendering and server-side validation failures use the
  existing 30-second requeue. Administrators correct the spec or install the
  required BMO CRD. Adopted-resource restoration conflicts pause reconciliation
  until the conflict is resolved and the pause annotation is removed.
- **Scale**: No change to `maxConcurrentReconciles`. The number of additional
  manifests grows linearly with the cluster-level HNA entries; node bindings
  add no HNA objects.

### Goals

1. Enable declarative TOR switch port configuration for AI, IBI, and HCP.
2. Reuse cluster-level HNAs across nodes and support explicit references to
   existing namespaced HNAs without taking over their lifecycle.
3. Apply and clean up managed resources in dependency order.
4. Preserve behavior for clusters that omit the new fields.

### Non-Goals

1. Managing the HostNetworkAttachment CRD itself — that is the responsibility
   of the Baremetal Operator. SiteConfig manages only HNA objects rendered from
   cluster-level definitions, restoring pre-existing ones during cleanup;
   external HNAs merely referenced by nodes remain untouched.
2. Validating that interface names/MAC addresses in `NetworkInterface` match actual
   hardware — that is deferred to BMO's runtime validation after host
   inspection.
3. Supporting day-2 modifications to HostNetworkAttachments on a provisioned
   cluster (unless in a re-install use case) — they are immutable
   post-provisioning, consistent with BMO's enforcement.
4. Implementing the actual switch port configuration protocol — that is handled
   by BMO's switch management integration.

## Proposal

### Workflow Description

#### Dependencies and Scope

This feature requires BMO HostNetworkAttachment API and runtime support,
expected to land in the BMO 0.15.x series.
BMO must have `IronicNetworking` enabled and its switch configuration and
credentials configured according to the
[BMO deployment requirements](https://github.com/metal3-io/baremetal-operator/blob/main/docs/configuration.md).

This proposal also depends on two independently reviewed capabilities:

- Template rendering
  ([PR #1413](https://github.com/stolostron/siteconfig/pull/1413)) defines generic
  template streams and per-document processing. HNA templates consume it to
  render one object per cluster-level entry.
- Resource restoration
  ([PR #1414](https://github.com/stolostron/siteconfig/pull/1414)) defines adoption
  and restoration of pre-existing rendered resources. HNA cleanup consumes it
  for inventory BMHs and adopted HNAs. An existing resource that is merely
  referenced is not adopted.

The parser, recovery record format, claim handling, conditional restoration,
and reinstall cleanup retry mechanism belong to those proposals. This proposal
specifies the HNA API, templates, validation, and ordering that use them.

#### Defining HostNetworkAttachments

1. The cluster administrator may define HostNetworkAttachment entries at the
   cluster level in `spec.hostNetworkAttachmentTemplates`. These entries are rendered
   by SiteConfig. A node binding may also reference a pre-existing HNA that is
   not in this list; such an HNA may be shared by multiple ClusterInstances.

2. For each node that requires switch port configuration, the administrator
   defines bindings in `spec.nodes[].networkInterfaces` that associate a
   network interface (by name or MAC address) with a HostNetworkAttachment.
   The binding may also include an optional `switchPort` to identify the
   physical switch port when LLDP or inspection cannot provide that information.

3. The administrator submits the ClusterInstance CR.

#### Rendering and Application

4. The CRD schema and validating webhook validate the ClusterInstance, including:
   - Unique HostNetworkAttachment template names
   - Valid HostNetworkAttachment names and references
   - NetworkInterface name and MAC-address formats via Kubebuilder validation markers
   - Exactly one interface selector (name or MAC address)
   - `SwitchPortIdentifier` validation when `switchPort` is provided
   - Valid BMO HostNetworkAttachmentRef names and namespaces
   - Managed references must match a cluster-level entry; external references
     must specify a namespace

5. The ClusterInstanceReconciler renders templates:
   - **Cluster-level**: The HostNetworkAttachment template iterates over
     `spec.hostNetworkAttachmentTemplates` and produces one HostNetworkAttachment CR
     per entry.
   - **Node-level**: The BareMetalHost template conditionally adds
     `spec.networkInterfaces` entries that reference HostNetworkAttachments by
     namespace and name, and passes through the optional `switchPort` physical
     port identifier. A managed reference defaults to the rendered HNA
     namespace when its namespace is omitted; external references preserve the
     explicitly supplied namespace. These references do not manage the HNA
     lifecycle.

6. Objects are created or updated using the lifecycle rules below, in sync-wave
   order. Updates use Server-Side Apply:
   - Wave 1: HostNetworkAttachment CRs rendered from cluster-level definitions
     (and other wave-1 resources)
   - Wave 2: BareMetalHost CRs with `spec.networkInterfaces` referencing the
     HostNetworkAttachments (for IBI flows)
   - Wave 3: BareMetalHost CRs with `spec.networkInterfaces` referencing the
     HostNetworkAttachments (for AI/HCP flows), and ImageClusterInstall for IBI

   When rendered, `spec.networkInterfaces` is managed as a complete list;
   entries from other managers are not merged. For adopted BareMetalHosts,
   cleanup restores the saved original list as defined by
   [PR #1414](https://github.com/stolostron/siteconfig/pull/1414), overwriting
   later changes to that list.

   Creating an HNA CR does not establish networking readiness. The supported
   BMO runtime and installation flow must ensure that networking is configured
   before provisioning requires it; SiteConfig adds no HNA readiness gate.

#### Deprovisioning

7. When the ClusterInstance is deleted:
   - Rendered manifests created by SiteConfig are deleted in descending
     sync-wave order. For IBI, ImageClusterInstall (wave 3) precedes BMH
     (wave 2), which precedes HNA (wave 1). AI/HCP BMHs use wave 3.
   - External HNAs merely referenced by nodes are not rendered by SiteConfig and remain
     untouched.
   - Sync-wave ordering ensures higher-wave resources are removed before
     lower-wave resources they depend on

8. For pre-existing resources that were modified (not created) by siteconfig
   (e.g., pre-existing BareMetalHosts that had `spec.networkInterfaces` added):
   - SiteConfig's modifications are reverted while changes to untracked fields
     survive, as defined by the resource restoration proposal
     ([PR #1414](https://github.com/stolostron/siteconfig/pull/1414)).
     Restoration conflicts pause reconciliation for administrator resolution.

#### Reinstallation

9. During a reinstall workflow:
   - SiteConfig-created HostNetworkAttachment CRs are deleted and adopted HNAs
     are restored in descending sync-wave order; external referenced HNAs
     remain untouched.
   - After cleanup completes, templates are re-rendered from the current
     ClusterInstance spec and re-applied
   - `hostNetworkAttachmentTemplates` and
     `nodes[].networkInterfaces` may be changed only when a new reinstall
     generation is requested. The corresponding reinstall allow-list entries
     allow these fields during the cluster-wide reinstall while ordinary
     post-provisioning updates continue to reject them.
   - All rendered BMHs are deleted or reverted before managed HNAs are deleted
     or restored, and the current spec is then reapplied. External HNAs remain
     untouched.
   - Reinstall waits for cleanup to complete, including pending finalizers.
     Restoration conflicts pause reconciliation; pending cleanup is retried.

#### Example ClusterInstance

```yaml
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: ClusterInstance
metadata:
  name: example-cluster
  namespace: example-cluster
spec:
  clusterName: example-cluster
  # ... other cluster-level fields ...

  hostNetworkAttachmentTemplates:
    - name: storage-trunk
      spec:
        mode: trunk
        nativeVLAN: 100
        allowedVLANs: ["101", "102", "110-120"]
        mtu: 9000
    - name: management-access
      spec:
        mode: access
        nativeVLAN: 200

  nodes:
    - hostName: node-1
      bmcAddress: idrac-virtualmedia://192.168.1.10/redfish/v1/Systems/System.Embedded.1
      bmcCredentialsName:
        name: node-1-bmc-secret
      bootMACAddress: "00:11:22:33:44:55"
      networkInterfaces:
        - name: eno1
          hostNetworkAttachment:
            name: storage-trunk
          switchPort:
            switchID: "AA:BB:CC:DD:EE:FF"
            portID: Ethernet1/10
            switchSystemName: tor-1
        - macAddress: "AA:BB:CC:DD:EE:FF"
          hostNetworkAttachment:
            name: management-access
```

**Cluster-level Go template**:

```gotemplate
{{ if .Spec.HostNetworkAttachmentTemplates }}
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
{{ end }}
```

The API embeds BMO's `HostNetworkAttachmentSpec`, and the template serializes
that spec directly rather than duplicating its individual fields. This keeps
the rendered fields and their YAML names aligned with the BMO API as it evolves.

When `HostNetworkAttachmentTemplates` is nil or empty, the template renders no
objects.

This produces the following rendered resources (among others). Each
HostNetworkAttachment is a separate Kubernetes resource:

**HostNetworkAttachment CRs**:

```yaml
apiVersion: metal3.io/v1alpha1
kind: HostNetworkAttachment
metadata:
  name: storage-trunk
  namespace: example-cluster
spec:
  mode: trunk
  nativeVLAN: 100
  allowedVLANs: ["101", "102", "110-120"]
  mtu: 9000
---
apiVersion: metal3.io/v1alpha1
kind: HostNetworkAttachment
metadata:
  name: management-access
  namespace: example-cluster
spec:
  mode: access
  nativeVLAN: 200
```

**BareMetalHost**:

```yaml
apiVersion: metal3.io/v1alpha1
kind: BareMetalHost
metadata:
  name: node-1
  namespace: example-cluster
spec:
  # ... other BMH fields ...
  networkInterfaces:
    - name: eno1
      hostNetworkAttachment:
        name: storage-trunk
        namespace: example-cluster
      switchPort:
        switchID: "AA:BB:CC:DD:EE:FF"
        portID: Ethernet1/10
        switchSystemName: tor-1
    - macAddress: "AA:BB:CC:DD:EE:FF"
      hostNetworkAttachment:
        name: management-access
        namespace: example-cluster
```

### API Extensions

#### New Types

**HostNetworkAttachmentTemplate** — wraps the BMO
`HostNetworkAttachmentSpec` with a name for rendering:

```go
type HostNetworkAttachmentTemplate struct {
    // +required
    // +kubebuilder:validation:MaxLength=253
    // +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?([.][a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
    Name string `json:"name"`

    // +required
    Spec bmh_v1alpha1.HostNetworkAttachmentSpec `json:"spec"`
}
```

Only entries in `spec.hostNetworkAttachmentTemplates` are rendered by SiteConfig.
Node-level bindings only create BareMetalHost references; they may reference a
rendered HNA or an existing HNA and do not affect HNA lifecycle.

**Node bindings** reuse BMO's `NetworkInterface` type directly. Its `name` and
`macAddress` selectors are optional strings, and its optional `switchPort` uses
`SwitchPortIdentifier`; the attachment is a `HostNetworkAttachmentRef`. No
SiteConfig `InterfaceRef` or second `HostNetworkAttachment` type is introduced.
The CRD inherits the upstream schema and validation markers. SiteConfig's
webhook enforces exactly one nonempty selector and validates HNA references.

If the referenced name is present in the cluster-level
`spec.hostNetworkAttachmentTemplates` list and the namespace is
omitted (or is the rendered HNA namespace), SiteConfig renders and manages that
HNA; an omitted namespace resolves to `spec.clusterName` in the current
templates. Otherwise the reference is to an existing external HNA and must
include its namespace. A node reference alone never causes SiteConfig to create
or delete an HNA.

For a cross-namespace reference, BMO must be able to read the referenced HNA
namespace. If BMO watches a restricted set of namespaces, both the BMH and HNA
namespaces must be included; SiteConfig does not validate BMO's watch scope.

`SwitchPort` is scoped to the node binding because the physical switch port
may differ for each host interface. It reuses BMO's `SwitchPortIdentifier`
type and is rendered into the corresponding BMO `NetworkInterface`. When it is
omitted, BMO may derive the switch port from inspection data or LLDP.
An explicit `switchPort` overrides LLDP information.

#### Modified Types

**ClusterInstanceSpec** — new optional field:

```go
// +optional
HostNetworkAttachmentTemplates []HostNetworkAttachmentTemplate `json:"hostNetworkAttachmentTemplates,omitempty"`
```

**NodeSpec** — new optional field:

```go
// +optional
NetworkInterfaces []bmh_v1alpha1.NetworkInterface `json:"networkInterfaces,omitempty"`
```

#### Immutability

Both `spec.hostNetworkAttachmentTemplates` and `spec.nodes[].networkInterfaces`
are immutable during ordinary post-provisioning updates. During a requested
cluster-wide reinstall, they may be changed because all rendered BMHs are
deleted or reverted before the new configuration is applied. HNA specs cannot
be modified while referenced by a BareMetalHost, and existing external HNAs are
never modified by SiteConfig.

A new worker or a replacement with a new hostname may supply its own bindings
under the existing node-addition rules. This does not authorize changes to the
cluster-level template list. For example, if a new worker needs
`storage-trunk-2` and that template is not already defined, ordinary scale-out
can reference a separately created HNA:

```yaml
networkInterfaces:
  - name: eno1
    hostNetworkAttachment:
      name: storage-trunk-2
      namespace: hardware
```

Changing bindings for a retained hostname, including its interface selector or
HNA reference, requires an eligible new reinstall request. The reinstall
allow-lists include both node `networkInterfaces` and cluster-level
`hostNetworkAttachmentTemplates`.

During an eligible reinstall, additions, removals, and replacements are
permitted under `/hostNetworkAttachmentTemplates` and
`/nodes/*/networkInterfaces`. Omitted and empty lists are equivalent.
Ordinary post-provisioning changes to these fields remain rejected,
except for bindings supplied with newly added nodes.

### Siteconfig Impact

- **Controllers**: `ClusterInstanceReconciler` registers the new HNA template
  and uses existing manifest validation, application, and status reporting.
  Cleanup deletes created resources and restores adopted resources in reverse
  dependency order. No changes to `ClusterDeploymentReconciler` or
  `ConfigurationMonitor`.
- **Templates**: Add a cluster-level HNA template to AI, IBI, and HCP; update
  BMH templates to render `networkInterfaces` and optional `switchPort`.
  Set HNA to wave 1, IBI BMH to wave 2, AI/HCP BMH to wave 3, and IBI
  ImageClusterInstall to wave 3. Node bindings render references only.
- **API fields**: Add optional `ClusterInstanceSpec.HostNetworkAttachmentTemplates`
  and `NodeSpec.NetworkInterfaces`; regenerate CRDs, DeepCopy code, and bundles.
- **Validation**: Validate template name uniqueness, interface selectors, BMO
  names/namespaces and switch port identifiers, managed/external references,
  post-provisioning immutability, and reinstall allow-lists. Treat omitted and
  empty new lists equivalently for post-provisioning comparisons, including
  GitOps reconciliation in either direction.
- **RBAC**: Add HNA get, list, watch, create, update, patch, and delete
  permissions for rendered resources. External references alone never trigger
  SiteConfig writes or deletion.

### Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| BMH is applied before its HNA, or IBI ICI before its BMH | Use distinct sync waves instead of template scope or same-wave kind order. |
| An external shared HNA is changed or deleted | Only cluster-level definitions render managed objects; node references alone never manage the HNA lifecycle. |
| An adopted HNA is restored while a rendered BMH still references it | Complete higher-wave BMH deletion or restoration before lower-wave HNA cleanup. External consumers of a rendered HNA must be coordinated by the administrator. |
| BMO CRD or runtime support is missing | Server-side validation blocks apply for an absent CRD. Qualify each installation flow against BMO networking behavior; SiteConfig adds no readiness gate. |
| Cross-namespace HNA cannot be read by BMO | Require explicit external namespaces and document BMO watch-scope and RBAC requirements. |

### Drawbacks

- Adds ClusterInstance fields and validation tied to BMO networking APIs.
- Reuses upstream BMO types, so upstream schema changes require compatibility
  review and regenerated SiteConfig schemas.
- Requires runtime qualification of networking readiness for each installation
  flow.
- Does not by itself guarantee that switch configuration has completed before
  provisioning; that remains an installation-flow and BMO responsibility.

## Design Details

### Test Plan

**Unit and controller tests:**

- Render zero, one, and several HNAs in each supported flow; check names,
  namespaces, specs, and waves.
- Render managed references with omitted and explicit matching namespaces,
  external namespaced references, interface names/MACs, and optional switch ports
  in every BMH template. Node-only references must not render HNAs.
- Validate unique nonempty template names, interface selector exclusivity and
  formats, BMO reference and switch port schemas, and reference resolution.
- Reject ordinary post-provisioning HNA and retained-node binding changes;
  allow eligible reinstall changes and new-node bindings under existing rules.
  Cover omitted/empty lists in both directions, including GitOps reconciliation.
- Verify wave ordering for HNA, BMH, and IBI ImageClusterInstall on application,
  finalization, pruning, and reinstall. Higher-wave BMH cleanup must complete
  before HNA cleanup advances.
- Verify a missing HNA CRD fails `RenderedTemplatesValidated` before any actual
  manifest apply, and clusters omitting the feature remain unaffected.

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] HNA API, schemas, templates, validation, RBAC, and wave ordering implemented
      with adequate test coverage
- [ ] Networking readiness qualified with supported BMO versions; IBI support
      qualified or deferred with its HNA template changes removed
- [ ] Documented in user-facing docs, including external references and recovery
- [ ] Released in version 2.18.0

### Upgrade / Downgrade Strategy

**Upgrade**: The new `hostNetworkAttachmentTemplates` and
`nodes[].networkInterfaces` lists are optional and have no list-level CRD or
webhook default. Existing CIs omitting them do not acquire HNA entries solely
from the CRD upgrade. Embedded BMO defaults still apply to supplied entries,
including `mode: access` inside an HNA template's spec. Install the required
BMO APIs before enabling HNA templates.

**Downgrade**: Complete HNA/BMH cleanup with the capable controller before
downgrading, including releasing adopted resources before using an older
controller that could delete them. Remove use of the new fields and
templates before reverting to a schema/controller that does not support them;
do not rely on an older schema preserving unknown fields.
After downgrade, the older controller still attempts cleanup of HNAs
recorded in `status.manifestsRendered`. Missing HNA permissions can block
CI finalization; if permissions are retained, adopted HNAs are deleted
rather than restored. Complete HNA/BMH cleanup before downgrade.

### Version Skew Strategy

The siteconfig operator depends on the BMO HostNetworkAttachment CRD being
present in the cluster. Support must be qualified against the shipped BMO API
and runtime.
If the CRD is not installed, the HNA template still renders the resource, but
server-side dry-run validation fails. The controller sets
`RenderedTemplatesValidated` to failed and does not enter actual apply: the
full rendered manifest list is validated before any resources are applied.
The existing validation-error flow requeues the CI.
Explicit `pruneManifests` cleanup runs before validation and is not prevented
by a subsequent validation failure.

Clusters omitting `spec.hostNetworkAttachmentTemplates` render no HNAs and do
not depend on that CRD solely because this feature is installed. A node-only
external reference still requires a BMO version that understands BMH networking
and can read the HNA namespace; SiteConfig does not validate BMO's watch scope
or provide a networking readiness gate.

## Implementation History

- 2026-05-08: Initial proposal created.

## Alternatives

### Supply HNAs and BMH Bindings Only Through Custom Templates

Custom templates can express the resources, but each administrator would need
to reproduce reference resolution, validation, and ordering. Typed CI fields
and shipped templates provide a consistent interface across installation flows.

## Infrastructure Needed

No new repository or service is needed. Runtime qualification requires BMO
networking support and an environment capable of exercising switch management
with the supported installation flows.

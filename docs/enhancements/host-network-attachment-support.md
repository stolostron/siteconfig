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
last-updated: 2026-09-22
status: provisional
tracking-link:
  - https://issues.redhat.com/browse/CNF-22904
see-also:
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

This enhancement adds support for declarative TOR (Top-of-Rack) switch port
configuration on BareMetalHost network interfaces through the Metal3
HostNetworkAttachment API. The primary feature is new API fields and templates
for rendering HostNetworkAttachment CRs and associating them with BareMetalHost
network interfaces across all installation flows. Two prerequisite capabilities
are required to enable this: (1) multi-document YAML support in the template
engine, which allows a single template to render multiple HostNetworkAttachment
CRs via a range loop, and (2) a revert-on-deprovision mechanism for
pre-existing resources, which is needed because in `hostRef` based deployments
BareMetalHosts are pre-created as available server inventory and siteconfig 
must undo its modifications (e.g., adding `spec.networkInterfaces`) when the 
cluster is deprovisioned so that the BMH instances are available to be used
in future deployments.

SiteConfig supports both cluster-scoped management and references to existing
HostNetworkAttachments. Entries listed at the cluster level are rendered and
managed by SiteConfig. A node-level entry whose name matches one of those
entries may omit the namespace and resolves to the rendered HNA namespace. A
node may also reference an existing HNA shared with other ClusterInstances;
that external reference must specify its namespace, and the reference itself
does not cause SiteConfig to create, modify, or delete the HNA.

## Motivation

The Metal3 project is introducing a networking feature that allows the Baremetal
Operator (BMO) to configure switch ports associated with BareMetalHost network
interfaces. This is accomplished through a new HostNetworkAttachment CRD that
defines switchport parameters (mode, VLANs, MTU), and a new
`spec.networkInterfaces` field on BareMetalHost that references these
attachments.

For siteconfig to support this feature in cluster deployments, the operator must
be able to define HostNetworkAttachment configurations at the cluster level and
bind them to specific node interfaces, following the existing template/binding
pattern. This is the core of this enhancement.

Enabling this feature requires two prerequisite changes:

1. **Multi-document YAML template support** — A cluster may define several
   HostNetworkAttachment configurations, each of which must be rendered as a
   separate Kubernetes resource. The existing template engine only supports
   single-document output. Multi-document YAML support allows a single template
   to use a range loop and produce one HostNetworkAttachment CR per entry in the
   cluster-level list, separated by `---` document markers. Node-level
   references do not cause additional HNAs to be rendered.

2. **Revert-on-deprovision for pre-existing resources** — In `hostRef` based
   deployments, BareMetalHost resources are pre-created as available server
   inventory. BMO inspects them, powers them off, and they sit in an available
   pool waiting to be assigned to a cluster. When siteconfig provisions a
   cluster, it modifies these pre-existing BMHs (e.g., adding
   `spec.networkInterfaces`). Currently siteconfig can create resources on
   installation and delete them on deprovisioning, but it cannot revert
   modifications to resources it did not create. A revert mechanism is needed
   so that BMHs are returned to their original state when the cluster is
   deprovisioned and the BMH can be reused for future cluster deployments.

### User Stories

**As a cluster administrator**, I want to define TOR switch port configurations
(VLAN assignments, trunk/access modes, MTU) as part of my ClusterInstance
specification so that network switch ports are automatically configured when
bare metal hosts are provisioned.

**As a cluster administrator**, I want to reuse the same switch port
configuration across multiple nodes so that I do not have to duplicate
configuration for every interface on every host.

**As a cluster administrator**, I want network switch port configurations to be
cleaned up when I deprovision a cluster so that switch ports are returned to
their default state.

**As a cluster administrator**, I want modifications made to pre-existing
BareMetalHost resources during installation to be reverted on deprovisioning so
that the BMH returns to its original state and can be reused for a different
cluster deployment.

**Day-2 operations considerations:**

- **Lifecycle**: The current implementation does not provide an application-order
  guarantee based on template scope. Although it renders cluster-level and
  node-level templates in separate phases, it later groups all rendered objects
  by sync wave and sorts same-wave objects by kind before applying them. As a
  result, placing HostNetworkAttachment and BareMetalHost resources
  in the same IBI wave can apply the BareMetalHost first (`BareMetalHost` sorts
  before `HostNetworkAttachment`), even though the BMH depends on the HNA being
  present. Similarly, moving the BMH to a later wave without moving
  ImageClusterInstall would leave ICI able to reference a BMH that has not been
  applied yet. The proposed fix is to use distinct waves: HNA at wave 1, BMH at
  wave 2 for IBI and wave 3 for AI/HCP, and IBI ImageClusterInstall at wave 3.
  This establishes the dependency chain
  `HostNetworkAttachment → BareMetalHost → ImageClusterInstall` without relying
  on template processing order or same-wave kind sorting. On deprovisioning,
  siteconfig-created resources would be deleted in descending sync-wave order—
  ImageClusterInstall, then BareMetalHost, then HostNetworkAttachment—and
  pre-existing resources that were modified would be reverted. On reinstall,
  all rendered manifests would be deleted and re-created from the current spec.
- **Monitoring**: No new status conditions are introduced.
  `RenderedTemplatesValidated` reports server-side validation failures, such as
  a missing HostNetworkAttachment CRD, before any manifests are applied.
  `RenderedTemplatesApplied` reports the subsequent actual apply phase.
  Structured logs include the HostNetworkAttachment resource ID for debugging.
- **Remediation**: Standard siteconfig remediation patterns apply. If rendering
  or server-side validation fails, the controller requeues with a 30-second
  delay. Administrators can use the pause annotation to halt reconciliation for
  manual intervention.
- **Scale**: No impact on `maxConcurrentReconciles`. HostNetworkAttachment
  rendering scales linearly with the number of attachments defined per cluster
  (typically a small number per deployment).

### Goals

1. Enable declarative TOR switch port configuration through the ClusterInstance
   API for all installation flows (Assisted Installer, Image Based Installer,
   Hosted Control Plane).
2. Maintain backward compatibility — clusters without HostNetworkAttachments
   are unaffected.
3. (Prerequisite) Support multi-document YAML rendering in the template engine
   so that a single template can produce multiple Kubernetes resources.
4. (Prerequisite) Provide a mechanism to revert modifications to pre-existing
   resources (such as BareMetalHosts) when a ClusterInstance is deprovisioned.

### Non-Goals

1. Managing the HostNetworkAttachment CRD itself — that is the responsibility
   of the Baremetal Operator. SiteConfig creates and deletes only HNA objects 
   rendered from cluster-level definitions; existing HNAs referenced by nodes 
   remain owned by their creator.
2. Validating that interface names/MAC addresses in `InterfaceRef` match actual
   hardware — that is deferred to BMO's runtime validation after host
   inspection.
3. Supporting day-2 modifications to HostNetworkAttachments on a provisioned
   cluster (unless in a re-install use case) — they are immutable 
   post-provisioning, consistent with BMO's enforcement.
4. Implementing the actual switch port configuration protocol — that is handled
   by BMO's switch management integration.

## Proposal

### Workflow Description

#### Defining HostNetworkAttachments

1. The cluster administrator may define HostNetworkAttachment entries at the
   cluster level in `spec.hostNetworkAttachmentTemplates`. These entries are rendered
   by SiteConfig. A node binding may also reference an pre-existing HNA that is
   not in this list; such an HNA may be shared by multiple ClusterInstances.

2. For each node that requires switch port configuration, the administrator
   defines bindings in `spec.nodes[].hostNetworkAttachments` that associates a
   network interface (by name or MAC address) with a HostNetworkAttachment.
   The binding may also include an optional `switchPort` to identify the
   physical switch port when LLDP or inspection cannot provide that information.

3. The administrator submits the ClusterInstance CR.

#### Rendering and Application

4. The CRD schema and validating webhook validate the ClusterInstance, including:
   - Unique HostNetworkAttachment template names
   - Valid HostNetworkAttachment names and references
   - InterfaceRef name and MAC-address formats via Kubebuilder validation markers
   - Exactly one InterfaceRef selector (name or MAC address)
   - `SwitchPortIdentifier` validation when `switchPort` is provided
   - Valid BMO HostNetworkAttachmentRef names and namespaces
   - Managed references must match a cluster-level entry; external references
     must specify a namespace

5. The ClusterInstanceReconciler renders templates:
   - **Cluster-level**: The HostNetworkAttachment template iterates over
     `spec.hostNetworkAttachmentTemplates` and produces one HostNetworkAttachment CR
     per entry, separated by `---` document markers. The multi-document YAML
     parser splits these into individual RenderedObjects.
   - **Node-level**: The BareMetalHost template conditionally adds
     `spec.networkInterfaces` entries that reference HostNetworkAttachments by
     namespace and name, and passes through the optional `switchPort` physical
     port identifier. A managed reference defaults to the rendered HNA
     namespace when its namespace is omitted; external references preserve the
     explicitly supplied namespace. These references do not manage the HNA
     lifecycle.

6. Objects are applied via Server-Side Apply in sync-wave order:
   - Wave 1: HostNetworkAttachment CRs rendered from cluster-level definitions
     (and other wave-1 resources)
   - Wave 2: BareMetalHost CRs with `spec.networkInterfaces` referencing the
     HostNetworkAttachments (for IBI flows)
   - Wave 3: BareMetalHost CRs with `spec.networkInterfaces` referencing the
     HostNetworkAttachments (for AI/HCP flows)

#### Deprovisioning

7. When the ClusterInstance is deleted:
   - Rendered manifests that were created by siteconfig are deleted in
     descending sync-wave order (e.g., ManagedCluster at wave 2, then
     ClusterDeployment and HostNetworkAttachment at wave 1)
   - Existing HNAs referenced by nodes are not rendered by SiteConfig and remain
     untouched.
   - Sync-wave ordering ensures higher-wave resources are removed before
     lower-wave resources they depend on

8. For pre-existing resources that were modified (not created) by siteconfig
   (e.g., pre-existing BareMetalHosts that had `spec.networkInterfaces` added):
   - Modifications are reverted to restore the resource to its original state
     (see [Revert-on-Deprovision](#revert-on-deprovision-for-pre-existing-resources)
     below)

#### Reinstallation

9. During a reinstall workflow:
   - HostNetworkAttachment CRs rendered from cluster-level definitions are
     deleted as part of the standard rendered manifest deletion (descending
     sync-wave order); existing referenced HNAs remain untouched.
   - After deletion completes, templates are re-rendered from the current
     ClusterInstance spec and re-applied
   - `hostNetworkAttachmentTemplates` and
     `nodes[].hostNetworkAttachments` may be changed only when a new reinstall
     generation is requested. The corresponding reinstall allow-list entries
     allow these fields during the cluster-wide reinstall while ordinary
     post-provisioning updates continue to reject them.
   - All rendered BMHs are deleted or reverted before managed HNAs are deleted,
     and the resources are then recreated from the current spec. External HNAs
     remain untouched.

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
      hostNetworkAttachments:
        - interfaceRef:
            name: eno1
          hostNetworkAttachment:
            name: storage-trunk
          switchPort:
            switchID: "AA:BB:CC:DD:EE:FF"
            portID: Ethernet1/10
            switchSystemName: tor-1
        - interfaceRef:
            macAddress: "AA:BB:CC:DD:EE:FF"
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

When `HostNetworkAttachmentTemplates` is nil or empty, the outer conditional
renders no objects. With one entry, the output contains one YAML document
beginning with `---`; with multiple entries, each document begins with `---`
and there is no trailing separator.

This produces the following rendered resources (among others). Each
HostNetworkAttachment is a separate Kubernetes resource; they are shown below
as a multi-document YAML stream separated by `---`:

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
    Name string                              `json:"name"`
    Spec bmh_v1alpha1.HostNetworkAttachmentSpec `json:"spec"`
}
```

Only entries in `spec.hostNetworkAttachmentTemplates` are rendered by SiteConfig.
Node-level bindings only create BareMetalHost references; they may reference a
rendered HNA or an existing HNA and do not affect HNA lifecycle.

**InterfaceRef** — selects a network interface by name or MAC address (mutually
exclusive):

```go
type InterfaceRef struct {
    // +optional
    // +kubebuilder:validation:Pattern=`^[a-zA-Z0-9._-]+$`
    Name       *string `json:"name,omitempty"`
    // +optional
    // +kubebuilder:validation:Pattern=`^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}$`
    MACAddress *string `json:"macAddress,omitempty"`
}
```

The CRD schema enforces the individual selector formats. Webhook validation
enforces that exactly one selector is provided and validates HNA references.

**HostNetworkAttachment** — binds a node interface to a managed or external
HostNetworkAttachment:

```go
type HostNetworkAttachment struct {
    InterfaceRef          InterfaceRef `json:"interfaceRef"`
    HostNetworkAttachment bmh_v1alpha1.HostNetworkAttachmentRef `json:"hostNetworkAttachment"`
    SwitchPort             *bmh_v1alpha1.SwitchPortIdentifier `json:"switchPort,omitempty"`
}
```

The node binding reuses BMO's `HostNetworkAttachmentRef` rather than
duplicating its name and namespace fields. If the referenced name is present in
the cluster-level `spec.hostNetworkAttachmentTemplates` list and the namespace is
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

#### Modified Types

**ClusterInstanceSpec** — new optional field:

```go
HostNetworkAttachmentTemplates []HostNetworkAttachmentTemplate `json:"hostNetworkAttachmentTemplates,omitempty"`
```

**NodeSpec** — new optional field:

```go
HostNetworkAttachments []HostNetworkAttachment `json:"hostNetworkAttachments,omitempty"`
```

#### Immutability

Both `spec.hostNetworkAttachmentTemplates` and `spec.nodes[].hostNetworkAttachments`
are immutable during ordinary post-provisioning updates. During a requested
cluster-wide reinstall, they may be changed because all rendered BMHs are
deleted or reverted before the new configuration is applied. HNA specs cannot
be modified while referenced by a BareMetalHost, and existing external HNAs are
never modified by SiteConfig.

### Siteconfig Impact

- **Controllers**: `ClusterInstanceReconciler` is modified to register the
  HostNetworkAttachment template. No changes to `ClusterDeploymentReconciler`
  or `ConfigurationMonitor`.
- **Templates**: New `HostNetworkAttachment` cluster-level template added to
  all three installation flows (AI, IBI, HCP). Existing `BareMetalHost`
  node-level templates updated to conditionally render `spec.networkInterfaces`
  and the optional `switchPort` identifier.
  Only cluster-level HNA entries are rendered; node-level entries contribute
  references only. Managed references may omit the namespace; external
  references must provide it.
- **API fields**: New fields added to `ClusterInstanceSpec` and `NodeSpec` as
  described above.
- **Validation**: New webhook validation functions for HostNetworkAttachment
  template uniqueness, BMO reference names/namespaces, managed versus external
  reference resolution, and node-level reference integrity.
- **Template engine**: `render()` updated to support multi-document YAML
  parsing; `renderManifestFromTemplate()` updated to return `[]RenderedObject`.
- **RBAC**: New permissions added for `HostNetworkAttachments` resources
  (get, list, watch, create, update, patch, delete) to reconcile entries
  rendered from the cluster-level list. External HNAs are not written or
  deleted by SiteConfig; node references to them are read-only.

### Revert-on-Deprovision for Pre-existing Resources

In `hostRef` based deployments, BareMetalHost resources are pre-created
as available server inventory. BMO inspects them, powers them off, and they
remain in an available pool until assigned to a cluster. When siteconfig
provisions a cluster, it modifies these pre-existing BMHs (e.g., adding
`spec.networkInterfaces`). On deprovisioning, these modifications must be
reverted without deleting the BMH itself, so that it returns to the available
inventory pool and can be reused for future cluster deployments.

This is a general capability gap in siteconfig — currently the operator can
create resources on installation and delete them on deprovisioning, but it
cannot revert modifications to resources it did not create.

#### Key Considerations

The revert mechanism must handle several scenarios correctly:

1. **Field additions vs. modifications** — Siteconfig may add entirely new
   fields to a pre-existing resource (e.g., adding `spec.networkInterfaces` to
   a BMH that had none) or modify existing fields. For additions, the revert
   simply removes the field. For modifications, the revert must restore the
   original value — merely removing the field would leave the resource in an
   incorrect state.

2. **Concurrent modifications** — Between provisioning and deprovisioning,
   another controller or user may modify a field that siteconfig set. The
   revert must not blindly overwrite such changes. If an external actor has
   taken responsibility for a field, siteconfig should leave it alone rather
   than risk reverting a deliberate change.

3. **CRD version skew** — The resource may be upgraded to a new CRD version
   between provisioning and deprovisioning. Field representations could change
   (e.g., defaults added, field formats normalized, deprecated fields removed).
   Any snapshot-based approach must account for the possibility that a saved
   state no longer matches the current schema.  Future changes to any CRD
   definitions, that have an impact on compatibility, will need to be handled
   on a case by case basis.

#### Proposed Approach: JSON Patch with Compare-and-Swap

The proposed approach uses a
[JSON Patch (RFC 6902)](https://datatracker.ietf.org/doc/html/rfc6902) document
stored as an annotation on the modified resource. JSON Patch includes a `test`
operation that acts as a compare-and-swap guard — it asserts that a field still
holds the value siteconfig set before the inverse operation is applied. If the
test fails — because another controller or user modified the field — the entire
patch is rejected atomically by the API server and siteconfig reports the
mismatch.

**How it works:**

1. Before modifying a pre-existing resource, siteconfig reads the current state
   and computes a revert patch — a JSON Patch document containing interleaved
   `test` and inverse operations for each field it will change. For example:

   ```json
   [
     {"op": "test", "path": "/spec/networkInterfaces", "value": "<what siteconfig set>"},
     {"op": "remove", "path": "/spec/networkInterfaces"}
   ]
   ```

   Or for a modified field (where the original value must be restored):

   ```json
   [
     {"op": "test", "path": "/spec/someField", "value": "<what siteconfig set>"},
     {"op": "replace", "path": "/spec/someField", "value": "<original value>"}
   ]
   ```

2. For a pre-existing resource, siteconfig includes the computed revert patch
   in the `siteconfig.open-cluster-management.io/revert-patch` annotation of
   the same SSA request that applies the desired labels and spec fields. The
   request includes the resource version observed by the initial `GET`; if the
   resource changed before the apply, the API server rejects the stale request
   and reconciliation retries from a fresh read. Thus, a failed apply persists
   neither the revert marker nor the desired resource changes. Resources that
   do not exist at the initial `GET` are created without a revert patch.

3. On deprovisioning, siteconfig reads the annotation and issues the JSON Patch
   via `client.RawPatch(types.JSONPatchType, data)`. The API server evaluates
   the `test` operations atomically:
   - If all tests pass, the inverse operations execute and the resource is
     reverted.
   - If any test fails (the field was changed by another actor, or a CRD
     version change altered the representation), the patch is rejected.
     Siteconfig reports the mismatch and surfaces it in the ClusterInstance
     status, allowing the administrator to resolve it manually.

4. Before sending the request, siteconfig appends an operation that removes the
   revert-patch annotation to the same JSON Patch document. Therefore, the
   inverse operations and annotation removal succeed or fail atomically. If
   any test or operation fails, the resource and annotation remain unchanged so
   siteconfig can retry or report the failure for manual resolution.

**Lifecycle disposition and retries:** The revert-patch annotation is also the
durable record that siteconfig adopted an existing resource. The initial `GET`
establishes the disposition before the first SSA apply: if the resource is
absent, SSA creates it without a revert-patch annotation; if it exists and is
not already claimed by the ClusterInstance, the SSA request atomically applies
the annotation, ownership label, and desired fields. On a resource-version
conflict, siteconfig retries from a fresh `GET`. On later reconciliations,
siteconfig does not recompute the original state when it finds either its
matching `owned-by` label or a revert-patch annotation captured for the same
ClusterInstance. During deprovisioning, after verifying the `owned-by` label,
siteconfig reverts resources with the annotation and deletes resources without
it. This metadata-based disposition survives controller restarts.

**Why this approach addresses the key considerations:**

- **Field additions and modifications** — JSON Patch supports both `remove`
  (for reverting added fields) and `replace` with the original value (for
  reverting modified fields), covering both cases in a single mechanism.
- **Concurrent modification safety** — the `test` operation is evaluated
  server-side as part of the same transaction as the inverse operation. There
  is no time-of-check to time-of-use (TOCTOU) race condition between checking
  the field value and modifying it during the revert. During provisioning, the
  SSA request uses the resource version observed while computing the patch, so
  a concurrent change causes a retry rather than applying a stale snapshot.
- **CRD version skew detection** — if a schema migration changed the field
  representation or removed it entirely, the `test` value will not match the 
  current value and the patch will fail safely rather than corrupting the
  resource.
- **Resource lifecycle** — the revert patch is self-contained on the resource
  it describes. No external state, no lifecycle management of ConfigMaps, no
  referential integrity concerns. Administrators can inspect the annotation to
  see exactly what siteconfig will revert.
- **Native Kubernetes support** — the API server natively supports
  `application/json-patch+json`; controller-runtime exposes it via
  `client.RawPatch(types.JSONPatchType, data)`.

**Limitations:**

- The Kubernetes per-object metadata size limit (256KB, shared across all
  annotations) could be a constraint if siteconfig modifies many large fields,
  though this is unlikely in practice for the expected use cases (e.g.,
  `spec.networkInterfaces`).
- The annotation itself is a modification to the resource that needs to be
  applied and cleaned up rather than an external ConfigMap (see
  [ConfigMap Storage for Revert Patches](#configmap-storage-for-revert-patches)
  in the Alternatives section for the rationale).
- If the revert patch fails (test mismatch), manual intervention is required.
  This is a deliberate safety trade-off — failing loudly is preferable to
  silently corrupting state.

**Implementation sketch:**

- Detect pre-existing resources at apply time by checking if the resource
  already exists before the first SSA apply; an absent resource is created and
  has no revert-patch annotation
- Compute the revert patch by diffing the current state against the rendered
  manifest
- Include the patch in the
  `siteconfig.open-cluster-management.io/revert-patch` annotation and the
  observed resource version in the same SSA request that applies changes to an
  existing resource; retry conflicts from a fresh read
- Reuse the matching `owned-by` label or revert-patch annotation on retries;
  do not recompute the original state
- On deprovisioning, revert a resource with the annotation; otherwise delete
  the siteconfig-created resource
- On a successful revert, remove the manifest reference from
  `ClusterInstance.Status.ManifestsRendered`; report a failed revert with the
  existing `deletion-failed` status and error message

### Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| BMO HostNetworkAttachment API is not yet published upstream | The prerequisite capabilities (multi-document YAML, revert-on-deprovision) can be developed and merged independently. The HNA-specific changes will be gated on the upstream BMO API being published (CNF-22902). |
| Multi-document YAML templates could produce malformed output | Each document is independently validated after parsing. Malformed documents fail rendering and trigger a requeue with error status. |
| Revert-on-deprovision could conflict with concurrent resource modifications | JSON Patch `test` operations provide atomic compare-and-swap semantics. If a field was changed by another actor, the revert fails safely and reports the mismatch. |
| HostNetworkAttachment CRs could be deleted while still referenced by BareMetalHosts | Sync-wave ordering ensures higher-wave resources (e.g., BMH at wave 3) are removed or reverted before lower-wave resources (e.g., HNA at wave 1) during deprovisioning. |
| An existing or shared HNA could be modified or deleted by SiteConfig | HNAs are rendered and managed only when listed at the cluster level. Existing HNAs referenced by nodes require an explicit namespace and are excluded from SiteConfig rendering, modification, and deletion. |

### Drawbacks

- Adds complexity to the ClusterInstance API with new fields that are only
  relevant when BMO supports the networking feature.
- The revert-on-deprovision capability adds complexity to the deprovisioning
  flow and requires careful testing to ensure pre-existing resources are
  properly restored.

## Design Details

### Test Plan

**Unit tests:**
- Multi-document YAML parsing in the template engine (`render()` and
  `renderManifestFromTemplate()`)
- HostNetworkAttachment template rendering for all three installation flows
- Rendering of managed and namespaced external HNA references in all three
  BareMetalHost templates
- Rendering of optional BMO `switchPort` identifiers on node bindings
- Cluster-level HNA rendering and node references to existing HNAs
- Validation of cluster-level HostNetworkAttachment templates (uniqueness,
  non-empty names)
- CRD schema validation of InterfaceRef name and MAC-address patterns
- Validation of node-level bindings (InterfaceRef exclusivity, reference
  integrity, managed and external HNA namespace rules)
- Revert-on-deprovision logic for pre-existing resources

**Integration tests:**
- End-to-end rendering of a ClusterInstance with HostNetworkAttachments
- Deprovisioning flow including revert of pre-existing BMH modifications
- Reinstall flow with HostNetworkAttachments
- BMO switch-port identification with and without `switchPort`
- Verification that external HNAs remain unchanged during deprovisioning and
  reinstall

**Edge cases to cover:**
- ClusterInstance with no HostNetworkAttachments (backward compatibility)
- Node with empty HostNetworkAttachments list versus nil
- Multiple nodes referencing the same HostNetworkAttachment template
- Node references to existing HNAs, including HNAs shared by multiple ClusterInstances
- Templates that produce zero documents (conditional rendering when no HNAs
  defined)

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] Multi-document YAML template support implemented with tests
- [ ] HostNetworkAttachment API fields, templates, and validation implemented
      with tests
- [ ] Revert-on-deprovision mechanism implemented with tests
- [ ] BMO HostNetworkAttachment API published upstream and added as a dependency
- [ ] Documented in user-facing docs
- [ ] Released in version 2.18.0

### Upgrade / Downgrade Strategy

**Upgrade**: No migration is required for existing ClusterInstance CRs that do
not use this feature. The new `hostNetworkAttachmentTemplates` and
`hostNetworkAttachments` fields are optional and have no CRD or webhook
default; when omitted from an existing CR, they remain absent after upgrade and
render no HostNetworkAttachment resources. Introducing a default for either
field in the future requires an explicit migration and post-provisioning
validation strategy. 

**Downgrade**: Clusters deployed with HostNetworkAttachments would lose the
ability to manage those resources through siteconfig. The HostNetworkAttachment
CRs and BMH `networkInterfaces` fields would remain in the cluster but would no
longer be reconciled by siteconfig. Manual cleanup may be required.

### Version Skew Strategy

The siteconfig operator depends on the BMO HostNetworkAttachment CRD being
present in the cluster. If the CRD is not installed (older BMO version), the
HostNetworkAttachment template still renders the resource, but server-side
dry-run validation fails. The controller sets `RenderedTemplatesValidated` to
failed and does not enter the actual apply phase; because the full rendered
manifest list is validated before apply, this blocks the deployment. The
controller requeues using the existing validation-error flow.

Since HostNetworkAttachments are only rendered when
`spec.hostNetworkAttachmentTemplates` is populated, clusters that do not use this
feature are unaffected by CRD availability.

## Implementation History

- 2026-05-08: Initial proposal created

## Alternatives

### Server-Side Apply Field Ownership for Revert

Siteconfig could issue an SSA apply on deprovisioning that omits
siteconfig-managed fields, causing SSA to automatically remove them. While this
leverages existing infrastructure and requires no additional state, it can only
remove fields — it cannot restore original values for fields that siteconfig
modified (it would remove them entirely). Additionally, siteconfig uses
`ForceOwnership` on apply, which reclaims field ownership from other
controllers on each reconciliation, undermining the concurrent modification
safety that SSA field ownership would otherwise provide.

### Snapshot Backup/Restore for Revert

Siteconfig could snapshot the pre-existing resource state into a ConfigMap 
before modification and restore it on deprovisioning. This would provide an
additional benefit of cleaning up fields that must be reset in the `hostRef`
workflow to restore a BMH back to its original state -- today, that use case
is not handled properly as high level applications must perform this cleanup
separately as a workaround.  While this approach does solve multiple problems
it does introduce more complexity as it introduces external state that
must be managed (creation, lifecycle, cleanup).  It is affected by the
same pitfalls as the revert-patch in regard to what happens when a resource
schema changes between provisioning and deprovisioning.  It also lacks the
atomic compare-and-swap safety of JSON Patch `test` operations.

### ConfigMap Storage for Revert Patches

The revert patch (used by the recommended JSON Patch approach) could be stored
in a ConfigMap rather than as an annotation on the modified resource. This was
not selected because:

- **Lifecycle complexity** — ConfigMap-based storage would require creating a
  ConfigMap per modified resource (or a shared ConfigMap with keyed entries),
  handling cleanup on successful revert, orphan detection if the resource is
  deleted externally, and preservation during reinstall workflows. Annotations
  are created and removed as part of the resource's own patch operations.
- **Referential integrity** — the revert patch and the resource it describes
  can get out of sync (e.g., ConfigMap deleted accidentally, resource recreated
  without the corresponding ConfigMap entry). With annotations, the patch is
  co-located with the resource and travels with it if the resource is moved or
  the namespace is migrated.
- **RBAC expansion** — ConfigMap storage would require additional RBAC
  permissions and coordination with the existing PreservationHandler to avoid
  conflicts. Annotations are part of the resource metadata and require no
  additional permissions.

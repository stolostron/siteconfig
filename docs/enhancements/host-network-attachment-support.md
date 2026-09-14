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
last-updated: 2026-05-08
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
pre-existing resources, which is needed because in O-Cloud Manager Cluster
Lifecycle Manager (CLM) deployments BareMetalHosts are pre-created as available
server inventory and siteconfig must undo its modifications (e.g., adding
`spec.networkInterfaces`) when the cluster is deprovisioned so that the BMH
returns to the available inventory pool.

> **Note:** The HostNetworkAttachment CRD and the `spec.networkInterfaces`
> field on BareMetalHost are being introduced in upstream BMO and are still
> under active development (see [metal3-docs#586](https://github.com/metal3-io/metal3-docs/pull/586)).
> These changes have not yet been merged. This proposal is put forward now so
> that work on the prerequisite capabilities (multi-document YAML support and
> revert-on-deprovision) can proceed in parallel with the upstream BMO work.

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
   separate Kubernetes resource. The existing template engine only supported
   single-document output. Multi-document YAML support allows a single template
   to use a range loop and produce one HostNetworkAttachment CR per entry,
   separated by `---` document markers.

2. **Revert-on-deprovision for pre-existing resources** — In O-Cloud Manager
   CLM deployments, BareMetalHost resources are pre-created as available server
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

- **Lifecycle**: HostNetworkAttachment CRs are rendered at sync-wave 1 (before
  BareMetalHosts at wave 3 for AI/HCP). In the IBI flow, both BMH and HNA are
  at wave 1; however, ordering is still guaranteed because siteconfig processes
  cluster-level templates before node-level templates within each sync-wave.
  Since HNA is a cluster-level template and BMH is a node-level template, HNAs
  are always applied before BMHs regardless of sync-wave. On deprovisioning,
  siteconfig-created resources are deleted in descending sync-wave order, and
  pre-existing resources that were modified are reverted. On reinstall, all
  rendered manifests are deleted and re-created from the current spec.
- **Monitoring**: No new status conditions are introduced. Existing
  `RenderedTemplatesApplied` condition covers HostNetworkAttachment rendering.
  Structured logs include the HostNetworkAttachment resource ID for debugging.
- **Remediation**: Standard siteconfig remediation patterns apply. If rendering
  fails, the controller requeues with a 30-second delay. Administrators can use
  the pause annotation to halt reconciliation for manual intervention.
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
   of the Baremetal Operator (tracked under CNF-22902).
2. Validating that interface names/MAC addresses in `InterfaceRef` match actual
   hardware — that is deferred to BMO's runtime validation after host
   inspection.
3. Supporting day-2 modifications to HostNetworkAttachments on a provisioned
   cluster — they are immutable post-provisioning, consistent with BMO's
   enforcement.
4. Implementing the actual switch port configuration protocol — that is handled
   by BMO's switch management integration.

## Proposal

### Workflow Description

#### Defining HostNetworkAttachments

1. The cluster administrator defines HostNetworkAttachment templates at the
   cluster level in `spec.hostNetworkAttachments`, specifying a unique name and
   the switchport configuration (mode, nativeVLAN, allowedVLANs, MTU) for each.

2. For each node that requires switch port configuration, the administrator
   defines bindings in `spec.nodes[].hostNetworkAttachments` that associate a
   network interface (by name or MAC address) with a cluster-level
   HostNetworkAttachment template.

3. The administrator submits the ClusterInstance CR.

#### Rendering and Application

4. The validating webhook validates the ClusterInstance, including:
   - Unique HostNetworkAttachment template names
   - Valid InterfaceRef (exactly one of name or macAddress)
   - Valid references from node bindings to cluster-level templates

5. The ClusterInstanceReconciler renders templates:
   - **Cluster-level**: The HostNetworkAttachment template iterates over
     `spec.hostNetworkAttachments` and produces one HostNetworkAttachment CR per
     entry, separated by `---` document markers. The multi-document YAML parser
     splits these into individual RenderedObjects.
   - **Node-level**: The BareMetalHost template conditionally adds
     `spec.networkInterfaces` entries that reference the rendered
     HostNetworkAttachment CRs by namespace and name.

6. Objects are applied via Server-Side Apply in sync-wave order:
   - Wave 1: HostNetworkAttachment CRs (and other wave-1 resources)
   - Wave 3: BareMetalHost CRs with `spec.networkInterfaces` referencing the
     HostNetworkAttachments (for AI/HCP flows)

#### Deprovisioning

7. When the ClusterInstance is deleted:
   - Rendered manifests that were created by siteconfig are deleted in
     descending sync-wave order (e.g., ManagedCluster at wave 2, then
     ClusterDeployment and HostNetworkAttachment at wave 1)
   - Sync-wave ordering ensures higher-wave resources are removed before
     lower-wave resources they depend on

8. For pre-existing resources that were modified (not created) by siteconfig
   (e.g., pre-existing BareMetalHosts that had `spec.networkInterfaces` added):
   - Modifications are reverted to restore the resource to its original state
     (see [Revert-on-Deprovision](#revert-on-deprovision-for-pre-existing-resources)
     below)

#### Reinstallation

9. During a reinstall workflow:
   - HostNetworkAttachment CRs are deleted as part of the standard rendered
     manifest deletion (descending sync-wave order)
   - After deletion completes, templates are re-rendered from the current
     ClusterInstance spec and re-applied
   - No special `ReinstallPermissions` entry is needed — the
     HostNetworkAttachment fields are not mutated in place during reinstall;
     the resources are fully deleted and recreated from the current spec

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

  hostNetworkAttachments:
    - name: storage-trunk
      spec:
        mode: trunk
        nativeVLAN: 100
        allowedVLANs: [101, 102, 103]
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
          hostNetworkAttachmentName: storage-trunk
        - interfaceRef:
            macAddress: "AA:BB:CC:DD:EE:FF"
          hostNetworkAttachmentName: management-access
```

This produces the following rendered resources (among others). Each
HostNetworkAttachment is a separate Kubernetes resource; they are shown below
as a multi-document YAML stream separated by `---`:

**HostNetworkAttachment CRs** (cluster-level, sync-wave 1):

```yaml
apiVersion: metal3.io/v1alpha1
kind: HostNetworkAttachment
metadata:
  name: storage-trunk
  namespace: example-cluster
spec:
  mode: trunk
  nativeVLAN: 100
  allowedVLANs: [101, 102, 103]
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

**BareMetalHost** (node-level, with networkInterfaces):

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
        namespace: example-cluster
        name: storage-trunk
    - macAddress: "AA:BB:CC:DD:EE:FF"
      hostNetworkAttachment:
        namespace: example-cluster
        name: management-access
```

### API Extensions

#### New Types

**HostNetworkAttachmentTemplate** — wraps the BMO `HostNetworkAttachmentSpec`
with a name for rendering:

```go
type HostNetworkAttachmentTemplate struct {
    Name string                              `json:"name"`
    Spec bmh_v1alpha1.HostNetworkAttachmentSpec `json:"spec"`
}
```

**InterfaceRef** — selects a network interface by name or MAC address (mutually
exclusive):

```go
type InterfaceRef struct {
    Name       *string `json:"name,omitempty"`
    MACAddress *string `json:"macAddress,omitempty"`
}
```

**HostNetworkAttachment** — binds a node interface to a cluster-level
HostNetworkAttachment template:

```go
type HostNetworkAttachment struct {
    InterfaceRef              InterfaceRef `json:"interfaceRef"`
    HostNetworkAttachmentName string       `json:"hostNetworkAttachmentName"`
}
```

#### Modified Types

**ClusterInstanceSpec** — new optional field:

```go
HostNetworkAttachments []HostNetworkAttachmentTemplate `json:"hostNetworkAttachments,omitempty"`
```

**NodeSpec** — new optional field:

```go
HostNetworkAttachments *[]HostNetworkAttachment `json:"hostNetworkAttachments,omitempty"`
```

The `NodeSpec` field uses a pointer to a slice to distinguish between "not
configured" (nil) and "explicitly empty" (empty slice).

#### Immutability

Both `spec.hostNetworkAttachments` and `spec.nodes[].hostNetworkAttachments`
are immutable post-provisioning. This is consistent with BMO's enforcement that
a HostNetworkAttachment referenced by a BareMetalHost cannot be modified.

> **Note:** If day-2 mutability were desired in the future, it would need to
> conform to BMO's constraints: a BareMetalHost would need to be deprovisioned
> before its `spec.networkInterfaces` could be changed, and a
> HostNetworkAttachment would need to be detached from all referencing
> BareMetalHosts before it could be modified.

### Siteconfig Impact

- **Controllers**: `ClusterInstanceReconciler` is modified to register the
  HostNetworkAttachment template. No changes to `ClusterDeploymentReconciler`
  or `ConfigurationMonitor`.
- **Templates**: New `HostNetworkAttachment` cluster-level template added to
  all three installation flows (AI, IBI, HCP). Existing `BareMetalHost`
  node-level templates updated to conditionally render `spec.networkInterfaces`.
- **API fields**: New fields added to `ClusterInstanceSpec` and `NodeSpec` as
  described above.
- **Validation**: New webhook validation functions for HostNetworkAttachment
  template uniqueness and node-level reference integrity.
- **Template engine**: `render()` updated to support multi-document YAML
  parsing; `renderManifestFromTemplate()` updated to return `[]RenderedObject`.
- **RBAC**: New permissions added for `hostnetworkattachments` resources
  (get, list, watch, create, update, patch, delete).

### Revert-on-Deprovision for Pre-existing Resources

In O-Cloud Manager CLM deployments, BareMetalHost resources are pre-created
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
   state no longer matches the current schema.

4. **Resource lifecycle** — Pre-existing resources may have their own lifecycle
   managed by other controllers. The revert mechanism must not interfere with
   fields or state managed by those controllers.

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
     {"op": "test", "path": "/spec/someField", "value": "what-siteconfig-set"},
     {"op": "replace", "path": "/spec/someField", "value": "original-value"}
   ]
   ```

2. Siteconfig writes the revert patch to an annotation on the resource (e.g.,
   `siteconfig.open-cluster-management.io/revert-patch`) **before** applying
   any spec modifications. This ensures the revert information is persisted
   first — if siteconfig crashes after writing the annotation but before
   applying the spec changes, the annotation is already in place and the next
   reconciliation will proceed normally. If deprovisioning were triggered in
   that window, the `test` operation would fail (the spec fields have not been
   set yet) and the revert would safely no-op.

3. Siteconfig applies the spec modifications via SSA.

4. On deprovisioning, siteconfig reads the annotation and issues the JSON Patch
   via `client.RawPatch(types.JSONPatchType, data)`. The API server evaluates
   the `test` operations atomically:
   - If all tests pass, the inverse operations execute and the resource is
     reverted.
   - If any test fails (the field was changed by another actor, or a CRD
     version change altered the representation), the patch is rejected.
     Siteconfig reports the mismatch and surfaces it in the ClusterInstance
     status, allowing the administrator to resolve it manually.

5. On successful revert, the annotation is removed as a final cleanup step.

**Why this approach addresses the key considerations:**

- **Field additions and modifications** — JSON Patch supports both `remove`
  (for reverting added fields) and `replace` with the original value (for
  reverting modified fields), covering both cases in a single mechanism.
- **Concurrent modification safety** — the `test` operation is evaluated
  server-side as part of the same transaction as the inverse operation. There
  is no time-of-check to time-of-use (TOCTOU) race condition between checking
  the field value and modifying it, unlike client-side approaches that read and
  then patch in separate API calls.
- **CRD version skew resilience** — if a schema migration changed the field
  representation, the `test` value will not match the current value and the
  patch will fail safely rather than corrupting the resource.
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
  applied and cleaned up.
- If the revert patch fails (test mismatch), manual intervention is required.
  This is a deliberate safety trade-off — failing loudly is preferable to
  silently corrupting state.

The revert patch is stored as an annotation on the modified resource itself
rather than in an external ConfigMap (see
[ConfigMap Storage for Revert Patches](#configmap-storage-for-revert-patches)
in the Alternatives section for the rationale).

**Implementation sketch:**

- Detect pre-existing resources at apply time by checking if the resource
  already exists before the first SSA apply
- Compute the revert patch by diffing the current state against the rendered
  manifest
- Store the patch in the
  `siteconfig.open-cluster-management.io/revert-patch` annotation
- On deprovisioning, apply the revert patch instead of deleting the resource
- Track revert outcomes in `ClusterInstance.Status.ManifestsRendered` (e.g.,
  status "reverted" or "revert-failed")

### Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| BMO HostNetworkAttachment API is not yet published upstream | The prerequisite capabilities (multi-document YAML, revert-on-deprovision) can be developed and merged independently. The HNA-specific changes will be gated on the upstream BMO API being published (CNF-22902). |
| Multi-document YAML templates could produce malformed output | Each document is independently validated after parsing. Malformed documents fail rendering and trigger a requeue with error status. |
| Revert-on-deprovision could conflict with concurrent resource modifications | JSON Patch `test` operations provide atomic compare-and-swap semantics. If a field was changed by another actor, the revert fails safely and reports the mismatch. |
| HostNetworkAttachment CRs could be deleted while still referenced by BareMetalHosts | Sync-wave ordering ensures higher-wave resources (e.g., BMH at wave 3) are removed or reverted before lower-wave resources (e.g., HNA at wave 1) during deprovisioning. |

### Drawbacks

- Adds complexity to the ClusterInstance API with new fields that are only
  relevant when BMO supports the networking feature.
- The revert-on-deprovision capability adds complexity to the deprovisioning
  flow and requires careful testing to ensure pre-existing resources are
  properly restored.

## Design Details

### Open Questions

1. Should the revert-patch annotation be applied at the same time as the
   initial SSA apply, or as a separate patch operation? Combining them
   reduces round-trips but mixes SSA and annotation concerns.
2. When a revert patch fails (test mismatch), should siteconfig pause
   reconciliation and require manual intervention, or should it report the
   failure in status and continue deprovisioning other resources?

### Test Plan

**Unit tests:**
- Multi-document YAML parsing in the template engine (`render()` and
  `renderManifestFromTemplate()`)
- HostNetworkAttachment template rendering for all three installation flows
- Validation of cluster-level HostNetworkAttachment templates (uniqueness,
  non-empty names)
- Validation of node-level bindings (InterfaceRef exclusivity, reference
  integrity)
- Revert-on-deprovision logic for pre-existing resources

**Integration tests:**
- End-to-end rendering of a ClusterInstance with HostNetworkAttachments
- Deprovisioning flow including revert of pre-existing BMH modifications
- Reinstall flow with HostNetworkAttachments

**Edge cases to cover:**
- ClusterInstance with no HostNetworkAttachments (backward compatibility)
- Node with empty HostNetworkAttachments list versus nil
- Multiple nodes referencing the same HostNetworkAttachment template
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

**Upgrade**: No migration required. The new `hostNetworkAttachments` fields are
optional and default to empty/nil. Existing ClusterInstance CRs continue to
work without modification. The CRD is updated with the new fields via the
standard operator upgrade process.

**Downgrade**: Clusters deployed with HostNetworkAttachments would lose the
ability to manage those resources through siteconfig. The HostNetworkAttachment
CRs and BMH `networkInterfaces` fields would remain in the cluster but would no
longer be reconciled by siteconfig. Manual cleanup may be required.

### Version Skew Strategy

The siteconfig operator depends on the BMO HostNetworkAttachment CRD being
present in the cluster. If the CRD is not installed (older BMO version), the
HostNetworkAttachment template produces resources that will fail to apply. This
is handled by the existing rendering error flow — the `RenderedTemplatesApplied`
condition will report the failure, and the controller will requeue.

Since HostNetworkAttachments are only rendered when
`spec.hostNetworkAttachments` is populated, clusters that do not use this
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

Siteconfig could snapshot the pre-existing resource state (or specific fields)
into a ConfigMap before modification and restore it on deprovisioning. While
this can restore original values, snapshots become stale if the resource is
modified by another controller after provisioning — restoring a stale snapshot
would silently overwrite those changes. It also introduces external state that
must be managed (creation, lifecycle, cleanup) and is vulnerable to CRD version
skew if the resource schema changes between provisioning and deprovisioning.
The snapshot approach lacks the atomic compare-and-swap safety of JSON Patch
`test` operations.

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

The annotation size limit (256KB) is not a practical concern for the expected
use cases, where siteconfig modifies a small number of well-defined fields
(e.g., `spec.networkInterfaces`).

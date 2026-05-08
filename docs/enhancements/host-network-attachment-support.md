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
last-updated: 2026-10-28
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
   `spec.networkInterfaces`). Currently, siteconfig can create resources on
   installation and delete them on deprovisioning, but it cannot revert
   modifications to resources it did not create. A revert mechanism is needed
   so that SiteConfig restores its own changes when the cluster is deprovisioned,
   while preserving unrelated changes by other controllers. This applies to
   every pre-existing rendered resource, including inventory BMHs.

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
BareMetalHost resources during installation to be reverted on deprovisioning,
preserving unrelated changes, so that the BMH can be reused for a different
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
  eligible rendered resources are deleted or restored before reapplying the
  current spec; existing reinstall exclusions, such as ManagedCluster, remain.
  These waves establish resource order, not switch readiness.
- **Monitoring**: Existing status conditions report validation and application.
  `RenderedTemplatesValidated` reports server-side validation failures, such as
  a missing HostNetworkAttachment CRD, before any manifests are applied.
  `RenderedTemplatesApplied` reports the subsequent actual apply phase.
  Structured logs include the HostNetworkAttachment resource ID for debugging.
  Restoration conflicts report the failing manifest, pause reconciliation, and
  retain the CI finalizer during deletion. Reinstall uses the existing cleanup
  condition with a `RestorationFailed` reason.
- **Remediation**: Standard siteconfig remediation patterns apply. If rendering
  or server-side validation fails, the controller requeues with a 30-second
  delay. Restoration conflicts require correction and explicit unpause, as
  described in [Failure and Recovery](#failure-and-recovery). Concurrent writes
  and transient restoration errors retry without that pause.
- **Scale**: No impact on `maxConcurrentReconciles`. HostNetworkAttachment
  rendering scales linearly with the number of attachments defined per cluster
  (typically a small number per deployment).

### Goals

1. Enable declarative TOR switch port configuration through the ClusterInstance
   API for all installation flows.
2. Maintain backward compatibility — clusters without HostNetworkAttachments
   are unaffected.
3. (Prerequisite) Support multi-document YAML rendering in the template engine
   so that a single template can produce multiple Kubernetes resources.
4. (Prerequisite) Provide a mechanism to revert modifications to pre-existing
   resources (such as BareMetalHosts) when a ClusterInstance is deprovisioned.

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
     per entry, separated by `---` document markers. The multi-document YAML
     parser splits these into individual RenderedObjects.
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

   Creating an HNA CR does not establish networking readiness. The supported
   BMO runtime and installation flow must ensure that networking is configured
   before provisioning requires it; SiteConfig adds no HNA readiness gate.

#### Deprovisioning

7. When the ClusterInstance is deleted:
   - Rendered manifests that were created by siteconfig are deleted in
     descending sync-wave order (e.g., ManagedCluster at wave 2, then
     ClusterDeployment and HostNetworkAttachment at wave 1)
   - External HNAs merely referenced by nodes are not rendered by SiteConfig and remain
     untouched.
   - Sync-wave ordering ensures higher-wave resources are removed before
     lower-wave resources they depend on

8. For pre-existing resources that were modified (not created) by siteconfig
   (e.g., pre-existing BareMetalHosts that had `spec.networkInterfaces` added):
   - SiteConfig's modifications are reverted while unrelated changes survive
     (see [Revert-on-Deprovision](#revert-on-deprovision-for-pre-existing-resources)
     below)

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
   - Failed cleanup must retry after correction and unpause. Only a completed
     cleanup condition permits later reinstall phases to advance. If a resource
     is still waiting on finalizers, cleanup remains in progress.

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
The CRD inherits the upstream schema and validation markers. The upstream type
marks optional selectors and switch port explicitly; its required attachment
member follows BMO's implicit required-field convention. SiteConfig's webhook
enforces exactly one nonempty selector and validates HNA references.

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
`hostNetworkAttachmentTemplates`. Permission to edit the CI spec is distinct
from deleting or restoring the resources rendered from it.

### Siteconfig Impact

- **Controllers**: `ClusterInstanceReconciler` is modified to register the
  HostNetworkAttachment template and adopt pre-existing rendered resources.
  The shared deletion handler restores adopted resources for finalization,
  pruning, and reinstall, including discovery of served API versions during
  cleanup. Reinstall retries previously failed cleanup after unpause.
  No changes to `ClusterDeploymentReconciler` or `ConfigurationMonitor`.
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

SiteConfig adopts an eligible pre-existing rendered resource and restores its
own changes when the CI is deleted, the resource is pruned, or reinstall cleans
it up. This applies to every resource kind, including an inventory BMH rendered
through `hostRef`. Merely referencing an external resource does not adopt it.
SiteConfig-created resources retain their normal deletion behavior.

#### Claims and Lifecycle Disposition

Before applying, SiteConfig checks the live ownership label. A resource claimed
by a different CI is rejected; the shared SSA field manager does not establish
exclusive ownership between CIs. This claim check is a standalone prerequisite
fix. Adoption also validates any existing recovery record's owner against the
same live resource version used by the apply.

* **Absent resource:** use `Create`, not an SSA upsert. If another creator wins
  the race, `AlreadyExists` ends the attempt without modifying that resource.
  The next reconciliation reads it again and performs ownership/adoption
  checks. A successful create uses no recovery annotations and retains normal
  SSA field-removal behavior on subsequent applies.
* **Unclaimed pre-existing resource:** capture a recovery record and store it,
  an `adopted: "true"` annotation, the ownership label, and the desired changes
  together in the real SSA request, guarded by the observed resource version.
* **Existing adopted resource:** preserve its original field values and include
  recovery metadata in subsequent SSA requests. The annotation must not be
  dropped through SSA omission. Missing or invalid recovery data with an
  adopted marker blocks processing rather than authorizing deletion.
* **Matching ownership label without recovery annotations:** retain the existing
  created/legacy lifecycle behavior. There is no reliable baseline to reconstruct
  for resources already managed before this feature was installed.

Recovery records identify the CI by namespace/name, encoded as `namespace_name`
like the ownership label. They do not bind recovery to the CI UID: a CI restored
from backup with the same namespace/name can resume even if its UID changes.

#### Field Scope and Capture

An SSA dry-run computes the actual changes after schema-aware merging,
defaulting, and normalization. The recovery record stores each changed field's
value before its first actual modification. A later apply preserves the original
baseline and captures newly changed fields at that time. The real SSA request
persists the recovery record and desired changes atomically under a
resource-version guard. A stale write retries from a fresh read; an oversized
annotation rejects that request without persisting either part.

The scope includes all actual SiteConfig changes, not only BMH
`spec.networkInterfaces`:

* Granular maps, labels, and annotations are tracked by entry. Unrelated entries
  survive restoration. Lists, including associative/set lists, and schema-defined
  atomic maps are tracked as whole values. Restoration overwrites later changes
  to tracked fields, including changes made by collaborating controllers;
  already-restored fields need no further change.
* Status and system metadata are excluded. Templates adopting existing resources
  must not explicitly manage status, finalizers, or owner references. Secret
  `stringData` is normalized to the persisted `data` representation.
* Active reconciliation retains forced SSA: rendered values win while the CI
  is active. Recovery data supplies the original values SSA ownership alone
  cannot recover; it does not change active reconciliation into cooperative
  ownership of rendered fields.
* Template omission restores the removed field's baseline immediately and
  releases its recovery tracking. The SSA request first marks that field as
  retiring, so an interrupted reconciliation completes restoration before the
  next apply. This partial restoration retains the claim and recovery metadata
  for remaining fields. A field changed again later gets a new baseline.
  Initially equal fields are also preserved if SSA omission would otherwise
  remove their pre-existing value.

#### Recovery Annotation and Examples

The two reserved annotations are:

* `siteconfig.open-cluster-management.io/revert-patch`: a compact JSON recovery
  record, **not an executable JSON Patch**.
* `siteconfig.open-cluster-management.io/adopted: "true"`: an independent marker
  that prevents a lost record from turning an adopted resource into a deletable
  created resource.

Templates cannot supply these annotations. Format version 1 stores the CI owner,
the API representation most recently accepted, and original field values.
An omitted `original` means the field was absent; explicit `null`,
`false`, `0`, and empty values remain present values. Paths are JSON Pointers,
escaping `~` as `~0` and `/` as `~1`. Optional `retiring` marks pending field
restoration; optional `parents` lists maps introduced by SiteConfig, which are
removed only if empty so other writers' entries survive.

**Example: add networking and change power intent.** An inventory BMH initially
has `spec.online: false` and no `spec.networkInterfaces`. CI
`example-cluster/example-cluster` renders `online: true` and an HNA binding. Its
recovery annotation contains the following JSON, shown expanded for readability
(the stored annotation is compact):

```json
{
  "version": 1,
  "owner": "example-cluster_example-cluster",
  "apiVersion": "metal3.io/v1alpha1",
  "fields": [
    {
      "path": "/metadata/labels/siteconfig.open-cluster-management.io~1owned-by"
    },
    {
      "path": "/spec/networkInterfaces"
    },
    {"path": "/spec/online", "original": false}
  ]
}
```

This example assumes the metadata maps already existed and these were the only
changed fields. The missing `original` on `networkInterfaces` means restoration
removes the list; `online` is restored to `false`. If the BMH already had a
networking list, that field would include its entire prior list as `original`
and restoration would replace the current live list with it. For example:

```json
{
  "path": "/spec/networkInterfaces",
  "original": [
    {"name": "eno1", "hostNetworkAttachment": {"name": "idle-net", "namespace": "hardware"}}
  ]
}
```

**Example: generated restoration request.** For the first example, assume the
live values are still those rendered, the resource version is `12345`, and
unrelated labels/annotations keep those maps nonempty. The controller generates
a JSON Patch with the following operations. `<current BMH UID>` and `<exact saved
record>` stand for the actual UID and compact annotation string; they are not
literal values sent to the API. Operation order may vary after the guards.

```json
[
  {"op": "replace", "path": "/metadata/resourceVersion", "value": "12345"},
  {"op": "test", "path": "/metadata/uid", "value": "<current BMH UID>"},
  {"op": "test", "path": "/spec/online", "value": true},
  {"op": "replace", "path": "/spec/online", "value": false},
  {
    "op": "test",
    "path": "/spec/networkInterfaces",
    "value": [
      {"name": "eno1", "hostNetworkAttachment": {"name": "storage-trunk", "namespace": "example-cluster"}}
    ]
  },
  {"op": "remove", "path": "/spec/networkInterfaces"},
  {
    "op": "test",
    "path": "/metadata/labels/siteconfig.open-cluster-management.io~1owned-by",
    "value": "example-cluster_example-cluster"
  },
  {"op": "remove", "path": "/metadata/labels/siteconfig.open-cluster-management.io~1owned-by"},
  {"op": "test", "path": "/metadata/annotations/siteconfig.open-cluster-management.io~1adopted", "value": "true"},
  {"op": "remove", "path": "/metadata/annotations/siteconfig.open-cluster-management.io~1adopted"},
  {"op": "test", "path": "/metadata/annotations/siteconfig.open-cluster-management.io~1revert-patch", "value": "<exact saved record>"},
  {"op": "remove", "path": "/metadata/annotations/siteconfig.open-cluster-management.io~1revert-patch"}
]
```

For the second example, the networking inverse is a `replace` with the saved
`idle-net` list rather than `remove`, still preceded by a test of the current live
list. These tests use freshly observed values, not values stored in the recovery
record. Later changes to `online` or the tracked list are overwritten with their
baselines. An unrelated annotation or BMO status update survives. A write between
observation and patch submission invalidates the resource-version guard and
causes a fresh-read retry.

During interrupted field retirement,
`{"path":"/spec/example","original":"prior","retiring":true}` means the
template omitted that field and its original value must now be restored.

#### Atomic Restoration and Completion

Cleanup validates the recovery owner and current ownership label, then constructs
the intended restored object from the saved original values regardless of later
changes to tracked fields. It generates an invertible JSON Patch with value tests,
resource-version and UID guards. Before committing it, SiteConfig dry-runs that
exact patch and verifies the returned content matches the intended restoration.
Schema pruning, defaulting, or admission that changes the intended result blocks
restoration while preserving the recovery data. Admission must behave consistently
between the preview and real request.

Final restoration removes `owned-by`, `adopted`, and `revert-patch` in the same
persisted patch as the field changes. If updating `ManifestsRendered` afterward
fails, the retry encounters an unowned resource and completes bookkeeping without
deleting it. No permanent completion marker is needed; later adoption starts a
new baseline. Restoration is atomic per resource, not across all resources;
normal descending sync waves still apply.

A resource carrying either recovery annotation is processed through restoration
before the ordinary unowned-resource skip. A missing or changed ownership label
therefore causes a conflict rather than silently completing cleanup. 

#### Failure and Recovery

A restoration conflict immediately pauses reconciliation, reports the failing
manifest with `deletion-failed` and an error, and retains the CI finalizer during
deletion. Reinstall reports `RestorationFailed`. The resource and recovery data
remain unchanged; already completed resources are not rolled back. This does
not depend on the 30-minute deletion timeout, which requires a target deletion
timestamp that a rejected restoration never acquires.

Concurrent-write conflicts and transient restoration API errors retry normally.
Other generic reinstall cleanup failures can still pause, but correction and
unpause now retry the failed cleanup phase instead of skipping it. Reinstall
advances only after cleanup completes.

To recover from a restoration conflict:

1. Inspect the CI's manifest messages and paused status for the failed validation
   or API operation.
2. Resolve ownership, schema, or admission problems that prevent restoration.
   Keep recovery annotations intact. Invalid or missing records require repair
   from trusted data; SiteConfig does not guess a baseline. Later changes to
   tracked values do not require correction.
3. Remove the pause annotation to retry:

   ```sh
   oc annotate clusterinstance <name> -n <namespace> \
     clusterinstance.siteconfig.open-cluster-management.io/paused-
   ```

#### API Representation Changes

SiteConfig can preserve and restore adopted resources when their templates move
between compatible, served versions of the same API resource. The API server
must preserve the fields needed for restoration; SiteConfig does not translate
renamed or reshaped fields, and compatibility is not guaranteed for every
version pair.

If a template targets an unserved version, apply fails. During cleanup, if the
recorded version is no longer served, SiteConfig can try another served version
of the same API group, kind, and scope. Restoration stops if no compatible
version is available or the API rejects a saved original value. An unavailable
endpoint alone does not mean the resource is absent.

#### Limitations

* The annotation-size limit (256 KiB total per object) bounds recovery data.
  Large records reject the apply atomically. Annotations inherit the resource's
  access controls and backup lifecycle and may contain original sensitive values.
* Restoring a tracked list or atomic map overwrites later changes anywhere in
  that value, including different elements changed by another writer.
* Parent namespace deletion or external owner deletion can still remove a
  resource independently of SiteConfig restoration.
* Downgrading while adopted resources remain active is unsupported, as described
  below. Existing legacy resources do not acquire a retrospective baseline.

### Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Multi-document YAML templates could produce malformed output | Each document is independently validated after parsing. Malformed documents fail rendering and trigger a requeue with error status. |
| Revert-on-deprovision could conflict with concurrent resource modifications | Restore tracked fields to their saved baselines, preserving untracked fields. Dry-run the intended restoration, then commit one conditional patch with resource-version and UID guards; concurrent writes cause a fresh-read retry. |
| HostNetworkAttachment CRs could be deleted while still referenced by BareMetalHosts | Sync-wave ordering ensures higher-wave resources (e.g., BMH at wave 3) are removed or reverted before lower-wave resources (e.g., HNA at wave 1) during deprovisioning. |
| An existing or shared HNA could be modified or deleted by SiteConfig | External HNAs merely referenced by nodes require an explicit namespace and are not rendered or modified. An HNA explicitly rendered from the cluster-level list is adopted/restored if it already exists and is eligible, or created/deleted otherwise. |

### Drawbacks

- Adds complexity to the ClusterInstance API with new fields that are only
  relevant when BMO supports the networking feature.
- The revert-on-deprovision capability adds complexity to the deprovisioning
  flow and requires careful testing to ensure pre-existing resources are
  properly restored.

## Design Details

### Test Plan

The implementation adds a focused API-server integration suite for generic
resource restoration. These tests use focused resource and BMH schemas; they do
not run BMO, configure switches, provision hardware, or prove CLM behavior.

**Unit/controller coverage in the implementation:**

- Multi-document YAML parsing in the template engine (`render()` and
  `renderManifestFromTemplate()`)
- HostNetworkAttachment template rendering for all three installation flows
- Rendering of managed and namespaced external HNA references in all three
  BareMetalHost templates
- Rendering of optional BMO `switchPort` identifiers on node bindings
- Cluster-level HNA rendering and node references to existing HNAs
- Validation of cluster-level HostNetworkAttachment templates (uniqueness,
  non-empty names)
- Validation of node-level bindings (interface selector exclusivity, reference
  integrity, managed and external HNA namespace rules)
- Both new lists omitted versus empty through post-provisioning validation,
  including the reverse transition used by GitOps reconciliation
- HNA template/binding changes rejected without reinstall and allowed with it
- Competing CI claims, create-only races, logical owner identity after CI
  recreation, and resource-version guards
- Missing/invalid recovery data, field retirement, per-resource conflicts,
  correction/unpause, and finalizer retention
- Successful restoration followed by CI status-write failure and retry during
  finalization, pruning, and reinstall, without deleting the restored resource
- Generic failed reinstall cleanup retry, pending finalizers, and completion
- API discovery failure classification and group/kind/scope matching

**API-server coverage in the implementation:**

- Actual SSA merging/ownership and repeated apply without overwriting originals
- Template omission, interrupted retirement, initially equal fields, and readoption
- Defaults, explicit nulls, atomic maps, associative lists, JSON Pointer escaping,
  Secret normalization, annotation limits, and dry-run behavior
- Capture/restore races, missing recovery records, and inventory BMH restoration
  while preserving status and unrelated fields
- Compatible alpha-to-beta BMH-shaped transitions, second reconciliation,
  field omission across versions, and cleanup without an intervening new apply
- Incompatible/pruned originals, concurrent changes after restoration preview,
  and cleanup after the recorded API endpoint is removed

**Live tests run:**

- Adopted a pre-existing BMH through a cross-namespace `hostRef` and verified
  same-UID restoration after CI deletion.
- Reconciled a CI repeatedly and verified original baselines were preserved as
  rendered values changed, including removing and later reintroducing a field.
- Changed tracked and untracked fields externally, including assisted-service's
  ignition override changes; verified baseline restoration, preservation of
  unrelated metadata and fields, and removal of ownership/recovery annotations.
- Attempted a competing CI claim and verified rejection without changing the
  claimed BMH.
- Verified restoration of a pre-existing generic resource and deletion of a
  create-only ConfigMap.
- Verified sync-wave ordering restores a referenced BMH before removing its HNA.
- Changed an adopted resource from v1alpha1 to v1beta1 and verified original
  values and UID were preserved through restoration.
- Withdrew the recorded API version while leaving another version served;
  verified cleanup retained the CI finalizer and recovery record, then restored
  the resource and finalized the CI after the recorded version returned.
- Exercised reinstall cleanup with denied reads and deletes, retries after
  permission changes, a held deletion finalizer, and adopted-resource conflict
  recovery while preserving the original UID and unrelated metadata.

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] Multi-document YAML template support implemented with tests
- [ ] HostNetworkAttachment API fields, templates, and validation implemented
      with tests
- [ ] Determination of IBI support in BMO
- [ ] Revert-on-deprovision mechanism implemented with tests
- [ ] Documented in user-facing docs
- [ ] Released in version 2.18.0

### Upgrade / Downgrade Strategy

**Upgrade**: The new `hostNetworkAttachmentTemplates` and
`nodes[].networkInterfaces` lists are optional and have no list-level CRD or
webhook default. Existing CIs omitting them do not acquire HNA entries solely
from the CRD upgrade. Embedded BMO defaults still apply to supplied entries,
including `mode: access` inside an HNA template's spec. Introducing defaults on
either list requires an explicit migration and post-provisioning validation
strategy. Recovery is not retroactive for already owned legacy resources that
lack a recorded original baseline.

**Downgrade**: Do not downgrade to a controller without restoration support
while adopted resources remain active. Older controllers can treat their
ownership label as permission to delete them, even with a recovery annotation.
Complete restoration and release ownership/recovery metadata before downgrading;
preserving those annotations alone is not protection. HNA-specific downgrade
also requires completing cleanup or a separately validated migration procedure.

### Version Skew Strategy

The siteconfig operator depends on the BMO HostNetworkAttachment CRD being
present in the cluster. This functionality will appear in BMO version 0.15.0.
If the CRD is not installed (older BMO version), the HostNetworkAttachment 
template still renders the resource, but server-side dry-run validation fails. 
The controller sets `RenderedTemplatesValidated` to failed and does not enter 
the actual apply phase; because the full rendered manifest list is validated
before apply, this blocks the deployment. The controller requeues using the 
existing validation-error flow.

Since HostNetworkAttachments are only rendered when
`spec.hostNetworkAttachmentTemplates` is populated, clusters that do not use this
feature are unaffected by CRD availability.

For adopted resource cleanup and compatible version transitions, see
[API Representation Changes](#api-representation-changes). The recovery record's
API version is not a permanent restriction to that endpoint. Normal template
apply still requires a served version.

## Implementation History

- 2026-05-08: Initial proposal created
- 2026-10-15: Align the proposal with the local HNA/restoration implementation,
  generic claim/reinstall fixes, compatible API-version recovery, and remaining
  runtime/upgrade qualification requirements.
- 2026-10-28: Simplify recovery to baseline-only restoration without stored
  last-applied values or drift rejection.

## Alternatives

### Server-Side Apply Field Ownership Alone for Revert

SSA remains useful for schema-aware merging, defaulting, field omission, and the
preview used to capture actual changes. Omitting SiteConfig-managed fields at
cleanup, however, can remove fields or leave values owned by other managers; it
cannot recover overwritten original values. The shared manager also does not
establish exclusive ownership between CIs, and active forced SSA can reclaim
fields. The chosen design therefore combines SSA with explicit claim checks and
durable original values, then uses a conditional JSON Patch to restore.

### Snapshot Backup/Restore for Revert

SiteConfig could snapshot the entire pre-existing resource into a ConfigMap
before modification and restore it on deprovisioning. Replacing the object with
that snapshot would also overwrite unrelated changes by other writers. It
would require external-state lifecycle management and still face API schema
changes between capture and restoration. A guarded, field-selective restore
could address concurrent writes, but would still need the original-value
tracking and conditional updates described in the chosen approach.

### ConfigMap Storage for Revert Patches

The recovery record (used to generate the restoration JSON Patch) could be stored
in a ConfigMap rather than as an annotation on the modified resource. This was
not selected because:

- **Lifecycle complexity** — ConfigMap-based storage would require creating a
  ConfigMap per modified resource (or a shared ConfigMap with keyed entries),
  handling cleanup on successful revert, orphan detection if the resource is
  deleted externally, and preservation during reinstall workflows. Annotations
  are created and removed as part of the resource's own patch operations.
- **Referential integrity** — the recovery record and the resource it describes
  can get out of sync (e.g., ConfigMap deleted accidentally, resource recreated
  without the corresponding ConfigMap entry). With annotations, the record is
  co-located with the resource and follows its backup lifecycle. Restoring a CI
  under a different namespace/name still requires explicit handling of identity.
- **RBAC expansion** — ConfigMap storage would require additional RBAC
  permissions and coordination with the existing PreservationHandler to avoid
  conflicts. Annotations are part of the resource metadata and require no
  additional permissions.

---
title: node-grouping
authors:
  - "@cwilkers"
reviewers:
  - TBD
approvers:
  - TBD
api-approvers:
  - TBD
creation-date: 2026-05-11
last-updated: 2026-10-15
status: provisional
tracking-link:
  - https://redhat.atlassian.net/browse/CNF-21755
see-also:
  - "/docs/argocd.md"
replaces:
  - None
superseded-by:
  - None
---

# Node Grouping in SiteConfig

## Release Signoff Checklist

- [ ] Enhancement is `implementable`
- [ ] Design details are appropriately documented from clear requirements
- [ ] Test plan is defined
- [ ] Graduation criteria are defined
- [ ] User-facing documentation is updated

## Summary

Add node grouping to SiteConfig so that multiple nodes sharing common
configuration can be expressed once rather than repeated per node. An optional
`group` field on `NodeSpec` associates nodes with a named group. An optional
`spec.groups[]` array on `ClusterInstance` holds group-scoped settings
(pause, config refs, group template references) and a nested `nodeSpec`
for shareable NodeSpec operands (role, templateRefs, labels, and so on).
New ConfigMap families (`*-group-templates`, `*-grouped-node-templates`)
render once-per-group resources (InfraEnv, HyperShift NodePool) and
per-grouped-node resources (BareMetalHost) respectively, while existing
`*-node-templates-v1` ConfigMaps remain unchanged.

## Motivation

SiteConfig currently defines configurations on either a per-cluster or
a per-node basis, leading to duplication and scalability challenges:

- **HCP clusters:** Each node creates a separate InfraEnv and HyperShift
  NodePool with `replicas: 1`, requiring separate MachineConfig and
  PerformanceProfile ConfigMaps for each node.
- **AI/IBI clusters:** Same duplication of InfraEnv and node-level fields. 
- **Scalability:** Large clusters (50+ nodes) require hundreds of
  lines of nearly-identical nodeSpec configuration entries. ClusterInstance
  manifests may risk exceeding Kubernetes object size limits.

The fundamental issue is the lack of a grouping mechanism that allows
multiple nodes to share common configuration while maintaining individual
physical identities (BMC address, MAC address, hostname).

### User Stories

#### Story 0: Backwards Compatibility

As a cluster administrator upgrading the SiteConfig operator, I expect
existing ClusterInstance manifests with no `group` field to render the
same resources as before the upgrade.

**Requirements:**

- Stock `*-node-templates-v1` ConfigMaps MUST continue to create one
  InfraEnv (and for HCP, one NodePool with `replicas: 1`) per node,
  named by hostname.
- The operator must treat the absence of group definitions in a ClusterInstance
  as a no-op for any added group logic and rendering loops.
- Setting `node.group` while leaving `*-node-templates-v1` on the node
  MUST NOT change which resources are rendered. The `.SpecialVars.GroupName`
  variable MAY be populated, but the original `*-node-templates-v1` templates
  MUST NOT use it for resource naming.
- The operator SHOULD surface a status condition or warning level log message
  when `group` is set but v1 node templates are still referenced, recommending a
  switch to group/grouped-node templates.

#### Story 1: HCP Node Groups Rendered as NodePools

As a cluster administrator, I want to define one or more groups of
worker nodes so that each group renders one InfraEnv and one HyperShift
NodePool with `replicas` equal to the group's node count, instead of
one NodePool per node.

**Current state:** N workers produce N NodePools (`replicas: 1` each),
N InfraEnvs, N MachineConfig ConfigMaps, N PerformanceProfile ConfigMaps.

**Desired state:** N groups (e.g. 6 compute + 4 storage) produce N
NodePools, N InfraEnvs, N MachineConfig ConfigMaps, and N PerformanceProfile
ConfigMaps. A single homogeneous group is the same flow with one entry.

**Requirements:**

- Documentation must explain the `spec.groups[]` entry and introduce
  the `groupTemplateRefs` and `*-group-templates` ConfigMaps.
- Documentation must explain that grouped nodes must use
  `*-grouped-node-templates` (via `nodes[].templateRefs` or
  `groups[].nodeSpec.templateRefs`).
- Operator must create a special variable `.SpecialVars.NodeCount` during group
  template rendering loop.

#### Story 2: Configuration Deduplication Inside One ClusterInstance

As a cluster administrator with a large AI or HCP cluster, I want to
define shared NodeSpec operands (role, templateRefs, labels, boot mode,
etc.) once per group, under `nodeSpec`, instead of repeating them on every
node.

**Current state:** Every node repeats `role: worker`,
`templateRefs: [...]`, `ironicInspect: disabled`, etc.

**Desired state:** Shared node fields live under `spec.groups[].nodeSpec`;
each node supplies identity fields (hostname, BMC address, MAC address) and a
group name to associate the node with that group. Where a node's
configuration should differ, it overrides the group's `nodeSpec`.

#### Story 3: (Undecided) Label-Only Grouping

As a cluster administrator, I want to consolidate per-node InfraEnvs
into one InfraEnv per group to simplify management, by adding
`group: workers` to my existing nodes and switching to group templates.

**Requirements:**

- Similar to Story 1, documentation must note a requirement to change templates
  to `*-grouped-node-templates` for nodes.
- If there is no `spec.groups[]` entry, a new API path`.spec.groupTemplateRefs`
  would be required at the `clusterInstance.spec` level.

#### Story 4: Pausing a Group's NodePool

As a HCP cluster administrator performing maintenance on a group of nodes
(e.g. firmware updates), I want to pause reconciliation of that
group's HyperShift NodePool without hand-editing the generated
NodePool. (Doing this with an AI cluster is intended as future work)

**Requirements:**

- `spec.groups[].paused` (optional, bool, default False) MUST be exposed during
  group-template rendering as `.SpecialVars.GroupPaused`, following the same
  pattern as `GroupName` and `NodeCount`.
- The built-in `hcp-group-templates-v1` NodePool template MUST create or exclude
  the NodePool's `spec.pausedUntil` field based on the value of `.SpecialVars.GroupPaused`.
- Toggling `groups[].paused` on an existing ClusterInstance MUST
  update the already-rendered group object (e.g. add or remove the NodePool's
  `spec.pausedUntil` field) on the next reconciliation.
- The operator itself SHOULD NOT special-case NodePool, or any other
  kind, to apply pause semantics. All group level objects related to a group may
  thus be rerendered during a change to pause status.

### Goals

#### General

1. Maintain backwards compatibility (Story 0).
2. Reduce configuration duplication across nodes that share common settings.
3. Support all current cluster types: AI, IBI, HCP.
4. Provide template variables (`GroupName`, `NodeCount`) for group-aware templates.
5. Support lifecycle operations (MCP/NodePool pause, scale, hardware
   maintenance) without side-effects.
6. Implement new special variables for machineconfig and performanceprofiles per group

#### Specific to HCP

1. Support HyperShift NodePool `replicas: N` from ClusterInstance definitions.
2. Implement a group-level pause capability for NodePools through group templates.
3. Use machineconfig and performanceprofiles names to link NodePool to ConfigMaps

#### Future

1. Anticipate future NMState deduplication.
2. Anticipate future late-binding scenarios, possibly with a new NodeGroup CR in
   a separate EP.
3. Anticipate ACM Unification of the NodePool/MCP Interface see [Future Work](#future-work)
4. Make machineconfig and performanceprofiles ConfigMaps or other CRs available
   to unified MCP interface

### Non-Goals

1. **NMState deduplication:** Sharing NMState configurations with per-node
   overrides (MAC, IP) is future work outside this enhancement. `groups[].nodeSpec`
   SHOULD NOT include `nodeNetwork` without a mechanism to allow templating node
   identity values.
2. **Reserved default group or auto-grouping:** There MUST NOT be a reserved
   `default` group or automatic grouping by cluster name. (See story 0)
3. **Nodes defined outside of ClusterInstance:** A separate CR to designate
   nodes for future late-binding to an arbitrary set of clusters is not part of
   this enhancement. See [Future Work](#future-work).
4. **ACM's unified NodePool/MCP interface:** This EP will anticipate, but
   not attempt to implement any part of ACM's planned cross-project work to
   unify how NodePools and MachineConfigPools are represented. MachineConfig and
   PerformanceProfile lists will be available for HCP functionality, but not yet
   implemented in templates for assisted installer clusters.

## Proposal

### Terminology

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this
document are to be interpreted as described in RFC 2119.

- **group:** A string `NodeSpec.group` identifying which group a
  node belongs to.
- **`spec.groups[]`:** An array on `ClusterInstance.spec` holding named
  entries. Group-only fields (`paused`, `configRefs`, `tuningConfigRefs`,
  `groupTemplateRefs`, `extraLabels`, `extraAnnotations`) sit on the
  entry. Shareable node fields sit under `nodeSpec`.
- **`spec.groups[].nodeSpec`:** The shareable subset of `NodeSpec` for
  nodes that reference the group.
- **group templates:** ConfigMaps rendered once per unique group value
  (e.g. `ai-group-templates-v1`). Contain resources like InfraEnv and
  HyperShift NodePool.
- **grouped-node templates:** ConfigMaps rendered once per node within a group
  (e.g. `ai-grouped-node-templates-v1`). Contain per-node resources like
  BareMetalHost.
- **avoid "pool":** SiteConfig types and fields SHOULD NOT use `pool` or
  `NodePool` to avoid confusion and naming conflicts with HyperShift's
  `NodePool`.

### Workflow Description

**Actors:** Cluster administrator, SiteConfig operator.

1. **Define ClusterInstance:** (Unchanged) Start with existing or create new, defining
  cluster level variables and node lists.

2. **Identify groups:** (New) Add optional `spec.groups[]` entries. Put
   group-only settings on the entry and shared node fields under
   `nodeSpec`.

3. **Assign nodes to groups:** (New) Set `node.group` on each node that
   should participate in grouping.

4. **Select templates:** (New) Point `groups[].groupTemplateRefs` at
   `*-group-templates` ConfigMaps and `groups[].nodeSpec.templateRefs`
   (or the node's `templateRefs`) at `*-grouped-node-templates`
   ConfigMaps. Ungrouped nodes keep `*-node-templates-v1`.

5. **Consolidate NodeSpec variables:** (New) Add any shared node variables like
   `role: worker` under `spec.groups[].nodeSpec`. If working from an existing
   ClusterInstance, delete the same variables from node entries.

6. **SiteConfig processes the ClusterInstance:** (New processes in operator only)
   - Renders cluster-level templates.
   - For each unique `group` value: renders `groups[].groupTemplateRefs`
     (InfraEnv, HyperShift NodePool with `replicas: NodeCount`).
     This loop applies `groups[].extraLabels` and
     `groups[].extraAnnotations` to those CRs. It MUST NOT apply
     `groups[].nodeSpec.extraLabels` or
     `groups[].nodeSpec.extraAnnotations`.
   - For each node: renders the node's `templateRefs` (inherited from
     `groups[].nodeSpec` or listed on the node) once per node, using the
     merged node fields. This loop MUST NOT apply `groups[].extraLabels`
     or `groups[].extraAnnotations`.

7. **Monitor deployment:** (Unchanged) Observe and propagate status conditions of created
   cluster CRs to `ClusterInstance.Status`.

### API Extensions

#### ClusterInstance Extensions

```yaml
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: ClusterInstance
spec:
  groups:                           # NEW, optional
    - name: workers                 # MUST be unique; matches node.group
      paused: false                 # optional; see Group Pause
      configRefs:                   # optional; see Group Config Refs
        - worker-machineconfig
      tuningConfigRefs:             # optional; see Group Config Refs
        - worker-performanceprofile
      extraAnnotations:             # group-template CRs only
        NodePool:
          example.com/owner: platform
      extraLabels:                  # group-template CRs only
        InfraEnv:
          tier: compute
      groupTemplateRefs:            # rendered once per this group
        - name: ai-group-templates-v1
      nodeSpec:                     # shareable NodeSpec operands for member nodes
        role: worker
        templateRefs:               # inherited by grouped nodes
          - name: ai-grouped-node-templates-v1
        # Other shareable fields: cpuArchitecture, bootMode, ironicInspect,
        # automatedCleaningMode, installerArgs, ignitionConfigOverride,
        # nodeLabels, extraAnnotations, extraLabels, suppressedManifests,
        # pruneManifests, rootDeviceHints — see the shared-field
        # table below.
  nodes:
    - hostName: worker1.example.com
      group: workers                # NEW, optional; references groups[].name
      bmcAddress: idrac-virtualmedia://192.168.1.10/redfish/v1/Systems/System.Embedded.1
      bootMACAddress: "AA:BB:CC:DD:EE:01"
      bmcCredentialsName:
        name: worker1-bmc
      # templateRefs MAY be omitted when groups[].nodeSpec.templateRefs supplies them
    - hostName: worker2.example.com
      group: workers
      bmcAddress: idrac-virtualmedia://192.168.1.11/redfish/v1/Systems/System.Embedded.1
      bootMACAddress: "AA:BB:CC:DD:EE:02"
      bmcCredentialsName:
        name: worker2-bmc
```

**Field rules:**

- `groups[].name` MUST be unique within the ClusterInstance.
- Group-only fields (`name`, `paused`, `configRefs`, `tuningConfigRefs`,
  `groupTemplateRefs`, `extraLabels`, `extraAnnotations`) live on the
  `groups[]` entry. `extraLabels` and `extraAnnotations` here apply
  only to CRs rendered from that group's `groupTemplateRefs`.
- Fields intended for member nodes live under `groups[].nodeSpec`. The
  CRD schema for `nodeSpec` MUST be the shareable subset of `NodeSpec`.
- Identity fields MUST NOT be accepted on `groups[].nodeSpec`. The schema MUST
  omit them so the API server rejects invalid usage.
- `groups[].groupTemplateRefs` are rendered once for that group.
- `groups[].nodeSpec.templateRefs` are node templates inherited by
  members. A node MAY omit `templateRefs` when its group's
  `nodeSpec.templateRefs` supplies them. When both are set, they are a
  list union (see the shared-field table below).
- `templateRefs` at `.spec` remains the cluster-scoped template list.
  Story 3's undecided label-only path, `.spec.groupTemplateRefs`, would
  be group templates defined at cluster scope and applied to every
  group. That field is not part of the `groups[]` entry.
- The group template render loop is a no-op when `groups` is unset.
  Group is the only scope that may be a no-op.

Fields fall into three sets: group-only, shared, and node-only.

**Group only:**

These fields live on the `groups[]` entry and are used
while rendering that group's templates.

| Field | Notes |
|---|---|
| `name` | MUST be unique; matches `node.group` |
| `paused` | Exposed as `.SpecialVars.GroupPaused` |
| `configRefs` | |
| `tuningConfigRefs` | |
| `groupTemplateRefs` | Rendered once per group |
| `extraAnnotations` | Group-template CRs only. Union with `spec.extraAnnotations`; group wins. |
| `extraLabels` | Group-template CRs only. Union with `spec.extraLabels`; group wins. |

**Shared:**

These fields may be set on `groups[].nodeSpec` and on the
node. The node value is the more specific one.

- `Scalar merge` means the `nodeSpec` value and the node value are
  merged, and the most specific case wins.
- `List union` means the `nodeSpec` list and the node list are combined
  using set union logic.
- `Map union by key` means maps on `nodeSpec` and the node are combined
  by key. On a key collision, the most specific value stands.

| Field | Conflict strategy | Notes |
|---|---|---|
| `rootDeviceHints` | Scalar merge | |
| `role` | Scalar merge | |
| `cpuArchitecture` | Scalar merge | Overriding arch may be an anti-pattern in real world use. |
| `bootMode` | Scalar merge | |
| `ironicInspect` | Scalar merge | |
| `automatedCleaningMode` | Scalar merge | |
| `installerArgs` | Scalar merge | |
| `ignitionConfigOverride` | Scalar merge | Node-level BareMetalHost override. Distinct from cluster `spec.ignitionConfigOverride`, which still targets the group's InfraEnv. |
| `suppressedManifests` | List union | Applied in the node render loop. The cluster-level list still applies to every loop. |
| `pruneManifests` | List union | Same scope rule as `suppressedManifests`. |
| `templateRefs` | List union | Union with `nodes[].templateRefs` |
| `nodeLabels` | Map union | |
| `extraAnnotations` | Map union | Member-node render loop only. Distinct from `groups[].extraAnnotations`. |
| `extraLabels` | Map union | Member-node render loop only. Distinct from `groups[].extraLabels`. |

**Node only:**

These fields may be defined only on the node. Defining
them on `groups[].nodeSpec` is an error.

| Field | Notes |
|---|---|
| `name` | |
| `hostName` | |
| `bmcAddress` | |
| `bmcCredentialsName` | Credentials are not sharable because some mechanisms delete Secrets with bareMetalHost |
| `bootMACAddress` | |
| `hostRef` | |
| `nodeNetwork` | NMState dedup is future work |

#### Template ConfigMap Families

Existing families (unchanged):

| Family | Cardinality | Examples |
|---|---|---|
| Cluster | Once per ClusterInstance | `hcp-cluster-templates-v1`, `ai-cluster-templates-v1`, `ibi-cluster-templates-v1` |
| Node (ungrouped) | Once per node | `hcp-node-templates-v1`, `ai-node-templates-v1`, `ibi-node-templates-v1` |

New families:

| Family | Cardinality | Examples |
|---|---|---|
| Group | Once per unique group | `hcp-group-templates-v1`, `ai-group-templates-v1` |
| Grouped node | Once per grouped node | `hcp-grouped-node-templates-v1`, `ai-grouped-node-templates-v1` |

### Siteconfig Impact

**Controllers affected:**

- **ClusterInstanceReconciler:** Modified to iterate over unique `group`
  values and render `groupTemplateRefs` once per group, in addition to
  the existing cluster and per-node loops.

**Rendering pipeline:**

```text
ClusterInstance
  |
  +-- cluster templateRefs (once)
  |
  +-- groups[].groupTemplateRefs (once per unique group)
  |
  +-- each node's templateRefs (once per node)
        |-- ungrouped: *-node-templates-v1
        +-- grouped:   *-grouped-node-templates-v1
```

**Template variables:**

| Variable | Cluster templates | Group templates | Node / grouped-node templates |
|---|---|---|---|
| `.Spec.*` | ClusterInstance.spec | same | same |
| `.SpecialVars.CurrentNode` | empty | n/a | identity + merged `groups[].nodeSpec` fields |
| `.SpecialVars.GroupName` | n/a | `node.group` value | `node.group` (unset if no group) |
| `.SpecialVars.NodeCount` | n/a | number of nodes in group | n/a |
| `.SpecialVars.GroupPaused` | n/a | `groups[].paused` (bool, default `false`) | n/a |
| `.SpecialVars.ConfigRefs` | n/a | `groups[].configRefs` (list of ConfigMap names) | n/a |
| `.SpecialVars.TuningConfigRefs` | n/a | `groups[].tuningConfigRefs` (list of ConfigMap names) | n/a |

- `.SpecialVars.GroupName` MUST equal `node.group` when set. When `group`
is unset, `GroupName` MUST be unset.
- Group and grouped-node templates MUST key shared resources with `GroupName` and
identity resources with `CurrentNode.HostName` respectively.
- Using group templates without `node.group` set SHOULD fail validation.
- `.SpecialVars.GroupPaused` MUST equal `groups[].paused` (default
  `false` when unset) and SHOULD NOT be exposed to node / grouped-node
  templates; see [Group Pause](#group-pause).
- `.SpecialVars.ConfigRefs` and `.SpecialVars.TuningConfigRefs` MUST
  equal `groups[].configRefs` and `groups[].tuningConfigRefs`
  respectively (empty list when unset) and SHOULD NOT be exposed to
  node / grouped-node templates; see
  [Group Config Refs](#group-config-refs).

**Merge and override rules:**

*Ungrouped (Story 0):*

NodeSpec fields defined only in node. Today's behavior is unchanged.

*Grouped:*

For member nodes, `nodes[]` > `groups[].nodeSpec` > `.spec`.

- **Scalars** As defined above, the definition of scalar fields override to the
most specific scope.
- **Maps** Key-based union. Collisions override to most specific scope.
- **Lists** Simple union.

### Group Pause

`.SpecialVars.GroupPaused` (see Story 4) is scoped to group-template
rendering. SiteConfig passes the boolean through as-is and
SHOULD NOT interpret it for specific GVKs; mapping it onto a kind's
native pause field (e.g. NodePool `spec.pausedUntil`) is the
template's job. Because that mapping lives in the template, adapting to ACM's
eventual unified NodePool/MCP interface (see [Future Work](#future-work)) may
only require updating the shipped template.

### Group Config Refs

`.SpecialVars.ConfigRefs` and `.SpecialVars.TuningConfigRefs` carry
the group's `configRefs` / `tuningConfigRefs` — each a list of
names — to group-template rendering. The operator provided
`hcp-group-templates-v1` template provides a NodePool definition that
populates `spec.config` and `spec.tuningConfig` (both `[]LocalObjectReference`)
to point to ConfigMaps that must be provided by the customer.
SiteConfig treats the entries as opaque names; ACM's unified NodePool/MCP
interface (see [Future Work](#future-work)) may choose another mechanism for
providing MachineConfigs and PerformanceProfiles than encapuslating them in
ConfigMaps as HCP does.

### Risks and Mitigations

- **Merge logic complexity:**
  Three-level merge (Node > `groups[].nodeSpec` > Cluster) may produce
  unexpected effective configurations.

  *Mitigation*: Clear documentation, validation warnings.

- **Template migration effort:** Users switching from ungrouped to
  grouped must select new ConfigMaps.

  *Mitigation*: Provide built-in `*-group-templates-v1` and
  `*-grouped-node-templates-v1` for each cluster type. Document
  migration steps.

- **HCP NodePool replica drift:** `replicas` in the NodePool may
  diverge from the actual node count if nodes are added or removed.

  *Mitigation*: Reconciler SHOULD recompute `NodeCount` on changes.

### Drawbacks

- **Increased API surface:** `spec.groups[]` and `node.group` add new
  fields to ClusterInstance.
- **Template proliferation:** Two new ConfigMap families per cluster type.
- **Testing complexity:** Merge logic, mixed grouped/ungrouped nodes,
  and per-cluster-type template variants all require dedicated test scenarios.
  Node count sanity checks will require a pass through group and node fields
  first.

## Design Details

### Implementation Notes

1. **One group per node:** A node MUST reference at most one group.

2. **CRD defaulting trap:** `NodeSpec.role` defaults to `master` via
   `+kubebuilder:default`. The merge implementation MUST distinguish
   "user wrote `role: master`" from "CRD defaulted `role` to `master`"
   so that `groups[].nodeSpec.role: worker` is not blocked by a node's
   defaulted value. Use user-specified / raw field detection, not the
   post-defaulted Go struct.

3. **IBI group templates:** IBI has no need of grouping because it is a SNO
   case. We SHOULD NOT ship an `ibi-group-templates-v1` ConfigMap.

### Open Questions

1. **Group label key:** What label key should the group templates use
   for HyperShift NodePool `agentLabelSelector`?
   Proposal: `siteconfig.open-cluster-management.io/group`.

2. **Template Mismatch:** Should there be any kind of detection and warning if a
   user fails to change templates when adding groups?

3. **Label-only grouping:** Should there even be a label-only grouping case?
   Is there value in having a ClusterInstance.spec.groupTemplateRefs as a
   catch-all? (This would be required to not break the group render loop?)

### Test Plan

**Unit Tests:**

- Backwards compatibility: ungrouped ClusterInstance renders identically
- Merge logic for all field types (union, scalar, nested), reading
  shareable node fields from `groups[].nodeSpec`
- Validation of group references and constraints
- CRD default detection (role, bootMode) during merge
- Mixed grouped/ungrouped node rendering
- `.SpecialVars.GroupPaused` correctly reflects `groups[].paused`
  during group-template rendering, and is absent/false by default
- Reconciliation re-renders and re-applies a group's rendered objects
  on every pass regardless of `GroupPaused`, i.e. a paused group is
  never skipped or short-circuited by the apply loop

**Integration Tests:**

- HCP cluster with multi-replica NodePools from groups
- AI cluster with grouped nodes sharing InfraEnv
- Label-only grouping with and without `spec.groups[]`
- Built-in `hcp-group-templates-v1` sets/clears NodePool
  `spec.pausedUntil` based on `groups[].paused`
- Toggling `groups[].paused` (`false` → `true` → `false`) on an
  existing ClusterInstance updates the already-rendered NodePool's
  `pausedUntil` in place on the next reconciliation, with no manual
  intervention

**E2E Tests:**

- Deploy HCP cluster with heterogeneous groups
- Deploy AI cluster with grouped workers
- Lifecycle: add/remove nodes from groups, verify re-reconciliation
- Pause a group's NodePool, confirm the NodePool controller halts
  node-level changes, then unpause and confirm the NodePool resumes
  reconciling and the node-level changes proceed

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] `group` field added to NodeSpec API
- [ ] `spec.groups[]` added to the ClusterInstance API, with shareable
      node fields nested under `groups[].nodeSpec`
- [ ] Group-level and grouped-node template rendering implemented
- [ ] Merge logic for group defaults and node overrides implemented
- [ ] Built-in `*-group-templates-v1` and `*-grouped-node-templates-v1`
      for HCP and AI
- [ ] `spec.groups[].paused` and `.SpecialVars.GroupPaused` implemented
      (see [Group Pause](#group-pause))
- [ ] Unit, integration, and E2E tests passing
- [ ] Validation webhooks for group references
- [ ] User-facing documentation and migration guide
- [ ] Released in version X.Y.Z

### Upgrade / Downgrade Strategy

**Upgrade:**

- Purely additive. Existing ClusterInstances without `group` or
  `groups[]` continue to work unchanged (Story 0).
- Users opt in by adding `spec.groups[]`, setting `node.group`, and
  switching templates.

**Downgrade:**

- Remove `group` from nodes, remove `spec.groups[]`, restore
  `*-node-templates-v1` on each node. Resources revert to per-node.
- Older operator ignores unknown `group` and `groups[]` fields.

### Version Skew Strategy

**SiteConfig operator vs ClusterInstance CRs:**

- Older operator ignores unknown `group` and `groups[]` fields.
- Newer operator handles both ungrouped and grouped ClusterInstances.

**SiteConfig vs Hub/ACM:**

- Generated resources (HyperShift NodePool, InfraEnv, etc.) are
  standard upstream types. No version skew concerns.

**SiteConfig vs templates:**

- Templates that do not use `GroupName` or `NodeCount` continue to work.
- Group template variables are populated only when nodes reference groups.

## Future Work

A namespace-scoped `NodeGroup` CR that includes `spec.nodes` from day
one (same shareable fields as `spec.groups[].nodeSpec`) is planned for a
follow-on enhancement. This would support:

- **Late-binding:** Nodes discovered and bound to clusters after cluster
  creation, rather than requiring all nodes in the ClusterInstance at
  creation time.
- **Shared inventory:** Multiple ClusterInstances referencing the same
  NodeGroup; each renders its own HyperShift NodePool binding to the
  shared nodes via agent selectors.
- **`ClusterInstance.spec.nodeGroups`:** A new field referencing
  NodeGroup CR names. `spec.nodes` MAY be empty when `nodeGroups` is
  set.
- In-line `spec.groups[]` MAY remain alongside the CR.

If a NodeGroup CR is introduced, it MUST include `spec.nodes` (inventory
is the reason the CR exists). A config-only CR without `spec.nodes` is
rejected; `spec.groups[]` already serves that purpose.

BMH ownership when two ClusterInstances reference one NodeGroup is
deferred to that enhancement.

### ACM Unification of the NodePool/MCP Interface

Separately, ACM has longer-term, cross-project work (outside the
scope of this EP) to unify how NodePools and MachineConfigPools are
represented and configured for assisted-installer-based clusters. This EP is
deliberately designed not to block that path:

- `spec.groups[]` and the group/grouped-node template ConfigMap
  families introduced here are additive and do not assume or encode a
  particular future NodePool/MCP interface.
- The [Group Pause](#group-pause) mechanism introduced by this EP
  (`spec.groups[].paused` exposed as `.SpecialVars.GroupPaused`) is
  deliberately kind-agnostic at the API/operator level.  When ACM's unified
  interface lands, only those templates would need to change, not the SiteConfig
  API or operator.
- Likewise, [Group Config Refs](#group-config-refs) exposes
  `configRefs` / `tuningConfigRefs` as plain ConfigMap name lists
  rather than typed NodePool or MachineConfigPool references, so they
  stay usable if ACM's unified interface changes how those configs
  are targeted.
- Designing or implementing ACM's unified NodePool/MCP interface
  itself is out of scope for this EP and is expected to be tracked as
  separate, follow-on work.

## Implementation History

- 2026-05-11: Initial proposal created
- 2026-06-01: Enhanced with requirements, design details, and examples
- 2026-06-10: Human review completed and pushed
- 2026-09-16: Refactored: ClusterInstance-only scope with `spec.groups[]`,
  NodeGroup CR moved to Future Work, rejected approaches consolidated
  under Alternatives, RFC 2119 normative language throughout
- 2026-09-25: Met with stakeholders and interested engineers to discuss EP.
  Multiple feedback items were provided.
- 2026-10-09: Implementing feedback from 9/25 meeting:
  - Added an exhaustive table of which `NodeSpec` fields are available on
  `groups[]` vs. node-only
  - Documented ACM's future unified NodePool/MCP interface as Future Work and a
  Non-Goal
  - Added `spec.groups[].paused` exposed to group templates
  as `.SpecialVars.GroupPaused` to be implemented purely as a group template
  special variable.
- 2026-10-15: Nested shareable node fields under `spec.groups[].nodeSpec`.
  Flat node fields on `groups[]` would make `extraLabels` and
  `extraAnnotations` ambiguous between group-rendered objects and member
  nodes. Resolved open question 4.
  - Added `groups[].extraLabels` and `groups[].extraAnnotations` for
  CRs rendered from group templates, distinct from the `nodeSpec` copies.

## Alternatives

### Use "Pool" Terminology

Use `nodePool`, `pool`, or `nodePoolName` instead of `group`.

**Rejected.** "Pool" implies fungibility, which does not apply beyond
HCP. Overloading `NodePool` between a SiteConfig construct and the
HyperShift resource (`nodepools.hypershift.openshift.io`) causes
confusion. "Group" accurately conveys shared configuration while
maintaining individual node identities.

### Default GroupName to HostName When group Is Unset

Populate `.SpecialVars.GroupName` with the node's hostname so one
template family could name both per-node and grouped resources.

**Rejected.** Though this default could help a single template family serve
both cases, it could lead to confusion and produce side-effects if an existing 
ZTP installation mixes default and customized templates that assume the current
node name behavior. This is a strong argument for Story 0.

### Silent Template Substitution

Have the operator automatically swap `*-node-templates-v1` for
`*-grouped-node-templates-v1` when `group` is set.

**Rejected.** This would create unexpected side-effects without user consent.
The operator MUST NOT change which ConfigMaps it renders. Story 3 documents the
recommended template switch.

## Infrastructure Needed

- No new infrastructure required.
- Existing test infrastructure (unit, integration, e2e) is sufficient.
- Scale testing (50+ nodes) may require additional test clusters.

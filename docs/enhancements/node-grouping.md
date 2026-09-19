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
last-updated: 2026-09-16
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
`spec.groups[]` array on `ClusterInstance` holds shareable NodeSpec operands
(role, templateRefs, labels, etc.) and group-scoped template references.
New ConfigMap families (`*-group-templates`, `*-grouped-node-templates`)
render once-per-group resources (InfraEnv, HyperShift NodePool) and
per-grouped-node resources (BareMetalHost) respectively, while existing
`*-node-templates-v1` ConfigMaps remain unchanged.

## Motivation

SiteConfig currently defines configurations on either a per-cluster or
a per-node basis, leading to duplication and scalability challenges:

- **HCP clusters**: Each node creates a separate InfraEnv and HyperShift
  NodePool with `replicas: 1`, requiring separate MachineConfig and
  PerformanceProfile ConfigMaps for each node.
- **AI/IBI clusters**: Same duplication of InfraEnv and node-level fields. 
- **Scalability**: Large clusters (50+ nodes) require hundreds of
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

**Current state**: N workers produce N NodePools (`replicas: 1` each),
N InfraEnvs, N MachineConfig ConfigMaps, N PerformanceProfile ConfigMaps.

**Desired state**: N groups (e.g. 6 compute + 4 storage) produce N
NodePools, N InfraEnvs, N MachineConfig ConfigMaps, and N PerformanceProfile
ConfigMaps. A single homogeneous group is the same flow with one entry.

**Requirements:**
- Documentation must explain the `spec.groups[]` entry and introduce
  the `groupTemplateRefs` and `*-group-templates` ConfigMaps.
- Documentation must explain that grouped nodes must use
  `*-grouped-node-templates` (via node or group `templateRefs`) 
- Operator must create a special variable `.SpecialVars.NodeCount` during group
  template rendering loop.

#### Story 2: Configuration Deduplication Inside One ClusterInstance

As a cluster administrator with a large AI or HCP cluster, I want to
define shared NodeSpec operands (role, templateRefs, labels, boot mode,
etc.) once per group instead of repeating them on every node.

**Current state:** Every node repeats `role: worker`,
`templateRefs: [...]`, `ironicInspect: disabled`, etc.

**Desired state:** Shared fields live in `spec.groups[]`; each node
supplies only identity fields (hostname, BMC address, MAC address).

**Requirements:**  Operator must maintain a `GroupSpec` object with a
list of `NodeSpec` entries that are determined to be relevant in a group
context.

#### Story 3: (Undecided) Label-Only Grouping

As an AI cluster administrator, I want to consolidate per-node InfraEnvs
into one InfraEnv per group to simplify management, by adding
`group: workers` to my existing nodes and switching to group templates.

**Requirements:**
- Similar to Story 1, documentation must note a requirement to change templates
  to `*-group-templates` / `*-grouped-node-templates` for group and nodes
  respectively.
- If there is no `spec.groups[]` entry, `.spec.groupTemplateRefs` would be
  required at the `clusterInstance.spec` level.

### Goals

1. Support HyperShift NodePool `replicas: N` from ClusterInstance definitions.
2. Reduce configuration duplication across nodes that share common settings.
3. Provide template variables (`GroupName`, `NodeCount`) for group-aware templates.
4. Maintain backwards compatibility (Story 0).
5. Support all current cluster types: AI, IBI, HCP.
6. Support lifecycle operations (MCP pause, scale, hardware maintenance) without
   side-effects.
7. Anticipate future NMState deduplication.
8. Anticipate future late-binding scenarios, possibly with a new NodeGroup CR in
   a separate EP.

### Non-Goals

1. **NMState deduplication**: Sharing NMState configurations with per-node
   overrides (MAC, IP) is future work outside this enhancement. `groups[]`
   SHOULD NOT include `nodeNetwork`.
2. **Cross-namespace NodeGroup**: Out of scope for this EP.
3. **Reserved default group or auto-grouping**: There MUST NOT be a reserved
   `default` group or automatic grouping by cluster name. (See story 0)
4. **NodeGroup CRD**: A separate `NodeGroup` CR is not part of this
   enhancement. See [Future Work](#future-work).

## Proposal

### Terminology

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this
document are to be interpreted as described in RFC 2119.

- **group**: A string `NodeSpec.group` identifying which group a
  node belongs to.
- **`spec.groups[]`**: An array on `ClusterInstance.spec` holding named
  entries with shareable NodeSpec fields and group-scoped template
  references.
- **group templates**: ConfigMaps rendered once per unique group value
  (e.g. `ai-group-templates-v1`). Contain resources like InfraEnv and
  HyperShift NodePool.
- **grouped-node templates**: ConfigMaps rendered once per node within a group
  (e.g. `ai-grouped-node-templates-v1`). Contain per-node resources like
  BareMetalHost.
- SiteConfig types and fields SHOULD NOT use `pool` or `NodePool` to avoid
  conflicting with HyperShift `NodePool`.

### Workflow Description

**Actors**: Cluster administrator, SiteConfig operator.

1. **Define ClusterInstance:** Start with existing or create new, defining
  cluster level variables and node lists.

2. **Identify groups:** Add optional `spec.groups[]` entries to the
   ClusterInstance with shared NodeSpec fields and template references.

3. **Assign nodes to groups**: Set `node.group` on each node that
   should participate in grouping.

4. **Select templates**: Point `groups[].groupTemplateRefs` at
   `*-group-templates` ConfigMaps and `groups[].templateRefs` (or
   node `templateRefs`) at `*-grouped-node-templates` ConfigMaps.
   Ungrouped nodes keep `*-node-templates-v1`.

5. **Consolidate NodeSpec variables:** Add any shared node variables like
   `role: worker` to the `spec.groups[]` entries.

6. **SiteConfig processes the ClusterInstance**:
   - Renders cluster-level templates once.
   - For each unique `group` value: renders `groups[].groupTemplateRefs`
     once (InfraEnv, HyperShift NodePool with `replicas: NodeCount`).
   - For each node: renders the node's `templateRefs` (inherited from
     `groups[]` or listed on the node) once per node.

7. **Monitor deployment**: Observe status conditions on ClusterInstance.

### API Extensions

#### ClusterInstance Extensions

```yaml
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: ClusterInstance
spec:
  groups:                           # NEW, optional
    - name: workers                 # MUST be unique; matches node.group
      role: worker                  # shareable NodeSpec operands
      templateRefs:                 # inherited by grouped nodes
        - name: ai-grouped-node-templates-v1
      groupTemplateRefs:            # rendered once per this group
        - name: ai-group-templates-v1
      # Other shareable fields: cpuArchitecture, bootMode, ironicInspect,
      # automatedCleaningMode, installerArgs, ignitionConfigOverride,
      # nodeLabels, extraAnnotations, extraLabels, suppressedManifests,
      # pruneManifests
  nodes:
    - hostName: worker1.example.com
      group: workers                # NEW, optional; references groups[].name
      bmcAddress: idrac-virtualmedia://192.168.1.10/redfish/v1/Systems/System.Embedded.1
      bootMACAddress: "AA:BB:CC:DD:EE:01"
      bmcCredentialsName:
        name: worker1-bmc
      # templateRefs MAY be omitted when groups[].templateRefs supplies them
    - hostName: worker2.example.com
      group: workers
      bmcAddress: idrac-virtualmedia://192.168.1.11/redfish/v1/Systems/System.Embedded.1
      bootMACAddress: "AA:BB:CC:DD:EE:02"
      bmcCredentialsName:
        name: worker2-bmc
```

**Field rules:**

- `groups[].name` MUST be unique within the ClusterInstance.
- Identity fields MUST stay on the node: `hostName`, `bmcAddress`,
  `bmcCredentialsName`, `bootMACAddress`, `hostRef`, `nodeNetwork`.
- Shareable fields on `groups[]` (same types as `NodeSpec`): `role`,
  `templateRefs`, `cpuArchitecture`, `bootMode`, `ironicInspect`,
  `automatedCleaningMode`, `installerArgs`, `ignitionConfigOverride`,
  `nodeLabels`, `extraAnnotations`, `extraLabels`, `suppressedManifests`,
  `pruneManifests`.
- Node `templateRefs` MAY be omitted when a matching `groups[]` entry
  supplies them.
- Group templates render only when the user references them via
  `groups[].groupTemplateRefs`.

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

- **ClusterInstanceReconciler**: Modified to iterate over unique `group`
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
| `.SpecialVars.CurrentNode` | empty | n/a | identity + merged group fields |
| `.SpecialVars.GroupName` | n/a | `node.group` value | `node.group` (unset if no group) |
| `.SpecialVars.NodeCount` | n/a | number of nodes in group | n/a |

- `.SpecialVars.GroupName` MUST equal `node.group` when set. When `group`
is unset, `GroupName` MUST be unset.
- Group and grouped-node templates MUST key shared resources with `GroupName` and
identity resources with `CurrentNode.HostName` respectively.
- Using group templates without `node.group` set SHOULD fail validation.

**Merge and override rules:**

*Ungrouped (Story 0):* Node > Cluster only. Today's behavior is
unchanged. Extra annotations/labels use Kind-level replace-or-fallback.
`suppressedManifests` / `pruneManifests` concatenate cluster and node
lists.

*Grouped (`spec.groups[]`):* Node > Group > Cluster.

- **Scalars** (`role`, `bootMode`, `cpuArchitecture`, `ironicInspect`,
  `automatedCleaningMode`, `installerArgs`, `ignitionConfigOverride`):
  First user-specified value wins. CRD schema defaults (e.g.
  `role: master`) MUST NOT be applied before merge when a matching
  group config exists; otherwise a worker group cannot override
  `role`. Merge MUST use user-specified fields, not post-defaulted
  NodeSpec values.
- **Maps** (`extraAnnotations`, `extraLabels`, `nodeLabels`):
  Key-based union (Kind key, then annotation/label key). Node wins
  collisions over group over cluster.
- **Lists** (`suppressedManifests`, `pruneManifests`, `templateRefs`):
  Union by identity key (`kind`; `apiVersion+kind`; `name+namespace`).
  Node value replaces group value on duplicate keys.

*Label-only (Story 3):* No `groups[]` entry supplies NodeSpec fields;
merge stays Node > Cluster. Which kinds render follows the templates
the user listed.

**Merge example:**

```yaml
spec:
  groups:
    - name: workers
      role: worker
      nodeLabels:
        tier: compute
        zone: us-east-1
      templateRefs:
        - name: ai-grouped-node-templates-v1
      groupTemplateRefs:
        - name: ai-group-templates-v1
  nodes:
    - hostName: special.example.com
      group: workers
      nodeLabels:
        zone: us-west-2      # overrides group's zone
        rack: rack-07         # added
      bmcAddress: ...
      bootMACAddress: ...
      bmcCredentialsName: { name: special-bmc }
# Effective for special.example.com:
#   role: worker              (from group)
#   nodeLabels:
#     tier: compute           (from group)
#     zone: us-west-2         (node overrides group)
#     rack: rack-07           (from node)
```

**Validation:**

- Nodes referencing the same group with incompatible configurations
  (e.g. non-worker roles in a group for HCP) SHOULD produce a validation
  error.
- `groups[].name` values MUST be unique.
- When `groupTemplateRefs` is set but no nodes reference that group,
  the operator SHOULD warn.

### Risks and Mitigations

- **Merge logic complexity**: Three-level merge (Node > Group > Cluster)
  may produce unexpected effective configurations.
  *Mitigation*: Clear documentation, validation warnings, status
  conditions showing effective configuration per node.

- **Template migration effort**: Users switching from ungrouped to
  grouped must select new ConfigMaps.
  *Mitigation*: Provide built-in `*-group-templates-v1` and
  `*-grouped-node-templates-v1` for each cluster type. Document
  migration steps in Story 3.

- **HCP NodePool replica drift**: `replicas` in the NodePool may
  diverge from the actual node count if nodes are added or removed.
  *Mitigation*: Reconciler MUST recompute `NodeCount` on every
  reconciliation.

### Drawbacks

- **Increased API surface**: `spec.groups[]` and `node.group` add new
  fields to ClusterInstance.
- **Template proliferation**: Two new ConfigMap families per cluster type.
- **Testing complexity**: Merge logic, mixed grouped/ungrouped nodes,
  and per-cluster-type template variants all require dedicated test
  scenarios.

## Design Details

### Implementation Notes

1. **One group per node**: A node MUST reference at most one group.
   No reserved `default` group. (see Alternatives)

2. **CRD defaulting trap**: `NodeSpec.role` defaults to `master` via
   `+kubebuilder:default`. The merge implementation MUST distinguish
   "user wrote `role: master`" from "CRD defaulted `role` to `master`"
   so that a group `role: worker` is not blocked by a node's defaulted
   value. Use user-specified / raw field detection, not the post-defaulted
   Go struct.

3. **IBI group templates**: IBI has no need of grouping because it is a SNO case.
   We should not ship an `ibi-group-templates-v1` ConfigMap.

### Open Questions

1. **Group label key**: What label key should the group templates use
   for HyperShift NodePool `agentLabelSelector`?
   Proposal: `siteconfig.open-cluster-management.io/group`.

2. **Template Mismatch:** Should there be any kind of detection and warning if a
   user fails to change templates when adding groups?

3. **Label-only grouping:** Should there even be a label-only grouping case?
   Is there value in having a ClusterInstance.spec.groupTemplateRefs as a
   catch-all? (This would be required to not break the group render loop?)

4. **NodeSpec entries in a group:** Should there be a container list for
   variables to be applied as a group's NodeSpec? e.g. `spec.groups[0].role` vs
   `spec.groups[0].nodeSpec.role`?

5. **Pausing Groups:** What mechanism would make sense to map a group to a MCP
   or NodePool's pause/pausedUntil? 

### Test Plan

**Unit Tests:**
- Backwards compatibility: ungrouped ClusterInstance renders identically
- Merge logic for all field types (union, scalar, nested)
- Template variable substitution with and without groups
- Validation of group references and constraints
- CRD default detection (role, bootMode) during merge
- Mixed grouped/ungrouped node rendering

**Integration Tests:**
- HCP cluster with multi-replica NodePools from groups
- AI cluster with grouped nodes sharing InfraEnv
- Label-only grouping with and without `spec.groups[]`

**E2E Tests:**
- Deploy HCP cluster with heterogeneous groups
- Deploy AI cluster with grouped workers
- Lifecycle: add/remove nodes from groups, verify re-reconciliation

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] `group` field added to NodeSpec API
- [ ] `spec.groups[]` added to ClusterInstance API
- [ ] Group-level and grouped-node template rendering implemented
- [ ] Merge logic for group defaults and node overrides implemented
- [ ] Built-in `*-group-templates-v1` and `*-grouped-node-templates-v1`
      for HCP and AI
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
one (same shareable fields as `spec.groups[]`) is planned for a
follow-on enhancement. This would support:

- **Late-binding**: Nodes discovered and bound to clusters after cluster
  creation, rather than requiring all nodes in the ClusterInstance at
  creation time.
- **Shared inventory**: Multiple ClusterInstances referencing the same
  NodeGroup; each renders its own HyperShift NodePool binding to the
  shared nodes via agent selectors.
- **`ClusterInstance.spec.nodeGroups`**: A new field referencing
  NodeGroup CR names. `spec.nodes` MAY be empty when `nodeGroups` is
  set.
- In-line `spec.groups[]` MAY remain alongside the CR.

If a NodeGroup CR is introduced, it MUST include `spec.nodes` (inventory
is the reason the CR exists). A config-only CR without `spec.nodes` is
rejected; `spec.groups[]` already serves that purpose.

BMH ownership when two ClusterInstances reference one NodeGroup is
deferred to that enhancement.

## Implementation History

- 2026-05-11: Initial proposal created
- 2026-06-01: Enhanced with requirements, design details, and examples
- 2026-06-10: Human review completed and pushed
- 2026-09-16: Refactored: ClusterInstance-only scope with `spec.groups[]`,
  NodeGroup CR moved to Future Work, rejected approaches consolidated
  under Alternatives, RFC 2119 normative language throughout

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

**Rejected.** This default only helps a single template family serve
both cases, which we explicitly avoid: v1 templates use
`CurrentNode.HostName` and MUST NOT change; grouped templates require
an explicit `group`. Setting `GroupName` from `hostName` would allow
group templates to silently produce node-named resources, violating
Story 0.

### Retarget v1 InfraEnv/NodePool at GroupName and De-Duplicate

Modify `*-node-templates-v1` to name InfraEnv and NodePool after
`GroupName`, then collapse duplicate objects in the render loop.

**Rejected.** N copies of a NodePool named `workers` with
`replicas: 1` are not one NodePool with `replicas: N`. Colliding
GVK+name objects fight `AddObjects`, status tracking, and sync-wave
accounting. v1 templates MUST stay hostname-based.

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

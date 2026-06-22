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
last-updated: 2026-05-11
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

Add node grouping capabilities to SiteConfig to reduce configuration duplication
when multiple nodes share similar configurations. For Hosted Control Plane (HCP)
clusters, this enables creating NodePools per common grouping of nodes instead
of one NodePool per node. For Assisted Installer (AI) and Image-Based Installer
(IBI) clusters, this enables nodes to share common configuration fields (role,
templateRefs, bootMode, etc.) by referencing NodeGroup CRs, significantly reducing
ClusterInstance manifest size for large clusters.

## Motivation

SiteConfig currently defaults to defining configurations on either a per-cluster
or a per-node basis, leading to significant duplication and scalability challenges:

- **HCP clusters**: Each node in a ClusterInstance creates a separate InfraEnv
  and HyperShift NodePool with `replicas: 1`, requiring separate MachineConfig and
  PerformanceProfile ConfigMaps for each node. Alternately, templates may be written
  to create the same resources once per cluster, requiring a completely homogeneous
  set of nodes.
- **AI/IBI clusters**: Resources like InfraEnv for AI must be duplicated for
  each node and all node-based configurations must be repeated, even if they are
  the same for all nodes (e.g. template references).
- **Scalability**: Large clusters (50+
  nodes) with homogeneous node groups result in hundreds of nearly-identical
  configuration resources. Duplication of common fields in each node contributes
  to the growth of the ClusterInstance resource, which may exceed Kubernetes
  size limits for manifests.

The fundamental issue is the lack of a grouping mechanism that allows multiple nodes to share common configurations while maintaining their individual physical identities (BMC address, MAC address, hostname).

### User Stories

#### Story 1: HCP Cluster (Late-Binding) with Homogeneous Workers

As a cluster administrator, I want to deploy an HCP cluster with 10 identical worker nodes so that I can efficiently manage them as a single logical group without creating 10 separate NodePools and 10 sets of MachineConfig/PerformanceProfile ConfigMaps.

**Current state**: Templates would create 10 NodePools (each with `replicas: 1`), 10 InfraEnvs, and the admin must manage on their own as a pre-req 10 MachineConfig ConfigMaps and 10 PerformanceProfile ConfigMaps.

**Desired state**: Templates create 1 NodePool with `replicas: 10` and 1 InfraEnv. Admin is responsible for 1 MachineConfig ConfigMap and 1 PerformanceProfile ConfigMap.

#### Story 2: HCP Cluster (Late-Binding) with Heterogeneous Node Groups

As a cluster administrator, I want to deploy an HCP cluster with 6 high-performance compute nodes and 4 storage-optimized nodes so that I can apply different performance profiles to each group without managing 10 individual NodePools.

**Current state**: Templates create 10 separate NodePools, each with node-specific configurations. Admin keeps track of two sets of different MachineConfig and PerformanceProfile ConfigMaps.

**Desired state**: Templates create 2 NodePools (compute-pool with 6 replicas, storage-pool with 4 replicas), each with group-specific configurations. Admin has to deliver a MachineConfig and PerformanceProfile per NodePool.

#### Story 3: Multiple HCP Clusters with a common set of nodes

As a cluster administrator, I want to deploy a set of nodes in one sync wave, then HostedClusters and associated NodePools that will select from the set of nodes.

**Current state**: Operator will not allow creation of a cluster without its own worker nodes, even if the admin wishes to create the nodes manually.

**Desired state**: Templates create an InfraEnv and BareMetalHosts for all
nodes. Each ClusterInstance creates a HostedCluster and one or more NodePools.
Nodes are provisioned into the clusters by their NodePools according to agent
selectors defined in the NodePools.

#### Story 4: Large Assisted Cluster (Early-Binding) with many nodes

As a cluster administrator, I want to deploy an AI cluster with tens to hundreds of nodes.

**Current state**: Admin must maintain each node's configuration with duplicate
fields, (e.g. role, ironicInspect, bootMode,templateRefs, extraAnnotations)
inside the ClusterInstance.spec.nodes array itself. These duplicated fields
grow the ClusterInstance manifest in order of the number of nodes.

**Desired state**: Define important, but duplicated fields once in the NodeGroup, each node only has to reference the NodeGroup and supply distinct values like MAC and BMC info.

#### Story 5: AI Cluster with Consolidated InfraEnv

As a cluster administrator, I want to simplify the ACM interface view of my AI cluster by consolidating multiple InfraEnvs into fewer resources, without needing to define a NodeGroup CR or change node configurations.

**Current state**: Each node creates its own InfraEnv resource, resulting in many InfraEnvs visible in the ACM interface for a single cluster.

**Desired state**: Nodes with the same `group` value share a single InfraEnv, even when no NodeGroup CR exists. The `group` field alone is sufficient to trigger resource consolidation at the template level.

**Example**: Existing ClusterInstance with 20 nodes can add `group: workers` to all nodes. Templates detect the grouping and render 1 InfraEnv instead of 20, without requiring a NodeGroup CR to be created.


### Goals

1. **Reduce configuration duplication**: Enable sharing of identical configurations (MachineConfigs, PerformanceProfiles, even InfraEnv) across multiple nodes
2. **Support replica counts for HCP NodePools**: Allow creating HyperShift NodePools with `replicas = N` from ClusterInstance definitions, de-duplicating MachineConfig and PerformanceProfile resources
3. **Backwards compatibility**: Existing ClusterInstance CRs continue to work without modification
4. **Support all cluster types**: Grouping must apply to all current installation types (AI, IBI, HCP). Grouping *should* anticipate and not block requested additional cluster installation types based around virtualization platforms like KubeVirt, VMWare, and Nutanix.
5. **Lifecycle support**: Grouping should be available at all stages of the ZTP lifecycle (e.g., using a group to pause/unpause an MCP for updates). Node hardware maintenance should be possible using established procedures without side-effects.
6. **Template variable for group**: Define a template variable for "group" that defaults appropriately (specific default TBD in open questions)
7. **Support future NMState de-duplication**: The actual NMState work is under non-goals, but consideration should be made for how this feature will affect that implementation.

### Stretch Goals

1. **Ability to scale node groups in or out**: Agent-based node installation allows nodes to be discovered and held ready for install without affecting a cluster's node count until it is time to scale the cluster.
2. **Support late binding scenarios for non-HCP clusters**: While maintaining backwards compatibility (Goal 3), provide an optional way to define nodes for late-binding scenarios. This supports new node definition flows where nodes can be discovered and bound to clusters after cluster creation, rather than requiring all nodes to be defined in the ClusterInstance at creation time.

### Non-Goals

1. **NMState deduplication**: Sharing NMState configurations across nodes with node-specific overrides (e.g., MAC and IP addresses) is considered future work outside the scope of this enhancement. The grouping mechanism created here will form the basis to implement this future goal, but the actual templating of NMStates represents a significant change that should be addressed separately.

## Proposal

This proposal introduces node grouping through:
1. An optional `group` field in `NodeSpec` for grouping nodes
2. An optional `NodeGroup` CRD that defines templates and configuration for nodes in that group

### Core Design

**When nodes have no `group` field** (backwards compatible):
- Behavior unchanged - one resource per node as today
- Each node uses its own individual configuration

**When nodes have a `group` field**:
- The `group` value is a simple string (e.g., `group: workers`)
- **Without NodeGroup CR**: Templates can detect grouping and consolidate resources (e.g., one InfraEnv per group instead of per node)
- **With NodeGroup CR**: Additional benefits:
  - Nodes can be defined in two ways:
    1. **In ClusterInstance**: Nodes reference a `group` value matching a NodeGroup CR name
    2. **In NodeGroup**: NodeGroup CR contains a `spec.nodes` array defining the actual nodes
  - The NodeGroup CR defines:
  - Group-level templates (`spec.templateRefs`) - rendered once per group for truly shared resources (InfraEnv, BareMetalHost)
  - Node-level configuration (`spec.nodeConfig`) - shared defaults for all nodes in the group (e.g., role, tuning)
  - Node-level templates (`spec.nodeConfig.templateRefs`) - rendered per node
  - Optional nodes array (`spec.nodes`) - the actual node definitions (hostname, BMC, etc.)
- **For HCP clusters**:
  - Nodes referencing a group render as a single HyperShift NodePool with `replicas` matching node count
  - **One-to-one**: Single ClusterInstance creates one NodePool
  - **Many-to-one** (see Open Questions): Multiple ClusterInstances may share a NodeGroup; each creates its own NodePool binding to the shared nodes via agent selectors
- **For AI/IBI clusters**:
  - Nodes referencing a group share configuration from NodeGroup (role, templateRefs, bootMode, etc.)
  - Reduces duplication in ClusterInstance.spec.nodes array
  - Future: NMState configuration sharing with variable substitution (see Non-Goals)

#### Example: AI Cluster with Group-based Resource Consolidation (No NodeGroup CR)

Simplest approach - just add `group` field to consolidate resources:

```yaml
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: ClusterInstance
metadata:
  name: ai-cluster-simple
spec:
  clusterType: AI
  nodes:
  - hostName: worker1.example.com
    group: workers  # Simple string, no NodeGroup CR needed
    bmcAddress: idrac-virtualmedia://192.168.1.10/redfish/v1/Systems/System.Embedded.1
    role: worker
  - hostName: worker2.example.com
    group: workers  # Same group
    bmcAddress: idrac-virtualmedia://192.168.1.11/redfish/v1/Systems/System.Embedded.1
    role: worker
  - hostName: worker3.example.com
    group: workers  # Same group
    bmcAddress: idrac-virtualmedia://192.168.1.12/redfish/v1/Systems/System.Embedded.1
    role: worker
```

**Results**:
- Templates detect that nodes share `group: workers`
- Creates 1 InfraEnv for the group instead of 3 (one per node)
- Simplifies ACM interface view
- No NodeGroup CR required - just the group string is sufficient for resource consolidation

#### Example: HCP Cluster with Multi-Replica NodePools

Using NodeGroup CRs for full configuration sharing:

First, define the NodeGroup CRs:

```yaml
---
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: NodeGroup
metadata:
  name: compute-workers
spec:
  templateRefs:
    - name: hcp-nodepool-template
    - name: infraenv-template
  nodeConfig:
    role: worker
    templateRefs:
      - name: baremetalhost-template
      - name: compute-tuning
---
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: NodeGroup
metadata:
  name: storage-workers
spec:
  templateRefs:
    - name: hcp-nodepool-template
    - name: infraenv-template
  nodeConfig:
    role: worker
    templateRefs:
      - name: baremetalhost-template
      - name: storage-tuning
```

Then, create the ClusterInstance referencing these groups:

```yaml
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: ClusterInstance
metadata:
  name: hcp-cluster-example
spec:
  clusterType: HCP
  nodes:
  - hostName: worker1.example.com
    group: compute-workers  # References NodeGroup CR
    bmcAddress: idrac-virtualmedia://192.168.1.10/redfish/v1/Systems/System.Embedded.1
  - hostName: worker2.example.com
    group: compute-workers
    bmcAddress: idrac-virtualmedia://192.168.1.11/redfish/v1/Systems/System.Embedded.1
  - hostName: worker3.example.com
    group: compute-workers
    bmcAddress: idrac-virtualmedia://192.168.1.12/redfish/v1/Systems/System.Embedded.1
  - hostName: worker4.example.com
    group: compute-workers
    bmcAddress: idrac-virtualmedia://192.168.1.13/redfish/v1/Systems/System.Embedded.1
  - hostName: storage1.example.com
    group: storage-workers  # References different NodeGroup CR
    bmcAddress: idrac-virtualmedia://192.168.1.20/redfish/v1/Systems/System.Embedded.1
  - hostName: storage2.example.com
    group: storage-workers
    bmcAddress: idrac-virtualmedia://192.168.1.21/redfish/v1/Systems/System.Embedded.1
```

**Results**:
- Creates 2 NodePools: `compute-workers` with `replicas: 4` and `storage-workers` with `replicas: 2`
- Creates 6 BareMetalHost resources (one per node)
- MachineConfig ConfigMaps needed: 2 (one per NodeGroup) instead of 6 (one per node)
- PerformanceProfile ConfigMaps needed: 2 instead of 6

#### Example: NodeGroup with Nodes Defined Inside

Alternatively, nodes can be defined directly in the NodeGroup CR:

```yaml
---
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: NodeGroup
metadata:
  name: compute-workers
spec:
  templateRefs:
    - name: hcp-nodepool-template
    - name: infraenv-template
  nodeConfig:
    role: worker
    templateRefs:
      - name: baremetalhost-template
      - name: compute-tuning
  nodes:
    - hostName: worker1.example.com
      bmcAddress: idrac-virtualmedia://192.168.1.10/redfish/v1/Systems/System.Embedded.1
    - hostName: worker2.example.com
      bmcAddress: idrac-virtualmedia://192.168.1.11/redfish/v1/Systems/System.Embedded.1
    - hostName: worker3.example.com
      bmcAddress: idrac-virtualmedia://192.168.1.12/redfish/v1/Systems/System.Embedded.1
---
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: ClusterInstance
metadata:
  name: hcp-cluster-example
spec:
  clusterType: HCP
  nodeGroups:
    - compute-workers  # Reference NodeGroup by name; nodes come from NodeGroup.spec.nodes
```

**Results**:
- Creates 1 NodePool: `compute-workers` with `replicas: 3`
- Creates 3 BareMetalHost resources (from NodeGroup.spec.nodes)
- ClusterInstance is simpler - just references the group, doesn't duplicate node definitions

### Workflow Description

**Actors**: Cluster administrator, SiteConfig operator

1. **Create NodeGroup CRs** (one-time setup per group type):
   - Define NodeGroup CRs for each type of node configuration needed
   - Configure group-level templates in `spec.templateRefs` (NodePool, InfraEnv - rendered once per group)
   - Configure node-level defaults in `spec.nodeConfig` (role, tuning, etc.)
   - Configure node-level templates in `spec.nodeConfig.templateRefs` (BareMetalHost - rendered per node)
   - **Optionally** define actual nodes in `spec.nodes` array (hostname, BMC, etc.)
   - Example: Create `compute-workers` and `storage-workers` NodeGroup CRs with different tuning templates

2. **Create ClusterInstance referencing the groups**:
   - **Option A**: Define nodes in ClusterInstance, each with a `group` field referencing a NodeGroup CR name
   - **Option B**: Reference NodeGroup names in `spec.nodeGroups`, and nodes come from NodeGroup.spec.nodes
   - Nodes inherit NodeGroup's configuration, with per-node overrides possible in Option A
   - Example Option A: ClusterInstance with 10 nodes, each with `group: compute-workers`
   - Example Option B: ClusterInstance with `nodeGroups: [compute-workers]`, nodes defined in NodeGroup

3. **SiteConfig processes the ClusterInstance**:
   - Operator validates that referenced NodeGroup CRs exist
   - For each unique group:
     - Renders group-level templates once (e.g., one NodePool with `replicas: <node-count>`)
     - Renders node-level templates for each node (e.g., BareMetalHost per node)
   - Administrator provides fewer ConfigMaps (1 MachineConfig ConfigMap per group instead of per node)

4. **Apply node-specific overrides** (optional):
   - Individual nodes can override NodeGroup defaults via their own spec fields
   - Example: One node in `compute-workers` can override the role or add extra labels

5. **Monitor deployment**:
   - Observe status conditions on ClusterInstance
   - For HCP: Verify NodePool has correct replica count and nodes are joining
   - For AI/IBI: Verify nodes are provisioned with group configurations

### API Extensions

This enhancement adds the following API elements:

#### ClusterInstance Extensions

```yaml
# Option A: Nodes with group references
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: ClusterInstance
spec:
  nodes:
  - group: string  # NEW: Optional field referencing a NodeGroup CR name
    hostName: worker1.example.com
    bmcAddress: ...
    # ... existing NodeSpec fields ...

# Option B: Reference NodeGroups directly
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: ClusterInstance
spec:
  nodeGroups:  # NEW: Optional field listing NodeGroup CR names
    - compute-workers
    - storage-workers
  # Nodes come from NodeGroup.spec.nodes arrays
```

#### New NodeGroup CRD

**NodeGroup CRD** (new):
```yaml
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: NodeGroup
metadata:
  name: worker-group
spec:
  templateRefs:
    # Group-level templates - rendered once per group (e.g., NodePool, InfraEnv)
    - name: hcp-nodepool-template
    - name: infraenv-template
  nodeConfig:
    # Node-level configuration shared by all nodes in the group
    role: worker
    templateRefs:
      # Node-level templates - rendered per node (e.g., BareMetalHost)
      - name: baremetalhost-template
      - name: worker-tuning
  nodes:
    # Optional: Define actual nodes in the group (can also reference this group from ClusterInstance)
    - hostName: worker1.example.com
      bmcAddress: idrac-virtualmedia://192.168.1.10/redfish/v1/Systems/System.Embedded.1
      # Node-specific overrides can go here
    - hostName: worker2.example.com
      bmcAddress: idrac-virtualmedia://192.168.1.11/redfish/v1/Systems/System.Embedded.1
  # agentSelector (optional, for future late-binding with discovered agents):
  # agentSelector:
  #   matchLabels:
  #     node-role: worker
  #     cpu-type: high-performance
```


### Siteconfig Impact

**Controllers affected:**
- **ClusterInstanceReconciler**: Modified to handle node grouping logic, render multi-replica resources, and merge group/node configurations

**Templates affected:**
- **HCP templates**: Some node-level templates like InfraEnv and NodePool would move to new group-level templates
- **AI/IBI templates**: InfraEnv templates may be updated to leverage grouping (NMStateConfig sharing is future work)

**Template variables:**

Templates have access to different variables depending on their scope. **Note**: `.Spec` is reserved for ClusterInstance.spec fields; NodeGroup fields are exposed as flat `.SpecialVars.<field>` after merging.

- **Node-level templates** (rendered per node, e.g., BareMetalHost):
  - `{{ .Spec.<field> }}` - fields from ClusterInstance.spec (e.g., clusterName, baseDomain)
  - `{{ .SpecialVars.GroupName }}` - the group name this node references (from node.group field)
  - `{{ .SpecialVars.<field> }}` - if NodeGroup CR exists: fields from `NodeGroup.spec.nodeConfig` merged with node overrides; otherwise: node's own fields
  - Example: `{{ .SpecialVars.role }}`, `{{ .SpecialVars.nodeLabels }}`, etc.

- **Group-level templates** (rendered once per group, e.g., InfraEnv, BareMetalHost):
  - `{{ .Spec.<field> }}` - fields from ClusterInstance.spec (e.g., clusterName, baseDomain)
  - `{{ .SpecialVars.GroupName }}` - the NodeGroup CR name (from metadata.name)
  - `{{ .SpecialVars.<field> }}` - fields from NodeGroup.spec and NodeGroup.spec.nodeConfig
    - Example: `{{ .SpecialVars.role }}`, `{{ .SpecialVars.templateRefs }}`, etc.
  - `{{ .SpecialVars.NodeCount }}` - computed value: number of nodes referencing this group (for replica counts)

- **Cluster-group templates** (proposed for many-to-one scenarios, see Open Questions):
  - Rendered once per ClusterInstance-NodeGroup pair (e.g., NodePool when multiple clusters share nodes)
  - Would have access to both `{{ .Spec.<field> }}` (ClusterInstance) and `{{ .SpecialVars.<field> }}` (NodeGroup)
  - Allows each cluster to create cluster-specific resources (NodePool) while sharing the NodeGroup's nodes

**API fields:**
- New `group` field in `NodeSpec` for referencing a NodeGroup CR by name (Option A)
- New `nodeGroups` field in `ClusterInstance.spec` for listing NodeGroup CR names (Option B)
- New `NodeGroup` CRD for defining:
  - Group-level and node-level templates
  - Node-level configuration defaults
  - Optional `nodes` array for defining actual nodes within the NodeGroup

**Validation:**
- Validate that nodes referencing the same group have compatible configurations (same role, compatible network topology)
- When a NodeGroup CR with matching name exists:
  - Validate that the NodeGroup CR is in the same namespace as the ClusterInstance
  - Validate referenced fields are compatible
- When no matching NodeGroup CR exists:
  - The `group` field is treated as a simple grouping label
  - Templates may use it for resource consolidation (e.g., one InfraEnv per group)
  - No error - NodeGroup CRs are optional

## Design Details

### Core API Design: Groups as Separate CRD

Groups are defined as a separate `NodeGroup` CRD rather than being implicit or embedded in ClusterInstance. This provides:

1. **Explicit definition**: Groups and their configurations are clearly visible and manageable as independent resources
2. **Separation of concerns**: NodeGroup CRs define templates and default configurations; ClusterInstance nodes reference them and provide node-specific details
3. **Reusability**: Same NodeGroup CR can be referenced by nodes in multiple ClusterInstances

### Constraint: One Group Per Node

To avoid undue complexity, **only one group should be assignable to a node** (excepting an optional "default group"). This constraint:

- Simplifies merge logic and reduces ambiguity
- Makes configurations easier to understand and debug
- Aligns with common use cases (nodes belong to one functional group)

A special "default" group may optionally be supported to provide base configurations that apply to all nodes unless overridden.

### Group Configuration Fields

The NodeGroup CRD includes two levels of configuration:

At `spec` level (group-wide):
- `agentSelector` - labels to match agents for late-binding
- `templateRefs` - group-level templates rendered once per group (e.g., NodePool, InfraEnv)

At `spec.nodeConfig` level (per-node defaults):
- `automatedCleaningMode`
- `bootMode`
- `cpuArchitecture`
- `extraAnnotations`
- `extraLabels`
- `ignitionConfigOverride`
- `installerArgs`
- `ironicInspect`
- `nodeLabels`
- `nodeNetwork`
- `pruneManifests`
- `role`
- `suppressedManifests`
- `templateRefs` - node-level templates rendered once per node (e.g., BareMetalHost)

### Merge Logic: Field Inheritance and Override

When a node in ClusterInstance references a NodeGroup, the final configuration is determined by merging:
- NodeGroup's `spec.nodeConfig` (group-level defaults)
- Individual node's fields in ClusterInstance (node-specific overrides)

The merge logic follows these rules:

#### Union Fields (Lists/Maps)

For `annotations`, `labels`, `pruneManifests`, `suppressedManifests`, and `templateRefs`:
- Result is the **union** of group and node lists/maps
- If a key/item is present in both group and node:
  - **Node's value overrides** the group's value for that key
  - For lists (like `pruneManifests`), duplicates are removed

**Example**:
```yaml
# NodeGroup CR
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: NodeGroup
metadata:
  name: workers
spec:
  nodeConfig:
    nodeLabels:
      tier: compute
      zone: us-east-1
    extraAnnotations:
      custom.io/setting: group-value
    templateRefs:
    - name: base-config
    - name: security-policy

# When a discovered agent has node-specific labels/annotations,
# they would be merged with the NodeGroup configuration:
#
# Effective configuration for a discovered node:
# nodeLabels:
#   tier: compute         # From NodeGroup
#   zone: us-east-1       # From NodeGroup
#   rack: rack-01         # From discovered agent
# extraAnnotations:
#   custom.io/setting: group-value    # From NodeGroup
#   node.io/specific: agent-value     # From discovered agent
# templateRefs:
# - name: base-config           # From NodeGroup
# - name: security-policy       # From NodeGroup
```

#### Scalar Fields

For scalar fields like `automatedCleaningMode`, `bootMode`, `cpuArchitecture`, `role`:
- Node value **completely overrides** group value if specified
- Group value is used if node doesn't specify

#### Complex Nested Fields: nodeNetwork

For `nodeNetwork` (NMstate configuration):
*Note: NMState configuration sharing is future work. The merge logic below describes the intended behavior when implemented.*

- If node specifies `nodeNetwork.config`, it completely overrides the group's config
- Group's `nodeNetwork.config` is only used if node doesn't specify its own

**Scope Override Logic Tree**:
```text
IF node has custom nodeNetwork.config:
    USE node's nodeNetwork.config
ELSE IF node references NodeGroup AND NodeGroup has nodeNetwork.config:
    USE NodeGroup's nodeNetwork.config
ELSE:
    No shared network config (use node-specific templates as today)
```

### Implementation Details/Notes/Constraints

1. **Template Rendering Scopes**:
   - **Group-level templates** (`NodeGroup.spec.templateRefs`): Rendered once per group, for truly shared resources (InfraEnv, BareMetalHost)
   - **Node-level templates** (`NodeGroup.spec.nodeConfig.templateRefs`): Rendered once per node
   - **Cluster-group templates** (see Open Questions): For many-to-one HCP scenarios where multiple ClusterInstances share a NodeGroup, each cluster may need its own NodePool template
     - Proposed: `ClusterInstance.spec.groupTemplateRefs` - rendered once per ClusterInstance-NodeGroup pair
     - This allows each cluster to create its own NodePool while sharing the NodeGroup's nodes
   - This distinction allows templates to be organized by their cardinality (1:group vs 1:node vs 1:cluster-group)

2. **Template Variable Naming**:
   - `{{ .Spec.<field> }}` is **reserved for ClusterInstance.spec fields** in all templates
   - `{{ .SpecialVars.GroupName }}` available in both node-level and group-level templates
   - All NodeGroup.spec.nodeConfig fields exposed as flat `{{ .SpecialVars.<field> }}` (e.g., `.SpecialVars.role`, `.SpecialVars.nodeLabels`)
   - **Merge happens before rendering**: Node-level templates receive NodeGroup defaults merged with per-node overrides
   - See "Template variables" section in Siteconfig Impact for complete reference
   - Backwards compatibility: When `group` field is not set, `.SpecialVars` contains node's own field values

3. **Rendering Order**:
   - Merge group and node configurations first
   - Render group-level templates once per group
   - Render node-level templates once per discovered agent/node
   - Ensure template variables have access to both group and final merged values

3. **HCP-Specific Constraints**:
   - All nodes in a group must have the same `role`
   - MachineConfig and PerformanceProfile ConfigMaps are referenced by the group name
   - NodePool `replicas` count must match number of nodes in group

4. **AI/IBI-Specific Constraints**:
   - InfraEnv can potentially be shared across nodes in same group
   - NMStateConfig sharing is future work and not part of this enhancement

5. **Validation Requirements**:
   - Validate group references exist before nodes reference them
   - Ensure no orphaned group definitions (groups not referenced by any node)
   - Check for conflicting configurations within a group (e.g., different roles for HCP)

### Use Cases

This design supports the following use cases identified in requirements:

1. **Groups of nodes with common perf-tuning and/or MachineConfig**:
   - HCP: Single NodePool with shared MachineConfig/PerformanceProfile ConfigMaps

2. **Groups of nodes with a common set of policies**:
   - HCP: Hub-side policies can target NodePools to control lifecycle
   - Use `nodeLabels` at group level to apply common policy selectors

3. **Upgrade multiple groups of workers in parallel with minimum disruption per group**:
   - HCP: Configure `maxUnavailable` or similar settings per NodePool
   - Multiple NodePools can be upgraded in parallel by hub policies

4. **Mechanism for minimum availability during install**:
   - Set minimum availability requirements at group level
   - Allow zeroing out at critical lifecycle events (e.g., initial install)
   - Support progressive rollout patterns

### Risks and Mitigations

- Complexity in merge logic could lead to unexpected configurations

  **Mitigation**: Clear documentation, validation warnings, status conditions showing effective config

- Breaking changes to existing templates

  **Mitigation**: Ensure backwards-compatibility -- existing unit tests should help.

  **Option**: Create a new set of node templates with a different name like "grouped-node" that can be used with new "group" templates.

- HCP NodePool replicas drift from ClusterInstance node count

  **Mitigation**: NodeGroup referential replicas should be adjustable over time.

### Drawbacks

- **Increased API complexity**: Adding NodeGroup CRD and merge logic increases API surface area.
- **Additional CRD to manage**: Users may now manage NodeGroup CRs in addition to ClusterInstance CRs.
- **Template migration effort**: Existing templates need updates to support group-level vs node-level rendering.
- **Testing complexity**: A new set of units will be required. Validator will need to cover more complex scenarios when counting how many nodes are in a cluster's configuration.

### Test Plan

**Unit Tests**:
- Merge logic for all field types (union, scalar, nested)
- Template variable substitution with groups
- Validation of group references and constraints
- Template override hierarchy tested with and without groups

**Integration Tests**:
- HCP cluster with multi-replica NodePools
- AI/IBI cluster with grouped nodes (establishing grouping mechanism)
- Mixed cluster with grouped and ungrouped nodes
- Group configuration overrides at node level

**E2E Tests**:
- Deploy HCP cluster using group field
- Deploy AI cluster with grouped nodes
- Scale operations (add/remove nodes from groups)
- Lifecycle operations (pause/unpause, upgrade) on groups

**Test Scenarios**:
1. Single group with two nodes (HCP)
2. Multiple groups with heterogeneous nodes (HCP)
3. AI/IBI cluster with grouped nodes
4. Backwards compatibility (no groups, existing behavior)
5. Mixed cluster with both grouped and ungrouped nodes
6. Invalid configurations (validation tests)

### Graduation Criteria

- [ ] Design reviewed and approved by maintainers
- [ ] `group` field added to NodeSpec API
- [ ] NodeGroup CRD designed and implemented
- [ ] HCP multi-replica NodePool rendering implemented
- [ ] Group-level and node-level template rendering working
- [ ] Merge logic for NodeGroup defaults and node-specific overrides implemented
- [ ] Unit tests cover grouping logic and merge behavior
- [ ] Integration tests for HCP use cases with multiple groups
- [ ] E2E tests for backwards compatibility (ungrouped nodes)
- [ ] Validation webhooks for NodeGroup references
- [ ] User-facing documentation complete
- [ ] Template examples for common use cases (HCP with groups, mixed grouped/ungrouped)
- [ ] Released in version X.Y.Z

### Upgrade / Downgrade Strategy

**Upgrade**:
- This feature is purely additive, no migration needed
- Existing ClusterInstances without the `group` field continue to work unchanged
- Users can opt-in to grouping by:
  1. Creating NodeGroup CRs
  2. Adding `group` fields to nodes in ClusterInstance referencing those NodeGroup CRs

**Downgrade**:
- Remove `group` field from nodes in ClusterInstance (nodes become ungrouped, revert to individual resources)
- Delete NodeGroup CRs (they are no longer referenced)
- Admission webhook should warn if NodeGroup CRs are still referenced by active ClusterInstances

### Version Skew Strategy

**SiteConfig Operator <-> ClusterInstance CRs**:
- Older operator ignores unknown `group` field in NodeSpec
- Newer operator handles both old (ungrouped) and new (grouped) ClusterInstances
- API version conversion webhooks handle schema evolution
- NodeGroup CRs are only processed by operators that understand them

**SiteConfig <-> Hub/ACM**:
- Generated resources (NodePools, etc.) are standard upstream types
- No version skew concerns for generated resources

**SiteConfig <-> Templates**:
- Templates that don't use group-related variables continue to work
- Group template variables are optional (only populated when nodes reference groups)
- Template validation warns if group variables are used but nodes don't reference groups

### Open Questions

- **Template variable default for `group`**:
  - Should it be **undefined by default**?
  - Should it be **related to the cluster name**?
  - Should it **default to the node name** (matching existing behavior)?

   **Recommendation**: Default to node name for backwards compatibility.

- **Special "default" group**:
  - Should there be a reserved `default` group that applies to all nodes?
  - How would it interact with explicit group assignments?

   **Recommendation**: Optional feature for future work, not required for this EP

- **NodeGroup CRD scope**:
 Should it support cross-namespace references?
 If so, should it be cluster-scoped or namespace-scoped?

   **Recommendation**: Namespace-scoped for security, evaluate cross-namespace in future

- **Should NodeGroup fields present under .Spec or .SpecialVar?**:
  - If we use `.Spec.` then we need to provide `.SpecialVar` to expose some `ClusterInstance.spec` fields
  - If we use `.SpecialVar` then do we provide the whole `NodeGroup` API?

  **Recommendation**: Reserve `.Spec` for ClusterInstance.spec fields; expose NodeGroup fields as flat `.SpecialVars.<field>`
  - `.Spec.<field>` - **always** refers to ClusterInstance.spec fields (clusterName, baseDomain, etc.) for consistency
  - `.SpecialVars.GroupName` - the group name, available in both node-level and group-level templates
  - `.SpecialVars.<field>` - NodeGroup.spec.nodeConfig fields exposed as **flat variables** (no hierarchy)
    - Examples: `.SpecialVars.role`, `.SpecialVars.nodeLabels`, `.SpecialVars.templateRefs`
    - Node-level templates: receive **merged values** (NodeGroup defaults + per-node overrides)
    - Group-level templates: receive NodeGroup.spec.nodeConfig values directly
  - `.SpecialVars.NodeCount` - computed value for replica counts, available in group-level templates
  - **No nested structures** like `.SpecialVars.Group.*` or `.SpecialVars.NodeConfig.*` - keep it simple and flat

- **How to define templates in many-to-one HCP case**:
If multiple HCP clusters need to create their own NodePools to bind to a NodeGroup's nodes, where does the templateRef for NodePool go?

**Problem**:
- NodeGroup.spec.templateRefs would create **one** NodePool for the group
- But in many-to-one scenario, we need **one NodePool per ClusterInstance**
- Each ClusterInstance's NodePool needs cluster-specific values (clusterName, namespace, etc.)

**Recommendation**: Add cluster-scoped templateRefs
- NodeGroup.spec.templateRefs: for truly shared resources (InfraEnv, BareMetalHost)
- ClusterInstance.spec.groupTemplateRefs: for per-cluster resources that bind to shared groups (NodePool)
- This creates a new template rendering scope: **cluster-group level** (one per ClusterInstance-NodeGroup pair)
- Templates at this level have access to both ClusterInstance.spec and NodeGroup.spec via SpecialVars

## Alternatives

### Alternative 1: Build groups API into ClusterInstance

**Pros**
- Simplifies siteconfig operator structure by not requiring a new CRD
- Is functionally equivalent except for sharing of groups between clusters

**Cons**
- Cannot create a node group that spans clusters (conflict with HCP model)

**Decision**
- This change is being driven by a need for HCP clusters, so we require a CRD not entangled with a particular ClusterInstance definition.

### Alternative 2: Use "Pool" Terminology

Use `nodePool`, `pool`, or `nodePoolName` instead of `group` for the field name.

**Pros**:
- Aligns superficially with HyperShift NodePool terminology
- "Pool" is a familiar concept in infrastructure management

**Cons**:
- **Implies fungibility**: "Pool" suggests nodes are interchangeable resources that can be drawn from arbitrarily, which is not necessarily accurate beyond the HCP example.
- **Confusing distinction**: Overloading of NodePool between the new CRD and HyperShift's actual NodePool resource. Admins will need to include full api path like `nodepools.hypershift.openshift.io` vs `nodepools.siteconfig.open-cluster-management.io`

**Decision**: Rejected in favor of "group" terminology. "Group" accurately conveys that nodes share common characteristics and configurations while maintaining their individual identities and non-fungible nature.

## Implementation History

- 2026-05-11: Initial proposal created
- 2026-06-01: Enhanced with requirements from source document, added Design Details, examples, and missing sections
- 2026-06-10: Human review and tweaking of AI output completed and pushed. WIP removed.

## Infrastructure Needed

- No new infrastructure required
- Existing test infrastructure (unit, integration, e2e) sufficient
- May need additional test clusters for scale testing (50+ nodes)

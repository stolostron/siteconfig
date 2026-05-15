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
(IBI) clusters, this enables sharing configurations like NMStateConfigs and
MachineConfigs across multiple nodes.

## Motivation

SiteConfig currently defaults to defining configurations on a per-node basis,
leading to significant duplication and scalability challenges:

- **HCP clusters**: Each node in a ClusterInstance creates a separate InfraEnv
  and HyperShift NodePool with `replicas: 1`, requiring separate MachineConfig and
  PerformanceProfile ConfigMaps for each node
- **AI/IBI clusters**: Identical NMStateConfigs, MachineConfigs, and other
  resources (like InfraEnv for AI) must be duplicated for each node with similar
  hardware and configuration requirements
- **Scalability**: Large clusters (50+
  nodes) with homogeneous node groups result in hundreds of nearly-identical
  configuration resources

The fundamental issue is the lack of a grouping mechanism that allows multiple nodes to share common configurations while maintaining their individual physical identities (BMC address, MAC address, hostname).

### User Stories

#### Story 1: HCP Cluster (Late-Binding) with Homogeneous Workers

As a cluster administrator, I want to deploy an HCP cluster with 10 identical worker nodes so that I can efficiently manage them as a single logical group without creating 10 separate NodePools and 10 sets of MachineConfig/PerformanceProfile ConfigMaps.

**Current state**: Templates would create 10 NodePools (each with `replicas: 1`), 10 InfraEnvs, and the admin must manage on their own as a pre-req 10 MachineConfig ConfigMaps and 10 PerformanceProfile ConfigMaps.

**Desired state**: Templates create 1 NodePool with `replicas: 10` and 1 InfraEnv. Admin is responsible for  1 MachineConfig ConfigMap and 1 PerformanceProfile ConfigMap.

#### Story 2: HCP Cluster (Late-Binding) with Heterogeneous Node Groups

As a cluster administrator, I want to deploy an HCP cluster with 6 high-performance compute nodes and 4 storage-optimized nodes so that I can apply different performance profiles to each group without managing 10 individual NodePools.

**Current state**: Templates create 10 separate NodePools, each with node-specific configurations. Admin keeps track of two sets of different MachineConfig and PerformanceProfile ConfigMaps.

**Desired state**: Templates create 2 NodePools (compute-pool with 6 replicas, storage-pool with 4 replicas), each with group-specific configurations. Admin has to deliver a MachineConfig and PerformanceProfile per NodePool.

#### Story 3: AI Cluster (Early-Binding) with Shared Network Configuration

As a cluster administrator, I want to deploy an AI cluster where 5 worker nodes share the same NMStateConfig (same network topology, different IPs) so that I don't have to duplicate the entire network configuration 5 times with only IP addresses changing.

**Current state**: Templates create 5 nearly-identical NMStateConfig resources, duplicating network interface definitions, routes, and DNS settings. Admin must maintain all these NMStateConfig resources in their entirety within the ClusterInstance.
**Desired state**: Define network configuration once, reference it from multiple nodes, with node-specific values (IPs) parameterized

#### Story 4: Multiple HCP Clusters with a common NodePool

As a cluster administrator, I want to deploy a set of nodes in one sync wave, then HostedClusters and associated NodePools that will select from the set of nodes.

**Current state**: Operator will not allow creation of a cluster without its own worker nodes, even if you handle this manually.
**Desired state**: Templates create a HostedCluster and one or more NodePools. Nodes are provisioned into the cluster by the NodePool as it finds agents that match.

### Goals

1. **Reduce configuration duplication**: Enable sharing of identical configurations (NMState templates, MachineConfigs, PerformanceProfiles, even InfraEnv) across multiple nodes
2. **Multi-replica NodePools for HCP**: Allow creating HyperShift NodePools with `replicas = N` from ClusterInstance definitions
3. **Backwards compatibility**: Existing ClusterInstance CRs continue to work without modification
4. **Support all cluster types**: Solution works for HCP, AI, and IBI clusters
5. **Clear semantics**: Grouping mechanism is intuitive and aligns with Kubernetes/OpenShift patterns
6. **Logical Decoupling of nodes from ClusterInstance**: While maintaining backwards compatibility provide an optional way to define nodes for late binding scenarios.

### Stretch Goals

1. **Ability to scale node groups in or out**: Agent-based node installation allows nodes to be discovered and held ready for install without affecting a cluster's node count until it is time to scale the cluster.

### Non-Goals

1. **Addition of unmanaged nodes**: Implementation requires explicit node definition in either ClusterInstance or a new CRD for groups of nodes
2. **Template parameter substitution**: Advanced templating for node-specific values in shared configs (orthogonal feature)

## Proposal

This proposal introduces node grouping through a two-phase approach:

**Phase 1**: Add `nodePoolName` field to NodeSpec API, enabling simple grouping of explicitly-defined nodes
**Phase 2**: Introduce NodePoolTemplate CRD for advanced late-binding scenarios with agent selectors

### Phase 1: NodePoolName Field

Add an optional `nodePoolName` string field to the `NodeSpec` structure in the ClusterInstance API. Nodes with the same `nodePoolName` are grouped and share configurations:

- **For HCP clusters**: Grouped nodes render as a single HyperShift NodePool resource with `replicas` matching the node count
- **For AI/IBI clusters**: Grouped nodes share template rendering, reducing configuration duplication
- **Backwards compatible**: When `nodePoolName` is omitted, behavior is unchanged (one resource per node)

Special Var NodePoolName should be available in two scopes:

1. **Per node scope** Each node's template renderer should have access to its own pool name
2. **New loop over NodePools**: Allow a new set of templates to be rendered on a per node pool basis.

### Phase 2: NodePoolTemplate CRD

Introduce a new `NodePoolTemplate` CRD that defines reusable node pool configurations separate from ClusterInstance. ClusterInstances reference these templates and specify replica counts. This enables:

- **Late-binding**: HCP clusters can discover nodes via agent selectors instead of explicit definitions
- **Reusability**: Multiple clusters can reference the same pool template
- **Separation of concerns**: Infrastructure team manages physical inventory, cluster team references pools

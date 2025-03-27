# Configuring CPU Architecture

There are two places where the CPU architecture can be defined.

## Setting a Default Architecture for the Cluster

The default CPU architecture for all nodes in a cluster can be set using the `cpuArchitecture` field in the ClusterInstance resource. This is provided to allow easily configuring multiple nodes. If a mixed-architecture deployment is required, the value `multi` can be used.

If no value is provided here then it will be assumed to be `x86_64`.

As Image Based Installer only supports SNO, the architecture should instead be set using [Overriding Architecture for Individual Nodes](#overriding-architecture-for-individual-nodes).

```yaml
...
spec:
    cpuArchitecture: multi
...
    nodes:
        - hostname: node1
          cpuArchitecture: aarch64
        ...
        - hostname: node2
          cpuArchitecture: x86_64
        ...
        - hostname: node3
        ...
```

## Overriding Architecture for Individual Nodes

Each node in the `nodes` list can specify its `cpuArchitecture` field to override the default set at the cluster level.

If no value is provided, the node inherits the cluster-level default (if specified). Otherwise, it defaults to `x86_64`.

```yaml
...
nodes:
    - hostname: example-node
      cpuArchitecture: aarch64
      ...
```

## Configuring ARM Nodes for Assisted Installer

Installing ARM nodes with Assisted Installer currently requires setting an annotation to ensure the Ironic agent has the correct image. This annotation is specified using the `extraAnnotations` field, which is supported at both the cluster and node levels. However, it should only be applied to ARM nodes.

```yaml
extraAnnotations:
    InfraEnv:
        infraenv.agent-install.openshift.io/ironic-agent-image-override: quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:placeholder
```

For more details, refer to the upstream documentation: <https://github.com/openshift/assisted-service/tree/master/docs/hive-integration#ironic-agent-image>.

## Configuring ARM Nodes for Image Based Installer

Installing ARM nodes with the Image Based Installer is currently unsupported.

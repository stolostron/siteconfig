# Configuring CPU architecture

You can define the CPU architecture for your nodes with the SiteConfig operator on cluster and node levels.

**Note:** Installing ARM nodes with the Image Based Installer is currently not supported.

## Configuring CPU architecture on cluster level

You can set the CPU architecture for all nodes in a cluster by using the `spec.cpuArchitecture` field in the `ClusterInstance` resource. This allows easy configuration of multiple nodes. The following values are supported for the `spec.cpuArchitecture` field:

- `x86_64` for a x86-64 deployment
- `aarch64` for an ARM64 deployment
- `multi` for a mixed-architecture deployment

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

If no value is provided, the SiteConfig operator sets the default `x86_64` value.

## Overriding architecture on node level

For each node in the `nodes` list, you can specify the node's `cpuArchitecture` field to override the cluster-level value. If no value is provided, the node inherits the cluster-level value, if specified. Otherwise, it defaults to the `x86_64` value.

When deploying single-node OpenShift clusters with the Image Based Installer Operator, you must override the default CPU architecture for individual nodes.

```yaml
...
spec:
...
    nodes:
        - hostname: example-node
          cpuArchitecture: x86_64
          ...
```

## Configuring ARM nodes for Assisted Installer

To install ARM nodes with the Assisted Installer, you must set an annotation to ensure the Ironic agent has the correct image. The annotation is specified using the `extraAnnotations` field in the `ClusterInstance` resource, which is supported on both cluster and node levels. You must only apply the annotation to ARM nodes.

```yaml
extraAnnotations:
    InfraEnv:
        infraenv.agent-install.openshift.io/ironic-agent-image-override: quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:placeholder
```

For more information, see the upstream documentation: <https://github.com/openshift/assisted-service/tree/master/docs/hive-integration#ironic-agent-image>.
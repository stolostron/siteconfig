# Configuring CPU Architecture

There are two places where the CPU architecture can be defined.

## Using the ClusterInstance CRD Field

The default architecture for all the nodes in the cluster can be defined once in the ClusterInstance field `cpuArchitecture`. This is provided to allow easily configuring multiple nodes. It is also valid to use the value `multi` here to show that a mixed architecture deployment is intended.

If no value is provided here then it will be assumed to be `x86_64`.

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

## Using the NodeSpec Struct Field

Each node defined in the Nodes list can have its `cpuArchitecture` explicitly defined.

If no value is provided here then it will be assumed to be `x86_64` unless a default is provided in the ClusterInstance.

```yaml
...
nodes:
    - hostname: example-node
      cpuArchitecture: aarch64
      ...
```

## ARM Specific Configuration using Assisted Installer

Installing ARM nodes with Assisted Installer currently requires setting an annotation to ensure the Ironic agent has the correct image. This annotation can be added using the `extraAnnotations` field. This field is valid on both the NodeSpec and ClusterInstance CRD but should only be set for ARM nodes.

```yaml
extraAnnotations:
    InfraEnv:
        infraenv.agent-install.openshift.io/ironic-agent-image-override: quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:placeholder
```

The upstream documentation has more information on this: <https://github.com/openshift/assisted-service/tree/master/docs/hive-integration#ironic-agent-image>.

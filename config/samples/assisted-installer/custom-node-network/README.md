# Assisted Installer custom node network sample

This sample replaces the NMStateConfig entry in a full node-template bundle with
a shared template taking `nodeIp`, `prefixLength`, `gwIp` and `dnsServer` from each
node's `templateData` map, alongside `nodeNetwork`. It uses `eno1`; adapt that
interface name consistently if your hardware differs. MAC mappings stay in the typed `interfaces` field.
The template's opening comment documents required parameter types and defaults.

The [ClusterInstance](clusterinstance.yaml) is held for inspection before
installation. Replace its image-set reference, namespaces, addresses, SSH key,
BMC information and disk hint. Create the namespace and referenced pull/BMC
Secrets beforehand. The example operator namespace is `siteconfig-operator`;
use the namespace of your operator installation in both `templateRefs` and the
commands below.

From the repository root, use `oc` and `jq` to assemble a ConfigMap with fresh
metadata and the complete data from the installed default bundle:

```sh
export OPERATOR_NAMESPACE=siteconfig-operator
oc get configmap ai-node-templates-v1 -n "$OPERATOR_NAMESPACE" -o json |
  jq --arg ns "$OPERATOR_NAMESPACE" \
     --rawfile network config/samples/assisted-installer/custom-node-network/NMStateConfig.yaml.tmpl \
     '{apiVersion: "v1", kind: "ConfigMap",
       metadata: {name: "custom-node-templates-v1", namespace: $ns},
       data: (.data + {NMStateConfig: $network})}' > /tmp/custom-node-templates-v1.json
```

Inspect the generated file: InfraEnv and BareMetalHost must still be present,
alongside exactly one NMStateConfig template. Then create the bundle and the
adapted ClusterInstance:

```sh
oc apply -f /tmp/custom-node-templates-v1.json
oc apply -f config/samples/assisted-installer/custom-node-network/clusterinstance.yaml
oc get clusterinstance example-network-sno -n example-network-sno -o yaml
oc get nmstateconfig node1.example.com -n example-network-sno -o yaml
```

Compare the network settings with [expected-nmstateconfig.yaml](expected-nmstateconfig.yaml).
The controller also adds ownership metadata. Verify that InfraEnv's selector
matches the NMStateConfig's `nmstate-label`. Correct any inputs while the hold is
enabled and provisioning is incomplete, then release installation:

```sh
oc patch clusterinstance example-network-sno -n example-network-sno \
  --type=merge -p '{"spec":{"holdInstallation":false}}'
```

The node must reference only the complete custom bundle, not both default and
custom node bundles. Keep the cluster-level reference. The custom template handles
`HostRef` names/namespaces like the default NMStateConfig template. Duplicate
resource identities are not automatically rejected; ensure that exactly one
selected template emits each intended resource. The rendering
unit test reads these files directly and checks both the expected output and a
second node with different parameters and a HostRef.

See the [user guide](../../../../docs/custom-node-network-templates.md) for helper
semantics, update restrictions and rollback requirements.

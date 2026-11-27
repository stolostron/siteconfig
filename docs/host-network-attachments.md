# Host Network Attachments

SiteConfig can render Metal3 `HostNetworkAttachment` resources and associate
them with BareMetalHost interfaces for the Assisted Installer and Hosted
Control Plane flows. Image-Based Install support is deferred until BMO
implements host networking for that flow.

This feature requires a BMO version that provides the
`metal3.io/v1alpha1` `HostNetworkAttachment` resource and the BMH
`spec.networkInterfaces` field.

BMO must have the `IronicNetworking` feature gate enabled and its switch
configuration and credentials configured.  Follow the
[BMO deployment requirements](https://github.com/metal3-io/baremetal-operator/blob/main/docs/configuration.md)
for the BMO version being deployed. API availability alone does not enable
networking support; BMO can reject HNA creation while the gate is disabled.

## Configuration

Define SiteConfig-managed attachments at cluster level, then reference them
from the interfaces of one or more nodes. A node may also reference an
existing HNA owned by another component:

```yaml
spec:
  hostNetworkAttachmentTemplates:
    - name: storage-trunk
      spec:
        mode: trunk
        nativeVLAN: 100
        allowedVLANs: ["101", "102", "103"]
        mtu: 9000
  nodes:
    - hostName: node-1
      networkInterfaces:
        - name: eno1
          hostNetworkAttachment:
            name: storage-trunk
        - macAddress: "AA:BB:CC:DD:EE:FF"
          hostNetworkAttachment:
            name: shared-storage
            namespace: inventory
```

Each attachment name must be unique and a valid Kubernetes DNS subdomain. A
node binding must select exactly one interface by name or MAC address. A
reference whose name is in the cluster-level list and whose namespace is omitted
or is the rendered HNA namespace is managed by SiteConfig; SiteConfig renders
that reference in the rendered HNA namespace. An external reference must
include its namespace. Node references do not cause SiteConfig to create,
modify, or delete the HNA.

Admission rejects duplicate interface selectors within a node using BMO's
case-insensitive comparison. The same selector may be used on different nodes.
`allowedVLANs` is a list of strings in BMO's API; quote VLAN IDs and ranges.
Each entry must be an ID from 1 through 4094 or a range with both endpoints
in that interval and start less than end. Access mode, including an omitted
mode that defaults to access, cannot specify `allowedVLANs`. These checks apply
to creation and permitted updates, including updates while installation is held.
For cross-namespace references, BMO must be able to read the referenced HNA
namespace. If BMO watches a restricted set of namespaces, include both the BMH
and HNA namespaces in that set. SiteConfig does not validate BMO's watch scope.

The node field uses BMO's `NetworkInterface` shape. When a node has no network
interfaces, either because the field is omitted or because it is an empty list,
SiteConfig omits `BareMetalHost.spec.networkInterfaces` from the rendered
manifest.

## Lifecycle behavior

Cluster-level attachment templates can produce multiple resources from one
template. SiteConfig applies HNAs at wave 0, before BMHs at wave 3. During
deprovisioning, higher waves finish cleanup before lower waves, so HNAs are
cleaned up after their BMHs. Resources in the same wave are cleaned up
independently.

Sync waves order resource application and cleanup; they do not establish
networking readiness. BMO remains responsible for configuring networking
before provisioning requires it.

A cluster-level template makes SiteConfig manage the HNA with that name in
`spec.clusterName`. If the resource already exists, SiteConfig applies the
template using server-side apply with forced field ownership. This can replace
conflicting values for fields in the template and set SiteConfig's ownership
label. SiteConfig deletes the HNA during deprovisioning and reinstall cleanup
if the ClusterInstance owns it.

To keep a pre-existing HNA under external management, reference its name and
namespace from a node binding and leave that resource out of the cluster-level
templates. SiteConfig leaves HNAs referenced this way untouched.

SiteConfig deletes managed BMHs during deprovisioning and reinstall cleanup,
including BMHs that existed before SiteConfig applied the rendered manifests.

## Day-2 changes

After provisioning, `spec.hostNetworkAttachmentTemplates` and retained nodes'
`spec.nodes[].networkInterfaces` are immutable during ordinary updates. This
includes adding or removing attachment templates and changing VLANs, MTU,
interface selectors, or attachment references. To change these fields,
[enable SiteConfig reinstall support](configure_siteconfig.md#updating-the-configuration),
which is disabled by default, and request an
eligible cluster-wide reinstall with a new `spec.reinstall.generation`.
Include the desired networking changes in that request. Reinstall cleanup
completes before SiteConfig applies the new configuration.

New workers may supply their own interface bindings under the existing
node-addition rules. They can reference an existing managed HNA or a
pre-created external HNA with an explicit namespace. Adding a worker does not
permit changing the cluster-level attachment templates or retained nodes'
bindings. SiteConfig does not modify external HNAs.

## Downgrade

Downgrading to a SiteConfig controller or schema without HNA support is
unsupported while a ClusterInstance still uses the new fields or HNA
templates. Before downgrading, use the HNA-capable controller to complete one
of these workflows for each affected ClusterInstance:

- Enable reinstall support and complete an eligible cluster-wide reinstall
  that removes the new fields and stops rendering HNAs.
- Delete the ClusterInstance and wait for cleanup to complete.

Complete managed HNA cleanup before downgrading and stop using HNA templates.
Do not rely on an older schema to preserve the new fields. An older controller
still attempts to clean up HNAs recorded in `status.manifestsRendered`, but
its bundle may lack HNA permissions, blocking ClusterInstance finalization.
External HNAs referenced only by nodes remain untouched.

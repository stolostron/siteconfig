# Shared extra-manifest ConfigMaps sample

Use one ConfigMap on the hub to supply the same extra manifests to two
ClusterInstances. The operator creates `siteconfig-shared-resources` at startup;
users add their shared ConfigMaps there. Only ClusterInstances that explicitly
reference a shared ConfigMap receive an owned copy in their own hub namespace.

## Prepare the source and references

Install the operator and matching ClusterInstance CRD with shared-resource support.
Use `oc` with your **hub** kubeconfig. From the repository root:

```sh
oc get namespace siteconfig-shared-resources
oc apply -f config/samples/shared-extra-manifests/shared-configmap.yaml
```

The [source ConfigMap](shared-configmap.yaml) contains a manifest for a harmless
`default/shared-manifests-proof` ConfigMap on each spoke.

Prepare two complete ClusterInstances using your normal Assisted Installer or
Image Based Installer definitions. For example, use `cluster-a` in namespace
`cluster-a` and `cluster-b` in namespace `cluster-b`. For each, `spec.clusterName`
must equal its namespace. Supply the normal installation prerequisites, including
node definitions, template references and Secrets.

Merge the [reference fragment](clusterinstance-fragment.yaml) into each definition
**before installation**, preserving any other required extra-manifest references:

```yaml
spec:
  extraManifestsRefs:
    - name: common-manifests-v1
      namespace: siteconfig-shared-resources
```

The fragment is not a standalone ClusterInstance. Apply your complete definitions
using the normal installation procedure. Do not edit references during active
provisioning. Existing local references without a namespace remain supported.

## Inspect the hub copies

Set these names to match your definitions:

```sh
export CI_A=cluster-a NS_A=cluster-a
export CI_B=cluster-b NS_B=cluster-b
oc get clusterinstance "$CI_A" -n "$NS_A" \
  -o jsonpath='{.spec.extraManifestsRefs}{"\n"}'
oc get clusterinstance "$CI_B" -n "$NS_B" \
  -o jsonpath='{.spec.extraManifestsRefs}{"\n"}'
oc get configmap common-manifests-v1 -n siteconfig-shared-resources -o yaml
oc get configmap common-manifests-v1 -n "$NS_A" -o yaml
oc get configmap common-manifests-v1 -n "$NS_B" -o yaml
```

Expected reference output (illustrative, not captured output):

```json
[{"name":"common-manifests-v1","namespace":"siteconfig-shared-resources"}]
```

After reconciliation, both local copies must have matching source `data` and
`binaryData`, a `siteconfig.open-cluster-management.io/managed-copy: "true"` label,
and an ownerReference matching the local ClusterInstance UID. Their
`siteconfig.open-cluster-management.io/source-namespace` annotation must be
`siteconfig-shared-resources`. Inspect the CI UID with:

```sh
oc get clusterinstance "$CI_A" -n "$NS_A" -o jsonpath='{.metadata.uid}{"\n"}'
oc get clusterinstance "$CI_B" -n "$NS_B" -o jsonpath='{.metadata.uid}{"\n"}'
```

Installer resources consume the local ConfigMap names; the original CI references
retain the shared namespace. A third ClusterInstance without this reference does
not receive a copy. A user-owned ConfigMap with the same destination name is
rejected instead of overwritten.

## Verify the spoke result

Wait for both installations to complete using the normal installer and cluster
health checks. On **each spoke**, run:

```sh
oc --kubeconfig /path/to/spoke-kubeconfig get configmap shared-manifests-proof \
  -n default -o yaml
```

Expect `data.version: v1`. This is the resource described inside the shared
ConfigMap. It is different from the `common-manifests-v1` copies on the hub.
Finding a hub copy alone does not prove the spoke installed successfully.

## Source updates and limitations

After provisioning, change `version: v1` to `version: v2` inside
`shared-configmap.yaml` and apply it again. Both existing managed hub copies should
converge to the new content without changing ClusterInstance generation/spec or
installer specs. Record those fields before the edit to compare them afterward.
Copy UIDs and `initial-resource-version` stay unchanged;
`source-resource-version` advances when the new content propagates.

This does **not** apply v2 to the already-installed spokes. Day-2 spoke changes
require a separate mechanism such as ACM policy/TALM. Restore the sample payload
and reapply it when finished testing.

Changing a reference to a different ConfigMap name after installation is a separate
scenario. The tested Assisted Installer rejects the downstream reference update
as immutable, even though SiteConfig accepts the CI edit. Expect
`RenderedTemplatesValidated=False` on that installer; do not claim successful
staged rollout based on API admission alone. Post-install IBI reference switching
remains untested.

Source deletion retains existing copies. Managed copy deletion triggers targeted recovery; startup also checks for missing
copies. Edited copy content is repaired by the sync controller. This implements
the proposed recovery design, still subject to EP review. See the [operator guide](../../../docs/configure_siteconfig.md#shared-extra-manifest-configmaps-proposal-implementation)
for synchronization settings, provenance and the provisional EP status.

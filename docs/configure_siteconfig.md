# Configuring SiteConfig Operator

This guide provides instructions for configuring the SiteConfig Operator.
The SiteConfig Operator relies on a ConfigMap to define key parameters that influence its behavior,
such as whether reinstallation operations are allowed and the the maximum number of ClusterInstance resources
that can be reconciled concurrently.

## Configuration overview

The SiteConfig Operator configuration is stored in a ConfigMap named **`siteconfig-operator-configuration`**.
This ConfigMap must reside in the same namespace where the SiteConfig Operator is running.

Key configuration options include:
- **`allowReinstalls`**: Specifies whether reinstallation operations are permitted.
- **`maxConcurrentReconciles`**: Determines the maximum number of concurrent reconcile operations.
- **`sharedManifestSyncMaxConcurrentReconciles`**: Maximum number of shared ConfigMaps reconciled
  concurrently by `SharedManifestSync`. Defaults to `4`; must be a positive integer.
  This setting is independent of the ClusterInstance worker count. Updates to the copies
  of any one source are still sequential.

**Note**: Changing either concurrency value requires restarting the SiteConfig manager pod.

---

## Creating the configuration ConfigMap

If the configuration ConfigMap does not already exist, follow these steps to create it:

1. **Ensure you are targeting the correct namespace:**
   ```sh
   NAMESPACE=<siteconfig-operator-namespace>
   ```

2. **Create the ConfigMap with desired values:**
   ```sh
   cat <<EOF | oc apply -f -
   apiVersion: v1
   kind: ConfigMap
   metadata:
     name: siteconfig-operator-configuration
     namespace: $NAMESPACE
   data:
     allowReinstalls: "false"
     maxConcurrentReconciles: "1"
     sharedManifestSyncMaxConcurrentReconciles: "4"
   EOF
   ```

This will create the ConfigMap with reinstall disabled.

---

## Updating the configuration

To modify the configuration, use the `oc patch` command to update the ConfigMap:

1. **Update the `allowReinstalls` setting:**
   ```sh
   oc patch configmap siteconfig-operator-configuration \
     -n $NAMESPACE \
     --type=json \
     -p '[{"op": "replace", "path": "/data/allowReinstalls", "value": "true"}]'
   ```

2. **Update the `maxConcurrentReconciles` setting:**
   ```sh
   oc patch configmap siteconfig-operator-configuration \
     -n $NAMESPACE \
     --type=json \
     -p '[{"op": "replace", "path": "/data/maxConcurrentReconciles", "value": "10"}]'
   ```

   **Important**: If you modify `maxConcurrentReconciles`, you must restart the SiteConfig manager pod for the
   changes to take effect.

---

## Restarting the SiteConfig manager pod

To configure sync workers on an existing configuration ConfigMap (including one without the new key):

```sh
oc patch configmap siteconfig-operator-configuration -n "$NAMESPACE" --type=merge \
  -p '{"data":{"sharedManifestSyncMaxConcurrentReconciles":"8"}}'
```

If the key is absent at startup, the operator uses `4`. Invalid values are rejected;
use a positive integer. A running controller retains its startup worker count until restarted.

To apply either concurrency change, restart the SiteConfig Operator pod:

   ```sh
   oc rollout restart deployment -n $NAMESPACE siteconfig-controller-manager
   ```

   The Operator's deployment will automatically recreate the pod.

---

## Verifying the Configuration

After updating the ConfigMap or restarting the pod:
1. **Check the ConfigMap data:**
   ```sh
   oc get configmap siteconfig-operator-configuration -n $NAMESPACE -o yaml
   ```

2. **Ensure the Operator is using the new configuration:**
   Review the Operator logs to confirm the updated configuration:
   ```sh
   oc logs -n $NAMESPACE --selector app.kubernetes.io/name=siteconfig-controller --follow
   ```

---

## Troubleshooting

- If the ConfigMap is missing, the SiteConfig Operator will recreate it with default values.
Check the Operator logs for relevant messages.
- Ensure the ConfigMap name is **`siteconfig-operator-configuration`** and is located in the correct namespace.

## Shared extra-manifest ConfigMaps (proposal implementation)

The operator creates `siteconfig-shared-resources` for extra-manifest ConfigMaps
shared by multiple ClusterInstances. The namespace is created even when no
ClusterInstance uses it; users create the shared ConfigMaps themselves. See the
[two-cluster sample](../config/samples/shared-extra-manifests/README.md) for YAML,
verification commands, and the distinction between hub copies and spoke resources.
Create the source ConfigMaps there and refer to them explicitly:

```yaml
spec:
  extraManifestsRefs:
    - name: common-manifests-v1
      namespace: siteconfig-shared-resources
    - name: cluster-specific-manifests
```

A reference without a namespace remains local to the ClusterInstance namespace.
Shared references require `spec.clusterName` to equal the ClusterInstance namespace.
Shared references support ConfigMaps only. Sharing Secret objects, including hub
pull secrets or Secrets destined for spokes, is outside this feature's scope.
The operator creates an owned copy of each shared ConfigMap in that namespace;
installer templates consume the local copy. Existing user-owned ConfigMaps with
the same name are rejected rather than overwritten.

The `SharedManifestSync` controller propagates source `data` and `binaryData`
changes to existing managed copies, including after provisioning. It does not
change ClusterInstance generation or render/apply installer resources. Metadata-only
source edits do not rewrite identical copies. The `source-resource-version`
annotation records the source resourceVersion observed at the last content copy or
synchronization; metadata-only source edits do not advance it. `initial-resource-version`
records copy creation and is preserved by sync. A copy first created after
installation does not describe what was installed originally.

Only the source name, data, and binaryData are copied. Source labels and annotations,
including GitOps tracking metadata, are never propagated during creation, sync, or
reinstall. The operator adds its own ownership and provenance metadata to each copy.

Content propagation continues during provisioning and while installation is held.
If the source changes after the copy is created, the installer may consume newer
content than `initial-resource-version` records. This is expected behavior; that
annotation is not proof of the content installed on the spoke.

After successful installation, users can add, replace, remove, or reorder
`extraManifestsRefs` without reinstalling. The operator creates and synchronizes the
selected shared copies, then deletes obsolete managed copies only after successful
reconciliation. Ownership and deletion preconditions protect unrelated ConfigMaps.
A managed shared copy cannot be reused as a local reference; create a distinct
user-owned local ConfigMap for that transition. Spec changes remain blocked during
active provisioning.

Reference changes flow through normal template rendering and dry-run validation.
SiteConfig does not special-case installer fields. If an installer webhook rejects
a change, the ClusterInstance update persists, but reconciliation reports
`RenderedTemplatesValidated=False`. Selected shared copies are created before
this validation; obsolete copies are only removed after successful reconciliation.
Users can choose to:

- Revert the reference changes.
- Keep the changes and accept the dry-run error, while day-N policy consumes the
  current ClusterInstance references and synchronized copies.
- Suppress the rejecting manifest using `spec.suppressedManifests`, for example:

  ```yaml
  spec:
    suppressedManifests:
      - AgentClusterInstall
  ```

Use `ImageClusterInstall` for the image-based installer if that resource rejects
the change. Preserve any existing suppression entries. Suppression skips validation
and apply of the entire selected manifest, leaving the existing resource in place.
**Remove the installer suppression before reinstalling** so the installer resource
can be reconciled using the current references.
Also ensure `ManagedCluster` is not suppressed: reimport requires a successfully
rendered ManagedCluster entry in ClusterInstance status, and can stall without it.

Live tests confirmed that both Assisted Installer and IBI reject rendered reference
changes after installation and that suppressing the rejecting installer manifest
allows ClusterInstance reconciliation and shared-copy synchronization to continue.
Suppression does not change the installer's own update rules. See the
[recorded results](shared-extra-manifests-testing.md#recorded-live-results) for
coverage and the remaining reinstall validation gap.

Sync stops at the hub copy: ACM policy/TALM must drive changes to an already-installed
spoke. Policies must follow the current ClusterInstance references, rather than the
installer references. In the intended workflow, policy runs in inform mode
and TALM controls progressive enforcement. Policy-owner confirmation of reference
selection and missing-copy reporting remains pending. See
[EP #1056](https://github.com/stolostron/siteconfig/pull/1056).

A missing shared source leaves existing copies intact and emits warning events.
Manual edits, updates, or deletions of copied ConfigMaps are unsupported. Edit the
shared source instead. Managed content is overwritten on a subsequent source
reconciliation, and obsolete managed copies can be removed during normal cleanup.
There is no watch on the copies, so manual edits do not trigger prompt repair.

Automatic recovery of missing copies, including on operator startup or hub restore,
is outside this enhancement's scope. Source synchronization only updates existing
managed copies; it does not recreate missing ones. Normal ClusterInstance
reconciliation can create copies when it proceeds past the generation/restore guards.

Referenced shared sources receive `cluster.open-cluster-management.io/backup`.
Copies do not inherit that label. Backup selection must be verified with the
deployed ACM configuration; automatic reconstruction of missing copies after
restore is not provided by this feature.

The shared namespace receives `pod-security.kubernetes.io/enforce: restricted`
and the operator managed-by label on creation and startup. Runtime label edits
are not watched; startup restores these labels. Namespace recreation waits for a
terminating namespace to disappear.

Sync uses `sharedManifestSyncMaxConcurrentReconciles` from the operator configuration
ConfigMap (default `4`); large-fleet throughput still needs live validation.

The namespace is now `siteconfig-shared-resources`, as agreed in EP #1056. The
intermediate PoC used `openshift-siteconfig-resources`; it is not an alias. Users
of that build must migrate shared sources and references explicitly. This change
does not move or delete objects in the previous namespace.

### Upgrade, downgrade, and uninstall

Install the matching operator and CRD before using namespaced references. Existing
local references with non-empty names continue to work; empty or omitted names
must be corrected before upgrading to the stricter schema.

Downgrading stops shared-content synchronization. Before removing
namespace support, preserve source inputs and migrate references to explicitly
user-owned local ConfigMaps compatible with the older operator. Do not just remove
`namespace` from a reference to an operator-managed copy: the current validator
rejects that ambiguous transition. Post-install reference changes can also be
rejected by the downstream installer. Validate the migration on the target installer
or use its supported reinstall workflow before downgrading; this feature does not
provide an automatic in-place downgrade for installed shared-reference users.

Uninstalling the operator leaves the shared namespace and its user-created
ConfigMaps in place. Administrators may remove them manually after confirming no
remaining consumers need them. Copies remain owned by their ClusterInstances and
are garbage-collected when those owners are removed; uninstall alone does not
remove the owners.

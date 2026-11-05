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
annotation records the last content propagation; `initial-resource-version`
records copy creation and is preserved by sync. A copy first created after
installation does not describe what was installed originally.

Only the source name, data, and binaryData are copied. Source labels and annotations,
including GitOps tracking metadata, are never propagated during creation, sync, or
reinstall. The operator adds its own ownership and provenance metadata to each copy.

Content propagation continues during provisioning and while installation is held.
If the source changes after the copy is created, the installer may consume newer
content than `initial-resource-version` records. This is expected behavior; that
annotation is not proof of the content installed on the spoke.

The webhook rejects a normal post-provisioning change to `extraManifestsRefs` and
continues to block all spec changes during active provisioning. In-place repointing
(the proposed staged workflow of a separate `common-manifests-v2` with selected
ClusterInstances moved to it) is rejected at admission, because the downstream
installer treats the manifest reference as immutable once installation starts (see
the limitation below); admitting the edit would only wedge the cluster at
`RenderedTemplatesValidated=False`. The `extraManifestsRefs` change is admitted only
as part of a reinstall request. Whether the downstream installer accepts the switched
reference on reinstall has not yet been validated on a live cluster. When a reference
is set at provisioning or reinstall time, obsolete copies are deleted only after
successful application, with label, owner UID, source namespace, and deletion
precondition checks. A managed shared copy cannot be reused as a local reference;
create a distinct user-owned local ConfigMap for that transition.

This is an implementation of the provisional [EP #1056](https://github.com/stolostron/siteconfig/pull/1056).
This resolves EP Open Question 1 for the Assisted Installer: live validation on
2026-11-03 confirmed the tested Assisted Installer webhook rejects changing
`AgentClusterInstall.spec.manifestsConfigMapRefs` names after installation starts.
A SiteConfig-only change cannot override that restriction, so SiteConfig blocks a
normal post-provisioning `extraManifestsRefs` change at admission rather than letting
the cluster wedge at `RenderedTemplatesValidated=False`. In-place staged reference
switching is therefore not supported on that installer. The `extraManifestsRefs`
change is admitted only as part of a reinstall request, following the EP's
reinstall-based switching approach; downstream acceptance on reinstall still requires
live validation and is tracked as a follow-up. Image Based Installer reference
switching is untested; its scope relative to this EP is not yet confirmed with
reviewers. Sync stops at the hub copy: ACM policy/TALM must drive changes to an
already-installed spoke. In the intended workflow, the policy runs in inform mode
and TALM controls progressive enforcement. Whether the extra-manifests policy
reports missing copies as non-compliant and follows the ClusterInstance reference
set still requires confirmation from the policy owners.

A missing shared source leaves existing copies intact and emits warning events.
The operator watches managed copies. Content edits enqueue the source in
`SharedManifestSync`, which repairs managed data without rendering installer
resources. Copy deletion enqueues the owning ClusterInstance. For an already
reconciled or restored ClusterInstance, a recovery step creates only absent copies
before the generation/restore guards; existing copies are untouched. Paused or
deleting ClusterInstances are not recovered. A user-owned replacement is not
adopted or overwritten.

The ClusterInstance informer's initial list provides a recovery pass on each
controller startup, including copies absent while the operator was stopped. No
additional periodic sweep is added. These changes implement the proposal in EP
Open Question 2; design approval and a full ACM/Velero restore test remain separate.

Referenced shared sources receive `cluster.open-cluster-management.io/backup`.
Copies never inherit it and are recreated from restored sources. Backups must also
include the referencing ClusterInstances; actual backup selection and restore
ordering must be verified with the deployed ACM backup configuration.

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

Downgrading stops shared-content synchronization and copy recovery. Before removing
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

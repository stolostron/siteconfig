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

The operator creates `openshift-siteconfig-resources` for extra-manifest ConfigMaps
shared by multiple ClusterInstances. Create the source ConfigMaps there and refer
to them explicitly:

```yaml
spec:
  extraManifestsRefs:
    - name: common-manifests-v1
      namespace: openshift-siteconfig-resources
    - name: cluster-specific-manifests
```

A reference without a namespace remains local to the ClusterInstance namespace.
Shared references require `spec.clusterName` to equal the ClusterInstance namespace.
The operator creates an owned copy of each shared ConfigMap in that namespace;
installer templates consume the local copy. Existing user-owned ConfigMaps with
the same name are rejected rather than overwritten.

The `SharedManifestSync` controller propagates source `data` and `binaryData`
changes to existing managed copies, including after provisioning. It does not
change ClusterInstance generation or render/apply installer resources. Metadata-only
source edits do not rewrite identical copies. The `source-resource-version`
annotation records the last content propagation; `provisioned-resource-version`
records copy creation and is preserved by sync. A copy first created after
installation does not describe what was installed originally.

The webhook permits changing `extraManifestsRefs` after provisioning, but continues
to block spec changes during active provisioning. To stage a change, create a
separate `common-manifests-v2` and repoint selected ClusterInstances. This runs the
normal validation/render/dry-run/apply path. Obsolete copies are deleted only after
successful application, with label, owner UID, source namespace, and deletion
precondition checks. A managed shared copy cannot be reused as a local reference;
create a distinct user-owned local ConfigMap for that transition.

This is an implementation of the provisional [EP #1056](https://github.com/stolostron/siteconfig/pull/1056).
In-place reference changes after installation still require live validation against
both installer webhooks. Do not assume they are accepted solely because SiteConfig
admission accepts the change. A rejected render/apply leaves the existing copies
in place. Sync stops at the hub copy: ACM policy/TALM must drive changes to an
already-installed spoke.

A missing shared source leaves existing copies intact and emits warning events.
The sync controller does not watch or recreate missing destination copies; an
eligible ClusterInstance reconciliation is required to create them. Recovery after
copy deletion/hub restore and namespace usability on ROSA/OSD remain EP questions.
Sync uses `sharedManifestSyncMaxConcurrentReconciles` from the operator configuration
ConfigMap (default `4`); large-fleet throughput still needs live validation.

The earlier PoC used `siteconfig-shared-resources`. That name is not an alias for
the new namespace. PoC users must explicitly migrate their shared sources and
references; this change does not move or delete objects in the old namespace.

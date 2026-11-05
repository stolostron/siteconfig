# Shared extra-manifests: test guide

Audience: SiteConfig developers and E2E automation authors.

## Version and scope

The saved tests from October 28–29, 2026 used PR #1391 revision `3280e082`, with shared namespace `siteconfig-shared-resources`. The current local draft returns to that namespace and includes new sync logic and the `initial-resource-version` annotation. The saved results do **not** validate those draft changes. Record the deployed operator image digest and revision for each run; set the namespace below to match that build.

This guide tests initial copying and installation on two spokes. Post-install source-update, deletion, and synchronization behavior requires separate tests against the agreed EP and deployed implementation. A hub copy update alone does not prove that an already installed spoke was updated.

## Resource flow

All of these namespaces are on the **hub**:

```text
shared namespace/common-manifests (created by the test)
    ├── cluster-a/common-manifests (copy created by the operator)
    └── cluster-b/common-manifests (copy created by the operator)
```

Each ClusterInstance explicitly references the shared source. A namespace without a referencing ClusterInstance gets no copy. Each copy belongs to its own ClusterInstance.

During installation, the installer consumes the local copy and applies the manifests **inside it** to the spoke. The containing hub ConfigMap is not itself replicated to the spoke. In this example the payload intentionally creates another ConfigMap, named `shared-manifest-proof`, in the spoke's `default` namespace.

## Prerequisites

- Hub with the tested SiteConfig operator, CRDs, admission webhook, and Assisted Installer available.
- Two independent, installable spoke definitions, with distinct namespaces, hosts, MACs, IP addresses, and storage.
- Valid pull secrets, BMC credentials, release image set, installation templates, DNS and NTP.
- `oc`, `jq`, and hub credentials. Obtain each spoke's kubeconfig after installation.
- Two complete ClusterInstance YAML files based on working lab definitions. The fragment below is **not** a complete installable ClusterInstance.

Use explicit kubeconfigs to avoid applying hub resources to a spoke:

```bash
export HUB_KUBECONFIG=/path/to/hub.kubeconfig
export SHARED_NS=siteconfig-shared-resources  # current branch; also revision 3280e082
# Intermediate revision 819f128a used openshift-siteconfig-resources.
export CLUSTER_A=cluster-a
export CLUSTER_B=cluster-b
hub() { oc --kubeconfig "$HUB_KUBECONFIG" "$@"; }
```

## 1. Verify operator-created namespace

```bash
hub get namespace "$SHARED_NS"
```

Expected: namespace exists after operator startup. Do not create it manually in this test: that would hide a namespace-creation failure. In a dedicated operator-startup test, also cover a pre-existing namespace and verify it is preserved.

## 2. Create one shared source

```bash
hub apply -f - <<EOF_SOURCE
apiVersion: v1
kind: ConfigMap
metadata:
  name: common-manifests
  namespace: ${SHARED_NS}
data:
  00-proof.yaml: |
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: shared-manifest-proof
      namespace: default
    data:
      origin: shared-extra-manifests-test
      revision: initial
EOF_SOURCE

hub get configmap common-manifests -n "$SHARED_NS" -o json \
  > shared-source-before.json
```

Expected: exactly one source ConfigMap. Do not create the destination copies yourself.

## 3. Reference it from two ClusterInstances

In **each complete ClusterInstance definition**, use the corresponding cluster name and namespace and add:

```yaml
spec:
  extraManifestsRefs:
    - name: common-manifests
      namespace: siteconfig-shared-resources
```

Use the namespace matching the deployed build. For example, ClusterInstance `cluster-a` lives in namespace `cluster-a`; `cluster-b` lives in `cluster-b`. Create required namespaces and installation secrets, then apply the full definitions:

```bash
hub apply -f cluster-a.yaml
hub apply -f cluster-b.yaml
```

For an Assisted Installer test, `spec.holdInstallation: true` can hold provisioning while copying/rendering are checked. Release it after the checks pass. Allow reconciliation time; CI should poll with a bounded timeout instead of assuming immediate results.

## 4. Inspect the hub copies

```bash
hub get configmap -A --field-selector metadata.name=common-manifests
```

Expected layout (illustrative, **not captured live output**):

```text
NAMESPACE                   NAME
siteconfig-shared-resources  common-manifests
cluster-a                   common-manifests
cluster-b                   common-manifests
```

Inspect the reference and copy owner:

```bash
hub get clusterinstance "$CLUSTER_A" -n "$CLUSTER_A" \
  -o jsonpath='{.spec.extraManifestsRefs}{"\n"}'
hub get configmap common-manifests -n "$CLUSTER_A" \
  -o jsonpath='{.metadata.ownerReferences}{"\n"}'
```

Expected: the original ClusterInstance retains the shared namespace. The destination ConfigMap's owner UID matches that ClusterInstance's UID.

Automatable assertions after reconciliation:

```bash
set -euo pipefail
for cluster in "$CLUSTER_A" "$CLUSTER_B"; do
  hub get configmap common-manifests -n "$cluster" -o json > "$cluster-copy.json"
  hub get clusterinstance "$cluster" -n "$cluster" -o json > "$cluster-ci.json"

  jq -e --slurpfile source shared-source-before.json \
    '.data == $source[0].data and
     ((.binaryData // {}) == ($source[0].binaryData // {})) and
     (.metadata.uid != $source[0].metadata.uid)' "$cluster-copy.json"

  jq -e --slurpfile ci "$cluster-ci.json" \
    'any(.metadata.ownerReferences[]; .uid == $ci[0].metadata.uid)' \
    "$cluster-copy.json"

  jq -e --arg ns "$SHARED_NS" \
    'any(.spec.extraManifestsRefs[];
      .name == "common-manifests" and .namespace == $ns)' "$cluster-ci.json"

  hub get agentclusterinstall "$cluster" -n "$cluster" -o json |
    jq -e 'any(.spec.manifestsConfigMapRefs[]; .name == "common-manifests")'
done

jq -e --slurpfile other "$CLUSTER_B-copy.json" \
  '.metadata.uid != $other[0].metadata.uid' "$CLUSTER_A-copy.json"

hub get configmap common-manifests -n "$SHARED_NS" -o json |
  jq -e --slurpfile before shared-source-before.json \
    '.metadata.uid == $before[0].metadata.uid and
     .metadata.resourceVersion == $before[0].metadata.resourceVersion and
     .data == $before[0].data'
```

The default Assisted Installer templates must reference the **local** ConfigMap by name. Inspect the full `spec.manifestsConfigMapRefs` as well; it should not direct installation to a different namespace. If testing different installation templates, assert their corresponding rendered reference instead.

## 5. Complete both installations

If installation was held:

```bash
for cluster in "$CLUSTER_A" "$CLUSTER_B"; do
  hub patch clusterinstance "$cluster" -n "$cluster" --type=merge \
    -p '{"spec":{"holdInstallation":false}}'
done
```

Perform the normal lab-specific discovery/boot/approval procedure. Wait for both installations, with a suitable installation timeout:

```bash
for cluster in "$CLUSTER_A" "$CLUSTER_B"; do
  hub wait agentclusterinstall/"$cluster" -n "$cluster" \
    --for=condition=Completed=True --timeout=120m
  hub wait clusterinstance/"$cluster" -n "$cluster" \
    --for=condition=Provisioned=True --timeout=10m
  hub get clusterdeployment "$cluster" -n "$cluster" -o json |
    jq -e '.spec.installed == true'
done
```

Fail early if installation reports `Failed=True`. For an ACM import scenario, also require ManagedCluster import success, Joined=True, and Available=True.

## 6. Verify the payload on both spokes

```bash
export SPOKE_A_KUBECONFIG=/path/to/cluster-a.kubeconfig
export SPOKE_B_KUBECONFIG=/path/to/cluster-b.kubeconfig

for spoke_config in "$SPOKE_A_KUBECONFIG" "$SPOKE_B_KUBECONFIG"; do
  oc --kubeconfig "$spoke_config" get configmap shared-manifest-proof \
    -n default -o json |
    jq -e '.data == {
      "origin":"shared-extra-manifests-test",
      "revision":"initial"
    }'
  oc --kubeconfig "$spoke_config" get nodes
  oc --kubeconfig "$spoke_config" get clusteroperators
  oc --kubeconfig "$spoke_config" get clusterversion
 done
```

Full E2E pass requires **both** spokes to have the exact payload, all nodes Ready, all operators Available=True/Progressing=False/Degraded=False, and ClusterVersion Available=True/Progressing=False. CI must assert these conditions, not merely collect the tables. Poll while operators settle, then fail on timeout. Repeat the source/copy assertions after installation to prove the source was not changed.

## Additional coverage

Use isolated fixtures for these tests; do not mutate successful installation resources to run negative cases.

| Case | Expected result |
|---|---|
| Two references to the same shared source | Two exact copies with distinct owners; both spokes receive payload |
| No shared reference | No shared copy created in that ClusterInstance namespace |
| Local reference, namespace omitted or explicitly local | Existing local ConfigMap used and preserved |
| Missing shared source | Validation fails with an actionable message |
| Unrelated source namespace | Admission rejects reference |
| Destination name collision with user-owned ConfigMap | Error; user ConfigMap unchanged |
| Shared source with `binaryData` | Exact copy on hub; test separately from installer payload format |
| Delete one test ClusterInstance | Its owned copy garbage-collected; shared source and other copy retained |
| Update/delete source after installation | Separate EP-defined sync test; verify hub and spoke effects independently |

Deletion/garbage-collection tests belong in disposable fixtures. Deleting a ClusterInstance used for a real installation can trigger deprovisioning; it is not routine cleanup for an installation retained for inspection.

## Recorded results from the earlier sandbox

These are saved observations, not fresh commands run against the current build:

- PR revision `3280e082`; operator image digest `sha256:d785ee063665da401468756c3ba053e29c20a6b8bbf3babc15ad2240bd4e0e6e`.
- Shared source: `siteconfig-shared-resources/e2e-shared-manifests`.
- Referencing ClusterInstances and copy namespaces: `520955-sno` and `520955-sno2`.
- PASS: unchanged source UID/resourceVersion/data; two distinct exact copies, each owned by its own ClusterInstance; both actual installers use local references.
- PASS: first spoke completed OpenShift 4.22.11 installation, healthy operators/node, successful hub import, and exact proof ConfigMap plus NTP MachineConfig on the spoke. Rechecked October 29.
- INCOMPLETE: second spoke reached finalizing at 80%; final health, import, and installed payload assertions were not completed. Its nested networking fix was awaiting approval. Do not report two completed E2E installations.
- First-spoke installation required sandbox networking adjustments and a monitoring-operator restart. Record equivalent interventions separately from feature behavior in future runs.

Saved evidence: `two-copy-results.json`, `second-rendering-results.json`, `e2e-results.json`, and `second-install-status.json` under `/tmp/siteconfig-ztp-4qqpoze9/`. The previous hub kubeconfig was unavailable on November 2, so no fresh hub output was captured for this document.

For every new run, archive the image/revision, test inputs without secrets, source/copy UIDs and owner references, installer conditions, spoke payload checks, cluster health, timestamps, and environmental interventions. Never include pull secrets, kubeconfigs, passwords, or signed download URLs in the shared report.

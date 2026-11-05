# QE procedure: shared extra-manifest ConfigMaps

Before testing: use the latest recovery-v2 image and current CRD described in
`shared-extra-manifests-qe-handoff.md`. The renamed build passed live initial-copy
and source-content synchronization smoke tests with ConfigMap-only templates;
full spoke installation and post-install IBI reference switching remain untested
on this image. The November 3 image is historical and uses the previous names.
Switching to a different ConfigMap name after installation failed against the
tested Assisted Installer webhook; treat that scenario as a known failure.
These commands are test instructions, not captured terminal output. Run from the
repository root with `oc`, `jq`, and credentials for a dedicated test hub.

## 1. Prepare two installations

Deploy the updated operator revision and matching CRD using your normal QE
operator deployment procedure. Set the hub kubeconfig and your actual CI names:

```sh
export KUBECONFIG=/path/to/hub-kubeconfig
export NS_A=qe-spoke-a CI_A=qe-spoke-a
export NS_B=qe-spoke-b CI_B=qe-spoke-b
export SHARED_NS=siteconfig-shared-resources
export SOURCE=qe-shared-manifests-v1
oc get namespace "$SHARED_NS"
oc apply -f docs/examples/shared-extra-manifests/source.yaml
```

The namespace must exist after operator startup without any CI references.
Create the source yourself; the operator does not populate shared payloads.

Use two complete, valid QE ClusterInstance definitions and merge the
[fragment](examples/shared-extra-manifests/clusterinstance-fragment.yaml) into
both **before starting installation**. Preserve other required references.
For each CI, `spec.clusterName` must equal its namespace. The fragment is not a
standalone installation manifest. Configure nodes, templates, networking, pull
secrets and installer resources through the existing QE installation procedure.

For a negative control, use another valid CI with no shared reference. It should
not acquire this ConfigMap. Do not change references during active provisioning.

## 2. Verify references, copies and installation

Run on the hub:

```sh
oc get clusterinstance "$CI_A" -n "$NS_A" \
  -o jsonpath='{.spec.extraManifestsRefs}{"\n"}'
oc get clusterinstance "$CI_B" -n "$NS_B" \
  -o jsonpath='{.spec.extraManifestsRefs}{"\n"}'
oc get configmap "$SOURCE" -n "$SHARED_NS" -o yaml
oc get configmap "$SOURCE" -n "$NS_A" -o yaml
oc get configmap "$SOURCE" -n "$NS_B" -o yaml
```

The JSONPath output is JSON, for example:

```json
[{"name":"qe-shared-manifests-v1","namespace":"siteconfig-shared-resources"}]
```

For both copies, compare `data` and `binaryData` with the source. Require the
managed-copy label, source namespace annotation, and an ownerReference to the
correct CI UID. Record copy UID, resourceVersion and provenance annotations.
Use this bounded check for each namespace after reconciliation:

```sh
check_copy() {
  ns="$1" ci="$2"
  source_json=$(oc get cm "$SOURCE" -n "$SHARED_NS" -o json) || return 1
  copy_json=$(oc get cm "$SOURCE" -n "$ns" -o json) || return 1
  owner_uid=$(oc get clusterinstance "$ci" -n "$ns" -o jsonpath='{.metadata.uid}') || return 1
  jq -e --argjson src "$source_json" --arg uid "$owner_uid" \
    --arg source_ns "$SHARED_NS" '
    (.data // {}) == ($src.data // {}) and
    (.binaryData // {}) == ($src.binaryData // {}) and
    .metadata.labels["siteconfig.open-cluster-management.io/managed-copy"] == "true" and
    .metadata.annotations["siteconfig.open-cluster-management.io/source-namespace"] == $source_ns and
    any(.metadata.ownerReferences[]?; .uid == $uid and .kind == "ClusterInstance")
  ' <<< "$copy_json" >/dev/null
}
wait_copies() {
  for attempt in $(seq 1 60); do
    if check_copy "$NS_A" "$CI_A" && check_copy "$NS_B" "$CI_B"; then
      echo 'PASS: both hub copies match the shared source and CI ownership'
      return 0
    fi
    sleep 5
  done
  echo 'FAIL: copies did not converge within 300 seconds' >&2
  return 1
}
wait_copies
```

Also verify:

- Installer resources reference the **local** copy name; CI source references
  retain `siteconfig-shared-resources`.
- Both installations complete using the normal installer/ACM readiness checks.
- On **each spoke**, `oc --kubeconfig /path/to/spoke-kubeconfig get cm
  siteconfig-shared-qe-proof -n default -o yaml` shows `data.version: v1`.
  This verifies application of the inner manifest, not presence of a hub copy.
- A CI without the shared reference has no `$SOURCE` copy in its namespace.

A copy's presence alone does not prove a successful installation.

## 3. Verify post-install source synchronization

Use only the disposable source above. Capture both CI generations/specs and
installer specs before changing it. Record each copy UID and
`initial-resource-version` annotation. For Assisted Installer, inspect
`oc get agentclusterinstall -n "$NS_A" -o json` and the equivalent in `$NS_B`.

Change the payload and include a binary probe, then wait for both hub copies:

```sh
oc patch cm "$SOURCE" -n "$SHARED_NS" --type=merge -p \
  '{"data":{"00-proof.yaml":"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: siteconfig-shared-qe-proof\n  namespace: default\ndata:\n  version: v2\n"},"binaryData":{"qe-probe":"cWUtcHJvYmU="}}'
wait_copies
```

Pass criteria: both copies match text and binary content; copy UIDs and
`initial-resource-version` stay unchanged; `source-resource-version` advances
to the propagated source version. CI generation/spec and installer specs stay
unchanged. The creation annotation does not prove which content a spoke installed.

The existing spoke proof should still show `v1` in this isolated test without a
day-2 consumer. This sync controller updates hub copies, not installed spoke
resources. Test any policy/TALM rollout separately.

Next record copy resourceVersions, annotate only the source, and observe for
30 seconds:

```sh
oc annotate cm "$SOURCE" -n "$SHARED_NS" qe-metadata-probe=one --overwrite
```

Matching copies should not be rewritten for this metadata-only edit. Their
resourceVersions should stay unchanged, absent unrelated writers.

Restore the test payload and remove probes, then rerun `wait_copies`:

```sh
oc apply -f docs/examples/shared-extra-manifests/source.yaml
oc patch cm "$SOURCE" -n "$SHARED_NS" --type=merge -p '{"binaryData":null}'
oc annotate cm "$SOURCE" -n "$SHARED_NS" qe-metadata-probe-
wait_copies
```

## 4. Reference switching — known Assisted Installer failure

On a dedicated installed CI, create a second shared source named
`qe-shared-manifests-v2` with a distinct proof version. Save the complete original
`extraManifestsRefs` list before editing it. Change only that CI's shared entry to
v2, preserving other entries. Keep the other CI on v1.

Do not count successful API admission as success. Inspect:

```sh
oc get clusterinstance "$CI_A" -n "$NS_A" -o json | \
  jq '{generation:.metadata.generation,status:.status}'
oc get agentclusterinstall -n "$NS_A" -o yaml
```

The desired staged behavior needs successful render/apply and a changed downstream
reference for A while B stays on v1. **On the tested installer, this fails:**
`RenderedTemplatesValidated=False`, with the webhook reporting
`ManifestsConfigMapRefs.Name` immutable after installation starts. The existing
installer reference remains unchanged. Record the installer version and failure;
do not bypass its webhook. IBI must be evaluated separately.

Restore the entire saved reference list immediately. Wait for the restored CI to
reconcile successfully and obsolete owned v2 copies to be cleaned up, then remove
the disposable v2 source. Keep this a known failing scenario until the EP design
and downstream support are resolved.

## 5. Additional coverage

| Case | Expected behavior / scope |
| --- | --- |
| Local reference without namespace | Existing local ConfigMap behavior remains supported |
| User-owned destination with same name | Rejected; user ConfigMap is not overwritten |
| Source deleted | Existing copies retained; warning event emitted |
| Destination copy deleted | Recovery build recreates missing managed copies; see recovery validation report |
| Namespace deleted | Namespace monitor recreates namespace, not lost source ConfigMaps; use an isolated destructive-test environment |
| Active provisioning ref edit | SiteConfig admission rejects the change |
| Post-install IBI ref edit | Not validated in the current lab; requires an installed IBI test cluster |
| Namespace naming | Uses `siteconfig-shared-resources`; the `openshift-` prefix question is retired |
| Worker configuration | Default 4; set `sharedManifestSyncMaxConcurrentReconciles: "8"` in operator configuration, restart operator and verify startup logs and sync; restore original configuration and restart |

Worker configuration commands and restart procedure are in
[the operator guide](configure_siteconfig.md). Record the prior configuration
before testing. Different source ConfigMaps can reconcile concurrently; copies
of one shared source are still processed sequentially. The two-cluster test does
not establish large-fleet throughput.

## Evidence to attach

Record operator digest, source revision, CRD revision, installer image versions,
CI references/status, source and copy payload comparisons, owner UIDs, provenance,
and each spoke's proof resource. Separate PASS, FAIL, and NOT RUN results.
Never attach kubeconfigs, pull secrets or tokens. Preserve failed-case evidence
before cleanup; do not delete namespaces containing real installations.

## Recovery build validation

The later recovery build is described in
[recovery validation](shared-extra-manifests-recovery-validation.md). With that build,
verify managed-copy deletion and content drift are repaired without modifying CI
spec/generation/observedGeneration or rendered-object resourceVersions. Also stop
the operator, remove one disposable copy, restart, and verify only the missing copy
is created; an existing copy must retain its resourceVersion. Use isolated
ConfigMap-only test templates for this controller-path test. Full hub restore and
installed-spoke behavior require separate environments.

# SiteConfig shared extra manifests — QE handoff

Share one extra-manifest ConfigMap across multiple ClusterInstances using
`spec.extraManifestsRefs` and `siteconfig-shared-resources`.

## Recovery build

Latest local branch image: `quay.io/aabugosh/siteconfig-poc:shared-resources-20261105-recovery-v2`.

```text
quay.io/aabugosh/siteconfig-poc@sha256:20e3292134cb69d73002c5a38d42ab7680b82b076f2a4fb2e863ab076faa338f
```

This adds managed-copy deletion/startup recovery, prompt content-drift repair,
source backup labeling, namespace security labels and termination retries.
See [recovery validation](shared-extra-manifests-recovery-validation.md) for test
scope and remaining external follow-ups. Use this build for the recovery tests;
the earlier review-updates image below does not contain recovery.

## Earlier November 5 image — initial smoke test passed

Use `quay.io/aabugosh/siteconfig-poc:shared-resources-20261105-review-updates`,
or pin its immutable reference:

```text
quay.io/aabugosh/siteconfig-poc@sha256:e71993c49f1a6c1d5934438f2bc64bc7245cf7353a42586dafdff2b9eded63ba
```

This image includes the local namespace and annotation renames. Use the CRD from
this working tree. Two ConfigMap-only ClusterInstances passed initial copying,
source-content synchronization, ownership, GitOps metadata isolation, and initial
version preservation. This run did not install spokes or validate post-install
reference switching. See the [November 5 report](shared-extra-manifests-live-check-2026-11-05.md)
for exact source provenance, scope, and deployment details.

## Historical build and general test scope

| Resource | Link |
| --- | --- |
| Operator image | [Quay](https://quay.io/repository/aabugosh/siteconfig-poc?tab=tags), `linux/amd64` |
| Implementation | [PR #1391](https://github.com/stolostron/siteconfig/pull/1391) — draft |
| Enhancement proposal | [PR #1056](https://github.com/stolostron/siteconfig/pull/1056) — pending approval/design decisions |
| Epic / EP tracking | [CNF-27593](https://redhat.atlassian.net/browse/CNF-27593), [ACM-33537](https://redhat.atlassian.net/browse/ACM-33537) |
| Historical tested source | [819f128aa69cf860c7bc87fb18cab0c7278f7c64](https://github.com/aabughosh/siteconfig/tree/819f128aa69cf860c7bc87fb18cab0c7278f7c64) |
| Historical CRD | [ClusterInstance CRD at tested commit](https://github.com/aabughosh/siteconfig/blob/819f128aa69cf860c7bc87fb18cab0c7278f7c64/config/crd/bases/siteconfig.open-cluster-management.io_clusterinstances.yaml) |
| Usage and examples | [Two-cluster example, YAML and verification](https://github.com/aabughosh/siteconfig/blob/07dd74351fcccae4716cf213597f256b445d98b8/config/samples/shared-extra-manifests/README.md) |
| Case | Expected behavior / scope |
| --- | --- |
| Local reference without namespace | Existing local ConfigMap behavior remains supported |
| User-owned destination with same name | Rejected; user ConfigMap is not overwritten |
| Source deleted | Existing copies retained; warning event emitted |
| Destination copy deleted | Current branch recreates missing managed copies; see recovery validation report |
| Namespace deleted | Namespace monitor recreates namespace, not lost source ConfigMaps; use an isolated destructive-test environment |
| Active provisioning ref edit | SiteConfig admission rejects the change |
| Post-install IBI ref edit | Not validated in the current lab; requires an installed IBI test cluster |
| Namespace naming | EP now uses `siteconfig-shared-resources`; the `openshift-` prefix question is retired |
| Worker configuration | Default 4; set `sharedManifestSyncMaxConcurrentReconciles: "8"` in operator configuration, restart operator and verify startup logs and sync; restore original configuration and restart |

Historical image tag (before the namespace and annotation rename):

```text
quay.io/aabugosh/siteconfig-poc:siteconfig-shared-resources-819f128a-20261103
```

Immutable pull reference:

```text
quay.io/aabugosh/siteconfig-poc@sha256:47f81ed4c809e5938cf449984bb489e9e514d005dbd50dfe872fd64465527b4b
```

The examples below target the November 5 image above and this working tree’s CRD. The November 3 image and CRD linked above use
`openshift-siteconfig-resources` and `provisioned-resource-version`; they do not
validate the renamed namespace or annotation. Do not use that image with these
updated examples. Record the deployed revision and digest before QE execution.

QE needs Quay read access and an image pull secret for this repository.
On the historical revision, local `make ci-job` passed. Live source synchronization to hub copies passed with
two already-installed ClusterInstances on the tested revision.

## How it works

1. The operator creates `siteconfig-shared-resources` on the hub at startup.
2. The user creates a shared ConfigMap there and references it in each participating ClusterInstance.
3. The operator copies it into each referencing ClusterInstance's namespace on the hub.
4. During installation, the installer applies the manifests inside it to the spoke.

A ClusterInstance without the shared reference gets no copy. Later source content
updates synchronize existing hub copies; they do not automatically apply changes
to installed spokes. This guide covers initial references and source content sync.

## 1. Create the shared ConfigMap

Use `oc` with your hub kubeconfig and the matching operator/CRD installed.
Save this as `shared-configmap.yaml`, then run `oc apply -f shared-configmap.yaml`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: qe-shared-manifests-v1
  namespace: siteconfig-shared-resources
data:
  00-proof.yaml: |
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: siteconfig-shared-qe-proof
      namespace: default
    data:
      version: v1
```

## 2. Create the ClusterInstance

Save the following full Assisted Installer SNO example as `clusterinstance.yaml`.
Replace its example values with your lab settings: ClusterImageSet, SSH key,
base domain, hostname, BMC, disk, MAC, interface, IP, subnet, DNS and gateway.
Use your operator namespace in both `templateRefs` entries.

Create namespace `qe-spoke-a` and its referenced pull/BMC Secrets first, using
your normal QE setup. Configure the cluster DNS records. For shared references,
`spec.clusterName` must equal `metadata.namespace`.

```yaml
# Assisted Installer SNO example: replace all lab-specific values before use.
# Create namespace qe-spoke-a and its pull/BMC Secrets before applying this CR.
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: ClusterInstance
metadata:
  name: qe-spoke-a
  namespace: qe-spoke-a
spec:
  clusterName: qe-spoke-a
  baseDomain: example.com
  # Keep installation held until the rendered resources have been inspected.
  holdInstallation: true
  clusterImageSetNameRef: replace-with-your-clusterimageset
  pullSecretRef:
    name: assisted-deployment-pull-secret
  sshPublicKey: "ssh-ed25519 REPLACE_WITH_YOUR_PUBLIC_KEY"
  networkType: OVNKubernetes
  clusterNetwork:
    - cidr: 10.128.0.0/14
      hostPrefix: 23
  serviceNetwork:
    - cidr: 172.30.0.0/16
  machineNetwork:
    - cidr: 192.0.2.0/24
  extraManifestsRefs:
    - name: qe-shared-manifests-v1
      namespace: siteconfig-shared-resources
  templateRefs:
    - name: ai-cluster-templates-v1
      namespace: siteconfig-operator
  nodes:
    - hostName: node0.qe-spoke-a.example.com
      role: master
      bmcAddress: "redfish-virtualmedia+https://bmc.example.com/redfish/v1/Systems/1"
      bmcCredentialsName:
        name: qe-spoke-a-bmc-secret
      bootMACAddress: "02:00:00:00:00:01"
      bootMode: UEFI
      rootDeviceHints:
        deviceName: /dev/sda
      nodeNetwork:
        interfaces:
          - name: eno1
            macAddress: "02:00:00:00:00:01"
        config:
          interfaces:
            - name: eno1
              type: ethernet
              state: up
              ipv4:
                enabled: true
                dhcp: false
                address:
                  - ip: 192.0.2.10
                    prefix-length: 24
              ipv6:
                enabled: false
          dns-resolver:
            config:
              server:
                - 192.0.2.53
          routes:
            config:
              - destination: 0.0.0.0/0
                next-hop-interface: eno1
                next-hop-address: 192.0.2.1
                table-id: 254
      templateRefs:
        - name: ai-node-templates-v1
          namespace: siteconfig-operator
```

Apply the adapted file and inspect the rendered resources while installation is held:

```sh
oc apply -f clusterinstance.yaml
oc get clusterinstance qe-spoke-a -n qe-spoke-a -o yaml
oc get agentclusterinstall,clusterdeployment,infraenv,baremetalhost,nmstateconfig -n qe-spoke-a
oc get configmap qe-shared-manifests-v1 -n qe-spoke-a -o yaml
```

Once rendering/validation succeeds and the configuration is correct, release installation:

```sh
oc patch clusterinstance qe-spoke-a -n qe-spoke-a --type=merge \
  -p '{"spec":{"holdInstallation":false}}'
```

For a second installation, use a separate file with name, namespace and clusterName
`qe-spoke-b`, and its own node, hardware, network values and namespace-local Secrets.
Keep the same `qe-shared-manifests-v1` reference. Do not reuse the first node.

## 3. Check the result

On the hub, confirm both ClusterInstances still reference the shared namespace:

```sh
oc get clusterinstance qe-spoke-a -n qe-spoke-a -o jsonpath='{.spec.extraManifestsRefs}{"\n"}'
oc get clusterinstance qe-spoke-b -n qe-spoke-b -o jsonpath='{.spec.extraManifestsRefs}{"\n"}'
```

Expected reference:

```json
[{"name":"qe-shared-manifests-v1","namespace":"siteconfig-shared-resources"}]
```

Inspect the source and both hub copies:

```sh
oc get cm qe-shared-manifests-v1 -n siteconfig-shared-resources -o yaml
oc get cm qe-shared-manifests-v1 -n qe-spoke-a -o yaml
oc get cm qe-shared-manifests-v1 -n qe-spoke-b -o yaml
```

Pass criteria:

- Both copies contain the same `data` and `binaryData` as the source.
- Each copy has the managed-copy label and an ownerReference matching its local
  ClusterInstance UID, plus a source-namespace annotation identifying
  `siteconfig-shared-resources`.
- Both installations complete and pass the usual cluster health checks.
- On each spoke, the command below shows `data.version: v1`:

```sh
oc --kubeconfig /path/to/spoke-kubeconfig get cm siteconfig-shared-qe-proof -n default -o yaml
```

The spoke resource is `siteconfig-shared-qe-proof`; the hub copy is
`qe-shared-manifests-v1`.

## 4. Check source content synchronization

After installation, change `version: v1` to `version: v2` inside the source file
and apply `shared-configmap.yaml` again. Wait for both hub copies to show the new
content. ClusterInstance generation/spec and installer specs should stay unchanged;
record them before the edit and compare afterward.

This checks hub-copy synchronization. The proof ConfigMap on an already-installed
spoke stays at v1 without a separate day-2 application mechanism. Restore the source
file to v1 and reapply it when finished.

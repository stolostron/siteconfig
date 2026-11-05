# Shared extra-manifests lab check — November 5, 2026

Two runs were performed on the saved `clus0` lab: first against its existing
operator, then against a new image built from the current local branch. The
second run passed the shared-copy and synchronization smoke checks. Neither
run installed a spoke or tested post-install IBI reference switching.

## First run: previous deployed image

Image:
`quay.io/jmontesi/test@sha256:6be8efbd199e2267bb422e68720a0edaabd80f845dbfcd089959716e120d88b0`

### Procedure and outcome

Created a disposable shared ConfigMap in `siteconfig-shared-resources` and two
ClusterInstances in separate disposable namespaces. Both used the existing
ConfigMap-only smoke templates, dummy credentials, and held installation. No
installer or hardware resources were rendered.

Both ClusterInstances failed validation: the operator tried to retrieve the
extra-manifest ConfigMap from the ClusterInstance namespace rather than the
explicitly referenced shared namespace. No destination copies were created.
The deployed CRD does include the optional namespace field, so the schema supports
this reference but the deployed controller did not honor it in this test.

- Initial shared copying: failed on the deployed image.
- Ownership and GitOps metadata isolation: not evaluated because copies were absent.
- Source-content synchronization: not attempted because initial copying failed.
- Post-install IBI switching: not tested; this hub has no ImageClusterInstall objects.

All disposable resources were submitted for deletion successfully. Existing test
resources and the operator deployment were left unchanged.

Deploy an image built from the current branch with its matching CRD before
repeating this check. Local API/controller test success is separate from live
validation. Raw results and the test script are in
`/tmp/siteconfig-live-check-20261105/` (temporary local evidence).

## Second run: updated branch — PASS

Built with the repository Dockerfile for `linux/amd64`, pushed to Quay, and
successfully rolled out in `open-cluster-management/siteconfig-controller-manager`.

- Tag: `quay.io/aabugosh/siteconfig-poc:shared-resources-20261105-review-updates`
- Digest: `quay.io/aabugosh/siteconfig-poc@sha256:e71993c49f1a6c1d5934438f2bc64bc7245cf7353a42586dafdff2b9eded63ba`
- Source: local working tree based on `07dd74351fcccae4716cf213597f256b445d98b8`,
  including the uncommitted namespace/annotation changes and metadata regression test.
- Saved source patch SHA-256: `ef8bcac0182cf4626ef431a87585b2e8e21f5a868c0ced5a83f46f9a9cbc4a30`.

The deployed CRD already matches this branch except for the API-defaulted
conversion strategy and an unrelated additive `nodes.templateData` field. It
was preserved to avoid removing that other lab extension. Existing RBAC already
allows namespace watches; no RBAC change was needed. MultiClusterHub was already
paused and its setting was left unchanged.

| Check | Result |
| --- | --- |
| Shared source copied into both new ClusterInstance namespaces | PASS |
| Copies owned by the corresponding ClusterInstance UID | PASS |
| Argo CD source label and tracking annotation excluded | PASS |
| `initial-resource-version` populated | PASS |
| Initial version preserved after content synchronization | PASS |
| Source v1 → v2 content synchronized to both copies within 45 seconds | PASS |
| ClusterInstance specs and generations unchanged by synchronization | PASS |

Both ClusterInstances also reported successful validation, rendering, rendered
resource validation, and application. Templates rendered only ConfigMaps, so
these results do not prove installer acceptance or changes on installed spokes.

The new operator image remains deployed. Disposable test sources and namespaces
were removed. Deployment and CRD rollback snapshots, source patch, and image digest
are saved locally in `/tmp/siteconfig-deploy-20261105/`. Results from both runs and
the executable test script are in `/tmp/siteconfig-live-check-20261105/`.

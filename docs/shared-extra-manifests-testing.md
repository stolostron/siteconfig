# Shared extra-manifests: QE checklist

Use the operator and matching CRD from the branch under test. Follow the
[two-cluster sample](../config/samples/shared-extra-manifests/README.md) for inputs,
installation prerequisites, and hub/spoke verification commands. See the
[operator guide](configure_siteconfig.md#shared-extra-manifest-configmaps-proposal-implementation)
for synchronization behavior and limitations.

## Installation and synchronization

- Verify startup creates `siteconfig-shared-resources` with the managed-by label
  and `pod-security.kubernetes.io/enforce: restricted`.
- Install two clusters referencing the same shared ConfigMap. Verify owned hub
  copies, local installer references, successful installation, and the payload on
  both spokes. Cover Assisted Installer and IBI independently.
- Check that copies contain source data but no source GitOps or backup metadata.
  Referenced sources should receive `cluster.open-cluster-management.io/backup`.
- Update source content. Verify both copies converge while their UIDs and
  `initial-resource-version` stay unchanged. ClusterInstance generation,
  `ObservedGeneration`, and rendered-resource versions must remain unchanged.
  Synchronization alone must not be counted as applying content to the spokes.
- Verify metadata-only source edits do not rewrite copies, unreferenced clusters
  remain untouched, and user-owned destination ConfigMaps are never overwritten.
- With installation held, update source content and release the hold. Verify the
  propagated content is retained and the initial version annotation is preserved.

## Lifecycle and scope

- On a disposable, already-reconciled ClusterInstance, delete a managed copy.
  Source synchronization and operator restart must not be counted as recovery:
  automatic recreation is out of scope.
- Manual copy edits are unsupported. A subsequent source reconciliation overwrites
  managed content; do not expect a copy edit alone to trigger prompt repair.
- Delete a source: verify existing copies remain and warning events are emitted.
- On a disposable setup, verify shared namespace recreation and copy garbage
  collection when the owning ClusterInstance is deleted.

## Day-N validation procedure

Validate day-N reference changes on both Assisted Installer and IBI using the final
build. On a successfully installed CI, add, replace, remove, and reorder references.
Verify CI admission succeeds and selected copies converge. Reference changes must
reach installer dry-run validation without installer-specific preservation.
For an installer that rejects the change, verify `RenderedTemplatesValidated=False`,
the updated CI references persist, and shared source updates still reach the selected
copies. Obsolete managed copies must remain until reconciliation succeeds.

Add the rejecting kind to `spec.suppressedManifests` (`AgentClusterInstall` or
`ImageClusterInstall`), retaining existing suppression entries. Verify the CI
reconciles without an error, the installer is recorded as suppressed, its live
resource remains unchanged, and obsolete managed copies are removed. Update source
content again and verify synchronization continues. Remove suppression before
reinstalling and verify the reinstall consumes the current references. Also test
reverting a rejected reference change and accepting the dry-run error as alternatives.

Ensure `ManagedCluster` is not suppressed before reinstalling. Reimport looks for
a successfully rendered ManagedCluster entry in `status.manifestsRendered`; a
missing or suppressed entry can prevent progress even if the live resource exists.

## Recorded live results

The following summarizes the recorded live-test findings. These are live results,
separate from the simulated installer-webhook unit tests. The
[original reports and final reinstall conditions](https://github.com/stolostron/siteconfig/pull/1391#issuecomment-6794137505)
record 23/23 ACI checks across two spokes and 12/12 IBI checks, including cleanup.
They identify the tested image digest and working-tree build, which predates the
squashed PR commit. The exact Assisted Installer version was not recorded.

| Scenario | Assisted Installer | IBI |
| --- | --- | --- |
| Day-N CI reference update persists; installer rejects rendered update | Confirmed | Confirmed |
| Suppressing rejecting installer clears CI error and leaves live installer unchanged | Confirmed | Confirmed |
| Shared-copy synchronization continues while installer is suppressed | Confirmed | Confirmed |
| Removing installer suppression restores webhook rejection | Confirmed | Confirmed |
| Fresh installation consumes current reference | Confirmed (install used the shared ref; no spoke proof recorded) | Confirmed |
| Two installed spokes (day-N) | Confirmed (520955-sno, 520955-sno2) | Not established (one IBI spoke) |
| SiteConfig reinstall operator workflow completes | Attempted; blocked (identity metadata not preserved; agent never registered) | Confirmed on retry after recovery; all seven conditions True |
| Full OS reprovision through reinstall | Attempted; discovery ISO booted, no agent/install | Attempted; reprovisioned OS did not rejoin |

Live validation confirmed the workflow for both Assisted Installer and IBI. Direct
reference changes persisted in ClusterInstance while the installer webhook rejected
the rendered update. Suppressing the rejecting installer manifest cleared the CI
error, retained the updated references, and allowed shared-copy synchronization to
continue without changing the live installer resource. Removing suppression restored
the webhook rejection. Neither workflow applies content to an installed spoke without
policy enforcement.

The ACI spoke was installed on 2026-11-06 with `e2e-shared-reinstall-dayn-20261106`
from `siteconfig-shared-resources` in `manifestsConfigMapRefs`; that report did not
record a spoke-side proof artifact.

An ACI reinstall attempt on 2026-11-12 completed teardown but found no
preservation-labeled identity resources (the preservation controller backs up only
ConfigMaps and Secrets, not the ClusterDeployment/AgentClusterInstall
`clusterMetadata`). The ACI identity secrets were recreated, the cluster metadata
was reconstructed from the pre-reinstall snapshot, and the lab BMC proxy was fixed
(HTTP + Redfish `Id`), which let the host reach provisioning. The discovery ISO
booted, but no agent registered and the installer never started. The old spoke was
restored and the BMH re-paused for recovery.

An IBI reinstall follow-up using a ClusterInstance with `ManagedCluster` suppressed
completed resource teardown but then waited in `ReinstallClusterReimported`, because
the suppressed ManagedCluster was absent from the cleared rendered-manifest status.
A fresh IBI installation consumed the current reference successfully. Retry
generation `ibi-refs-20261109-4` completed all seven SiteConfig reinstall conditions,
including `ReinstallClusterReimported=True / Completed`, after removing ManagedCluster
suppression, repairing lab inputs, and clearing stale preserved backups; the original
report records those interventions and related preservation issues for separate
triage. A later clean retry on 2026-11-12 needed no repairs or stale-backup cleanup;
see [Verification status](#verification-status).

Full OS reprovision was not demonstrated. A 2026-11-12 factory-ISO attempt installed
a fresh root disk, but the resulting OS did not preserve the spoke identity (the
hub's preserved admin credentials were rejected), so it could not rejoin. QE should
verify an identity-preserving full IBI reprovision with current references, final-build
regression, multi-spoke IBI coverage, and policy/TALM enforcement. Operator workflow
completion alone does not establish end-to-end reprovision.

`holdInstallation` is rendered by the Assisted Installer templates and is not
consumed by the IBI templates. Run the held-installation source-update/release case
against an Assisted Installer target; setting the field on an IBI ClusterInstance
does not gate IBI media generation or provisioning.

## Verification status

- Final commit `81fa8f7b`: local `make ci-job` on 2026-11-11 passed dependency
  tidying, generation, `gofmt`, `go vet`, golangci-lint v2.11.4 (0 issues), the
  full unit suite, shellcheck, bashate, enhancement lint, and bundle generation
  and validation. Its clean-tree gate flagged only an untracked local `.kilo/`
  workspace directory, which is not part of this change.
- The original evidence used image
  `quay.io/aabugosh/siteconfig-poc@sha256:2f35252a9fb81718b7fb100557e354e53f4723a9f2e8081ca2c2e3d091011497`,
  built from `25fabc1d`. A later image built from the final code commit
  (`sha256:26ea09298...`) was deployed and passed the ACI and IBI day-N runs
  (12/12 each) and the reinstall workflow conditions.
- A clean retry on 2026-11-12 (`clean-retry-20261112`) needed no manual repairs
  and no stale-backup cleanup; teardown and restoration completed cleanly.
  End-to-end reimport still failed: the recreated BareMetalHost left the previous
  IBI DataImage terminating with its `dataimage.metal3.io` finalizer, so the ICI
  could not generate new media and the VM stayed powered off. Manual power-on was
  required to recover. A separate factory-ISO reprovision attempt also failed to
  preserve the spoke identity: the hub's preserved admin credentials were
  rejected (HTTP 401), so the reprovisioned OS never rejoined.
- Merge gate: the maintainer and QE should agree which outstanding cases below
  are required before merge versus follow-up work.

## Outstanding validation and external follow-ups

Representative fleet-load testing remains required: measure convergence time,
API-server throughput, and provisioning-controller latency. Fake-client benchmarks
do not establish fleet-scale acceptance.

Policy-owner confirmation of missing-copy reporting and must-gather collection of
the shared namespace are external follow-ups. Automatic recovery of missing or
manually edited copies is out of scope. The day-N error-handling agreement is tracked in [EP #1056](https://github.com/stolostron/siteconfig/pull/1056).

Attach the tested commit/image, installer versions, inputs without credentials,
conditions, timings, and cleanup results to the test report. Keep run-specific
images and lab histories in that report rather than in this reusable checklist.

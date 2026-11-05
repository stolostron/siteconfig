# Shared manifests recovery validation — November 5, 2026

The local implementation now includes namespace Pod Security Admission labeling,
terminating-namespace retries, aggregate propagation errors, source backup labels,
copy deletion recovery, startup recovery, and prompt content-drift repair.

Recovery uses the ClusterInstance informer's initial list as the startup pass;
this avoids a second explicit list/sweep while covering objects already present
at startup. Copy deletion events bypass the ClusterInstance controller's general
predicates. Recovery creates only missing copies before generation/restore guards.
It preserves existing copies and does not render installer resources. Paused or
deleting owners are skipped. SharedManifestSync owns content repair, not creation.
This implements the proposal in EP Open Question 2; reviewer approval remains needed.

## Live run

Two disposable ClusterInstances rendered only ConfigMaps. Installation hold was
released through the real webhook. No spoke was installed and this does not
validate post-install installer reference switching. An operator stop/start with
one missing copy models the startup aspect of restore; it is not a full ACM/Velero
restore exercise.

Final image tag: `quay.io/aabugosh/siteconfig-poc:shared-resources-20261105-recovery-v2`

Image tested: `quay.io/aabugosh/siteconfig-poc@sha256:20e3292134cb69d73002c5a38d42ab7680b82b076f2a4fb2e863ab076faa338f`

| Check | Result |
| --- | --- |
| initial copy | PASS |
| ownership | PASS |
| no gitops metadata | PASS |
| initial resource version recorded | PASS |
| initial resource version preserved | PASS |
| source update sync within 45 seconds | PASS |
| ci spec and generation unchanged | PASS |
| sync does not render | PASS |
| namespace restricted | PASS |
| source backed up | PASS |
| copies not backed up | PASS |
| held install release | PASS |
| hold release preserves initial version | PASS |
| deletion recovered | PASS |
| recovery does not render | PASS |
| recovery leaves existing copy | PASS |
| drift repaired without source event | PASS |
| startup recovery without render | PASS |
| startup preserves existing copy | PASS |

All disposable resources were submitted for cleanup successfully. The recovery
operator was restored to one replica after the restart test.

## Local checks

- Full Go suite passed.
- Repository Go lint passed with zero issues.
- Added recovery/no-render tests, no-op cases, source-backup failure coverage,
  namespace race/error tests, exact metadata assertions, warning-event checks,
  aggregate-error checks, drift repair, and reference-index coverage.
- Local fake-client fanout benchmark: 100 copies ~9 ms; 1,000 ~74 ms;
  3,500 ~260 ms on this workstation (one iteration each). Every copy converged.
  These are synthetic controller-cost measurements, not API-server throughput or
  fleet-scale acceptance. Reproduce with:

```sh
GOTOOLCHAIN=auto go test ./internal/controller -run TestSharedManifest \
  -bench BenchmarkSharedManifestFanout -benchtime=1x -benchmem
```

## External work

See [prepared follow-ups](followups/shared-manifests-external-followups.md).
The policy source correlates ConfigMaps from ClusterInstance references, but has
no explicit missing-copy failure. A proposed policy patch and a must-gather patch
are prepared locally; neither has been submitted or deployed. IBI reference
switching, full hub restore, and representative fleet-load acceptance remain open.

At the time of the live run, implementation changes were local and uncommitted.
The tested source snapshot was subsequently checked against the implementation
prepared for the PR. Published container images are separate artifacts from the
implementation branch.

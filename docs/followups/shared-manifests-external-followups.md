# Shared manifests: external follow-ups

Reviewed upstream sources on November 5, 2026. These patches are prepared locally;
neither has been submitted, applied upstream, or deployed.

## Extra-manifests policy

Source: https://github.com/openshift-kni/telco-reference/blob/main/telco-hub/configuration/reference-crs/required/gitops/ztp-policies/extra-manifests-policy.yaml

The policy already looks up the ClusterInstance by managed-cluster name and iterates
its `spec.extraManifestsRefs`. It reads each named copy in the cluster namespace.
Thus reference correlation is implemented in this version.

Missing ConfigMaps are guarded by `if lookup` without an error branch. There is no
explicit missing-copy failure. The policy builds a list of desired MachineConfigs
and later generates `mustnothave` rules for previously managed MachineConfigs not
in that list, so a missing input can also affect removal decisions. This is a source
review, not a live observation of policy compliance behavior.

`shared-manifests-policy.patch` proposes failing hub-template resolution when a
referenced copy is absent rather than generating an incomplete desired list.
Policy owners must validate that the deployed template resolver supports `fail`,
that the resulting error surfaces as policy non-compliance, and that no stale or
partial policy is enforced by TALM. Test deletion, restoration, empty reference
lists, and a mixture of present/missing copies. Keep inform mode. EP Open Question
3 requires owner confirmation or a tracked epic story; this local patch is neither.

## Must-gather

Source: https://github.com/stolostron/must-gather/blob/main/collection-scripts/gather_hub_logs

The current script collects ClusterInstances but contains no explicit collection
of `siteconfig-shared-resources`. `shared-manifests-must-gather.patch` adds namespace
and ConfigMap inspection alongside the existing SiteConfig collection. The modified
script passes `bash -n`. Validate with an actual must-gather bundle, including hubs
where SiteConfig/shared namespace is absent, before merging in that repository.

## Remaining environment-dependent validation

See [reference-switching findings and proposed scope](shared-manifests-reference-switching.md)
for the Assisted Installer restriction and a draft EP reviewer question.

- IBI post-install reference switching requires a completed IBI installation.
- The previously tested Assisted Installer rejects name switching; report that
  result to EP Open Question 1 and agree whether repointing ships separately.
- Restart recovery tests model missing copies while the operator is stopped;
  full ACM/Velero backup-and-restore remains a distinct test.
- Fleet-scale acceptance needs representative API server load, propagation
  latency, and provisioning-controller latency measurements. Small smoke tests or
  fake-client fanout tests must not be represented as fleet-scale acceptance.

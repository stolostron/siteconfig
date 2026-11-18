# Migration registration investigation

## Evidence and limits

The November 16, 2026 run used an experimental build, not the current PR.
Its saved registration snapshot at 08:27:09 UTC shows:

- `migration-local-cluster` KlusterletConfig selected `LocalSecrets`, referenced
  `bootstrap-local-cluster`, and used `IncludeCurrentHub`.
- The source hub's Klusterlet ManifestWork had empty `bootstrapKubeConfigs`.
- The live spoke Klusterlet had bootstrap type `None` and reported
  `HubKubeConfigUnauthorized` against its existing hub endpoint.
- The source ManagedCluster snapshots already contained
  `import.open-cluster-management.io/disable-auto-import` during the attempt.

Read-only checks on November 18 confirmed that the configuration still names the
bootstrap Secret, and that a Secret with that name exists in `multicluster-engine`
(metadata inspected only). The ManifestWork still has empty bootstrap configuration.
Recent import-controller logs repeatedly report `Auto import is disabled` for
`520955-sno2`, interleaved with import Secret regeneration. These recent logs do not
establish what the controller did during the original migration window. A disabled
auto-import path may be intentional during migration and is not by itself a root
cause.

The source ManagedCluster now reports availability Unknown, with
`ManagedClusterLeaseUpdateStopped` since November 17. This differs from the saved
successful rollback observation and must be investigated before another migration.
No live objects were changed by this investigation.

The evidence localizes the missing configuration between KlusterletConfig and the
applied Klusterlet ManifestWork. It does not establish a transient error, an endpoint
root cause, or independence from the feature. Do not report a migration pass.

## Required follow-up with Global Hub/OCM and QE

1. Restore and verify baseline source/spoke health before rerunning migration.
2. Trace KlusterletConfig selection and generated import resources through the
   import controller to the Klusterlet ManifestWork. Check whether the deployed
   controller version and migration path intentionally suppress updates while
   `disable-auto-import` is set; do not remove that annotation speculatively.
3. Verify target bootstrap credentials, endpoint identity and reachability from the
   spoke without exposing credentials in reports. Secret existence alone is insufficient.
4. Capture controller logs for the complete rerun window, both hub versions,
   migration conditions, ManifestWork generations, spoke bootstrap configuration,
   target CSR/approval and final ManagedCluster registration/availability.
5. Use the current PR build and documented manual copy restoration/adoption.
   Include a migrated copy whose old source version exceeds the target source's
   version; verify subsequent target source updates and installer-spec preservation.
6. Verify rollback and source availability if migration fails. Include the case in
   QE's owned test plan and obtain a completed end-to-end result before feature
   completion. This document is a checklist, not confirmation that QE has accepted
   or executed it.

Saved artifacts are under `/private/tmp/siteconfig-migration-20261116/` in the test
workspace. That directory includes private kubeconfigs; share only reviewed,
sanitized evidence, never the entire directory.

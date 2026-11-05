# Post-install reference switching: findings and proposed scope

Status: proposed scope decision, pending EP reviewer agreement. Reviewed November
5, 2026 against EP PR #1056 head
`f12572ded786ca9cf1e0fa014d45a63df7e84b4d`.

## Evidence

The recorded Assisted Installer test failed with
`RenderedTemplatesValidated=False`: its webhook reported a change to
`ManifestsConfigMapRefs.Name` as immutable after installation starts. The
downstream reference stayed unchanged. See the
[QE reproduction and rollback instructions](../shared-extra-manifests-qe-tests.md#4-reference-switching--known-assisted-installer-failure).
This is the earlier test result, not a new installation test. Capture the exact
deployed installer image/version and full rejection when reproducing it.

The current upstream
[AgentClusterInstall validation webhook](https://github.com/openshift/assisted-service/blob/master/pkg/webhooks/hiveextension/v1beta1/agentclusterinstall_validation_hook.go)
corroborates that behavior: after `installAlreadyStarted`, it compares the spec
while ignoring only permitted mutable fields. `ManifestsConfigMapRefs` is not
excluded from that comparison. An installed condition still counts as having
started installation. This source review does not identify the version deployed
in the earlier test.

SiteConfig currently permits post-install `/extraManifestsRefs` edits in
`api/v1alpha1/clusterinstance_validation.go`. Passing that admission check does
not imply successful downstream application. The subsequent rendered-resource
dry-run gate can reject the change before any rendered resources are applied.

IBI remains untested. A hub and an Assisted Installer spoke do not establish IBI
behavior; validation requires a completed IBI installation and its
`ImageClusterInstall` resource.

## Proposed initial scope

Ship shared ConfigMap copying and content synchronization independently of
post-install reference-name switching, as the EP's graduation discussion allows.
Retain the same reference names after installation. Updating source content
synchronizes the cluster-namespace copies; applying it to an installed spoke
remains the responsibility of policy/TALM.

Do not advertise post-install switching as supported. Before implementing a
scope split, reviewers must agree how SiteConfig admission should handle these
edits, including existing local references and reinstall workflows. The current
admission allowance has not been changed by this documentation update.

If switching remains required, it needs an Assisted Installer owner decision and
a supported downstream change, followed by end-to-end retesting. IBI must also
be validated independently. Do not bypass installer admission. The EP proposes
reinstall-based repointing as a fallback; that workflow still needs validation.

## Draft for EP reviewers

> The tested Assisted Installer rejected a post-install ConfigMap reference-name
> change, leaving SiteConfig's RenderedTemplatesValidated condition False. The
> upstream webhook also excludes manifest references from its allowed mutable
> fields after installation starts. IBI remains untested. Can we separate
> post-install reference switching from the initial shared-copy/content-sync
> scope, and agree the corresponding SiteConfig admission behavior? Otherwise,
> we need downstream Assisted Installer support and validation of both installer
> flows before claiming switching support. Content synchronization does not
> itself apply changes to an installed spoke.

This draft has not been posted. EP Open Question 1 remains open until reviewers
agree the scope and required validation.

---
title: hcp-status-sync
authors:
  - "@cwilkers"
reviewers:
  - "@sakhoury"
approvers:
  - "@sakhoury"
api-approvers:
  - "None"
creation-date: 2026-04-30
last-updated: 2026-04-30
status: provisional
tracking-link:
  - TBD
see-also:
  - "/docs/enhancements/related-proposal.md"
replaces:
  - "/docs/enhancements/old-proposal.md"
superseded-by:
  - "/docs/enhancements/new-proposal.md"
---

# Hosted Control Plane cluster status sync

## Release Signoff Checklist

- [ ] Enhancement is `implementable`
- [ ] Design details are appropriately documented from clear requirements
- [ ] Test plan is defined
- [ ] Graduation criteria are defined
- [ ] User-facing documentation is updated

## Summary

SiteConfig Operator should update status of ClusterInstances that deploy HCP
clusters in much the same way it already does for AI and IBI clusters.

## Motivation

The `Provisisoned` `ClusterInstance.status.conditions[]` field is used in ZTP
workflows to signal completion of the deployment and clears automation to
proceed to the next phase of a multi-part deployment.

### User Stories

- As a ZTP workflow author, I want HCP clusters' status reflected in their owner
`ClusterInstances` so that my workflows can monitor the successful deployment of
the clusters.

- As a ZTP workflow author, I want an HCP cluster's status to remain `Provisioned` when it reaches `Provisioned` so that there are no side effects in automation if that changes.

Other stories that may need to be filled out:

- **Remediation**: How do admins recover when the feature fails? Does it use the
  pause annotation pattern? What requeue delays apply? Is there troubleshooting
  guidance?
- **Scale**: Does it affect `maxConcurrentReconciles`? How does it behave with
  many ClusterInstances?

### Goals

* In the context of status.conditions updates, a ClusterInstance should report
  the deployment status of the underlying cluster in much the same way
  regardless of the type of the underlying cluster.
* Respecting the previous goal, the ClusterInstance should pass through useful
  information about a hosted cluster's deployment progress, which will follow a
  different path than an AI or IBI cluster.

### Non-Goals

* ClusterInstance does not need to be involved in day two life-cycle events of a
  hosted cluster, and should not revert the `Provisioned` status once achieved.

## Proposal

### Workflow Description

Describe the detailed user workflow. Include all actors, roles, APIs, and
interfaces involved. Use sub-sections for variations such as error handling and
failure recovery.

**TBD**

- **Monitoring**: Does the feature add or affect status conditions
  (e.g., `Provisioned`, `RenderedTemplatesApplied`)? Are structured logs
  sufficient for debugging?

### API Extensions

None, outside of vendoring the hypershift project.

### Siteconfig Impact

Summarize which siteconfig components are affected:

- **Controllers**: Which controllers are modified? (ClusterInstanceReconciler,
  ClusterDeploymentReconciler, ConfigurationMonitor)

The set of controllers will likely change depending on the implementation chosen (HostedCluster or ManagedCluster are likely choices.)

- **Templates**: Are new templates added or existing ones modified? Which
  installation flow? (Assisted Installer, Image Based Installer, Hosted Control
  Plane)

No template changes are required.

- **API fields**: Are new fields added to ClusterInstance or related types?

No type changes are required.

- **Validation**: Are webhook or spec validation rules changed?

No validation rules are affected.

### Implementation Details/Notes/Constraints [optional]

Caveats, important details, core concepts and relationships.

Much of the implementation is still TBD. There are PoCs coded already to follow a HostedCluster's .status.conditions, or .status.version[] to gather the required status.

### Risks and Mitigations

What are the risks of this proposal? Consider security, UX, and ecosystem
impact. How are they mitigated?

Hypershift will possibly need to be vendored in, but this remains an implementation TBD item.

### Drawbacks

Why should this enhancement NOT be implemented? Discuss trade-offs: technical
cost, UX impact, flexibility, supportability, and maintenance burden.

Maintenance burden could be increased by following HostedCluster status fields. If a ManagedCluster status approach is taken, it would suggest rewriting the AI and IBI ClusterDeploymentReconciler to follow ManagedCluster instead.

## Design Details

### Open Questions [optional]

* Which path (HostedCluster.status or ManagedCluster.status) should we take to provide the status?
* If ManagedCluster, will work need to be done in ocm to make it reflect a more comprehensive status than 'API is healthy?'

### Test Plan

Describe how this enhancement will be tested. Consider:

- Unit tests are likely the best plan.
- E2e integration tests will be required to provide a demonstration, but could be expensive or prohitively flaky to implement in CI due to the need for a hub/hosted cluster pair.
- What scenarios need to be covered

### Graduation Criteria

Define the milestones for this enhancement:

- [ ] Design reviewed and approved by maintainers
- [ ] Implementation merged with adequate test coverage
- [ ] Documented in user-facing docs
- [ ] Released in version X.Y.Z

### Upgrade / Downgrade Strategy

It would be good to test whether a downgrade will affect the status of a
ClusterInstance that has reached `Provisioned` when the code to update it is
gone.

### Version Skew Strategy

It is unlikely that a version will be advanced during a hosted cluster's deployment, but in that case, status updates should proceed normally

#### Failure Modes

The primary failure mode will be a missing or unknown `Provisioned` status, which is the state of the current operator.

#### Support Procedures

SiteConfig Controller logs should show if there is an error while reading
status. The original manifests will also still be unaltered for debugging.

## Implementation History

- If ManagedCluster needs an update, this must happen before this work can merge.
- Once merged, even existing deployments will report an appropriate status.

## Alternatives

There is not an alternative to modifying the SiteConfig Operator to somehow read a status update from some CR in the Hypershift workflow.

A previous attempt was made to simply follow `HostedCluster.status.conditions[]`
and provide `ClusterInstance.status.conditions[]` from that, but it was rejected
as requiring too much maintenance burden on SiteConfig Operator.
# Siteconfig Enhancement Proposals

This directory contains enhancement proposals for the siteconfig project. Enhancement proposals document the design and rationale for significant changes before implementation begins.

This process is inspired by and adapted from the [openshift-kni/kni-enhancements](https://github.com/openshift-kni/kni-enhancements) repository.

## When to Write an Enhancement Proposal

An enhancement proposal is required for:

- New features or significant behavioral changes
- Changes to the ClusterInstance API or CRDs
- Architectural decisions affecting controllers, templates, or the rendering pipeline
- Changes that affect upgrade/downgrade or version skew

An enhancement proposal is NOT required for:

- Bug fixes
- Minor refactors or code cleanup
- Documentation updates
- Dependency updates

When in doubt, discuss with a maintainer before writing a full proposal.

## How to Create a Proposal

1. Copy the [enhancement template](./guidelines/enhancement_template.md)
2. Name your file using kebab-case (e.g., `cluster-reinstallation-workflow.md`)
3. Place it directly in `docs/enhancements/` (no subdirectories)
4. Fill out at minimum the **Summary** and **Motivation** sections
5. Open a pull request with your proposal

Proposals can start lightweight — fill in just the Summary and Motivation to
gauge interest from the team. If the idea has enough support, complete the
remaining required sections in follow-up commits to the same PR. The enhancement
lint will fail until all required sections are filled in, which is expected for
early-stage proposals.

## Review and Approval

Enhancement proposals follow the same review process as code changes, using the project-wide OWNERS. The proposal PR should be assigned to domain experts for review. The PR only receives approval once all required sections are complete and the enhancement lint passes. Once the PR is merged, implementation can begin.

Enhancement proposals must be merged before implementation PRs are submitted.

## Enhancement Lifecycle

```text
                 PR opened           consensus            code
   Author         with               reached &           merged
   drafts       proposal             PR merged           in main
   proposal        |                    |                   |
      |            v                    v                   v
      +-----> [provisional] -----> [implementable] -----> [implemented]
                   |                    |
                   |     needs rework   |
                   +<-------------------+
```

Proposals track their status via the `status` field in the YAML frontmatter:

- **`provisional`**: Design is being discussed. The proposal PR is open and under review.
- **`implementable`**: Design has been agreed upon. The proposal PR has been merged. Implementation can begin.
- **`implemented`**: The feature has been implemented and released. The author updates the `status` and `last-updated` fields.

## Naming Conventions

- **Filename**: kebab-case, descriptive of the feature (e.g., `vsphere-platform-support.md`)
- **No numbering prefix**: filenames are descriptive, not sequential
- **Flat structure**: all proposals live directly in `docs/enhancements/`; the `guidelines/` subdirectory is reserved for the template only
- **YAML `title` field**: should match the filename but can be more readable

# Contributing guidelines

<details>
<summary><b>Table of Contents</b></summary>

- [Terms](#terms)
- [Certificate of Origin](#certificate-of-origin)
- [DCO Sign Off](#dco-sign-off)
- [Code of Conduct](#code-of-conduct)
- [Development Environment Setup](#development-environment-setup)
- [Development Workflow](#development-workflow)
- [Code Style and Best Practices](#code-style-and-best-practices)
- [Testing Requirements](#testing-requirements)
- [Code Coverage Requirements](#code-coverage-requirements)
- [API and CRD Changes](#api-and-crd-changes)
- [Documentation Requirements](#documentation-requirements)
- [Pull Request Guidelines](#pull-request-guidelines)
- [Contributing a patch](#contributing-a-patch)
- [Issue and pull request management](#issue-and-pull-request-management)

</details>

## Terms

All contributions to the repository must be submitted under the terms of the [Apache Public License 2.0](https://www.apache.org/licenses/LICENSE-2.0).

## Certificate of Origin

By contributing to this project, you agree to the Developer Certificate of Origin (DCO). This document was created by the Linux Kernel community and is a simple statement that you, as a contributor, have the legal right to make the contribution. See the [DCO](https://github.com/open-cluster-management-io/community/blob/main/DCO) file for details.

## DCO Sign Off

You must sign off your commit to state that you certify the [DCO](https://github.com/open-cluster-management-io/community/blob/main/DCO). To certify your commit for DCO, add a line like the following at the end of your commit message:

```
Signed-off-by: John Smith <john@example.com>
```

This can be done with the `--signoff` option to `git commit`. See the [Git documentation](https://git-scm.com/docs/git-commit#Documentation/git-commit.txt--s) for details.

## Code of Conduct

The Open Cluster Management project has adopted the CNCF Code of Conduct. Refer to the Community [Code of Conduct](https://github.com/open-cluster-management-io/community/blob/main/CODE_OF_CONDUCT.md) for details.

## Development Environment Setup

### Prerequisites

- Go 1.21 or later
- Docker or Podman for building container images
- kubectl for Kubernetes cluster interaction
- Make

### Getting Started

1. Clone the repository:
   ```bash
   git clone https://github.com/stolostron/siteconfig.git
   cd siteconfig
   ```

2. Install dependencies:
   ```bash
   make common-deps-update
   ```

3. Build the project:
   ```bash
   make build
   ```

## Development Workflow

Before submitting any code changes, ensure your development environment is properly set up and all CI checks pass locally.

### Running CI Checks Locally

Before submitting a pull request, you **must** run the CI validation suite locally to ensure there are no linter errors and all unit tests pass:

```bash
make ci-job
```

The `ci-job` target runs the following checks:
- **Dependency updates**: Updates and tidies Go modules
- **Code generation**: Generates DeepCopy methods and mock implementations
- **Formatting**: Runs `go fmt` to format the code
- **Vetting**: Runs `go vet` to detect suspicious constructs
- **Linting**: Runs `golangci-lint` to catch common code issues
- **Unit tests**: Executes all unit tests
- **Shell script validation**: Runs shellcheck and bashate on shell scripts
- **Bundle validation**: Checks that operator bundle manifests are up-to-date

All checks must pass before your code can be merged.

### Individual Make Targets

You can also run individual targets during development:

- `make fmt` - Format code
- `make vet` - Run go vet
- `make golangci-lint` - Run linter
- `make unittest` - Run unit tests
- `make test-coverage` - Generate coverage report
- `make generate` - Generate code
- `make manifests` - Generate CRDs and RBAC manifests

## Code Style and Best Practices

### Go Code Style

This project follows standard Go coding conventions and best practices:

- Follow [Effective Go](https://go.dev/doc/effective_go) guidelines
- Use `gofmt` for code formatting (automatically applied by `make fmt`)
- Follow the [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md) for additional best practices
- Keep functions focused and concise
- Use meaningful variable and function names
- Add comments for exported functions, types, and complex logic

### Kubernetes Operator Best Practices

As this is a Kubernetes operator project, follow these additional guidelines:

- **Controller Logic**: Keep reconciliation logic idempotent and handle edge cases
- **Status Updates**: Always update resource status with meaningful conditions
- **Error Handling**: Return errors appropriately to trigger reconciliation with backoff
- **Finalizers**: Use finalizers for cleanup logic when resources are deleted
- **Watches**: Be mindful of what resources your controller watches to avoid unnecessary reconciliations
- **Logging**: Use structured logging with appropriate log levels (Info, Error, Debug)

### Code Organization

- Place API types in `api/v1alpha1/`
- Place controller logic in `internal/controller/`
- Keep utility functions in separate packages with clear responsibilities
- Use interfaces to enable testing and mocking

### Commit Messages

Write clear, descriptive commit messages:

```
Short (50 chars or less) summary

More detailed explanatory text, if necessary. Wrap it to about 72
characters. The blank line separating the summary from the body is
critical.

Explain the problem that this commit is solving. Focus on why you
are making this change as opposed to how (the code explains that).

Signed-off-by: Your Name <your.email@example.com>
```

## Testing Requirements

### Unit Tests

All code changes must include appropriate unit tests. When adding new functionality or fixing bugs:

1. Write unit tests that cover the new code paths
2. Ensure existing tests still pass
3. Run tests locally before submitting:
   ```bash
   make unittest
   ```

### Test Organization

- Place unit tests in `*_test.go` files alongside the code they test
- Use table-driven tests where appropriate for better test coverage
- Mock external dependencies using the mock framework (see `internal/controller/mocks/`)

## Code Coverage Requirements

All new functionality must meet the following code coverage requirements:

- **Minimum**: 70% code coverage for newly added code
- **Target**: 90% code coverage (strongly encouraged)

### Checking Coverage

To generate and view a coverage report:

```bash
make test-coverage
go tool cover -html=test-coverage.out
```

The coverage report will help identify untested code paths. Reviewers will check coverage as part of the pull request review process.

### Coverage Guidelines

- Focus on testing business logic and error handling paths
- Mock external dependencies to enable thorough testing
- Document any intentionally untested code with a clear justification

## API and CRD Changes

Changes to the ClusterInstance API or Custom Resource Definitions (CRDs) require special attention:

### Modifying API Types

When modifying types in `api/v1alpha1/`:

1. **Backward Compatibility**: Ensure changes are backward compatible when possible
   - Use optional fields (pointers) for new additions
   - Avoid removing or renaming existing fields
   - Use deprecation notices for fields being phased out

2. **Validation**: Add appropriate validation tags and webhook validation
   - Use kubebuilder markers for OpenAPI validation
   - Implement webhook validation for complex rules

3. **Documentation**: Add `// +kubebuilder:` markers and comments
   - Document field purpose and expected values
   - Include examples in comments

4. **Generation**: After API changes, regenerate manifests and code:
   ```bash
   make generate
   make manifests
   make bundle
   ```

### Sample Updates

- Update sample CRs in `config/samples/` to reflect API changes
- Ensure samples are valid and demonstrate new functionality
- Test samples against the updated operator

### Breaking Changes

Breaking API changes must be:
- Documented in the pull request with migration guide
- Backed by a JIRA issue with architecture/design review
- Discussed with maintainers before implementation
- Include deprecation warnings in the previous version when possible

## Documentation Requirements

Proper documentation is essential for maintainability and user adoption:

### Code Documentation

- Add godoc comments for all exported types, functions, and constants
- Document complex algorithms or business logic with inline comments
- Include examples in function documentation when helpful

### User-Facing Documentation

When adding new features or changing behavior, update relevant documentation in the `docs/` directory:

- **Configuration Guide** (`docs/configure_siteconfig.md`): Document new configuration options
- **Troubleshooting** (`docs/troubleshooting.md`): Add common issues and solutions
- **Architecture Docs**: Explain significant design decisions

### Sample Files and Examples

- Update example manifests in `config/samples/` and `examples/`
- Ensure examples are complete and can be used directly by users
- Add comments to explain non-obvious configurations

### README Updates

Update the main [README.md](README.md) if your changes affect:
- Installation instructions
- Quick start guide
- Requirements or dependencies

### Inline Documentation

For complex configuration or CRD fields, add:
- Field descriptions in struct tags
- Usage examples in comments
- Links to related documentation

## Pull Request Guidelines

When you create a pull request, GitHub will automatically populate a template to help you provide all necessary information. Please fill out all applicable sections of the template.

### Branch and Target

- All pull requests must be opened against the `main` branch
- Create a feature branch from the latest `main` for your changes

### Pull Request Title Format

Pull request titles must follow this format:

```
JIRA-12345: Brief description of the change
```

or, if there is no associated JIRA issue:

```
NO-ISSUE: Brief description of the change
```

**Examples:**
- `JIRA-12345: Add contributing guideline`
- `JIRA-67890: Fix validation error for multi-node clusters`
- `NO-ISSUE: Update README with new examples`

### JIRA Requirements

- **Breaking changes** or **significant functionality changes** must be backed by an official JIRA issue
- Minor changes (documentation, typos, minor up-versioning) can use `NO-ISSUE`
- Reference the JIRA issue in your pull request description

### AI-Generated Code Disclosure

If you used Artificial Intelligence (AI) tools (e.g., Claude, Gemini, GPT-4, GitHub Copilot) to generate, co-develop, or assist with your code changes, you **must** disclose this information in your pull request using the AI Assistance section in the [pull request template](.github/PULL_REQUEST_TEMPLATE.md).

**Important Requirements:**

- All AI-generated code must be thoroughly reviewed and understood by the contributor
- Contributors remain fully responsible for ensuring AI-generated code meets all project standards
- AI-generated code must pass all tests, linting, and coverage requirements
- Contributors must verify that AI-generated code does not introduce security vulnerabilities or licensing issues

Using AI tools is acceptable and can improve productivity, but **transparency is essential** for code quality, security auditing, and knowledge transfer.

### Before Submitting

Before submitting your pull request:

1. Run `make ci-job` locally and ensure all checks pass
2. Review the checklist in the [pull request template](.github/PULL_REQUEST_TEMPLATE.md) and ensure all applicable items are completed
3. Verify your PR title follows the required format (`JIRA-12345: Description` or `NO-ISSUE: Description`)
4. Ensure all commits are signed off with DCO (`git commit --signoff`)

## Contributing a patch

1. **Discuss the change**: For significant changes, submit an issue or JIRA ticket describing your proposed change. The repository owners will respond to your issue promptly.
2. **Fork and develop**: Fork the repository, create a feature branch, and develop your code changes following the [Development Workflow](#development-workflow) guidelines.
3. **Test thoroughly**: Ensure all tests pass and code coverage requirements are met (see [Testing Requirements](#testing-requirements) and [Code Coverage Requirements](#code-coverage-requirements)).
4. **Run CI checks**: Execute `make ci-job` locally and fix any issues.
5. **Submit a pull request**: Follow the [Pull Request Guidelines](#pull-request-guidelines) for title format and description requirements.

## Issue and pull request management

Anyone can comment on issues and submit reviews for pull requests. In order to be assigned an issue or pull request, you can leave a `/assign <your Github ID>` comment on the issue or pull request.

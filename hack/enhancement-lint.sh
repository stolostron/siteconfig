#!/bin/bash
# Validates enhancement proposal files for required sections and YAML frontmatter.

set -euo pipefail

ENHANCEMENTS_DIR="docs/enhancements"

REQUIRED_HEADERS=(
    "## Release Signoff Checklist"
    "## Summary"
    "## Motivation"
    "### User Stories"
    "### Goals"
    "### Non-Goals"
    "## Proposal"
    "### Workflow Description"
    "### API Extensions"
    "### Siteconfig Impact"
    "### Risks and Mitigations"
    "### Drawbacks"
    "## Design Details"
    "### Test Plan"
    "### Graduation Criteria"
    "### Upgrade / Downgrade Strategy"
    "### Version Skew Strategy"
    "## Implementation History"
    "## Alternatives"
)

REQUIRED_METADATA=(
    "title:"
    "authors:"
    "status:"
    "creation-date:"
    "tracking-link:"
)

VALID_STATUSES="provisional|implementable|implemented"

find_changed_files() {
    local changed_files=()

    if ! git rev-parse --is-inside-work-tree &>/dev/null; then
        echo "warning: not a git repository, scanning all enhancement files" >&2
        while IFS= read -r -d '' file; do
            changed_files+=("$file")
        done < <(find "${ENHANCEMENTS_DIR}" -maxdepth 1 -name '*.md' ! -name 'README.md' -print0 2>/dev/null)
    else
        local base_ref="${PULL_BASE_SHA:-origin/main}"
        local diff_output
        if ! diff_output=$(git diff --name-only --diff-filter=d "${base_ref}...HEAD" -- "${ENHANCEMENTS_DIR}" 2>/dev/null); then
            echo "warning: unable to diff against ${base_ref}; scanning all enhancement files" >&2
            while IFS= read -r -d '' file; do
                changed_files+=("$file")
            done < <(find "${ENHANCEMENTS_DIR}" -maxdepth 1 -name '*.md' ! -name 'README.md' -print0 2>/dev/null)
        else
            while IFS= read -r file; do
                changed_files+=("$file")
            done < <(printf '%s\n' "$diff_output" | \
                grep '\.md$' | \
                grep -v "${ENHANCEMENTS_DIR}/README.md" | \
                grep -v "${ENHANCEMENTS_DIR}/guidelines/" || true)
        fi
    fi

    if [ ${#changed_files[@]} -gt 0 ]; then
        printf '%s\n' "${changed_files[@]}"
    fi
}

check_frontmatter() {
    local file="$1"
    local errors=0

    if ! head -1 "$file" | grep -q '^---$'; then
        echo "  ERROR: missing YAML frontmatter (file must start with ---)" >&2
        return 1
    fi

    if ! grep -q '^---$' <(sed -n '2,$p' "$file"); then
        echo "  ERROR: missing closing --- marker in YAML frontmatter" >&2
        return 1
    fi

    local frontmatter
    frontmatter=$(sed -n '2,/^---$/{ /^---$/d; p; }' "$file")

    if [ -z "$frontmatter" ]; then
        echo "  ERROR: empty YAML frontmatter" >&2
        return 1
    fi

    for field in "${REQUIRED_METADATA[@]}"; do
        if ! echo "$frontmatter" | grep -q "^${field}"; then
            echo "  ERROR: missing required metadata field: ${field}" >&2
            errors=$((errors + 1))
        fi
    done

    local status_value
    status_value=$(echo "$frontmatter" | grep "^status:" | sed 's/^status:[[:space:]]*//')
    if [ -n "$status_value" ] && ! echo "$status_value" | grep -qE "^(${VALID_STATUSES})$"; then
        echo "  ERROR: invalid status value: ${status_value}. Must be one of: ${VALID_STATUSES}" >&2
        errors=$((errors + 1))
    fi

    return $errors
}

check_sections() {
    local file="$1"
    local errors=0

    if ! grep -q '^# ' "$file"; then
        echo "  ERROR: missing document title (line starting with '# ')" >&2
        errors=$((errors + 1))
    fi

    for header in "${REQUIRED_HEADERS[@]}"; do
        if ! grep -qF "$header" "$file"; then
            echo "  ERROR: missing required section: ${header}" >&2
            errors=$((errors + 1))
        fi
    done

    return $errors
}

main() {
    local files=()

    if [ $# -gt 0 ]; then
        files=("$@")
    else
        while IFS= read -r file; do
            [ -n "$file" ] && files+=("$file")
        done < <(find_changed_files)
    fi

    if [ ${#files[@]} -eq 0 ]; then
        echo "No enhancement files to validate."
        exit 0
    fi

    local total_errors=0

    for file in "${files[@]}"; do
        if [ ! -f "$file" ]; then
            echo "WARNING: file not found: $file" >&2
            continue
        fi

        echo "Validating: $file"
        local file_errors=0

        check_frontmatter "$file" || file_errors=$((file_errors + $?))
        check_sections "$file" || file_errors=$((file_errors + $?))

        if [ "$file_errors" -eq 0 ]; then
            echo "  OK"
        fi

        total_errors=$((total_errors + file_errors))
    done

    if [ "$total_errors" -gt 0 ]; then
        echo ""
        echo "Enhancement lint failed with ${total_errors} error(s)."
        exit 1
    fi

    echo ""
    echo "All enhancement files passed validation."
    exit 0
}

main "$@"

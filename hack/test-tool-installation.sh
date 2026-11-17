#!/bin/bash
# Copyright 2026 Red Hat, Inc.
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
tool_test_dir=$(mktemp -d)

function cleanup {
    rm -rf "${tool_test_dir}"
}
trap cleanup EXIT

function assert_executable {
    if [ ! -x "$1" ]; then
        echo "Expected the requested tool to be installed at $1" >&2
        exit 1
    fi
}

# Exercise every go-get-tool caller with an overridden LOCALBIN. These installs
# must not silently reuse or overwrite the tools in the repository's bin dir.
make -C "${repo_root}" envtest controller-gen mockgen LOCALBIN="${tool_test_dir}/tools"
for tool_name in setup-envtest controller-gen mockgen; do
    assert_executable "${tool_test_dir}/tools/${tool_name}"
done

# A direct ENVTEST override must work independently of LOCALBIN.
make -C "${repo_root}" envtest ENVTEST="${tool_test_dir}/override/setup-envtest"
assert_executable "${tool_test_dir}/override/setup-envtest"

echo "Go tool installation directory checks passed"

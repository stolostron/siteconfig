#!/bin/bash

# This script patches vendor files to fix webhook.Validator deprecation issues
# Run this after 'go mod vendor'

set -e

echo "Patching vendor files for webhook.Validator deprecation..."

# Files to patch
WEBHOOK_FILES=(
    "vendor/github.com/openshift/assisted-service/api/v1beta1/agent_webhook.go"
    "vendor/github.com/openshift/assisted-service/api/v1beta1/agentclassification_webhook.go"
    "vendor/github.com/openshift/assisted-service/api/v1beta1/infraenv_webhook.go"
)

for file in "${WEBHOOK_FILES[@]}"; do
    if [ -f "$file" ]; then
        echo "Patching $file..."

        # Add context import if not present
        if ! grep -q '"context"' "$file"; then
            sed -i '/^import (/a\\t"context"' "$file"
        fi

        # Replace webhook.Validator with webhook.CustomValidator
        sed -i 's/webhook\.Validator/webhook.CustomValidator/g' "$file"

        # Fix ValidateCreate method signature
        sed -Ei 's/func (.*) ValidateCreate\(\) (.*)/func \1 ValidateCreate(ctx context.Context, obj runtime.Object) \2/g' "$file"

        # Fix ValidateUpdate method signature  
        sed -Ei 's/func (.*) ValidateUpdate\(old runtime\.Object\)/func \1 ValidateUpdate(ctx context.Context, old, newObj runtime.Object)/g' "$file"

        # Fix ValidateDelete method signature
        sed -Ei 's/func (.*) ValidateDelete\(\) (.*)/func \1 ValidateDelete(ctx context.Context, obj runtime.Object) \2/g' "$file"

        echo "Patched $file successfully"
    else
        echo "Warning: $file not found"
    fi
done

echo "Vendor patching completed!"

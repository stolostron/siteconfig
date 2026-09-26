# Skipping pull secret and BMC secret presence checks

Siteconfig’s ClusterInstance controller normally verifies that the pull secret
and each node’s BMC credential secret exist before reconciliation continues. If
you manage those secrets with another controller (for example External Secrets
Operator or Vault), those objects may not exist yet when the `ClusterInstance` is
applied.

You can opt out of **existence-only** checks by setting optional annotations on
the `ClusterInstance`. Either annotation may be used alone or together. Other
validation (ClusterImageSet, extra manifests, template references, CRD/webhook
rules) is unchanged.

For design background, see
[skip-cluster-secrets](enhancements/skip-cluster-secrets.md).

## Annotations

| Full annotation key | Purpose |
| --- | --- |
| `clusterinstance.siteconfig.open-cluster-management.io/externally-provisioned-pull-secret` | Do not require `spec.pullSecretRef` to exist in the ClusterInstance namespace yet. Value must be empty or `"true"`. |
| `clusterinstance.siteconfig.open-cluster-management.io/externally-provisioned-bmc-secret` | Do not require each node’s BMC credentials secret to exist yet (namespace follows `HostRef` when set). Value must be empty or `"true"`. |

For both annotations, only an empty value or `"true"` is accepted; any other value
fails validation.

## Example

Apply a `ClusterInstance` that sets one or both annotations and the rest of your
spec as usual. The fragment below only shows metadata and a few spec fields;
adapt the rest from your cluster template.

```yaml
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: ClusterInstance
metadata:
  name: sno4-bmc-test
  namespace: sno4
  annotations:
    clusterinstance.siteconfig.open-cluster-management.io/externally-provisioned-pull-secret: ""
    clusterinstance.siteconfig.open-cluster-management.io/externally-provisioned-bmc-secret: ""
spec:
  clusterName: sno4
  pullSecretRef:
    name: pullsecret-cluster-sno4
  clusterImageSetNameRef: img4.18.15-x86-64-appsub
  # ... remainder of ClusterInstance spec ...
```

## Operational notes

- You are responsible for ensuring secrets exist before any downstream install
  path needs them; skipping checks does not create or sync secrets.
- After secrets are in place, you may remove the annotations so the controller
  enforces presence again on future reconciles.

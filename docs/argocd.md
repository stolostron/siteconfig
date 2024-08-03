# Integrating GitOps Flow with the SiteConfig Operator

This guide provides instructions for integrating a GitOps workflow with the SiteConfig operator.
We recommend deploying the ArgoCD operator using the
[Red Hat OpenShift GitOps operator](https://catalog.redhat.com/software/operators/detail/5fb288c70a12d20cbecc6056).

## Table of Contents
1. [Prepare Git Repository](#prepare-git-repository)
2. [Configure ArgoCD](#configure-argocd)
    - [Create ArgoCD Project](#create-argocd-project)
    - [Grant ArgoCD Permissions](#grant-argocd-permissions)
    - [Configure Access to the Git Repository](#configure-access-to-the-git-repository)
    - [Create ArgoCD Application](#create-argocd-application)
3. [Advanced Topics](#advanced-topics)
    - [Generate extra-manifests ConfigMap using kustomize](#generate-extra-manifests-configmap-using-kustomize)

## Prepare Git Repository

To start, create a Git repository with a directory structure similar to the suggested example shown below.

```sh
clusters/
 |- spoke1/
 |-- extra-manifests-cm1.yaml
 |-- extra-manifests-cm2.yaml
 |-- clusterinstance.yaml
 |-- ns.yaml
 |-- secrets.yaml
 |-- kustomization.yaml
 |- kustomization.yaml
```

where:
- `ns.yaml` is the Namespace manifest
- `secrets.yaml` consists of the BMC credential and pull secrets.

### Example kustomization.yaml files

Below are examples of `kustomization.yaml` files required for the setup:

#### clusters/kustomization.yaml
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - spoke1/
```

#### clusters/spoke1/kustomization.yaml
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - ns.yaml
  - secrets.yaml
  - extra-manifests-cm1.yaml
  - extra-manifests-cm2.yaml
  - clusterinstance.yaml
```

## Configure ArgoCD

### Create ArgoCD Project

First, create an ArgoCD `AppProject`. At a minimum, the `ClusterInstance`, `Namespace`, `ConfigMap`, and `Secret` kinds
should be added to the respective resource whitelists.

```yaml
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: siteconfig-v2
  namespace: openshift-gitops
spec:
  clusterResourceWhitelist:
    - group: ""
      kind: Namespace
  namespaceResourceWhitelist:
    - group: ""
      kind: ConfigMap
    - group: ""
      kind: Secret
    - group: siteconfig.open-cluster-management.io
      kind: ClusterInstance
  sourceRepos:
    - '*'
  destinations:
    - namespace: '*'
      server: '*'
```

### Grant ArgoCD Permissions

Create a `ServiceAccount` and `ClusterRoleBinding` to grant RBAC permissions to ArgoCD. Optionally, you can create a
custom `ClusterRole` with fewer privileges. In this example, the OpenShift GitOps ServiceAccount
`openshift-gitops-argocd-application-controller` is used.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: gitops-cluster
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-admin
subjects:
- kind: ServiceAccount
  name: openshift-gitops-argocd-application-controller
  namespace: openshift-gitops
```

### Configure Access to the Git Repository

Configure access to the Git repository using the ArgoCD UI. Under Settings, configure the following:

- **Repositories**: Add connection information (URL ending in `.git`, e.g., `https://repo.example.com/repo.git`,
and credentials).
- **Certificates**: Add the public certificate for the repository if needed.

For development purposes, you can configure `insecure-skip-server-verification` using the
[ArgoCD CLI](https://argo-cd.readthedocs.io/en/stable/cli_installation/). The example below utilizes OpenShift GitOps
operator resources.

```sh
argoPass=$(oc get secret/openshift-gitops-cluster -n openshift-gitops -o jsonpath='{.data.admin\.password}' | base64 -d)
argoURL=$(oc get route openshift-gitops-server -n openshift-gitops -o jsonpath='{.spec.host}{"\n"}')
argocd login --insecure --grpc-web $argoURL --username admin --password $argoPass
argocd repo add https://repo.example.com/repo.git --insecure-skip-server-verification
```

### Create ArgoCD Application

Create an ArgoCD `Application` that points to the Git repository. Modify the example below based on your Git repository:

- **repoURL**: Update to point to the Git repository. The URL must end with `.git`,
e.g., `https://repo.example.com/repo.git`.
- **targetRevision**: Indicate which branch to monitor.
- **path**: Specify the path to the directory containing the `ClusterInstance` CRs.

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: clusters
  namespace: openshift-gitops
spec:
  destination:
    namespace: clusters-sub
    server: https://kubernetes.default.svc
  project: siteconfig-v2
  source:
    path: clusters
    repoURL: https://repo.example.com/repo.git
    targetRevision: main
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
```

## Advanced Topics

### Generate extra-manifests ConfigMap using kustomize

Assume you have the following directory structure, where the `extra-manifests` directory consists of individual
extra-manifest source files (such as `MachineConfigs`):

```sh
clusters/
 |- spoke1/
 |-- extra-manifests/
 |--- file1.yaml
 |--- ...
 |--- fileN.yaml
 |-- clusterinstance.yaml
 |-- ns.yaml
 |-- secrets.yaml
 |-- kustomization.yaml
 |- kustomization.yaml
```

You can use the `configMapGenerator` in `kustomize` to generate the extra-manifests `ConfigMap` as shown below.
The example is for `clusters/spoke1/kustomization.yaml`.

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

configMapGenerator:
- files:
  - extra-manifests/file1.yaml
  - ...
  - extra-manifests/fileN.yaml
  name: extra-manifests-cm
  namespace: spoke1

generatorOptions:
  disableNameSuffixHash: true

resources:
  - ns.yaml
  - secrets.yaml
  - clusterinstance.yaml
```

This expanded guide aims to provide clear and detailed instructions to help you successfully integrate a GitOps workflow
with the SiteConfig operator. If you have any questions or encounter issues, please refer to the respective
documentation links provided or reach out for support.

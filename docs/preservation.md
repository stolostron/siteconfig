# Safeguarding ConfigMaps and Secrets During Cluster Reinstalls with SiteConfig Operator

# Table of Contents

1. [Introduction](#safeguarding-configmaps-and-secrets-during-cluster-reinstalls-with-siteconfig-operator)
2. [Key Features](#key-features)
   - [Backup](#backup)
   - [Restore](#restore)
   - [Selective Resource Management](#selective-resource-management)
3. [Preservation Modes](#preservation-modes)
   - [None](#1-none)
   - [All](#2-all)
   - [ClusterIdentity](#3-clusteridentity)
4. [Backup Workflow](#backup-workflow)
   - [Backup ConfigMaps and Secrets](#backup-configmaps-and-secrets)
5. [Restoration Workflow](#restoration-workflow)
   - [Restore ConfigMaps and Secrets](#restore-configmaps-and-secrets)
     - [ConfigMaps](#configmaps)
     - [Secrets](#secrets)
6. [Example Usage: Preserving ConfigMap and Secret](#example-usage-preserving-configmap-and-secret)
   - [Original Resource: ConfigMap](#original-resource-configmap)
   - [Backed-Up Resource: ConfigMap](#backed-up-resource-configmap)
   - [Restored Resource: ConfigMap](#restored-resource-configmap)
   - [Original Resource: Secret](#original-resource-secret)
   - [Backed-Up Resource: Secret](#backed-up-resource-secret)
   - [Restored Resource: Secret](#restored-resource-secret)
7. [Key Notes](#key-notes)


The **preservation** feature within the **SiteConfig Operator** provides a robust mechanism to back up and restore
critical Kubernetes resources—**ConfigMaps** and **Secrets**—that reside in the same namespace as the
**ClusterInstance** Custom Resource (CR). This functionality ensures cluster data integrity during reinstallations
and recovery operations.

---

## Key Features

1. **Backup**: Creates preserved copies of ConfigMaps and Secrets, including their metadata, without altering the
   original resources.
2. **Restore**: Reconstructs preserved ConfigMaps and Secrets, restoring their original structure and metadata.
3. **Selective Resource Management**: Employs **Preservation Modes** to determine which resources are backed up and
   restored.

---

## Preservation Modes

The SiteConfig Operator allows users to control data preservation through three distinct modes:

### 1. **None**
- **Behavior**: No ConfigMaps or Secrets are backed up.
- **Usage**: Select this mode if data preservation is unnecessary for the cluster.

---

### 2. **All**
- **Behavior**:
  - All ConfigMaps and Secrets labeled with `siteconfig.open-cluster-management.io/preserve` (any value) in the same
    namespace as the ClusterInstance CR are backed up.
  - The original names of these resources are stored as keys in the **Data** object of the preserved copies. The
    corresponding values contain a JSON-encoded representation of the original data.

**Label Requirement**: Add the label `siteconfig.open-cluster-management.io/preserve` with any value.

**Example Label**:
```yaml
siteconfig.open-cluster-management.io/preserve: ""
```

---

### 3. **ClusterIdentity**
- **Behavior**:
  - Only ConfigMaps and Secrets labeled with `siteconfig.open-cluster-management.io/preserve: "cluster-identity"` in
    the same namespace as the ClusterInstance CR are backed up.
  - The original names of these resources are stored as keys in the **Data** object of the preserved copies. The
    corresponding values contain a JSON-encoded representation of the original data.

**Label Requirement**: Add the label `siteconfig.open-cluster-management.io/preserve` with the value `cluster-identity`.

**Example Label**:
```yaml
siteconfig.open-cluster-management.io/preserve: "cluster-identity"
```

---

## Backup Workflow

### Backup ConfigMaps and Secrets

- **Preserved Name**: Backup resources are created with the original name plus a **reinstallGeneration** suffix to
  distinguish them from the originals.
- **Metadata Preservation**:
  - **Annotations and Labels**: Original annotations and labels are copied, and an additional label is added to
    indicate preservation:
    - **Preserved Data Label**:
      - **Key**: `siteconfig.open-cluster-management.io/preserved-data`
      - **Value**: A timestamp marking the time of backup.
- **Data Storage**:
  - **ConfigMaps**: The **Data** object in the preserved ConfigMap includes the original resource name as a key, and
    the value is a JSON-encoded representation of the original data.
  - **Secrets**: Similarly, the **Data** object in the preserved Secret includes the original resource name as a key,
    with the value being a JSON-encoded representation of the original data. Additionally, the original **SecretType**
    is retained for compatibility during restores.

This design ensures that the original ConfigMaps and Secrets remain unaltered and ready for restoration when needed.

---

## Restoration Workflow

### Restore ConfigMaps and Secrets

Restored resources are reconstructed using their original names and metadata.

#### ConfigMaps
- **Name**: Extracted from the first key in the preserved **Data** object and set as the original resource name.
- **Metadata**:
  - **Namespace, Annotations, Labels**: Copied from the preserved ConfigMap.
  - **Preservation Data Label**: Removed to indicate the resource is no longer a backup.
- **Data**: Decoded from the preserved ConfigMap to restore its original structure.

#### Secrets
- **Name**: Extracted from the first key in the preserved **Data** object and set as the original resource name.
- **Metadata**:
  - **Namespace, Annotations, Labels**: Copied from the preserved Secret.
  - **Preservation Data Label**: Removed to indicate the resource is no longer a backup.
- **SecretType**: Retained to ensure compatibility with its intended usage.
- **Data**: Decoded from the preserved Secret to restore its original structure.

---

## Example Usage: Preserving ConfigMap and Secret

### Original Resource: ConfigMap
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: example-configmap
  namespace: default
  labels:
    siteconfig.open-cluster-management.io/preserve: ""
  annotations:
    example-annotation: "original-configmap"
data:
  key1: "value1"
  key2: "value2"
```

### Backed-Up Resource: ConfigMap
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: example-configmap-1234567890
  namespace: default
  labels:
    siteconfig.open-cluster-management.io/preserve: ""
    siteconfig.open-cluster-management.io/preserved-data: "2024-12-14T15:04:05Z"
  annotations:
    example-annotation: "original-configmap"
data:
  example-configmap: >
    {"key1":"value1","key2":"value2"}
```

### Restored Resource: ConfigMap
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: example-configmap
  namespace: default
  labels:
    siteconfig.open-cluster-management.io/preserve: ""
  annotations:
    example-annotation: "original-configmap"
data:
  key1: "value1"
  key2: "value2"
```

---

### Original Resource: Secret
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: example-secret
  namespace: default
  labels:
    siteconfig.open-cluster-management.io/preserve: "cluster-identity"
  annotations:
    example-annotation: "original-secret"
type: Opaque
data:
  username: dXNlcm5hbWU=  # "username" encoded in base64
  password: cGFzc3dvcmQ=  # "password" encoded in base64
```

### Backed-Up Resource: Secret
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: example-secret-1234567890
  namespace: default
  labels:
    siteconfig.open-cluster-management.io/preserve: "cluster-identity"
    siteconfig.open-cluster-management.io/preserved-data: "2024-12-14T15:04:05Z"
  annotations:
    example-annotation: "original-secret"
type: Opaque
data:
  example-secret: >
    {"username":"dXNlcm5hbWU=","password":"cGFzc3dvcmQ="}
```

### Restored Resource: Secret
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: example-secret
  namespace: default
  labels:
    siteconfig.open-cluster-management.io/preserve: "cluster-identity"
  annotations:
    example-annotation: "original-secret"
type: Opaque
data:
  username: dXNlcm5hbWU=  # "username" encoded in base64
  password: cGFzc3dvcmQ=  # "password" encoded in base64
```

---

### Key Notes:
1. **Backup Resource Naming**: The backup resources include a suffix (`-1234567890`), which is the `reinstallGeneration`.
2. **Preservation Data Label**: The backup resource contains a label `siteconfig.open-cluster-management.io/preserved-data`
   with a timestamp to mark when it was backed up.
3. **Restored Resource Metadata**: Restored resources remove the preservation data label and retain their original names
   and metadata (namespace, labels, annotations).
4. **Data Encoding**: The original resource name is stored as a key in the `data` object, and the corresponding value
   is a JSON-encoded representation of the original `data`.




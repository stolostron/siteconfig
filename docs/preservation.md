# Safeguarding ConfigMaps and Secrets During Cluster Reinstalls with SiteConfig Operator

## Table of Contents

1. [Introduction](#introduction)
2. [Key Features](#key-features)
   - [Backup](#backup)
   - [Restore](#restore)
   - [Selective Resource Management](#selective-resource-management)
3. [Preservation Modes](#preservation-modes)
   - [None](#none)
   - [All](#all)
   - [ClusterIdentity](#clusteridentity)
4. [Backup Workflow](#backup-workflow)
   - [Backup ConfigMaps and Secrets](#backup-configmaps-and-secrets)
5. [Restoration Workflow](#restoration-workflow)
   - [Restore ConfigMaps and Secrets](#restore-configmaps-and-secrets)
     - [ConfigMaps](#configmaps)
     - [Secrets](#secrets)
6. [Example Usage](#example-usage)
   - [Preserving a ConfigMap](#preserving-a-configmap)

---

## Introduction

The **preservation** feature within the **SiteConfig Operator** provides a mechanism to back up and restore important
Kubernetes resources such as **ConfigMaps** and **Secrets** that reside in the same namespace as the
**ClusterInstance** Custom Resource (CR). This functionality ensures cluster data integrity during reinstallations.

---

## Key Features

1. **Backup**: Creates preserved copies of ConfigMaps and Secrets, including their metadata.
2. **Restore**: Restores the preserved ConfigMaps and Secrets.
3. **Selective Resource Management**: Employs **Preservation Modes** to determine which resources are backed up and
   restored.

---

## Preservation Modes

The SiteConfig Operator allows users to control data preservation through three distinct modes:

### 1. **None**
- **Behavior**: No ConfigMaps or Secrets are preserved.
- **Usage**: Select this mode if data preservation is unnecessary for the cluster during reinstallation.

---

### 2. **All**
- **Behavior**:
  - All ConfigMaps and Secrets labeled with `siteconfig.open-cluster-management.io/preserve` (any value) in the same
    namespace as the ClusterInstance CR are backed up.
  - The original CRs of these resources are stored as immutable Kubernetes secrets.

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
  - The original CRs of these resources are stored as immutable Kubernetes secrets.

**Label Requirement**: Add the label `siteconfig.open-cluster-management.io/preserve` with the value `cluster-identity`.

**Example Label**:
```yaml
siteconfig.open-cluster-management.io/preserve: "cluster-identity"
```

---

## Backup Workflow

### Backup ConfigMaps and Secrets

The SiteConfig Operator ensures the preservation of ConfigMaps and Secrets by performing the following steps:
- **Sanitizing Metadata**: Information (such as creation timestamp, UID, and owner references) is removed from
  the resource metadata.
- **Serialization**: The resource's data is serialized into YAML format, ensuring that all relevant configuration
  information is captured.
- **Storage**: The serialized YAML data is stored in a newly created, immutable Kubernetes Secret, with appropriate labels
  and annotations for identification as a backup.

The backup data is encoded in base64 within the Secret, preserving both the original resource data and metadata in a
format that can be easily restored later.

---

## Restoration Workflow

### Restore ConfigMaps and Secrets

Restoring a previously backed-up resource involves the following steps:
- **Fetching the Backup**: The preserved resource(s) (stored via Kubernetes Secret) is retrieved and validated.
- **Deserialization**: The YAML data is deserialized into the corresponding resource type (ConfigMap or Secret).
- **Sanitizing Metadata**: As with backups, metadata such as UID and resource version is sanitized to ensure consistency.
- **Restoration**: The resource is either created or updated within the cluster to match the original state, with a
  `restoredAt` timestamp added for reference.

---

## Example Usage

### Preserving a ConfigMap

Given the following ClusterInstance definition:

```yaml
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: ClusterInstance
metadata:
  name: sample-ci
  namespace: default
spec:
  reinstall:
    generation: "1234"
    preservationMode: "All"
```

#### Original ConfigMap Resource

This is the original resource that will be preserved:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: example-1
  namespace: default
  labels:
    siteconfig.open-cluster-management.io/preserve: ""
  annotations:
    example-annotation: "original-configmap"
data:
  key1: "value1"
  key2: "value2"
```


#### Example Preserved ConfigMap Resource

The preserved resource, stored as an immutable Kubernetes Secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: ConfigMap-example-1-1234
  namespace: default
  labels:
    siteconfig.open-cluster-management.io/owned-by: "default-sample-ci"
    siteconfig.open-cluster-management.io/preserved-data: "2025-01-01T10:00:00Z"
  annotations:
    siteconfig.open-cluster-management.io/reinstall.generation: "1234"
    siteconfig.open-cluster-management.io/preservedResourceType: "ConfigMap"
    siteconfig.open-cluster-management.io/preserve: "All"
type: Opaque
immutable: true
data:
  original-resource: YXBpVmVyc2lvbjogdjEKa2luZDogQ29uZmlnTWFwCm1ldGFkYXRhOgogIG5hbWU6IGV4YW1wbGUtMQogIG5hbWVzcGFjZTogZGVmYXVsdAogIGxhYmVsczoKICAgIHNpdGVjb25maWcub3Blbi1jbHVzdGVyLW1hbmFnZW1lbnQuaW8vcHJlc2VydmU6ICIiCiAgYW5ub3RhdGlvbnM6CiAgICBleGFtcGxlLWFubm90YXRpb246ICJvcmlnaW5hbC1jb25maWdtYXAiCmRhdGE6CiAga2V5MTogInZhbHVlMSIKICBrZXkyOiAidmFsdWUyIg==
```

#### Example Restored ConfigMap Resource

Once the resource is restored, it will appear as follows, with a new annotation indicating the restoration time:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: example-1
  namespace: default
  labels:
    siteconfig.open-cluster-management.io/preserve: ""
  annotations:
    example-annotation: "original-configmap"
    siteconfig.open-cluster-management.io/preserve.restoredAt: "2025-01-01T11:00:00Z"
data:
  key1: "value1"
  key2: "value2"
```

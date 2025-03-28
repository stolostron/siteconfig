# Cluster Reinstallation Using the SiteConfig operator

<details>
  <summary>Table of Contents</summary>

1. [Overview](#overview)  
2. [Cluster Reinstallation Workflow](#cluster-reinstallation-workflow)  
3. [Enabling Cluster Reinstallation in the SiteConfig Operator](#enabling-cluster-reinstallation-in-the-siteconfig-operator)  
   - [Checking the Current Configuration](#checking-the-current-configuration)  
   - [Enabling Cluster Reinstallation](#enabling-cluster-reinstallation)  
4. [Labeling Resources for Preservation](#labeling-resources-for-preservation)  
5. [Initiating a Cluster Reinstallation](#initiating-a-cluster-reinstallation)  
   - [Updating the `ClusterInstance` Resource](#updating-the-clusterinstance-resource)  
   - [Applying the Changes](#applying-the-changes)  
6. [Monitoring the Cluster Reinstallation](#monitoring-the-cluster-reinstallation)  
   - [Additional Reinstallation Tracking Information](#additional-reinstallation-tracking-information)  
7. [Advanced Topics](#advanced-topics)  
   - [Image-Based Break/Fix (IBBF)](#image-based-breakfix-ibb)

</details>

## Overview
The SiteConfig operator simplifies OpenShift cluster reinstallation through the `ClusterInstance` API while preserving critical configuration data.

With a GitOps-compatible, declarative approach, users can trigger reinstallations by updating the `ClusterInstance` resource. The operator also includes a backup and restore mechanism for `Secret` and `ConfigMap` resources, ensuring essential cluster data, such as authentication credentials and configuration resources, remains intact.

### Cluster Identity Preservation
Cluster reinstallation supports both Ssingle-node OpenShift (SNO) and multi-node OpenShift (MNO) clusters. However, cluster identity preservation is only supported for Single Node OpenShift clusters installed using the Image Based Install (IBI) provisioning method.

## Cluster Reinstallation Workflow  
To perform a cluster reinstallation, complete the following steps:

1. **Enable the SiteConfig operator reinstallation service**: If not already enabled, update the `siteconfig-operator-configuration` `ConfigMap` resource.
2. **Label resources for preservation**.
3. **Update the `ClusterInstance` resource**: 
   - Set the `spec.reinstall.preservationMode` field.
   - Trigger reinstallation by assigning a new, unique value to `spec.reinstall.generation`. 
   - Apply any other necessary updates, as outlined [here](#updating-the-clusterinstance-resource).
4. **Apply the changes** to initiate the reinstallation process.
5. **Monitor the reinstallation progress**:  
   - **Phase 1: Reinstallation request handling** – Monitor the reinstallation request handling processes through the `status.reinstall.conditions` field in the `ClusterInstance` CR:  
     - **Request validation**: The SiteConfig operator validates the reinstallation request.
     - **Data preservation**: The operator backs up labeled data.
     - **Cleanup**: The operator removes existing installation manifests. If this step fails within a specified time, deletion times out, and reinstallation stops.
     - **Data restoration**: The operator restores the preserved data.      
   - **Phase 2: Cluster provisioning** – Monitor the cluster provisioning stages through the `status.conditions` field in the `ClusterInstance` CR:  
     - **Manifest regeneration**: The operator generates new installation manifests from the referenced templates.  
     - **Cluster installation**: The operators consuming the installation manifests provision the cluster using the newly generated manifests.
6. **Completion verification**: Ensure the cluster is successfully provisioned and verify its availability.  


## Enabling Cluster Reinstallation in the SiteConfig Operator

Cluster reinstallation must be explicitly enabled in the `siteconfig-operator-configuration` ConfigMap.

### Checking the Current Configuration  
By default, reinstallation is disabled. Verify the configuration:

```yaml
apiVersion: v1  
kind: `ConfigMap`  
metadata:  
  name: siteconfig-operator-configuration  
  namespace: <namespace>  
data:  
  allowsReinstalls: false  
  ...  
```

- `<namespace>`: The namespace where the SiteConfig operator is installed.  
- `allowsReinstalls`: Set to `true` enables reinstallation. 

The operator continuously monitors `siteconfig-operator-configuration` `ConfigMap` for changes.

### Enabling Cluster Reinstallation  
Update the `ConfigMap` by running the following command:

```sh
NAMESPACE=<namespace>  
oc patch configmap siteconfig-operator-configuration \  
  -n $NAMESPACE \  
  --type=json \  
  -p '[{"op": "replace", "path": "/data/allowsReinstalls", "value": "true"}]'  
```

Verify the update by running the following command:

```sh
oc get configmap siteconfig-operator-configuration -n $NAMESPACE -o yaml  
```


## Labeling Resources for Preservation
To retain essential cluster configuration data after reinstallation, the SiteConfig operator provides a backup and restore mechanism for `ConfigMap` and `Secret` resources within the `ClusterInstance` namespace.

To preserve specific resources, apply the appropriate preservation label as described in the [preservation document](#preservation.md). The SiteConfig operator backs up resources based on the following labels:

- `siteconfig.open-cluster-management.io/preserve: "<arbitrary-value>"`: Backs up both non-identity-related and cluster identity-related resources.
- `siteconfig.open-cluster-management.io/preserve: "cluster-identity"`: Backs up cluster identity-related resources.

To label a resource for preservation, run the following command:

```sh
oc label configmap <your_configmap> "siteconfig.open-cluster-management.io/preserve="
```

The `ClusterInstance` CR is updated with the correct `preservationMode` corresponding to the applied labels. The following preservation modes are supported:

- `None`: No data is backed up.
- `All`: Backs up all Secrets and ConfigMaps that have the label `siteconfig.open-cluster-management.io/preserve`.
- `ClusterIdentity`: Backs up only Secrets and ConfigMaps labeled with `siteconfig.open-cluster-management.io/preserve: "cluster-identity"`.  


## Initiating a Cluster Reinstallation

To start the reinstallation, update the `ClusterInstance` resource with a new `spec.reinstall.generation` value.

### Updating the `ClusterInstance` Resource  

Modify the `spec.reinstall.generation` value in the `ClusterInstance` resource:

```yaml
apiVersion: siteconfig.open-cluster-management.io/v1alpha1  
kind: `ClusterInstance`  
metadata:  
  name: clusterinstance-example  
  namespace: some-namespace  
spec:  
  reinstall:  
    generation: "unique-generation-string"  
    preservationMode: "None | All | ClusterIdentity"  
```

When defining the `spec.reinstall` object, you can update the following fields:

- `spec.extraAnnotations`
- `spec.extraLabels`
- `spec.suppressedManifests`
- `spec.pruneManifests`
- `spec.nodes/<node-index>/extraAnnotations`
- `spec.nodes/<node-index>/extraLabels`
- `spec.nodes/<node-index>/suppressedManifests`
- `spec.nodes/<node-index>/pruneManifests`

:information_source: `<node-index>` represents the index of the `NodeSpec` object.


### Applying the Changes  
Use one of the following methods:

- **GitOps Approach**: Commit and push the updated `ClusterInstance` resource to your Git repository.
- **Manual Application**: Apply the changes directly on the hub cluster:

  ```sh
  oc apply -f clusterinstance-example.yaml  
  ```  

## Monitoring the Cluster Reinstallation

The SiteConfig operator provides several status conditions to track the cluster reinstallation progress. These conditions offer insights into various stages of the process:

- `ReinstallRequestProcessed` – Indicates the overall status of the reinstallation request.
- `ReinstallRequestValidated` – Confirms the validity of the reinstallation request.
- `ReinstallPreservationDataBackedUp` – Tracks the backup status of preserved data.
- `ReinstallClusterIdentityDataDetected` – Determines whether cluster identity data is available for preservation.
- `ReinstallRenderedManifestsDeleted` – Monitors the deletion of rendered manifests associated with the `ClusterInstance` resource.
- `ReinstallPreservationDataRestored` – Tracks the restoration status of preserved data.

For more details on reinstallation conditions, refer to the [status conditions documentation](status_conditions.md).

### Additional Reinstallation Tracking Information

The`ClusterInstance.status.reinstall.reinstallStatus` field provides further information about the reinstallation process with the following fields:

- `InProgressGeneration` – Identifies the active generation being processed for reinstallation.
- `ObservedGeneration` – Indicates the last successfully processed reinstallation request.
- `RequestStartTime` – Timestamp marking when the reinstallation request was initiated.
- `RequestEndTime` – Timestamp marking when the reinstallation process was completed.
- `History` – A record of past reinstallation attempts, including generation details, timestamps, and specification changes to the `ClusterInstance` resource.


### Verifying the Cluster Reinstallation

1. Verify that the reinstallation request is being processed:  

    ```sh
    oc get clusterinstance clusterinstance-example -n some-namespace -o json | jq -r '.status.reinstall.conditions[] | select(.type=="ReinstallRequestProcessed")'
    {
    "type": "ReinstallRequestProcessed"
    "reason": "InProgress",
    "status": "False",
    ...
    }
    ```

2. Verify that the cluster reinstallation request is validated successfully:

    ```sh
    oc get clusterinstance clusterinstance-example -n some-namespace -o json | jq -r '.status.reinstall.conditions[] | select(.type=="ReinstallRequestValidated")'
    {
    "type": "ReinstallRequestValidated"
    "reason": "Completed",
    "status": "True",
    ...
    }
    ```

3. Check if the cluster identity data is preserved when the `spec.reinstall.preservationMode` field is set to `All` or `ClusterIdentity`:

    ```sh
    oc get clusterinstance clusterinstance-example -n some-namespace -o json | jq -r '.status.reinstall.conditions[] | select(.type=="ReinstallPreservationDataBackedup")'
    {
    "type": "ReinstallPreservationDataBackedup"
    "reason": "Completed",
    "status": "True",
    ...
    }
    ```

4. Optional. Verify that the cluster identity data are detected when the `spec.reinstall.preservationMode` field is set to `All` or `ClusterIdentity`:
   
    ```sh
    oc get clusterinstance clusterinstance-example -n some-namespace -o json | jq -r '.status.reinstall.conditions[] | select(.type=="ReinstallClusterIdentityDataDetected")'
    {
    "type": "ReinstallClusterIdentityDataDetected"
    "reason": "DataAvailable",
    "status": "True",
    ...
    }
    ```


5. Verify that the installation manifests are deleted. This can take several minutes to complete.

    ```sh
    oc get clusterinstance clusterinstance-example -n some-namespace -o json | jq -r '.status.reinstall.conditions[] | select(.type=="ReinstallRenderedManifestsDeleted")'
    {
    "type": "ReinstallRenderedManifestsDeleted"
    "reason": "Completed",
    "status": "True",
    ...
    }
    ```

6. If the `preseservationMode` field is set to `All` or `ClusterIdentity`, verify that the data preserved earlier are restored.

    ```sh
    oc get clusterinstance clusterinstance-example -n some-namespace -o json | jq -r '.status.reinstall.conditions[] | select(.type=="ReinstallPreservationDataRestored")'
    {
    "type": "ReinstallPreservationDataRestored"
    "reason": "Completed",
    "status": "True",
    ...
    }
    ```

7. After you verified the previous steps, ensure that the reinstallation request completes successfully.

    ```sh
    oc get clusterinstance clusterinstance-example -n some-namespace -o json | jq -r '.status.reinstall.conditions[] | select(.type=="ReinstallRequestProcessed")'
    {
    "type": "ReinstallRequestProcessed"
    "reason": "Completed",
    "status": "True",
    ...
    }
    ```

## Advanced Topics

### Image-Based Break/Fix (IBBF)

The Image-Based Break/Fix (IBBF) feature simplifies single-node OpenShift (SNO) hardware replacement by minimizing downtime while preserving the cluster’s original identity. The IBBF feature retains critical cluster details, including identifiers, cryptographic keys, such as `kubeconfig`, and authentication credentials, enabling the replacement node to seamlessly assume the identity of the failed hardware.

For more details, see [Image-Based Break/Fix](image_based_break_fix.md).

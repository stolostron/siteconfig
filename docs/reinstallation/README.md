# Cluster reinstallation using the SiteConfig operator

<details>
  <summary>Table of Contents</summary>

1. [Overview](#overview)
   - [Cluster identity preservation](#cluster-identity-preservation)
2. [Cluster reinstallation workflow](#cluster-reinstallation-workflow)  
3. [Enabling cluster reinstallation in the SiteConfig operator](#1-enabling-cluster-reinstallation-in-the-siteconfig-operator)  
   - [Checking the current configuration](#checking-the-current-configuration)  
   - [Enabling cluster reinstallation](#enabling-cluster-reinstallation)  
4. [Labeling resources for preservation](#2-labeling-resources-for-preservation)  
5. [Initiating a cluster reinstallation](#3-initiating-a-cluster-reinstallation)  
   - [Updating the ClusterInstance resource](#updating-the-clusterinstance-resource)  
   - [Applying the changes](#applying-the-changes)  
6. [Optional. Monitoring the cluster reinstallation](#4-optional-monitoring-the-cluster-reinstallation)
7. [Verify cluster availability](#5-verify-cluster-availability)
8. [Advanced topics](#advanced-topics)  
   - [Image-Based Break/Fix](#image-based-breakfix-for-single-node-openshift-hardware-replacement)

</details>

## Overview
The SiteConfig operator simplifies OpenShift cluster reinstallation through the `ClusterInstance` API while preserving critical configuration data.

With a GitOps-compatible, declarative approach, users can trigger reinstallations by updating the `ClusterInstance` resource. The operator also includes a backup and restore mechanism for hub-side `Secret` and `ConfigMap` resources, ensuring essential cluster data, such as authentication credentials and configuration resources, remains intact.

### Cluster identity preservation
Cluster reinstallation supports both single-node OpenShift and multi-node OpenShift clusters. However, cluster identity preservation is only supported for single-node OpenShift clusters installed using the Image Based Install provisioning method.

## Cluster reinstallation workflow  
To perform a cluster reinstallation, complete the following steps:

1. [Enable the SiteConfig operator reinstallation service.](#1-enabling-cluster-reinstallation-in-the-siteconfig-operator)
2. [Optional. Label resources for preservation.](#2-labeling-resources-for-preservation)
3. [Initiate a cluster reinstallation.](#3-initiating-a-cluster-reinstallation)
4. [Optional. Monitor the reinstallation progress.](#4-optional-monitoring-the-cluster-reinstallation)
5. [Verify that the cluster is provisioned and available.](#5-verify-cluster-availability)

You can complete step 1. and 2. well before initiating the cluster reinstallation.

### 1. Enabling cluster reinstallation in the SiteConfig operator

You must explicitly enable cluster reinstallation in the `siteconfig-operator-configuration` `ConfigMap` resource, which you can complete well before initiating the process.

#### Checking the current configuration  
By default, reinstallation is disabled. You can check the current configuration by completing the following steps:

Create the `NAMESPACE` environment variable for the `siteconfig-operator-configuration` `ConfigMap` resource. The namespace must match the namespace where the SiteConfig operator is installed. Run the following command:

```sh
NAMESPACE=<namespace>
```

Verify the current configuration by running the following command:

```sh
oc get configmap siteconfig-operator-configuration -n $NAMESPACE -o yaml
```

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

The operator continuously monitors `siteconfig-operator-configuration` `ConfigMap` resource for changes.

#### Enabling cluster reinstallation

Update the `ConfigMap` resource by running the following command:

```sh
oc patch configmap siteconfig-operator-configuration \  
  -n $NAMESPACE \  
  --type=json \  
  -p '[{"op": "replace", "path": "/data/allowsReinstalls", "value": "true"}]'  
```

Verify the update by running the following command:

```sh
oc get configmap siteconfig-operator-configuration -n $NAMESPACE -o yaml  
```


### 2. Optional. Labeling resources for preservation

If you want to retain essential cluster configuration data after reinstallation, the SiteConfig operator provides a backup and restore mechanism for hub-side `Secret` and `ConfigMap` resources within the `ClusterInstance` namespace.

To preserve specific resources, apply the appropriate preservation label as described in the [preservation document](#preservation.md). You can label your resources well before initiating the reinstallation.

The SiteConfig operator backs up resources based on the following labels:

- `siteconfig.open-cluster-management.io/preserve: "<arbitrary-value>"`: Indicates to the operator to back up resources with the label only when the preservation mode is set to `All`. If no resources are labeled, the operator proceeds with the reinstallation without backing up any resources.
- `siteconfig.open-cluster-management.io/preserve: "cluster-identity"`: Indicates to the operator to back up resources with the label when the preservation mode is set to `All` or `ClusterIdentity`. If the preservation mode is set to `ClusterIdentity` and the operator does not find at least one resource with the `siteconfig.open-cluster-management.io/preserve: "cluster-identity"` label, the reinstallation stops.

To label a resource for preservation, run the following command:

```sh
oc label configmap <your_configmap> "siteconfig.open-cluster-management.io/preserve="
```

The `ClusterInstance` custom resource is updated with the correct `preservationMode` corresponding to the applied labels. The following preservation modes are supported:

- `None`: No data is backed up.
- `All`: Backs up all `Secret` and `ConfigMap` resources that have the `siteconfig.open-cluster-management.io/preserve` label. If no resources are labeled, the operator proceeds with the reinstallation without backing up any resources.
- `ClusterIdentity`: Backs up only `Secret` and `ConfigMap` resources labeled with `siteconfig.open-cluster-management.io/preserve: "cluster-identity"`. If no resources are labeled, the operator stops the reinstallation and displays an error message.

For more information, see [Preservation Modes](preservation.md#preservation-modes).

### 3. Initiating a cluster reinstallation

To start the reinstallation, update the `ClusterInstance` resource with a new `spec.reinstall.generation` value.

#### Updating the ClusterInstance resource  

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
    preservationMode: "<your-preservation-mode>"  
```

:information_source: Ensure you set the appropriate preservation mode when you modify the `ClusterInstance` resource. `"None"`, `"All"`, or `"ClusterIdentity"` are valid values.


When defining the `spec.reinstall` object, you can modify the following additional fields in the `ClusterInstance` resource:

- `spec.extraAnnotations`
- `spec.extraLabels`
- `spec.suppressedManifests`
- `spec.pruneManifests`
- `spec.nodes/<node-id>/extraAnnotations`
- `spec.nodes/<node-id>/extraLabels`
- `spec.nodes/<node-id>/suppressedManifests`
- `spec.nodes/<node-id>/pruneManifests`
- `spec.nodes/<node-id>/bmcAddress`
- `spec.nodes/<node-id>/bootMACAddress`
- `spec.nodes/<node-id>/nodeNetwork/interfaces/macAddress`
- `spec.nodes/<node-id>/rootDeviceHints`

:information_source: `<node-id>` represents the updated `NodeSpec` object.


#### Applying the changes  
Use one of the following methods:

- **GitOps Approach**: Commit and push the updated `ClusterInstance` resource to your Git repository.
- **Manual Application**: Apply the changes directly on the hub cluster:

  ```sh
  oc apply -f clusterinstance-example.yaml  
  ```  

### 4. Optional. Monitoring the cluster reinstallation

The reinstallation has two phases:

- **Phase 1**: Reinstallation request handling
  - **Request validation**: The SiteConfig operator validates the request.
  - **Data preservation**: The SiteConfig operator backs up labeled resources.
  - **Cleanup**: The SiteConfig operator removes existing installation manifests. If this step times out, the reinstallation stops and the `ClusterInstance` resource is paused.
  - **Data restoration**: The SiteConfig operator restores preserved data.

- **Phase 2**: Cluster provisioning
  - **Manifest regeneration**: The SiteConfig operator generates new manifests from templates.
  - **Cluster installation**: The cluster is provisioned using the new manifests.

You can track progress in the `status.reinstall.conditions` and `status.conditions` fields for phase 1 and 2, respectively. To track the cluster reinstallation progress, the SiteConfig operator provides the following status conditions:

- `ReinstallRequestProcessed` – Indicates the overall status of the reinstallation request.
- `ReinstallRequestValidated` – Confirms the validity of the reinstallation request.
- `ReinstallPreservationDataBackedUp` – Tracks the backup status of preserved data.
- `ReinstallClusterIdentityDataDetected` – Determines whether cluster identity data is available for preservation.
- `ReinstallRenderedManifestsDeleted` – Monitors the deletion of rendered manifests associated with the `ClusterInstance` resource.
- `ReinstallPreservationDataRestored` – Tracks the restoration status of preserved data.

The`status.reinstall` field provides further information about the reinstallation process with the following fields:

- `InProgressGeneration` – Identifies the active generation being processed for reinstallation.
- `ObservedGeneration` – Indicates the last successfully processed reinstallation request.
- `RequestStartTime` – Indicates the time when the reinstallation request was initiated.
- `RequestEndTime` – Indicates the time when the reinstallation process was completed.
- `History` – Displays past reinstallation attempts, including generation details, timestamps, and specification changes to the `ClusterInstance` resource.

For more information about the reinstallation conditions, see [Cluster reinstallation status conditions](status_conditions.md).

To monitor the cluster reinstallation progress, complete the following steps:

1. Verify that the reinstallation request is being processed:  

    ```sh
    oc get clusterinstance clusterinstance-example -n some-namespace -o json | jq -r '.status.reinstall.conditions[] | select(.type=="ReinstallRequestProcessed")'
    ```

    Example output:
    ```sh
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
    ```

    Example output:
    ```sh
    {
    "type": "ReinstallRequestValidated"
    "reason": "Completed",
    "status": "True",
    ...
    }
    ```

3. Optional. If you set the `spec.reinstall.preservationMode` field to `All` or `ClusterIdentity`, verify that the cluster identity data is preserved:

    ```sh
    oc get clusterinstance clusterinstance-example -n some-namespace -o json | jq -r '.status.reinstall.conditions[] | select(.type=="ReinstallPreservationDataBackedup")'
    ```

    Example output:
    ```sh
    {
    "type": "ReinstallPreservationDataBackedup"
    "reason": "Completed",
    "status": "True",
    ...
    }
    ```

4. Optional. If you set the `spec.reinstall.preservationMode` field to `All` or `ClusterIdentity`, verify that the cluster identity data is detected:
   
    ```sh
    oc get clusterinstance clusterinstance-example -n some-namespace -o json | jq -r '.status.reinstall.conditions[] | select(.type=="ReinstallClusterIdentityDataDetected")'
    ```

    Example output:
    ```sh
    {
    "type": "ReinstallClusterIdentityDataDetected"
    "reason": "DataAvailable",
    "status": "True",
    ...
    }
    ```


5. Verify that the installation manifests are deleted. The deletion can take several minutes to complete.

    ```sh
    oc get clusterinstance clusterinstance-example -n some-namespace -o json | jq -r '.status.reinstall.conditions[] | select(.type=="ReinstallRenderedManifestsDeleted")'
    ```

    Example output:
    ```sh
    {
    "type": "ReinstallRenderedManifestsDeleted"
    "reason": "Completed",
    "status": "True",
    ...
    }
    ```

6. Optional. If you set the `preseservationMode` field to `All` or `ClusterIdentity`, verify that the data preserved earlier are restored:

    ```sh
    oc get clusterinstance clusterinstance-example -n some-namespace -o json | jq -r '.status.reinstall.conditions[] | select(.type=="ReinstallPreservationDataRestored")'
    ```

    Example output:
    ```sh
    {
    "type": "ReinstallPreservationDataRestored"
    "reason": "Completed",
    "status": "True",
    ...
    }
    ```

7. After you verified the previous steps, ensure that the reinstallation request completed successfully:

    ```sh
    oc get clusterinstance clusterinstance-example -n some-namespace -o json | jq -r '.status.reinstall.conditions[] | select(.type=="ReinstallRequestProcessed")'
    ```

    Example output:
    ```sh
    {
    "type": "ReinstallRequestProcessed"
    "reason": "Completed",
    "status": "True",
    ...
    }
    ```

### 5. Verify cluster availability

Confirm that the cluster is successfully reinstalled and is operational by running an `oc` command with the `kubeconfig` file that is associated with the reinstalled cluster.

## Advanced topics

### Image-Based Break/Fix for single-node OpenShift hardware replacement

The Image-Based Break/Fix feature utilizes the cluster reinstallation mechanism of the SiteConfig operator to simplify single-node OpenShift hardware replacement. The feature minimizes downtime by preserving the original identity of the cluster. The Image-Based Break/Fix feature retains critical cluster details, including identifiers, cryptographic keys, such as `kubeconfig`, and authentication credentials, which enables the replacement node to seamlessly assume the identity of the failed hardware.

Designed for like-for-like hardware replacements in single-node OpenShift clusters installed using the Image Based Install method, Image-Based Break/Fix introduces a GitOps-compatible, declarative API. Users can initiate hardware replacement with a single Git commit. Powered by the SiteConfig operator and Image Based Install Operator, Image-Based Break/Fix enables cluster redeployment using the existing `ClusterInstance` custom resource.

With Image-Based Break/Fix, OpenShift users gain a resilient, automated, and GitOps-native solution for quickly restoring single-node OpenShift clusters after hardware failures.

#### Requirements

To reinstall an OpenShift cluster using the SiteConfig operator, ensure the following conditions are met:
- The cluster is a singe-node OpenShift cluster installed using the Image Based Install provisioning method.
- The faulty hardware is replaced with a new node with identical specifications.

#### Image-Based Break/Fix cluster reinstallation workflow

The Image-Based Break/Fix workflow is similar to the cluster reinstallation workflow with certain differences. 
To familiarize yourself with the differences in the workflows, see the high-level overview of the Image-Based Break/Fix cluster reinstallation workflow:

1. [Enable the SiteConfig operator reinstallation service.](#1-enabling-cluster-reinstallation-in-the-siteconfig-operator)
2. [Initiate a cluster reinstallation.](#3-initiating-a-cluster-reinstallation)
   - Set `spec.reinstall.preservationMode: "ClusterIdentity"`.
   - Update the `spec.nodes` object with the changed hardware information.
   - :information_source: Note that the Image Based Install Operator automatically labels cluster identity resources.
3. [Optional. Monitor the reinstallation progress.](#4-optional-monitoring-the-cluster-reinstallation)
   - During cluster provisioning, the operators consuming the installation manifests provision the cluster using the newly generated manifests.
     :information_source: Note that the [Image Based Install Operator](https://github.com/openshift/image-based-install-operator) detects the preserved cluster identity data and incorporates it into the configuration ISO image.
4. [Verify that the cluster is provisioned and available.](#5-verify-cluster-availability)
   - Use the `kubeconfig` that is associated with the failed hardware to access the reinstalled spoke cluster.

#### Initiating the Image-Based Break/Fix cluster reinstallation

To initiate the Image-Based Break/Fix cluster reinstallation, update the `ClusterInstance` resource by setting the `spec.reinstall.generation` field and updating the `spec.nodes` object with the changed hardware information.

Modify the `spec.reinstall.generation` field and update the `spec.nodes` object in the `ClusterInstance` resource with the new node details:

```yaml
apiVersion: siteconfig.open-cluster-management.io/v1alpha1  
kind: `ClusterInstance`  
metadata:  
  name: clusterinstance-example  
  namespace: some-namespace  
spec:
  ...
  reinstall:  
    generation: "unique-generation-string"  
    preservationMode: "ClusterIdentity"  
  nodes:
    - bmcAddress: <new-node-bmcAddress> 
      bootMACAddress: <new-node-bootMACAddress>
      rootDeviceHints: <new-node-rootDeviceHints>
      nodeNetwork:
        interfaces:
          macAddress: <new-node-macAddress>
          ...          
      ...      
```
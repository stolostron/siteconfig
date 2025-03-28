# Image-Based Break/Fix (IBBF) for SNO Hardware Replacement

<details>
  <summary>Table of Contents</summary>

1. [Overview](#overview)  
2. [Requirements](#requirements)  
3. [IBBF Cluster Reinstallation Workflow](#ibbf-cluster-reinstallation-workflow)  
4. [Enabling Cluster Reinstallation in the SiteConfig Operator](#enabling-cluster-reinstallation-in-the-siteconfig-operator)  
5. [Initiating the IBBF Cluster Reinstallation](#initiating-the-ibbf-cluster-reinstallation)  
6. [Monitoring the Cluster Reinstallation](#monitoring-the-cluster-reinstallation)  

</details>

## Overview

The Image-Based Break/Fix (IBBF) feature simplifies single-node OpenShift (SNO) hardware replacement by minimizing downtime while preserving the cluster’s original identity. The IBBF feature retains critical cluster details, including identifiers, cryptographic keys, such as `kubeconfig`, and authentication credentials, enabling the replacement node to seamlessly assume the identity of the failed hardware.

Designed for like-for-like hardware replacements in SNO clusters installed using the Image Based Install (IBI) method, IBBF introduces a GitOps-compatible, declarative API. Users can initiate hardware replacement with a single Git commit. Powered by the SiteConfig operator and IBI Operator, IBBF enables cluster redeployment using the existing `ClusterInstance` custom resource (CR).

With IBBF, OpenShift users gain a resilient, automated, and GitOps-native solution for quickly restoring SNO clusters after hardware failures.

## Requirements

To reinstall an OpenShift cluster using the SiteConfig operator, ensure the following conditions are met:
- The cluster is a singe-node OpenShift (SNO) cluster installed using the Image Based Install (IBI) provisioning method.
- The faulty hardware is replaced with a new node with identical specifications.

## IBBF Cluster Reinstallation Workflow

After replacing the faulty hardware, complete the following steps to reinstall the cluster:

1. **Enable the SiteConfig operator reinstallation service**: If not already enabled, update the `siteconfig-operator-configuration` `ConfigMap`.  
2. **Update the `ClusterInstance` resource**:  
   - Set `spec.reinstall.preservationMode: ClusterIdentity`.  
   - Assign a new, unique value to `spec.reinstall.generation`.  
   - Update the `spec.nodes` object with the changed hardware information.  
3. **Apply the changes** to initiate the reinstallation process.  
4. **Monitor the reinstallation progress**:  
   - **Phase 1: Reinstallation request handling** – Monitor the reinstallation request handling processes through the `status.reinstall.conditions` field in the `ClusterInstance` CR:  
     - **Request validation**: The SiteConfig operator validates the reinstallation request.  
     - **Data preservation**: The operator backs up labeled data.  
     - **Cleanup**: The operator removes existing installation manifests. If this step fails within a specified time, deletion times out, and reinstallation stops.  
     - **Data restoration**: The operator restores the preserved data.  
   - **Phase 2: Cluster provisioning** – Monitor the cluster provisioning processes through the `status.conditions` field in the `ClusterInstance` CR:  
     - **Manifest regeneration**: The SiteConfig operator generates new installation manifests from the referenced templates.  
     - **Cluster installation**: The operators consuming the installation manifests provision the cluster using the newly generated manifests. 
     :information_source: Note that the [Image-Based Install Operator (IBIO)](https://github.com/openshift/image-based-install-operator) detects the preserved cluster identity data and incorporates it into the configuration ISO image.  
5. **Completion verification**: Ensure the cluster is successfully provisioned and verify its availability. Use the previous `kubeconfig` to access the reinstalled spoke cluster.  


## Enabling Cluster Reinstallation in the SiteConfig Operator

To enable cluster reinstallation, update the `siteconfig-operator-configuration` `ConfigMap` resource by running the following command:

```sh
oc patch configmap siteconfig-operator-configuration \  
  -n $NAMESPACE \  
  --type=json \  
  -p '[{"op": "replace", "path": "/data/allowReinstalls", "value": "true"}]'
```

Verify that the `ConfigMap` resource is updated by running the following command:

```sh
oc get configmap siteconfig-operator-configuration -n $NAMESPACE -o yaml  
```

## Initiating the IBBF Cluster Reinstallation

To initiate the IBBF cluster reinstallation, update the `ClusterInstance` resource by setting the `spec.reinstall.generation` field and updating the `spec.nodes` object with the changed hardware information.

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

## Monitoring the Cluster Reinstallation

Follow the monitoring steps outlined in [Monitoring the Cluster Reinstallation](README.md#monitoring-the-cluster-reinstallation).

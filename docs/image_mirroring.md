# Mirroring images for disconnected environments

You can deploy a cluster with the SiteConfig operator by using the Image Based Install Operator as your underlying operator. If you deploy your clusters with the Image Based Install Operator in a disconnected environment, you must supply your mirror images as extra manifests in the `ClusterInstance` custom resource.
Complete the following steps:

Create a YAML file, named `idms-configmap.yaml`, for your `ImageDigestMirrorSet` object that contains your mirror registry locations:

```yaml
---
kind: ConfigMap
apiVersion: v1
metadata:
  name: "idms-configmap"
  namespace: "example-sno"
data:
  99-example-idms.yaml: |
    apiVersion: config.openshift.io/v1
    kind: ImageDigestMirrorSet
    metadata:
      name: example-idms
    spec:
      imageDigestMirrors:
      - mirrors:
        - mirror.registry.example.com/image-repo/image
        source: registry.example.com/image-repo/image
```
:information_source: The `ConfigMap` resource that contains the extra manifest must be defined in the same namespace as the `ClusterInstance` resource.

Create the resource by running the following command on the hub cluster:

```bash
oc apply -f idms-configmap.yaml
```

Reference your `ImageDigestMirrorSet` object in the `ClusterInstance` custom resource:

```yaml
apiVersion: siteconfig.open-cluster-management.io/v1alpha1
kind: ClusterInstance
metadata:
  name: "example-sno"
  namespace: "example-sno"
spec:
  ...
  extraManifestsRefs:
    - name: idms-configmap
...
```
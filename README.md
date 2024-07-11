# siteconfig-controller
Provide site deployment to Single and Multi Node OpenShift clusters to complete installation

## Description
The siteconfig-controller enables users to deploy clusters using either the Assisted Installer or Image Based Installer flows through the ClusterInstance API.

## Makefile targets

To see all `make` targets, run `make help` for more information on all potential `make` targets.


## GO formatting

GO has automated formatting. To update code and ensure it is formatted properly, run: `make fmt`

## Getting Started
You’ll need a Kubernetes cluster to run against. You can use [KIND](https://sigs.k8s.io/kind) to get a local cluster for testing, or run against a remote cluster.
**Note:** Your controller will automatically use the current context in your kubeconfig file (i.e. whatever cluster `oc cluster-info` shows).

### Building and deploying image

There are make variables you can set when building the image to customize how it is built and tagged. For example, you can set
`CONTAINER_TOOL=podman` if your build system uses podman instead of docker. To use a custom repository, you can use the `IMAGE_TAG_BASE` variable.

For example:

```console
# Build and push the image
make IMAGE_TAG_BASE=quay.io/${MY_REPO_ID}/siteconfig-manager VERSION=latest CONTAINER_TOOL=podman \
    docker-push

# Deploy the controller to your SNO (with KUBECONFIG set appropriately)
make IMAGE_TAG_BASE=quay.io/${MY_REPO_ID}/siteconfig-manager VERSION=latest CONTAINER_TOOL=podman \
    install \
    deploy
```

To watch the siteconfig-controller logs:

```console
oc logs -n siteconfig-operator --selector app.kubernetes.io/name=siteconfig-controller -c manager --follow
```


### Undeploy controller
To delete the CRDs and to remove the controller from the cluster:

```console
make undeploy
```

### How it works
This project aims to follow the Kubernetes [Operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/).

It uses [Controllers](https://kubernetes.io/docs/concepts/architecture/controller/),
which provide a reconcile function responsible for synchronizing resources until the desired state is reached on the cluster.

### Test It Out
1. Install the CRDs into the cluster:

```sh
make install
```

2. Run your controller (this will run in the foreground, so switch to a new terminal if you want to leave it running):

```sh
make run
```

**NOTE:** You can also run this in one step by running: `make install run`

### Modifying the API definitions
If you are editing the API definitions, generate the manifests such as CRs or CRDs using:

```sh
make manifests
```

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.


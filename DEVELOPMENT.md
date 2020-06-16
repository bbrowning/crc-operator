# Development

Because the operator needs to be able to SSH into the CRC Virtual
Machines to get OpenShift started after boot, it's easiest to always
push to a container repo and redeploy the operator pod instead of
running the operator locally.

It is possible to do this locally with `oc rsh`, but the operator code
doesn't branch to support that local code path yet.

Make sure to replace quay.io/bbrowning/crc-operator below with your
own container registry.

```
go mod vendor
operator-sdk generate k8s
operator-sdk generate crds

oc create ns crc-operator
oc apply -f deploy/crds/crc.developer.openshift.io_crcclusters_crd.yaml
oc apply -f deploy/service_account.yaml
oc apply -f deploy/role.yaml
oc apply -f deploy/role_binding.yaml

# Build and run the operator image
operator-sdk build quay.io/bbrowning/crc-operator:v0.0.1
buildah push quay.io/bbrowning/crc-operator:v0.0.1
cat deploy/operator.yaml | sed 's|REPLACE_IMAGE|quay.io/bbrowning/crc-operator:v0.0.1|g' | oc apply -f -

oc logs deployment/crc-operator -n crc-operator -f

# Create a CrcCluster resource
oc apply -f deploy/crds/crc.developer.openshift.io_v1alpha1_crccluster_cr.yaml
```


## Route Helper image

```
cd route-helper
podman build . -t quay.io/bbrowning/crc-operator-routes-helper:v0.0.1
podman push quay.io/bbrowning/crc-operator-routes-helper:v0.0.1
```

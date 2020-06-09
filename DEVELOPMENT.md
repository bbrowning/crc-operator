# Development

In a nutshell, clone this repo and:

```
go mod vendor
operator-sdk generate k8s
operator-sdk generate crds

oc apply -f deploy/crds/crc.developer.openshift.io_crcclusters_crd.yaml
operator-sdk run local --watch-namespace=""
oc apply -f deploy/crds/crc.developer.openshift.io_v1alpha1_crccluster_cr.yaml
```

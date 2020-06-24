# Development

Because the operator needs to be able to SSH into the CRC Virtual
Machines to get OpenShift started after boot, it's easiest to always
push to a container repo and redeploy the operator pod instead of
running the operator locally.

It is possible to do this locally with `oc rsh`, but the operator code
doesn't branch to support that local code path yet.

```
go mod vendor
operator-sdk generate k8s
operator-sdk generate crds

export RELEASE_VERSION=dev
export RELEASE_REGISTRY=quay.io/bbrowning
make release

oc create ns crc-operator
oc apply -f deploy/releases/release-vdev_crd.yaml
oc apply -f deploy/releases/release-vdev.yaml

oc logs deployment/crc-operator -n crc-operator -f

# Create a CrcCluster resource
cat <<EOF | oc apply -f -
apiVersion: crc.developer.openshift.io/v1alpha1
kind: CrcCluster
metadata:
  name: my-cluster
  namespace: crc
spec:
  cpu: 6
  memory: 16Gi
  pullSecret: $(cat pull-secret | base64 -w 0)
EOF

oc wait --for=condition=Ready crc/my-cluster -n crc --timeout=1800s
```


# Releasing a new crc-operator

First, ensure everything is committed, vendor/ directory is
up-to-date, and so on. Add an entry to CHANGELOG.md with any important
changes. Then, follow the steps below, substituting the proper release
version in the first step:

```
export RELEASE_VERSION=0.0.1
export RELEASE_REGISTRY=quay.io/bbrowning
make release
git add version/version.go
git add deploy/releases/release-v${RELEASE_VERSION}_crd.yaml 
git add deploy/releases/release-v${RELEASE_VERSION}.yaml
git commit -m "Release v${RELEASE_VERSION}"
git tag v${RELEASE_VERSION}
git push origin master --tags
```

Now, go to GitHub and add an actual release from the pushed
tag. Attach the appropriate deploy/releases/release-v*.yaml and
deploy/releases/release-v*_crd.yaml to the release.

# Other Notes Below

These are mainly Ben's notes put somewhere more public. They may not
be entirely accurate or easy to follow for anyone else yet.

## Building CRC container images for CNV

Build your own qcow2 files using https://github.com/code-ready/snc/,
copy them into bundle-containers/, and build a container image. The
actual images aren't stored in git since they're so large, and you may
need to pick one of the Dockerfiles in bundle-containers/ and modify
for your VM image.

### Changes needed for CRC images running inside CNV

https://github.com/bbrowning/snc/commit/6d416a5dca837ef2bf42c6269a2010a239caf965


### Uploading a custom VM image as a container

Copy your crc_*.qcow2 image into `bundle-containers/`, copy one of the
existing Dockerfiles under a new name, modify it for your needs, and
build/push the container.

```
pushd bundle-containers
podman build -t quay.io/bbrowning/crc_bundle_4.5.0-rc.1 -f Dockerfile_v4.5.0-rc.1
podman push quay.io/bbrowning/crc_bundle_4.5.0-rc.1
popd
```

### Using the new bundle image to start a CRC cluster

Create a new CrcBundle resource, using the examples in
deploy/crds/crc.developer.openshift.io_v1alpha1_crcbundle_cr.yaml as a
guide. The `sshKey` and `kubeconfig` fields need to be
base64-encoded. It's best to strip out the certificate-authority-data
field from the original CRC kubeconfig and replace it with
`insecure-skip-tls-verify: true`, at least until the operator figures
out how to generate the proper certificate authority data for its API
server URL.

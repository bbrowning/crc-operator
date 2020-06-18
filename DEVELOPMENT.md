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
export RELEASE_REGISTRY=quay.io/your-user
make release

oc create ns crc-operator
oc apply -f deploy/releases/release-dev_crd.yaml
oc apply -f deploy/releases/release-dev.yaml

oc logs deployment/crc-operator -n crc-operator -f

# Create a CrcCluster resource
cat <<EOF | oc apply -f -
apiVersion: crc.developer.openshift.io/v1alpha1
kind: CrcCluster
metadata:
  name: my-cluster
  namespace: crc
spec:
  cpu: 4
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

$ git --no-pager diff
diff --git a/createdisk.sh b/createdisk.sh
index 87abd48..2d12dc8 100755
--- a/createdisk.sh
+++ b/createdisk.sh
@@ -387,23 +387,23 @@ create_qemu_image $libvirtDestDir
 
 copy_additional_files $1 $libvirtDestDir
 
-tar cJSf $libvirtDestDir.$crcBundleSuffix $libvirtDestDir
-
-# HyperKit image generation
-# This must be done after the generation of libvirt image as it reuse some of
-# the content of $libvirtDestDir
-hyperkitDestDir="crc_hyperkit_${destDirSuffix}"
-mkdir $hyperkitDestDir
-generate_hyperkit_directory $libvirtDestDir $hyperkitDestDir $1
-
-tar cJSf $hyperkitDestDir.$crcBundleSuffix $hyperkitDestDir
-
-# HyperV image generation
+#tar cJSf $libvirtDestDir.$crcBundleSuffix $libvirtDestDir
 #
-# This must be done after the generation of libvirt image as it reuses some of
-# the content of $libvirtDestDir
-hypervDestDir="crc_hyperv_${destDirSuffix}"
-mkdir $hypervDestDir
-generate_hyperv_directory $libvirtDestDir $hypervDestDir
-
-tar cJSf $hypervDestDir.$crcBundleSuffix $hypervDestDir
+## HyperKit image generation
+## This must be done after the generation of libvirt image as it reuse some of
+## the content of $libvirtDestDir
+#hyperkitDestDir="crc_hyperkit_${destDirSuffix}"
+#mkdir $hyperkitDestDir
+#generate_hyperkit_directory $libvirtDestDir $hyperkitDestDir $1
+#
+#tar cJSf $hyperkitDestDir.$crcBundleSuffix $hyperkitDestDir
+#
+## HyperV image generation
+##
+## This must be done after the generation of libvirt image as it reuses some of
+## the content of $libvirtDestDir
+#hypervDestDir="crc_hyperv_${destDirSuffix}"
+#mkdir $hypervDestDir
+#generate_hyperv_directory $libvirtDestDir $hypervDestDir
+#
+#tar cJSf $hypervDestDir.$crcBundleSuffix $hypervDestDir
diff --git a/install-config.yaml b/install-config.yaml
index 1f40676..e3705ae 100644
--- a/install-config.yaml
+++ b/install-config.yaml
@@ -15,12 +15,12 @@ metadata:
   name: crc
 networking:
   clusterNetwork:
-  - cidr: 10.128.0.0/14
+  - cidr: 10.116.0.0/14
     hostPrefix: 23
   machineCIDR: 192.168.126.0/24
   networkType: OpenShiftSDN
   serviceNetwork:
-  - 172.30.0.0/16
+  - 172.25.0.0/16
 platform:
   libvirt:
     URI: qemu+tcp://192.168.122.1/system
diff --git a/snc.sh b/snc.sh
index d93fb6e..d6a6c02 100755
--- a/snc.sh
+++ b/snc.sh
@@ -249,6 +249,23 @@ ${YQ} write --inplace ${INSTALL_DIR}/manifests/cluster-ingress-02-config.yml spe
 ${YQ} write --inplace ${INSTALL_DIR}/openshift/99_openshift-cluster-api_master-machines-0.yaml spec.providerSpec.value[domainMemory] 14336
 ${YQ} write --inplace ${INSTALL_DIR}/openshift/99_openshift-cluster-api_master-machines-0.yaml spec.providerSpec.value[domainVcpu] 6
 
+cat <<EOF > ${INSTALL_DIR}/manifests/cluster-network-03-config.yml
+apiVersion: operator.openshift.io/v1
+kind: Network
+metadata:
+  name: cluster
+spec:
+  clusterNetwork:
+  - cidr: 10.116.0.0/14
+    hostPrefix: 23
+  serviceNetwork:
+  - 172.25.0.0/16
+  defaultNetwork:
+    type: OpenShiftSDN
+    openshiftSDNConfig:
+      mtu: 1400
+EOF
+
 # Add codeReadyContainer as invoker to identify it with telemeter
 export OPENSHIFT_INSTALL_INVOKER="codeReadyContainers"


### Uploading a custom VM image as a container

    cp /path/to/my/my-image bundle-containers/crc_4.4.5_serviceNetworkMtuCidr.qcow2
    pushd bundle-containers
    buildah bud -t quay.io/bbrowning/crc_bundle_4.4.5 -f Dockerfile_v4.4.5
    buildah push quay.io/bbrowning/crc_bundle_4.4.5
    popd bundle-containers

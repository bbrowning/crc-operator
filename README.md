# Experimental CodeReady Containers (CRC) Operator

This is an unofficial, experimental operator with the high level goal
of this operator is to let users login to a shared OpenShift 4
cluster, click a button, and get their own private OpenShift 4 cluster
sandbox with full admin access in 5 minutes or less.

It does this by using [CodeReady Containers
(CRC)](https://developers.redhat.com/products/codeready-containers)
virtual machines and [Container-native Virtualization
(CNV)](https://docs.openshift.com/container-platform/4.4/cnv/cnv-about-cnv.html).

## Installation

### Prerequisites

You need a recent OpenShift or Kubernetes cluster with at least one
worker node that is bare metal or that supports nested
virtualization. On AWS, this means *.metal (except a1.metal). On
Azure, this includes D_v3, Ds_v3, E_v3, Es_v3, F2s_v2, F72s_v2, and M
series machines. Other clouds and virtualization providers supported
by OCP 4 should work as well as long as they support nested
virtualization.

Kubernetes clusters will need ingress-nginx installed.

Whether using OpenShift or Kubernetes clusters, you'll need a recent
[oc](https://mirror.openshift.com/pub/openshift-v4/clients/ocp/latest-4.4/)
binary in your $PATH.

A known working setup is a default OCP4 Azure cluster and then add a
`Standard_D8s_v3` Machine to it for running 1-2 CRC VMs.

Another known working setup is a DigitalOcean Kubernetes with
8vCPU/32GB standard Droplets.

You also need a functioning install of [Container-native
Virtualization](https://docs.openshift.com/container-platform/4.4/cnv/cnv_install/installing-container-native-virtualization.html)
on OpenShift or
[KubeVirt](https://kubevirt.io/user-guide/#/installation/installation)
(on Kubernetes).

### Deploy the operator

Create the CrcCluster CRD

```
oc apply -f deploy/crds/crc.developer.openshift.io_crcclusters_crd.yaml
```

Deploy the operator

```
oc create ns crc-operator
oc apply -f deploy/crds/crc.developer.openshift.io_crcclusters_crd.yaml
oc apply -f deploy/service_account.yaml
oc apply -f deploy/role.yaml
oc apply -f deploy/role_binding.yaml
cat deploy/operator.yaml | sed 's|REPLACE_IMAGE|quay.io/bbrowning/crc-operator:v0.0.1|g' | oc apply -f -
```

Ensure the operator comes up with no errors in its logs

```
oc logs deployment/crc-operator -n crc-operator
```

## Create a CRC cluster

The operator is still a work-in-progress, so for now you'll need the
helper `crcStart.sh` script to actually get a CRC cluster up.

Clone this repo, copy your OpenShift pull secret into a file called
`pull-secret`, and run the commands below. You can substitute any name
for your CRC cluster in place of `my-cluster` and any namespace in
place of `crc` in the commands below.

```
oc new-project crc
DEBUG=true ./crcStart.sh my-cluster crc pull-secret
```

If the script hangs, fails, or otherwise something broke check the
known issues below to see if there's a workaround. The script is still
pretty brittle at the moment and things will be far more reliable once
everything is ported into the operator code.


After you have a cluster up, there's another helper script that will
ensure all OpenShift Routes in your CRC cluster get a corresponding
Route/Ingress in the parent cluster so they work end-to-end.

This is another hack until the functionality gets put into this or
some other operator. We'll likely need one pod per CRC cluster started
to monitor the Routes inside that cluster and shuffle things back to
the parent. This script runs forever, monitoring your cluster. CTRL+C
it to stop monitoring Routes.

```
./copyRoutes.sh my-cluster crc
```

# Development

For developer crc-operator itself, see [DEVELOPMENT.md]().

# Known Issues

- Sometimes the openshift-apiserver pod needs a second restart to
  unstick the `crcStart.sh` script - if its logs have a bunch of x509
  errors then it needs a kicking.
- The kubeconfigs have an incorrect certificate-authority-data that
  needs to get updated to match the actual cert from the running
  cluster. Should that have changed? May be unintentional...

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


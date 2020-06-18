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
on Kubernetes.

### OpenShift cluster-bot

If you're using OpenShift's cluster-bot, the following steps are known to work.

First, send cluster-bot a message via Slack to start a 4.4.5 cluster on Azure.

```
launch 4.4.5 azure
```

Once your cluster comes up and you login to it via `oc`, mark the
masters as schedulable:

```
oc patch schedulers.config.openshift.io cluster -p='{"spec": {"mastersSchedulable": true}}' --type=merge
```

Install OpenShift CNV as linked in the previous section. Then, follow
along with the steps below.

### Deploy the operator

Create the CrcCluster CRD

```
oc apply -f https://github.com/bbrowning/crc-operator/releases/download/v0.0.1/release-v0.0.1_crd.yaml
```

Deploy the operator

```
oc create ns crc-operator
oc apply -f https://github.com/bbrowning/crc-operator/releases/download/v0.0.1/release-v0.0.1.yaml
```

Ensure the operator comes up with no errors in its logs

```
oc logs deployment/crc-operator -n crc-operator
```

## Create a CRC cluster

Clone this repo, copy your OpenShift pull secret into a file called
`pull-secret`, and run the commands below. You can substitute any name
for your CRC cluster in place of `my-cluster` and any namespace in
place of `crc` in the commands below.

```
oc create ns crc

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

On reasonably sized Nodes, the CRC cluster usually comes up in 7-8
minutes. The very first time a CRC cluster is created on a Node, it
can take quite a bit longer while the CRC VM image is pulled into the
container image cache on that Node.

If the CRC cluster never becomes Ready, check the operator pod logs
(as shown in the installation section above) and the known issues list
below for any clues on what went wrong.

## Access the CRC cluster

Once your new cluster is up and Ready, the CrcCluster resource's
status block has all the information needed to access it.


### Log in to the CRC cluster's web console:

Console URL:

```
oc get crc my-cluster -n crc -o jsonpath={.status.consoleURL} && echo ""
```

Kubeadmin Password:

```
oc get crc my-cluster -n crc -o jsonpath={.status.kubeAdminPassword} && echo ""
```

Log in as the user kubeadmin with the password from above.

### Access the CRC cluster from the command line using oc:

Extract the kubeconfig to a `kubeconfig-crc` file in the current
directory and use that to access the cluster:

```
oc get crc my-cluster -n crc -o jsonpath={.status.kubeconfig} | base64 -d > kubeconfig-crc
oc --kubeconfig kubeconfig-crc get pod --all-namespaces
```

### Destroy the CRC cluster

To destroy the CRC cluster, just delete the `CrcCluster`
resource. Everything else related to it will get deleted
automatically.

```
oc delete crc my-cluster -n crc
```

# Known Issues

- The first time a CrcCluster gets created on any specific Node, it
  takes a LONG time to pull the CRC VM image from quay.io. There's a
  planned CrcBundle API that may be able to mitigate this by
  pre-pulling the VM images into the internal registry. For now, if
  crcStart.sh times out and this is the first time running a VM on
  that specific Node, just run the script again with the exact same
  arguments.
- The kubeconfigs have an incorrect certificate-authority-data that
  needs to get updated to match the actual cert from the running
  cluster. Should that have changed? Look at
  https://docs.openshift.com/container-platform/4.4/authentication/certificates/api-server.html
  for how to add an additional API server certificate with the proper
  name. The operator would need to generate a new cert for the exposed
  API server URL and follow those instructions.
- Credentials are stored directly in the CRD status for now. A future
  release will move these into Secrets.
- Only one CRC bundle image is supported at the moment. A future
  release will add a new API to manage multiple CRC VM images where
  the user can choose which (ie 4.4.5, 4.4.6, 4.5.0, etc) they want to
  spin up.

# Development

For tips on developing crc-operator itself, see [DEVELOPMENT.md]().

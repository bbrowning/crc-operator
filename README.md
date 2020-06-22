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
and [Containerized Data
Importer](https://github.com/kubevirt/containerized-data-importer/releases/download/v1.19.0/cdi-operator.yaml)
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

Create the necessary Custom Resource Definitions

```
oc apply -f https://github.com/bbrowning/crc-operator/releases/download/v0.0.3/release-v0.0.3_crd.yaml
```

Deploy the operator

```
oc create ns crc-operator
oc apply -f https://github.com/bbrowning/crc-operator/releases/download/v0.0.3/release-v0.0.3.yaml
```

Ensure the operator comes up with no errors in its logs

```
oc logs deployment/crc-operator -n crc-operator
```

## Create a CRC cluster

Copy your OpenShift pull secret into a file called `pull-secret`, and
run the commands below. You can substitute any name for your CRC
cluster in place of `my-cluster` and any namespace in place of `crc`
in the commands below.

Valid CRC bundle names are `ocp445`, `ocp450rc1`, and `ocp450rc2`.

Create a crc namespace:

```
oc create ns crc
```

Create an OpenShift 4.4.5 cluster (the default if `bundleName` is
unspecified) with ephemeral storage:

```
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
  bundleName: ocp445
EOF
```

Or, to create an OpenShift 4.5.0-rc.2 cluster with ephemeral storage:

```
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
  bundleName: ocp450rc2
EOF
```

Or, to create an OpenShift 4.5.0-rc.2 cluster with larger persistent
storage that will survive stops, starts, node reboots, and so on:

```
cat <<EOF | oc apply -f -
apiVersion: crc.developer.openshift.io/v1alpha1
kind: CrcCluster
metadata:
  name: my-cluster-persistent
  namespace: crc
spec:
  cpu: 6
  memory: 16Gi
  pullSecret: $(cat pull-secret | base64 -w 0)
  bundleName: ocp450rc2
  storage:
    persistent: true
    size: 100Gi
EOF
```

Wait for the new cluster to become ready:

```
oc wait --for=condition=Ready crc/my-cluster -n crc --timeout=1800s
```


On reasonably sized Nodes, a CRC cluster with ephemeral storage
usually comes up in 7-8 minutes. The very first time a CRC cluster is
created on a Node, it can take quite a bit longer while the CRC VM
image is pulled into the container image cache on that Node. A CRC
cluster with persistent storage can easily take twice as long to come
up the first time, although it has the added benefit of not losing
data if a Node reboots or the cluster gets stopped.

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

The easiest way is to login to the web console, click the dropdown for
the `kubeadmin` user in the upper right corner, and click 'Copy Login
Command'.

Alternatively, you can extract the kubeconfig to a `kubeconfig-crc` file
in the current directory and use that to access the cluster. The
client certificate in this kubeconfig expires in some period less than
30 days.

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

The clusters created by this operator should be quite usable for
development or testing needs. However, there are some known issues
documented below. Most should not impact development or testing use
cases significantly.

- The first time a CrcCluster gets created on any specific Node, it
  may take a long time to pull the CRC VM image from quay.io. There's
  a planned CrcBundle API that may be able to mitigate this by
  pre-pulling the VM images into the internal registry.
- The kubeconfigs have an incorrect certificate-authority-data that
  needs to get updated to match the actual cert from the running
  cluster. Should that have changed? Look at
  https://docs.openshift.com/container-platform/4.4/authentication/certificates/api-server.html
  for how to add an additional API server certificate with the proper
  name. The operator would need to generate a new cert for the exposed
  API server URL and follow those instructions.
- Credentials are stored directly in the CRD status for now. A future
  release will move these into Secrets.
- CRC bundle images are hardcoded at the moment. A future release will
  add a new API to change the available bundle images without code
  changes.
- The client certificate in the kubeconfig generated for the kubeadmin
  user is only valid for one month or less. Perhaps we shouldn't
  provide that and expect a user to just `oc login` with their
  username and password.
- Multiple CRC clusters running in a single parent cluster can result
  in the redhat/certified/community operator pods in the
  openshift-marketplace namespace crashlooping because they don't come
  up quickly enough and their liveness probe fails. This appears to be
  both too aggressive liveness probes for those pods combined with
  quay.io IP-based rate limiting and all these clusters appearing as
  one IP to quay.io.
- The disk size is fixed at 30GB for now. A future release will add
  that as an option when creating the cluster.
- The disk attached to the VM is ephemeral for now. A future release
  will add a persistent disk option.

# Development

For tips on developing crc-operator itself, see [DEVELOPMENT.md]().

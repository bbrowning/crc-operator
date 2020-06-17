#!/usr/bin/env bash

set -e

export VM_NAME="$1"
export VM_NAMESPACE="$2"
export PULL_SECRET_FILE="$3"
DEBUG=${DEBUG:-false}

DEFAULT_VM_CPUS=4
DEFAULT_VM_MEMORY=16Gi
VM_CPUS=${VM_CPUS:-$DEFAULT_VM_CPUS}
VM_MEMORY=${VM_MEMORY:-$DEFAULT_VM_MEMORY}

log () {
  echo "$@"
}

dlog () {
  if [ "true" == "${DEBUG}" ]; then
    log "$@"
  fi
}

if [ -z "$VM_NAME" -o -z "$VM_NAMESPACE" -o -z "$PULL_SECRET_FILE" -o ! -f "$PULL_SECRET_FILE" ]; then
  log "Usage: $0 <crc vm name> <crc vm namespace> <pull secret file>"
  log "Example: $0 my-cluster crc pull-secret.json"
  log "----"
  log "Environment variable overrides"
  log "VM_CPUS: Change the number of CPUs allocated to the VM (default: ${DEFAULT_VM_CPUS})"
  log "VM_MEMORY: Change the amount of memory allocated to the VM (example: ${DEFAULT_VM_MEMORY})"
  exit 1
fi

oc get namespace ${VM_NAMESPACE} 1>/dev/null

log "> Starting CRC Cluster ${VM_NAME} in namespace ${VM_NAMESPACE} with ${VM_CPUS} CPUs and ${VM_MEMORY} of memory- this can take up to 15 minutes..."

cat <<EOF | oc apply -f -
apiVersion: crc.developer.openshift.io/v1alpha1
kind: CrcCluster
metadata:
  name: ${VM_NAME}
  namespace: ${VM_NAMESPACE}
spec:
  cpu: ${VM_CPUS}
  memory: ${VM_MEMORY}
  pullSecret: $(cat $PULL_SECRET_FILE | base64 -w 0)
EOF

log "> Waiting for ${VM_NAME} cluster to be ready"
oc wait --for=condition=Ready crc/${VM_NAME} -n ${VM_NAMESPACE} --timeout=900s

export KUBECONFIGFILE="kubeconfig-${VM_NAME}-${VM_NAMESPACE}"

dlog "> Looking up API server"
while [ -z "${CRC_API_SERVER}" ]; do
  export CRC_API_SERVER=$(oc get crc ${VM_NAME} -n ${VM_NAMESPACE} -o jsonpath={.status.apiURL} || echo '')
done

dlog "> Looking up kubeconfig"
while [ -z "${KUBECONFIG_CONTENTS}" ]; do
  export KUBECONFIG_CONTENTS=$(oc get crc ${VM_NAME} -n ${VM_NAMESPACE} -o jsonpath={.status.kubeconfig} || echo '')
done
echo "${KUBECONFIG_CONTENTS}" | base64 -d > $KUBECONFIGFILE

export OCCRC="oc --kubeconfig $KUBECONFIGFILE"


dlog "> Final stabilization check"
until ${OCCRC} get route -n openshift-console console; do
  log -n "."
  sleep 2
done
sleep 5
while ${OCCRC} get pod --no-headers --all-namespaces | grep -v Running | grep -v Completed | grep -v Terminating; do
  log -n "."
  sleep 2
done
log ""

if [ "true" == "$DEBUG" ]; then
  ${OCCRC} get pod --all-namespaces
fi

CRC_CONSOLE="$(oc get crc ${VM_NAME} -n ${VM_NAMESPACE} -o jsonpath={.status.consoleURL})"
KUBEADMIN_PASSWORD="$(oc get crc ${VM_NAME} -n ${VM_NAMESPACE} -o jsonpath={.status.kubeAdminPassword})"

log "> CRC cluster is up!

Connect as an administrator on the CLI using:
${OCCRC}

Access the console at: ${CRC_CONSOLE}
Login as an administrator with username kubeadmin and password ${KUBEADMIN_PASSWORD}
"

#!/usr/bin/env bash

set -e

export VM_NAME="$1"
export VM_NAMESPACE="$2"
export PULL_SECRET_FILE="$3"
DEBUG=${DEBUG:-false}

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
  exit 1
fi

oc get namespace ${VM_NAMESPACE} 1>/dev/null

if oc api-versions | grep route.openshift.io/v1 1>/dev/null; then
  export IS_OS=true
else
  export IS_OS=false
fi

log "> Starting CRC Cluster ${VM_NAME} in namespace ${VM_NAMESPACE} - this will take several minutes ..."

cat <<EOF | oc apply -f -
apiVersion: crc.developer.openshift.io/v1alpha1
kind: CrcCluster
metadata:
  name: ${VM_NAME}
  namespace: ${VM_NAMESPACE}
spec:
  cpu: 16
  memory: 24Gi
  pullSecret: $(cat $PULL_SECRET_FILE | base64 -w 0)
EOF

log "> Waiting for ${VM_NAME} cluster to be ready"
oc wait --for=condition=Ready crc/${VM_NAME} -n ${VM_NAMESPACE} --timeout=600s


export KUBECONFIGFILE="kubeconfig-${VM_NAME}-${VM_NAMESPACE}"

dlog "> Looking up API server"
while [ -z "${CRC_API_SERVER}" ]; do
  export CRC_API_SERVER=$(oc get crc ${VM_NAME} -n ${VM_NAMESPACE} -o jsonpath={.status.apiUrl} || echo '')
done

dlog "> Looking up kubeconfig"
while [ -z "${KUBECONFIG_CONTENTS}" ]; do
  export KUBECONFIG_CONTENTS=$(oc get crc ${VM_NAME} -n ${VM_NAMESPACE} -o jsonpath={.status.kubeconfig} || echo '')
done
echo "${KUBECONFIG_CONTENTS}" | base64 -d > $KUBECONFIGFILE

export OCCRC="oc --insecure-skip-tls-verify --kubeconfig $KUBECONFIGFILE"

dlog "> Waiting for cluster to stabilize"
while ${OCCRC} get pod --no-headers --all-namespaces | grep -v Running | grep -v Completed 1>/dev/null 2>/dev/null; do
  dlog -n "."
  sleep 2
done
until ${OCCRC} get route -n openshift-console console 1>/dev/null 2>/dev/null; do
  sleep 2
done
while ${OCCRC} get pod --no-headers --all-namespaces | grep -v Running | grep -v Completed 1>/dev/null 2>/dev/null; do
  dlog -n "."
  sleep 2
done
until ${OCCRC} get route -n openshift-console console 1>/dev/null 2>/dev/null; do
  sleep 2
done
while ${OCCRC} get pod --no-headers --all-namespaces | grep -v Running | grep -v Completed 1>/dev/null 2>/dev/null; do
  dlog -n "."
  sleep 2
done
dlog ""

while [ -z "${ROUTE_DOMAIN}" ]; do
  export ROUTE_DOMAIN=$(oc get crc ${VM_NAME} -n ${VM_NAMESPACE} -o jsonpath={.status.baseDomain} || echo '')
done

if ${IS_OS}; then
  dlog "> Creating OpenShift Routes for console and oauth"
cat <<EOF | oc apply -f -
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: ${VM_NAME}-apps-oauth
  namespace: ${VM_NAMESPACE}
spec:
  host: oauth-openshift.${ROUTE_DOMAIN}
  port:
    targetPort: 443
  to:
    kind: Service
    name: ${VM_NAME}
  tls:
    termination: passthrough
---
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: ${VM_NAME}-apps-console
  namespace: ${VM_NAMESPACE}
spec:
  host: console-openshift-console.${ROUTE_DOMAIN}
  port:
    targetPort: 443
  to:
    kind: Service
    name: ${VM_NAME}
  tls:
    termination: passthrough
EOF
else
  dlog "> Creating Kubernetetes Ingress for console and oauth - this only works with ingress-nginx"
cat <<EOF | oc apply -f -
apiVersion: networking.k8s.io/v1beta1
kind: Ingress
metadata:
  name: ${VM_NAME}-apps-oauth
  namespace: ${VM_NAMESPACE}
  annotations:
    kubernetes.io/ingress.allow-http: "false"
    nginx.ingress.kubernetes.io/ssl-passthrough: "true"
    nginx.ingress.kubernetes.io/backend-protocol: "HTTPS"
spec:
  rules:
  - host: oauth-openshift.${ROUTE_DOMAIN}
    http:
      paths:
      - path: /
        backend:
          serviceName: ${VM_NAME}
          servicePort: 443
---
apiVersion: networking.k8s.io/v1beta1
kind: Ingress
metadata:
  name: ${VM_NAME}-apps-console
  namespace: ${VM_NAMESPACE}
  annotations:
    kubernetes.io/ingress.allow-http: "false"
    nginx.ingress.kubernetes.io/ssl-passthrough: "true"
    nginx.ingress.kubernetes.io/backend-protocol: "HTTPS"
spec:
  rules:
  - host: console-openshift-console.${ROUTE_DOMAIN}
    http:
      paths:
      - path: /
        backend:
          serviceName: ${VM_NAME}
          servicePort: 443
EOF
fi

OLD_ROUTE_DOMAIN=$(${OCCRC} get ingresscontroller default -n openshift-ingress-operator -o jsonpath={.status.domain})
if [ "${ROUTE_DOMAIN}" != "${OLD_ROUTE_DOMAIN}" ]; then
  dlog "> Updating default Ingress domain"
  ${OCCRC} patch ingress.config.openshift.io cluster -p "{\"spec\": {\"domain\": \"${ROUTE_DOMAIN}\"}}" --type merge 1>/dev/null

  dlog "> Recreating default router with updated domain"
  ${OCCRC} delete ingresscontrollers -n openshift-ingress-operator default
  cat <<EOF | ${OCCRC} apply -f -
apiVersion: operator.openshift.io/v1
kind: IngressController
metadata:
  name: default
  namespace: openshift-ingress-operator
spec:
  replicas: 1
  domain: ${ROUTE_DOMAIN}
EOF

  dlog "> Waiting for cluster to stabilize"
  while ${OCCRC} get pod --no-headers --all-namespaces | grep -v Running | grep -v Completed 1>/dev/null 2>/dev/null; do
    dlog -n "."
    sleep 2
  done
  until ${OCCRC} get route -n openshift-console console 1>/dev/null 2>/dev/null; do
    sleep 2
  done
  while ${OCCRC} get pod --no-headers --all-namespaces | grep -v Running | grep -v Completed 1>/dev/null 2>/dev/null; do
    dlog -n "."
    sleep 2
  done
  until ${OCCRC} get route -n openshift-console console 1>/dev/null 2>/dev/null; do
    sleep 2
  done
  while ${OCCRC} get pod --no-headers --all-namespaces | grep -v Running | grep -v Completed 1>/dev/null 2>/dev/null; do
    dlog -n "."
    sleep 2
  done
  dlog ""
fi

log "Updating console route for new cluster"
until ${OCCRC} patch route -n openshift-console console -p "{\"spec\": {\"host\": \"console-openshift-console.${ROUTE_DOMAIN}\"}}" --type=merge 1>/dev/null; do
  sleep 2
done

log "> Waiting for cluster to stabilize"
while ${OCCRC} get pod --no-headers --all-namespaces | grep -v Running | grep -v Completed 1>/dev/null 2>/dev/null; do
  log -n "."
  sleep 2
done
until ${OCCRC} get route -n openshift-console console 1>/dev/null 2>/dev/null; do
  sleep 2
done

sleep 10

while ${OCCRC} get pod --no-headers --all-namespaces | grep -v Running | grep -v Completed 1>/dev/null 2>/dev/null; do
  log -n "."
  sleep 2
done
until ${OCCRC} get route -n openshift-console console 1>/dev/null 2>/dev/null; do
  sleep 2
done
while ${OCCRC} get pod --no-headers --all-namespaces | grep -v Running | grep -v Completed 1>/dev/null 2>/dev/null; do
  log -n "."
  sleep 2
done
log ""


dlog "> Final stabilization check"
until ${OCCRC} get route -n openshift-console console 1>/dev/null 2>/dev/null; do
  sleep 2
done

if [ "true" == "$DEBUG" ]; then
  ${OCCRC} get pod --all-namespaces
fi

CRC_CONSOLE="https://$(${OCCRC} get route -n openshift-console console -o jsonpath={.spec.host})"
KUBEADMIN_PASSWORD="$(oc get crc ${VM_NAME} -n ${VM_NAMESPACE} -o jsonpath={.status.kubeAdminPassword})"

log "> CRC cluster is up!

Connect as kube:admin on the CLI using:
${OCCRC}

Connect as developer on the CLI using:
oc login --insecure-skip-tls-verify ${CRC_API_SERVER} -u developer -p developer

Access the console at: ${CRC_CONSOLE}
Login as kube:admin with kubeadmin/${KUBEADMIN_PASSWORD}
Login as developer with developer/developer
"

#!/usr/bin/env bash

set -e
set -o pipefail

# This script expects two environment variables to be set:
#   CRC_NAME: The name of the CRC resource
#   CRC_NAMESPACE: The namespace of the CRC resource

log () {
  echo "$@"
}

if [ -z "${CRC_NAME:-}" -o -z "${CRC_NAMESPACE:-}" ]; then
  log "CRC_NAME and CRC_NAMESPACE environment variables must be set"
  exit 1
fi

ROUTE_DOMAIN=$(oc get crc ${CRC_NAME} -n ${CRC_NAMESPACE} -o jsonpath={.status.baseDomain} || echo '')

if oc api-versions | grep route.openshift.io/v1; then
  export IS_OS=true
else
  export IS_OS=false
fi

KUBECONFIGFILE="/tmp/kubeconfig-${CRC_NAME}-${CRC_NAMESPACE}"

oc get crc ${CRC_NAME} -n ${CRC_NAMESPACE} -o jsonpath={.status.kubeconfig} | base64 -d > $KUBECONFIGFILE

SHOULD_LOOP=true
cleanup() {
  echo -e "\nStopping monitoring of OpenShift Routes\n"
  SHOULD_LOOP=false
}
trap 'cleanup' EXIT

OCCRC="oc --insecure-skip-tls-verify --kubeconfig $KUBECONFIGFILE"

log "> Monitoring OpenShift Routes and configuring networking."
log "    Press CTRL+C to stop."

declare -A PROXIED_ROUTES

OWNER_REFERENCES="ownerReferences:
  - apiVersion: apps/v1
    kind: Deployment
    name: ${CRC_NAME}-route-helper
    uid: $(oc get deployment -n ${CRC_NAMESPACE} ${CRC_NAME}-route-helper -o jsonpath={.metadata.uid})"

while $SHOULD_LOOP; do
  while read line; do
    namespace=$(echo "$line" | awk '{print $1}')
    name=$(echo "$line" | awk '{print $2}')
    version=$(echo "$line" | awk '{print $3}')
    key="${namespace}-${name}"

    if [ "${PROXIED_ROUTES[$key]}" != "$version" ]; then
      echo "Creating a route for $namespace/$name"
      routeHost=$(${OCCRC} get route -n $namespace $name -o jsonpath={.spec.host})
      routeTls=$(${OCCRC} get route -n $namespace $name -o jsonpath={.spec.tls})

      if [ -z "$routeTls" ]; then
        if ${IS_OS}; then
          cat <<EOF | oc apply -f -
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: ${CRC_NAME}-${name}-${namespace}
  namespace: ${CRC_NAMESPACE}
  $OWNER_REFERENCES
spec:
  host: ${routeHost}
  port:
    targetPort: 80
  to:
    kind: Service
    name: ${CRC_NAME}
EOF
        else
          cat <<EOF | oc apply -f -
apiVersion: networking.k8s.io/v1beta1
kind: Ingress
metadata:
  name: ${CRC_NAME}-${name}-${namespace}
  namespace: ${CRC_NAMESPACE}
  annotations:
    kubernetes.io/ingress.allow-http: "true"
    nginx.ingress.kubernetes.io/ssl-passthrough: "true"
    nginx.ingress.kubernetes.io/backend-protocol: "HTTP"
  $OWNER_REFERENCES
spec:
  rules:
  - host: ${routeHost}
    http:
      paths:
      - path: /
        backend:
          serviceName: ${CRC_NAME}
          servicePort: 80
EOF
        fi
      else
        if ${IS_OS}; then
          cat <<EOF | oc apply -f -
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: ${CRC_NAME}-${name}-${namespace}
  namespace: ${CRC_NAMESPACE}
  $OWNER_REFERENCES
spec:
  host: ${routeHost}
  port:
    targetPort: 443
  to:
    kind: Service
    name: ${CRC_NAME}
  tls:
    termination: passthrough
EOF
        else
          cat <<EOF | oc apply -f -
apiVersion: networking.k8s.io/v1beta1
kind: Ingress
metadata:
  name: ${CRC_NAME}-${name}-${namespace}
  namespace: ${CRC_NAMESPACE}
  annotations:
    kubernetes.io/ingress.allow-http: "false"
    nginx.ingress.kubernetes.io/ssl-passthrough: "true"
    nginx.ingress.kubernetes.io/backend-protocol: "HTTPS"
  $OWNER_REFERENCES
spec:
  rules:
  - host: ${routeHost}
    http:
      paths:
      - path: /
        backend:
          serviceName: ${CRC_NAME}
          servicePort: 443
EOF
        fi
      fi
      
      PROXIED_ROUTES[$key]=$version
    fi
  done <<EOF
$(${OCCRC} get route --all-namespaces -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,RESOURCEVERSION:.metadata.resourceVersion --no-headers)
EOF
  sleep 5
done

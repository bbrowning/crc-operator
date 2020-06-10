#!/usr/bin/env bash

export VM_NAME="$1"
export VM_NAMESPACE="$2"

log () {
  echo "$@"
}

if [ -z "$VM_NAME" -o -z "$VM_NAMESPACE" ]; then
  log "Usage: $0 <crc vm name> <crc vm namespace>"
  log "Example: $0 my-cluster crc"
  exit 1
fi

while [ -z "${ROUTE_DOMAIN}" ]; do
  export ROUTE_DOMAIN=$(oc get crc ${VM_NAME} -n ${VM_NAMESPACE} -o jsonpath={.status.baseDomain} || echo '')
done

if oc api-versions | grep route.openshift.io/v1 1>/dev/null; then
  export IS_OS=true
else
  export IS_OS=false
fi

export KUBECONFIGFILE="kubeconfig-${VM_NAME}-${VM_NAMESPACE}"

if [ ! -f "${KUBECONFIGFILE}" ]; then
  log "kubeconfig ${KUBECONFIGFILE} missing - have you used crcStart.sh first?"
  exit 1
fi

export OCCRC="oc --insecure-skip-tls-verify --kubeconfig $KUBECONFIGFILE"

log "> Monitoring OpenShift Routes and configuring networking."
log "    Press CTRL+C to stop."

declare -A PROXIED_ROUTES

while true; do
  while read line; do
    namespace=$(echo "$line" | awk '{print $1}')
    name=$(echo "$line" | awk '{print $2}')
    version=$(echo "$line" | awk '{print $3}')
    key="${namespace}-${name}"

    if [ "${PROXIED_ROUTES[$key]}" != "$version" ]; then
      echo "Creating a route for $namespace/$name"
      routeYaml=$(${OCCRC} get route -n $namespace $name -o yaml)
      routeHost=$(echo "$routeYaml" | yq r - spec.host)
      routeTls=$(echo "$routeYaml" | yq r - spec.tls)

      if [ -z "$routeTls" ]; then
        if ${IS_OS}; then
          cat <<EOF | oc apply -f -
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: ${VM_NAME}-${name}-${namespace}
  namespace: ${VM_NAMESPACE}
spec:
  host: ${routeHost}
  port:
    targetPort: 80
  to:
    kind: Service
    name: ${VM_NAME}
EOF
        else
          cat <<EOF | oc apply -f -
apiVersion: networking.k8s.io/v1beta1
kind: Ingress
metadata:
  name: ${VM_NAME}-${name}-${namespace}
  namespace: ${VM_NAMESPACE}
  annotations:
    kubernetes.io/ingress.allow-http: "true"
    nginx.ingress.kubernetes.io/ssl-passthrough: "true"
    nginx.ingress.kubernetes.io/backend-protocol: "HTTP"
spec:
  rules:
  - host: ${routeHost}
    http:
      paths:
      - path: /
        backend:
          serviceName: ${VM_NAME}
          servicePort: 80
EOF
        fi
      else
        if ${IS_OS}; then
          cat <<EOF | oc apply -f -
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: ${VM_NAME}-${name}-${namespace}
  namespace: ${VM_NAMESPACE}
spec:
  host: ${routeHost}
  port:
    targetPort: 443
  to:
    kind: Service
    name: ${VM_NAME}
  tls:
    termination: passthrough
EOF
        else
          cat <<EOF | oc apply -f -
apiVersion: networking.k8s.io/v1beta1
kind: Ingress
metadata:
  name: ${VM_NAME}-${name}-${namespace}
  namespace: ${VM_NAMESPACE}
  annotations:
    kubernetes.io/ingress.allow-http: "false"
    nginx.ingress.kubernetes.io/ssl-passthrough: "true"
    nginx.ingress.kubernetes.io/backend-protocol: "HTTPS"
spec:
  rules:
  - host: ${routeHost}
    http:
      paths:
      - path: /
        backend:
          serviceName: ${VM_NAME}
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

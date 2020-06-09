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
  log "Example: $0 my-cluster pull-secret.json"
  exit 1
fi

oc get namespace ${VM_NAMESPACE} 1>/dev/null

if oc api-versions | grep route.openshift.io/v1 1>/dev/null; then
  export IS_OS=true
else
  export IS_OS=false
fi

log "> Starting CRC Cluster ${VM_NAME} in namespace ${VM_NAMESPACE} - this will take several minutes ..."

dlog "> Creating ${VM_NAME} Virtual Machine in namespace ${VM_NAMESPACE}"
cat <<EOF | oc apply -f -
apiVersion: kubevirt.io/v1alpha3
kind: VirtualMachine
metadata:
  name: ${VM_NAME}
  namespace: ${VM_NAMESPACE}
  labels:
    app: ${VM_NAME}
    flavor.template.kubevirt.io/Custom: 'true'
    workload.template.kubevirt.io/server: 'true'
spec:
  running: true
  template:
    metadata:
      creationTimestamp: null
      labels:
        flavor.template.kubevirt.io/Custom: 'true'
        kubevirt.io/domain: ${VM_NAME}
        kubevirt.io/size: large
        vm.kubevirt.io/name: ${VM_NAME}
        workload.template.kubevirt.io/server: 'true'
    spec:
      domain:
        cpu:
          cores: 2
          sockets: 4
          threads: 1
        memory:
          guest: 16Gi
        resources:
          requests:
            cpu: 2
            memory: 9Gi
          overcommitGuestOverhead: true
        devices:
          disks:
            - bootOrder: 1
              disk:
                bus: virtio
              name: rootdisk
          interfaces:
            - masquerade: {}
              model: virtio
              name: nic0
          networkInterfaceMultiqueue: true
          rng: {}
        machine:
          type: pc-q35-rhel8.1.0
      hostname: crc
      networks:
        - name: nic0
          pod: {}
      terminationGracePeriodSeconds: 0
      volumes:
        - name: rootdisk
          containerDisk:
            image: quay.io/bbrowning/crc_bundle_4.4.5
status: {}
EOF

dlog "> Creating Kubernetes Service for ${VM_NAME} VM"
cat <<EOF | oc apply -f -
apiVersion: v1
kind: Service
metadata:
  name: ${VM_NAME}
  namespace: ${VM_NAMESPACE}
spec:
  ports:
  - name: ssh
    port: 2022
    protocol: TCP
    targetPort: 22
  - name: api
    port: 6443
    protocol: TCP
    targetPort: 6443
  - name: http
    port: 80
    protocol: TCP
    targetPort: 80
  - name: https
    port: 443
    protocol: TCP
    targetPort: 443
  selector:
    vm.kubevirt.io/name: ${VM_NAME}
  type: ClusterIP
EOF

if ${IS_OS}; then
  dlog "> Creating OpenShift Route for ${VM_NAME} VM APIServer"

  while [ -z "${ROUTE_DOMAIN}" ]; do
    export ROUTE_DOMAIN=$(oc get ingress.config.openshift.io cluster -o jsonpath={.spec.domain})
  done

  cat <<EOF | oc apply -f -
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: ${VM_NAME}-api
  namespace: ${VM_NAMESPACE}
spec:
  host: api.${VM_NAME}-${VM_NAMESPACE}.${ROUTE_DOMAIN}
  port:
    targetPort: 6443
  to:
    kind: Service
    name: ${VM_NAME}
  tls:
    termination: passthrough
---
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: ${VM_NAME}-apps-oauth
  namespace: ${VM_NAMESPACE}
spec:
  host: oauth-openshift.${VM_NAME}-${VM_NAMESPACE}.${ROUTE_DOMAIN}
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
  host: console-openshift-console.${VM_NAME}-${VM_NAMESPACE}.${ROUTE_DOMAIN}
  port:
    targetPort: 443
  to:
    kind: Service
    name: ${VM_NAME}
  tls:
    termination: passthrough
EOF
else
  dlog "> Creating Kubernetetes Ingress - this only works with ingress-nginx"

  while [ -z "${INGRESS_NGINX_IP}" ]; do
    export INGRESS_NGINX_IP=$(oc get svc -n ingress-nginx nginx-ingress-ingress-nginx-controller -o jsonpath={.status.loadBalancer.ingress[0].ip})
  done
  export ROUTE_DOMAIN="${INGRESS_NGINX_IP}.nip.io"
  cat <<EOF | oc apply -f -
apiVersion: networking.k8s.io/v1beta1
kind: Ingress
metadata:
  name: ${VM_NAME}-api
  namespace: ${VM_NAMESPACE}
  annotations:
    kubernetes.io/ingress.allow-http: "false"
    nginx.ingress.kubernetes.io/ssl-passthrough: "true"
    nginx.ingress.kubernetes.io/backend-protocol: "HTTPS"
spec:
  rules:
  - host: api.${VM_NAME}-${VM_NAMESPACE}.${ROUTE_DOMAIN}
    http:
      paths:
      - path: /
        backend:
          serviceName: ${VM_NAME}
          servicePort: 6443
---
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
  - host: oauth-openshift.${VM_NAME}-${VM_NAMESPACE}.${ROUTE_DOMAIN}
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
  - host: console-openshift-console.${VM_NAME}-${VM_NAMESPACE}.${ROUTE_DOMAIN}
    http:
      paths:
      - path: /
        backend:
          serviceName: ${VM_NAME}
          servicePort: 443
EOF
fi

dlog "> Waiting for ${VM_NAME} VM to start booting"
until [ "true" == "$(oc get virtualmachine ${VM_NAME} -n ${VM_NAMESPACE} -o jsonpath={.status.ready})" ]; do
  dlog -n "."
  sleep 5
done
dlog ""

dlog "> Starting SSH proxy pod"
oc get pod multitool-${VM_NAME} -n ${VM_NAMESPACE} 1>/dev/null 2>/dev/null || oc run -n ${VM_NAMESPACE} -it --attach=false --restart=Never multitool-${VM_NAME} --image=praqma/network-multitool
oc wait -n ${VM_NAMESPACE} --for=condition=Ready pod/multitool-${VM_NAME} 1>/dev/null

export OCRSH="oc rsh -n ${VM_NAMESPACE} multitool-${VM_NAME}"

dlog "> Copying SSH private key into proxy pod"
${OCRSH} sh -c "cat <<EOF > id_rsa_crc && chmod 0600 id_rsa_crc
-----BEGIN OPENSSH PRIVATE KEY-----
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
4GkUdjSO9DQoUAAAAEY29yZQECAwQFBgc=
-----END OPENSSH PRIVATE KEY-----
EOF" 2>/dev/null

export CRCSSH="ssh core@${VM_NAME}.${VM_NAMESPACE} -q -p 2022 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i id_rsa_crc"

dlog "> Waiting for ${VM_NAME} VM to finish booting"
until ${OCRSH} sh -c "${CRCSSH} pwd" 1>/dev/null 2>/dev/null; do sleep 5; done

cat <<EOF > crcStartKubelet.sh
HOSTNAME=\$1

set -e

echo "> Setting up DNS and starting kubelet in CRC VM"
echo ">> Setting up dnsmasq.conf"
echo "user=root
port= 53
bind-interfaces
expand-hosts
log-queries
srv-host=_etcd-server-ssl._tcp.crc.testing,etcd-0.crc.testing,2380,10
local=/crc.testing/
domain=crc.testing
address=/apps-crc.testing/10.0.2.2
address=/${VM_NAME}-${VM_NAMESPACE}.${ROUTE_DOMAIN}/10.0.2.2
address=/etcd-0.crc.testing/10.0.2.2
address=/api.crc.testing/10.0.2.2
address=/api-int.crc.testing/10.0.2.2
address=/\$HOSTNAME.crc.testing/192.168.126.11" > /var/srv/dnsmasq.conf

echo ">> Starting dnsmasq container in CRC VM"
podman rm -f dnsmasq 2>/dev/null || true
rm -f /var/lib/cni/networks/podman/10.88.0.8
podman run  --ip 10.88.0.8 --name dnsmasq -v /var/srv/dnsmasq.conf:/etc/dnsmasq.conf -p 53:53/udp --privileged -d quay.io/crcont/dnsmasq:latest

echo ">> Updating resolv.conf in CRC VM"
echo "# Generated by CRC
search crc.testing
nameserver 10.88.0.8
\$(cat /tmp/nameserver)
" > /etc/resolv.conf

echo ">> Verifying DNS setup in CRC VM"
until host -R 3 foo.apps-crc.testing; do sleep 1; done;
until host -R 3 quay.io; do sleep 1; done;

echo ">> Starting Kubelet in CRC VM"
systemctl start kubelet

EOF

oc cp crcStartKubelet.sh ${VM_NAMESPACE}/multitool-${VM_NAME}:/tmp/ 2>/dev/null
rm crcStartKubelet.sh

${OCRSH} scp -P 2022 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i id_rsa_crc /tmp/crcStartKubelet.sh core@${VM_NAME}.${VM_NAMESPACE}:/tmp/crcStartKubelet.sh 1>/dev/null 2>/dev/null

${OCRSH} sh -c "cat /etc/resolv.conf | grep nameserver > /tmp/nameserver" 2>/dev/null
${OCRSH} scp -P 2022 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i id_rsa_crc /tmp/nameserver core@${VM_NAME}.${VM_NAMESPACE}:/tmp/nameserver 1>/dev/null 2>/dev/null

${OCRSH} sh -c "${CRCSSH} sudo sh /tmp/crcStartKubelet.sh \$\(hostname\)" 1>/dev/null 2>/dev/null

dlog "> Updating pull secret and cluster ID"

# Ensure pull-secret gets copied to the node itself
oc cp $PULL_SECRET_FILE ${VM_NAMESPACE}/multitool-${VM_NAME}:/tmp/pull-secret 2>/dev/null
${OCRSH} scp -P 2022 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i id_rsa_crc /tmp/pull-secret core@${VM_NAME}.${VM_NAMESPACE}:/tmp/pull-secret 1>/dev/null 2>/dev/null
${OCRSH} sh -c "${CRCSSH} \"sudo sh -c 'cp /tmp/pull-secret /var/lib/kubelet/config.json && chmod 0600 /var/lib/kubelet/config.json'\"" 2>/dev/null

export KUBECONFIGFILE="kubeconfig-${VM_NAME}-${VM_NAMESPACE}"

while [ -z "${CRC_API_SERVER}" ]; do
  if ${IS_OS}; then
    export CRC_API_SERVER=$(oc get route ${VM_NAME}-api -n ${VM_NAMESPACE} -o jsonpath={.spec.host} || echo '')
  else
    export CRC_API_SERVER="api.${VM_NAME}-${VM_NAMESPACE}.${ROUTE_DOMAIN}"
  fi
done

cat <<EOF > $KUBECONFIGFILE
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUM3VENDQWRXZ0F3SUJBZ0lCQVRBTkJna3Foa2lHOXcwQkFRc0ZBREFtTVNRd0lnWURWUVFEREJ0cGJtZHkKWlhOekxXOXdaWEpoZEc5eVFERTFPVEV6TlRneE5UTXdIaGNOTWpBd05qQTFNVEUxTlRVeVdoY05Nakl3TmpBMQpNVEUxTlRVeldqQW1NU1F3SWdZRFZRUUREQnRwYm1keVpYTnpMVzl3WlhKaGRHOXlRREUxT1RFek5UZ3hOVE13CmdnRWlNQTBHQ1NxR1NJYjNEUUVCQVFVQUE0SUJEd0F3Z2dFS0FvSUJBUURTOUtJNFhUZHJRblMvTkdGS2thTGcKZStvdmEwSWxHYjNsbE5QVnJnZTBwdlNGNTRUakFUQlpOc2hOekRQN1huVkRYUFZ0VlU4OXNMTHZjZDJDSHFLaApSR1pHdnFCMGJlTmowZ2dnTlNWU3RBc1NCUSt2Smp2TTN2bS91R25nR3FxZGdXcUdPbGV1YUoxUlNTZUZwa2VLCmIvMGttbFZWRStoUHVZbXFjL1ErditiU0w0Um5Fb2pSRGU2QzdtZ2U4M2pGd0xmTjJjR3dpVjFjUG9kZFgrVEYKb2F5Y0xVaEh0SjZnTVN6SkZ1c1Z4Z3RPOFpRdkR1UXRPQ0ZLVUhWS2NDM3JpR096VUE3WkxxMWF3ZzRVRmJJTgoxODl4QkhPRnNlZWE5RjRXckZJWXBEZVF6a3BUeHJ2VnBuZ2wyRkZ3eGNTU1hLL0Y2WFZtY3g1SFNnZEsrY3pCCkFnTUJBQUdqSmpBa01BNEdBMVVkRHdFQi93UUVBd0lDcERBU0JnTlZIUk1CQWY4RUNEQUdBUUgvQWdFQU1BMEcKQ1NxR1NJYjNEUUVCQ3dVQUE0SUJBUUJlMjZmUnFsZFhFdE5mWEdYaVhtYStuaVhnMmRtQ2g2azdXYUNrMkdGNgpHbHhZMDNkcmNYeXpwUzRTT2Rac2VqaVBwVU9ubTgwdnZBai9LaWZmakxDUDIvUDBUT2w3cCtlNTBFbGFaZVIvCjMxRjRDMzdZYW5VbFV3YVVUblFtUXRSd002Szl2QWRiRUZ5SWVHV1AraU04TFFFUnRYRXA4M0tJS1BQbjVPd2YKNjBrUXBLSWRKL2ttR3pwRUllS0FVTmpITTgyM01JU3FZd21yVDN3elBmankxZEpyeUtXNGdLazZTVmJqVUZXTwp6UFpyMVk0Tmd0aG5HSFRvbnhNYWkxRDhZa2cvM3k0TWt3Q3FKWHk0ZlJEdnRpMklMaG5xNWx4RExzOThwaU1BCmRhMVdveWNHSlNWdHYySHkwKzg2amNFelo3T01mRGllSnRRdVpaUzgrdjVKCi0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0KLS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUM3VENDQWRXZ0F3SUJBZ0lCQVRBTkJna3Foa2lHOXcwQkFRc0ZBREFtTVNRd0lnWURWUVFEREJ0cGJtZHkKWlhOekxXOXdaWEpoZEc5eVFERTFPVEV6TlRneE5UTXdIaGNOTWpBd05qQTFNVEUxTlRVeVdoY05Nakl3TmpBMQpNVEUxTlRVeldqQW1NU1F3SWdZRFZRUUREQnRwYm1keVpYTnpMVzl3WlhKaGRHOXlRREUxT1RFek5UZ3hOVE13CmdnRWlNQTBHQ1NxR1NJYjNEUUVCQVFVQUE0SUJEd0F3Z2dFS0FvSUJBUURTOUtJNFhUZHJRblMvTkdGS2thTGcKZStvdmEwSWxHYjNsbE5QVnJnZTBwdlNGNTRUakFUQlpOc2hOekRQN1huVkRYUFZ0VlU4OXNMTHZjZDJDSHFLaApSR1pHdnFCMGJlTmowZ2dnTlNWU3RBc1NCUSt2Smp2TTN2bS91R25nR3FxZGdXcUdPbGV1YUoxUlNTZUZwa2VLCmIvMGttbFZWRStoUHVZbXFjL1ErditiU0w0Um5Fb2pSRGU2QzdtZ2U4M2pGd0xmTjJjR3dpVjFjUG9kZFgrVEYKb2F5Y0xVaEh0SjZnTVN6SkZ1c1Z4Z3RPOFpRdkR1UXRPQ0ZLVUhWS2NDM3JpR096VUE3WkxxMWF3ZzRVRmJJTgoxODl4QkhPRnNlZWE5RjRXckZJWXBEZVF6a3BUeHJ2VnBuZ2wyRkZ3eGNTU1hLL0Y2WFZtY3g1SFNnZEsrY3pCCkFnTUJBQUdqSmpBa01BNEdBMVVkRHdFQi93UUVBd0lDcERBU0JnTlZIUk1CQWY4RUNEQUdBUUgvQWdFQU1BMEcKQ1NxR1NJYjNEUUVCQ3dVQUE0SUJBUUJlMjZmUnFsZFhFdE5mWEdYaVhtYStuaVhnMmRtQ2g2azdXYUNrMkdGNgpHbHhZMDNkcmNYeXpwUzRTT2Rac2VqaVBwVU9ubTgwdnZBai9LaWZmakxDUDIvUDBUT2w3cCtlNTBFbGFaZVIvCjMxRjRDMzdZYW5VbFV3YVVUblFtUXRSd002Szl2QWRiRUZ5SWVHV1AraU04TFFFUnRYRXA4M0tJS1BQbjVPd2YKNjBrUXBLSWRKL2ttR3pwRUllS0FVTmpITTgyM01JU3FZd21yVDN3elBmankxZEpyeUtXNGdLazZTVmJqVUZXTwp6UFpyMVk0Tmd0aG5HSFRvbnhNYWkxRDhZa2cvM3k0TWt3Q3FKWHk0ZlJEdnRpMklMaG5xNWx4RExzOThwaU1BCmRhMVdveWNHSlNWdHYySHkwKzg2amNFelo3T01mRGllSnRRdVpaUzgrdjVKCi0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0KLS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURRRENDQWlpZ0F3SUJBZ0lJTGlKYklDbE9RaEl3RFFZSktvWklodmNOQVFFTEJRQXdQakVTTUJBR0ExVUUKQ3hNSmIzQmxibk5vYVdaME1TZ3dKZ1lEVlFRREV4OXJkV0psTFdGd2FYTmxjblpsY2kxc2IyTmhiR2h2YzNRdApjMmxuYm1WeU1CNFhEVEl3TURZd05URXhNVGcxTlZvWERUTXdNRFl3TXpFeE1UZzFOVm93UGpFU01CQUdBMVVFCkN4TUpiM0JsYm5Ob2FXWjBNU2d3SmdZRFZRUURFeDlyZFdKbExXRndhWE5sY25abGNpMXNiMk5oYkdodmMzUXQKYzJsbmJtVnlNSUlCSWpBTkJna3Foa2lHOXcwQkFRRUZBQU9DQVE4QU1JSUJDZ0tDQVFFQTdMQXlHY3NiaUc1OApDSzRUUUZQQ3cwVUc3ems0SVhTTDlKV0U1SjlMUHQycW12azdjR2hLTnlGL0NIZElodk0zY2tZM2dLcnhkSlZMCjhLWnJYbUxnRVlXM1hUYzFMWjc5TG5UQmt0RWFVMTFvVU1kMkVFaUh2WFVrSFJKaUNNNzFhOTZQOEFkZUZFQloKNEhnQkR5V3ZGcUFFWWlpSnc4M1hxYUVNdXBGUDJFM0ZTTjEra291Sk9BbE1OaDZHcEdvdVlNMGlMek5SKzVtSQpvRFdKUW92bk9OWlorb0l0MDBEQ1kwZHA5V3FqWEhGSzZuNmg5QXNldG4yS0dmZkVKS2ZoQzdOWDBnRW9yK1dICk5wa3Z6SG4wRjlaSkRjUGtoYm0vcHM0TGdDcWdMalBhTkR3RTFsMmZBNjkwTkJJZVZtQ0gxaGZ2SlhYM0ozbzEKVkV4UC80T3gzd0lEQVFBQm8wSXdRREFPQmdOVkhROEJBZjhFQkFNQ0FxUXdEd1lEVlIwVEFRSC9CQVV3QXdFQgovekFkQmdOVkhRNEVGZ1FVM2NCcEsrb2wweTFmZ211UUhnTE1xeDRFNW9Bd0RRWUpLb1pJaHZjTkFRRUxCUUFECmdnRUJBRFF5QkI0eUNjYjBvZmtpODZCU2piODJydVc2ckFoWVQ0cTljZnJWY2ZhdEs0ZURxSFAzMWROQTdRUmEKeFdmbCtsMFd6dkVmT2dVOGMxUDhSRE1NampubitteDdobnZOaUgwQ0xnL3R1RUFmRlZzZFZKYlNqMk5rZTAxRwpTN09RUkVmOGJkQklucmNkM0xiYThMU084MDhic0V0WmdnZG13RndBUWsvdWRYN1d2SUlQVkppeTZCeGpWZ0FWClJYaDZFcUxQaUlWTDJ3b0YwVGNHSXpGNE5UMlUzcWNLM2NKdUVjM1lzdkkrck1tWEJDT25FY0N2MzB2a1d0NmsKNnV4cEdOWGxidlRxekZTY3NZL09zZEVUeGpBVHhxTUEzZ2FLR000OTFnM3REUXhaUVNNRlJTMjRMYlFhSjJDQgpFbWUzMFhlempvdTNUNytyVWx1S1dCd3luUzg9Ci0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0KLS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURURENDQWpTZ0F3SUJBZ0lJTUwzZTJ4ZG9Xd0F3RFFZSktvWklodmNOQVFFTEJRQXdSREVTTUJBR0ExVUUKQ3hNSmIzQmxibk5vYVdaME1TNHdMQVlEVlFRREV5VnJkV0psTFdGd2FYTmxjblpsY2kxelpYSjJhV05sTFc1bApkSGR2Y21zdGMybG5ibVZ5TUI0WERUSXdNRFl3TlRFeE1UZzFOVm9YRFRNd01EWXdNekV4TVRnMU5Wb3dSREVTCk1CQUdBMVVFQ3hNSmIzQmxibk5vYVdaME1TNHdMQVlEVlFRREV5VnJkV0psTFdGd2FYTmxjblpsY2kxelpYSjIKYVdObExXNWxkSGR2Y21zdGMybG5ibVZ5TUlJQklqQU5CZ2txaGtpRzl3MEJBUUVGQUFPQ0FROEFNSUlCQ2dLQwpBUUVBeDNGL3pVd0tZanMvSDZIaG9NWWxqamlRVDg0eklMOGVTVmZGVEZJMVUwZVcxbDV1akp2Wlk3bDRnZyt3CmlybEtFRUtoWmtXNUpWbEZwaVpxbW8yR0lPc3JJWGoxL0Fpc1VVcWdXUUtEWHVDUnhWNitCeE5xM1Z6d1VJUDYKNDA4L2o4Z0tPZGp6NVRTTFUwa0VIYmVLc0FYYmNZSUVYQXorTDBEdFo4WWx0NnpJc2hjaCs0RGxMUDR3R0NVMAowNWx3d2dEcUZUOE1lUEhzb0pISFRTcDFxT0V4Z2E5bjBzTGMxTGFXUkJabzBGMmNleWVqdy95bUlwUkNpVHc4CmZvR0cwYm4ydWZFOG9TWkRpMUZSa0JJQ2puNWlLdlkxNFR0VVpCN2RiMXFlZmZ5MStmZThJTElRdEphcStVaDkKL3R2QnUyYVJITlQyWnV1MUNvRDNlaWhqdlFJREFRQUJvMEl3UURBT0JnTlZIUThCQWY4RUJBTUNBcVF3RHdZRApWUjBUQVFIL0JBVXdBd0VCL3pBZEJnTlZIUTRFRmdRVXJpbGxEUHhFVHFQbVVuUHZrWTArSVBvYXErTXdEUVlKCktvWklodmNOQVFFTEJRQURnZ0VCQU1TTExxR0NObmhEZUhScWtaUWEvNktUR21KZVpEc1F3MURHRUpYc1VMVloKcXpRODFjZUtreEVDQVByK2hzcmRORVB6bEhDbUpGQXVYczBXQ0lRN1NvaUNJcmYzQnV6cVNIK2QxME9sZjlkdApXS1lSTmg1UXVaODgxWWhDNDZsZ3hZVjk5RjU3WW5ROG8rellaN2ZDUTMreGRGbytudXNRYys4K2tQM1VGcUJtCjd4Q2V2MmtJUm1RT005c05WUTcrdnBkb2I2dTJwN1VLSFplQmd6Q2h2ODhXclF4M2lIOUlob2J5bnkyREk1Y1UKS1BQbWNVQlY1Y2pualZkVVp2SW1wNDVqcEwvWUNLam5OQjNWNmVOQ2ROSnozZWh4Y3B2bFFMdVgycUhGdzdGTQozRWU1UlRMbnl6SEFTVFQramRJQUVvUUpTSnNHNFJiZ0g3WDVZd2laVDI0PQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCi0tLS0tQkVHSU4gQ0VSVElGSUNBVEUtLS0tLQpNSUlETWpDQ0FocWdBd0lCQWdJSUczR3l1WWpVaU9nd0RRWUpLb1pJaHZjTkFRRUxCUUF3TnpFU01CQUdBMVVFCkN4TUpiM0JsYm5Ob2FXWjBNU0V3SHdZRFZRUURFeGhyZFdKbExXRndhWE5sY25abGNpMXNZaTF6YVdkdVpYSXcKSGhjTk1qQXdOakExTVRFeE9EVTJXaGNOTXpBd05qQXpNVEV4T0RVMldqQTNNUkl3RUFZRFZRUUxFd2x2Y0dWdQpjMmhwWm5ReElUQWZCZ05WQkFNVEdHdDFZbVV0WVhCcGMyVnlkbVZ5TFd4aUxYTnBaMjVsY2pDQ0FTSXdEUVlKCktvWklodmNOQVFFQkJRQURnZ0VQQURDQ0FRb0NnZ0VCQU03K1J1Y3pEWUpXVjAvK1FHaHNuTUUrV2dlcUJERS8KNDkrSUkyUEttL01rV0NDTlRYNU1yM05mZ0RKSDlMRDBRRzZMZlc3Q0lVWFZjeWZ4ZDd1WVNETERwVmJJSVRyQgpoa1g0WmdtQTd6d09RcUQ4SElhZUp0QmJ5ZnhaWFpBbkNoSlVMU2JscDFYa2NnTEVlVm5hTHZwOVF6QkpCOHRDCmRPczRqbFpkSmRXcm9GYlZJUUVJRHBYT0k4L0diY0dZTXd6cXRiNFRzUVFzNWZ6MG5FVlI0eXoveE9wN0xRQ1EKbVlwWER4cUhFbzVXb0w1Vk5YSGZmWUVRRE9WeVlTeDNEL21FYkd1QzNrUWdXd3F1LzFZeU1sbkFVN2djbDNMWQovSFg1bVRmSVRKVG1VKzJlOHFoNHZMTEVMckVVU2E4alVzR1cyUHIvSmx1NklPei9kR3d0OTdVQ0F3RUFBYU5DCk1FQXdEZ1lEVlIwUEFRSC9CQVFEQWdLa01BOEdBMVVkRXdFQi93UUZNQU1CQWY4d0hRWURWUjBPQkJZRUZDR0UKcmpMWk1wNk41a202NWZZUWVQalNnaDVqTUEwR0NTcUdTSWIzRFFFQkN3VUFBNElCQVFCMm1LVVRqNi9WUG5VRQoyeUI3NEtPQ3VBaldkU1JrVHpGU3JCNTFmZHBkQmZJSU1mRDRYai9ZRUkxSDZQQ2dZS2lzbWpaRUJ1QVNHQzNBCnNUNjBCVDFJQmFERG9PeWlOelhUUFdTRlVEUkFYeUYzWkJrVGlYM2JjTU9WRzI3VDBXRVRLU1Y3Q2N3N0pPcWEKSXJpQkJpc25RVUlhV0R3SnhEdm5ZUXNBendOWWVBNjRzdDhHNkRWaVF1ZkV1SFpBT0VUYXo5ek55NnNhd2ErUApjUUxVZTFsaVFSNk1EWjhGY2tPL1UxNURyeE16MVBJeUNiRW43LzVGVzFsL0lENkZBM05MQ3Yxa1hvTVdXd0dVCmpIYzRQMmZzVHBGbStGODNsdDlDcCtQWU92N1Jkdm1QQytUTSs1NW45djVES2p1SkVBZWtyNUdsN0NZcVJxaU8KTjdXdzNMc0YKLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo=
    server: https://${CRC_API_SERVER}
  name: crc
contexts:
- context:
    cluster: crc
    user: admin
  name: admin
current-context: admin
kind: Config
preferences: {}
users:
- name: admin
  user:
    client-certificate-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURaekNDQWsrZ0F3SUJBZ0lJTW9yOCtncGEwWGd3RFFZSktvWklodmNOQVFFTEJRQXdOakVTTUJBR0ExVUUKQ3hNSmIzQmxibk5vYVdaME1TQXdIZ1lEVlFRREV4ZGhaRzFwYmkxcmRXSmxZMjl1Wm1sbkxYTnBaMjVsY2pBZQpGdzB5TURBMk1EVXhNVEU0TlRWYUZ3MHpNREEyTURNeE1URTROVFZhTURBeEZ6QVZCZ05WQkFvVERuTjVjM1JsCmJUcHRZWE4wWlhKek1SVXdFd1lEVlFRREV3eHplWE4wWlcwNllXUnRhVzR3Z2dFaU1BMEdDU3FHU0liM0RRRUIKQVFVQUE0SUJEd0F3Z2dFS0FvSUJBUURzUHE3VDZWNS9JeWwzSlR6ais2REg4aFZqR0tGUWZGS3dya3l0NTNLNwprbHVKbXF1WXpIUDUwSHg5RDc2V2FVM0V5cmZJNWl1MElFOFhiQXcvUittT2M3QWErOHJqTWliVFc0UHFsSVZ3CkFNQTlLOExybG5HVnJvdmlaQ0Z3QmMwM0dZSUVKUENJZno4K25aQzhzSkswbEZteVY1SkY3NDdMY0RyTENTdVkKQnJEemdibWJOcTVjWndQVCsvUHMrZ283T3Q3dXlod25obndmeisyUmxBWFpsMk0zN25SY0ZJOGdBanM1Zjg1UgpNRTJNZk5jVHZLLzFXWThZREZSQ2ZNREtiUXNPR0NWUzFyRFd6MGIxaVJRS3JIVFdSWkNXczBXQWs0SmROODhuClRFdFZCcWtaZEp2dGxRY2dCR3pkMWg2WTVFWVZmOUM3ajlwdHdDc2YwaGozQWdNQkFBR2pmekI5TUE0R0ExVWQKRHdFQi93UUVBd0lGb0RBZEJnTlZIU1VFRmpBVUJnZ3JCZ0VGQlFjREFRWUlLd1lCQlFVSEF3SXdEQVlEVlIwVApBUUgvQkFJd0FEQWRCZ05WSFE0RUZnUVVZRFJXeVRpWUJqUlo2bHppQkxuUEFPM05ZZE13SHdZRFZSMGpCQmd3CkZvQVVHcUJLNmR2Wno1MUhlcjgvUEhPOC95cElydlV3RFFZSktvWklodmNOQVFFTEJRQURnZ0VCQUR6WUdjc3MKaUpOYm1wbHdSUk15cHF2UWMvdCtTcXk4cUhrU2xWSnpwMFN5d3RLVnFKTGh4VXRhZlBpVmlkQlFJZjdFVkZRMApQRG1FdXJidkJWSDNPWUtRZTlmdks1cVdjYmdsenFRS1hwcUxLaElvQ3V5VHZ2azNmT0xDMmdyYjNJTGx1WDlwCnBMVE9YbjV0akR6NlNsSTJYNnB6SjdpZGIvdHJtaVdDYWlNdmNkQ0Qrc0VMUGZzS0h5QWZZZ3RONk9zQ2hxTFYKcHYwRnQwRVZ4dnlFMzc5TkdnWnhyM3doWktGYjJRUFBWRWRVcGZPOFRpRnpWRWFueCtIdWxCZjVkWm1ZMUtmago3TU0xYmtoWUhqcFFyWEhWK2YyVHZLS0FRZHh4SlErODlCajlFK0YrSXl6djlyMFdQZ3JITXJUbTlzYjJpVGllCm9hcnVaZU9GYVJVUS8vTT0KLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo=
    client-key-data: LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQpNSUlFcEFJQkFBS0NBUUVBN0Q2dTArbGVmeU1wZHlVODQvdWd4L0lWWXhpaFVIeFNzSzVNcmVkeXU1SmJpWnFyCm1NeHorZEI4ZlErK2xtbE54TXEzeU9ZcnRDQlBGMndNUDBmcGpuT3dHdnZLNHpJbTAxdUQ2cFNGY0FEQVBTdkMKNjVaeGxhNkw0bVFoY0FYTk54bUNCQ1R3aUg4L1BwMlF2TENTdEpSWnNsZVNSZStPeTNBNnl3a3JtQWF3ODRHNQptemF1WEdjRDAvdno3UG9LT3pyZTdzb2NKNFo4SDgvdGtaUUYyWmRqTis1MFhCU1BJQUk3T1gvT1VUQk5qSHpYCkU3eXY5Vm1QR0F4VVFuekF5bTBMRGhnbFV0YXcxczlHOVlrVUNxeDAxa1dRbHJORmdKT0NYVGZQSjB4TFZRYXAKR1hTYjdaVUhJQVJzM2RZZW1PUkdGWC9RdTQvYWJjQXJIOUlZOXdJREFRQUJBb0lCQUNlSWljc09kM0RCR3BSRQpsLzd5d2NJVDRiNVdoZEFwTGRGQktiWEVVRy9SR3g1WTByUmNLbUE0b2t4dlVRNXNpc1lPd2xpTkkrMGRwdjZkClp5TkR6bkszSzFZb29wZ0ljWFRYRUtrMXQycTV4WEczSEFRK2hiMXRteDBFY3BBRGVJYnE3dFh3dEl1eTk0dHIKNUtlZXlMNE5RVUZWNURWdDFEQjVGRzJibUQ3MU5XRW5KNFhncTUxNUxkY2VUS1dBbm92NURmbmVNcXJqU21oUgpHeHV4RnorbVZyeUowUVIyL3JZY1l5cWRsZFJKQ2REcFdGRndSdDRVbmlLaHVHdEhGZlpTU2ZYU2QvYmhRbnUwCmtmbWh5OFlMMFpURUkvSE9pVWdBUzhFUHVWbWhsbmRESkwxY2ZoRUJNaFQvYXd1TDYvQklZS3gyWWJNTmpjZUUKbjdwVzlRa0NnWUVBK045enRRU296Z3JBYWwyV1Z1ei9rWENsMzlmd0VjZFpaUXV2b2MxaU5ZU1NtUzVLanFtbQpjSmRGNFd6bG0yUUJnUllFT044RE1vL3RQNmF3MmFiYjQ4SW1FYlRjKzFodzJBUThHSGpYNXlqTFkvM0VrT1FxCi9QNWE3QXFhbU9udS83TnplU1FYZERwQzhiWnZsNm5wdkVzallEZi95dWVnVmR6VmRMSktDK01DZ1lFQTh3S20KNWE1NTNDa2I5YUxXbVFOUGFqd25ZVXRHaG5EQUhvaThEV0E4ZTVxYzg1MHRSNTBBVDVNazhYVUF0YjRJbFkvUgpVS3FYY3U0blU5Tk9rYmVndmJoZWZ6ZDNyR0xPQkdlQUhqWmptUTV0T1hMT1Z4SVFJdElvRVBVTGtSclJrRDB2CjF5eVdZYXdhQXlka213Nk5haEowbldQRUxnbU1TcXlqZlBWMnN0MENnWUVBamJ0Yi91d3ZZbUFYSXJ3M29UdUoKZEgrdHg2UUhrV2h4VGExeEVYbVJBNStEaVg4bWNNYkhCZm53anlmZ1B6V2Q4YkRqS0t4QSt1dWlsb3hNelRkTQpwUkh0Y2tvSlM0OGJmTG8wcTA4dXpmT2FtVkJ0UUlMZ3hJSHFyK0IrR0xXcEtiQStBL0I4OXZFekxNclVGSkJzCmo1SlBERDM0QzhzTHNicDVTZU03YmpjQ2dZRUEzSmVFcnl4QnZHT28yTUszc1BCN1Q0RkpjaDFsNkxaQy83UzUKbUI3SzZKMENhblk4V3l5ZTBwMU14TTZrRlZacTduRTkzYzd0YWN2YjhWRDRtbmdwTnU4OUFKaDJUd3JsM3NPaApYa3VhLzU1RDhnbFFXMk92T0J5emVDa3BGZEJWZVd6Qmw3OEd4NlQxZS9WdmN2Mnp5eHp6dE1lU2x3UGQwUStECjNQUHBpeFVDZ1lBRVE5VFI4Z0s0V0NuTUxWSUxzWGZUUlVEdjRram9Ob3prT0JwVG5nejN4T0dRNW9vajNMVm0KTEtJU1VTeU15SkJNRCtNU2dnMVd1SkEzYUdGTWdMcndnRS9HaXQvTEtjaW8rMjFUVjdUQUtsZWJJMGd5SFdGbgpRSEtmOWVrTTl3Ym5xa0VPUmJ1cUJ0UUxsTWJjeXI0cHF0V3FjU3lkd1M5czllL2hTaGVCMWc9PQotLS0tLUVORCBSU0EgUFJJVkFURSBLRVktLS0tLQo=
EOF

export OCCRC="oc --insecure-skip-tls-verify --kubeconfig $KUBECONFIGFILE"

until ${OCCRC} get secret pull-secret -n openshift-config 1>/dev/null; do sleep 5;done
${OCCRC} patch secret pull-secret -p "{\"data\":{\".dockerconfigjson\":\"$(cat $PULL_SECRET_FILE | base64 -w 0)\"}}" -n openshift-config --type merge 1>/dev/null

until ${OCCRC} get clusterversion version 1>/dev/null; do sleep 5;done
${OCCRC} patch clusterversion version -p "{\"spec\":{\"clusterID\":\"$(uuidgen)\"}}" --type merge 1>/dev/null

sleep 2

dlog "> Waiting on requestheader-client-ca-file"
while [ -z "$(${OCCRC} get configmaps/extension-apiserver-authentication -o jsonpath={.data.requestheader-client-ca-file} -n kube-system)" ]; do
  dlog -n "."
  sleep 2
done
dlog ""

dlog "> Restarting openshift-apiserver"
${OCCRC} delete pod --all -n openshift-apiserver 1>/dev/null

dlog "> Approving pending CSRs"
until ${OCCRC} get csr 1>/dev/null; do
  dlog -n "."
  sleep 2
done
dlog ""
for csr in $(${OCCRC} get csr | grep Pending | awk '{print $1'}); do
  ${OCCRC} adm certificate approve ${csr}
done

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

OLD_ROUTE_DOMAIN=$(${OCCRC} get ingresscontroller default -n openshift-ingress-operator -o jsonpath={.status.domain})
if [ "${VM_NAME}-${VM_NAMESPACE}.${ROUTE_DOMAIN}" != "${OLD_ROUTE_DOMAIN}" ]; then
  dlog "> Updating default Ingress domain"
  ${OCCRC} patch ingress.config.openshift.io cluster -p "{\"spec\": {\"domain\": \"${VM_NAME}-${VM_NAMESPACE}.${ROUTE_DOMAIN}\"}}" --type merge 1>/dev/null

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
  domain: ${VM_NAME}-${VM_NAMESPACE}.${ROUTE_DOMAIN}
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
until ${OCCRC} patch route -n openshift-console console -p "{\"spec\": {\"host\": \"console-openshift-console.${VM_NAME}-${VM_NAMESPACE}.${ROUTE_DOMAIN}\"}}" --type=merge 1>/dev/null; do
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

dlog "> Removing SSH proxy pod"
oc delete pod multitool-${VM_NAME} -n ${VM_NAMESPACE} 1>/dev/null 2>/dev/null

if [ "true" == "$DEBUG" ]; then
  ${OCCRC} get pod --all-namespaces
fi

CRC_CONSOLE="https://$(${OCCRC} get route -n openshift-console console -o jsonpath={.spec.host})"

log "> CRC cluster is up!

Connect as kube:admin on the CLI using:
${OCCRC}

Connect as developer on the CLI using:
oc login --insecure-skip-tls-verify https://${CRC_API_SERVER} -u developer -p developer

Access the console at: ${CRC_CONSOLE}
Login as kube:admin with kubeadmin/DEP6h-PvR7K-7fYqe-IhLUP
Login as developer with developer/developer
"

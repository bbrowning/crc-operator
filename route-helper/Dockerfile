FROM registry.access.redhat.com/ubi8/ubi:latest

ENV USER_UID=1001 \
    OC_VERSION=4.4.5

RUN curl -L -o openshift-client-linux-${OC_VERSION}.tar.gz https://mirror.openshift.com/pub/openshift-v4/clients/ocp/${OC_VERSION}/openshift-client-linux-${OC_VERSION}.tar.gz \
  && tar -xzf openshift-client-linux-${OC_VERSION}.tar.gz \
  && mv oc /usr/local/bin/

COPY copyRoutes.sh /usr/local/bin

RUN mkdir -p /.kube/cache && chmod 0777 /.kube/cache

ENTRYPOINT ["/usr/local/bin/copyRoutes.sh"]

USER ${USER_UID}

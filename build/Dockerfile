FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

ENV OPERATOR=/usr/local/bin/crc-operator \
    USER_UID=1001 \
    USER_NAME=crc-operator

RUN microdnf update -y && rm -rf /var/cache/yum
RUN microdnf install openssl -y && microdnf clean all

# install operator binary
COPY build/_output/bin/crc-operator ${OPERATOR}

COPY build/bin /usr/local/bin
RUN  /usr/local/bin/user_setup

ENTRYPOINT ["/usr/local/bin/entrypoint"]

USER ${USER_UID}

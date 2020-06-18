# Release 0.0.2
- Don't wait for the community-operator or certified-operator pods in
  the openshift-marketplace namespace to become Ready before declaring
  the CrcCluster as Ready. These pods may crashloop for some time
  after the rest of the cluster is ready if multiple clusters are
  started up around the same time on a shared parent cluster where
  each CRC cluster appears as the same IP to quay.io, thus causing
  rate-limiting.

# Release 0.0.1
- Initial release

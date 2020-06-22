# Release 0.1.0
- A new `CrcBundle` API controls which CRC bundles are available for
  use. The default installation installs bundles for recent versions
  of OCP.
- There is now optionally persistent storage with configurable disk
  size for CRC clusters. The default is ephemeral (not persistent)
  storage. Set `spec.storage.persitent` to `true` in your `CrcCluster`
  to enable persistence. Set `spec.storage.size` to a value like
  `100Gi` to specify the size of the persistent storage. The
  underlying Node in the CRC cluster will automatically grow its root
  partition to the specified size.

# Release 0.0.3
- You may now specify which CRC bundle (and thus OCP version) to start
  with a new `bundleName` field in the CRD spec. Valid bundle names
  are `ocp445` for OCP 4.4.5, `ocp450rc1` for OCP 4.5.0-rc.1, and
  `ocp450rc2` for OCP 4.5.0-rc.2.
- All the default OpenShift Routes are now updated to use the real
  domain. Previously, the image registry, console downloads, and
  various monitoring routes still had the old *.crc.testing domain.
- The API Server URL as shown in the Console overview and 'Copy Login
  Command' screens is now correct.

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

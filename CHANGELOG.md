# Release 0.5.1
- Fixed OCP 4.3 / K8s 1.16 compatibility in route-helper pod by
  explicitly using `deployment.apps` instead of `deployment` in `oc`
  commands

# Release 0.5.0
- Release artifacts now split out the default bundles from the rest of
  the release. This allows a user to update the operator without
  overwriting their bundle configurations.
- Add a new `spec.url` field to `CrcBundle` resources to specify an
  optional URL that will be used, if given, when provisioning
  persistent clusters. This URL should point to a qcow2 or raw virtual
  machine image over HTTP or HTTPS. This significantly speeds up the
  initial provisioning time of persistent clusters compared to using a
  container image. Container images are still used for all
  non-persistent clusters.

# Release 0.4.3
- Added an OpenShift 4.5.0-rc.6 bundle in the default install

# Release 0.4.2
- Added an OpenShift 4.5.0-rc.5 bundle in the default install

# Release 0.4.1
- The operator will no longer wait for pods in the
  `openshift-operators` namespace to come up before marking a cluster
  as ready. These pods are cluster-wide optional operators and not
  required for core functioning of the cluster.

# Release 0.4.0
- Added an OpenShift 4.5.0-rc.4 bundle
- Every cluster gets a unique SSH key generated for the Node when
  starting the cluster and the default one from the bundle revoked.

# Release 0.3.1
- Added an OpenShift 4.4.8 bundle in the default install

# Release 0.3.0
- You can now enable monitoring on CrcClusters with the new
  `spec.enableMonitoring` *bool field. A value of true enables
  monitoring, a value of false disables it, and an unset value doesn't
  express an opinion.

# Release 0.2.1
- The route helper pod is now cleaned up when stopping a cluster and
  redeployed when starting that cluster again.

# Release 0.2.0
- You can now explicitly stop (but not delete) CrcClusters with the
  new `spec.stopped` boolean field. A value of true stops the cluster,
  a value of false starts the cluster. The default is false, meaning
  running. There's a corresponding `status.stopped` that can be read
  to know when the operator has actually stopped the cluster. This can
  work for clusters with ephemeral or persistent storage, but
  ephemeral clusters lose all their data between the stop and
  start. Persistent clusters retain their data.
- Only pods in the `openshift-*` or `kube-*` namespaces are considered
  when checking for cluster readiness. The starting and stopping work
  above exposed a problem where any random pod in the cluster could
  cause the operator to wait forever for readiness when it may be that
  random user pods were not coming up cleanly before stopping the
  cluster.

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

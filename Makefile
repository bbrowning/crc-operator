RELEASE_REGISTRY ?= "quay.io/bbrowning"

operator-dev:
	@operator-sdk build $(RELEASE_REGISTRY)/crc-operator:vdev
	@podman push $(RELEASE_REGISTRY)/crc-operator:vdev

release:
	@echo "Checking for RELEASE_VERSION environment variable..."
	[ ! -z "$(RELEASE_VERSION)" ]
	@if [ "$(RELEASE_VERSION)" != "dev" ]; then sed -i -e "s/Version = \".*\"/Version = \"$(RELEASE_VERSION)\"/" version/version.go; fi
	@podman build route-helper -t $(RELEASE_REGISTRY)/crc-operator-routes-helper:v$(RELEASE_VERSION)
	@podman push $(RELEASE_REGISTRY)/crc-operator-routes-helper:v$(RELEASE_VERSION)
	@operator-sdk build $(RELEASE_REGISTRY)/crc-operator:v$(RELEASE_VERSION)
	@podman push $(RELEASE_REGISTRY)/crc-operator:v$(RELEASE_VERSION)
	@cat deploy/service_account.yaml > deploy/releases/release-v$(RELEASE_VERSION).yaml
	@echo -e "\n---\n" >> deploy/releases/release-v$(RELEASE_VERSION).yaml
	@cat deploy/role.yaml >> deploy/releases/release-v$(RELEASE_VERSION).yaml
	@echo -e "\n---\n" >> deploy/releases/release-v$(RELEASE_VERSION).yaml
	@cat deploy/role_binding.yaml >> deploy/releases/release-v$(RELEASE_VERSION).yaml
	@echo -e "\n---\n" >> deploy/releases/release-v$(RELEASE_VERSION).yaml
	@cat deploy/operator.yaml | sed -e "s|REPLACE_IMAGE|quay.io/bbrowning/crc-operator:v$(RELEASE_VERSION)|g" -e "s|REPLACE_ROUTES_HELPER_IMAGE|quay.io/bbrowning/crc-operator-routes-helper:v$(RELEASE_VERSION)|g" >> deploy/releases/release-v$(RELEASE_VERSION).yaml
	@echo -e "\n---\n" >> deploy/releases/release-v$(RELEASE_VERSION).yaml >> deploy/releases/release-v$(RELEASE_VERSION).yaml
	@cat deploy/crds/crc.developer.openshift.io_v1alpha1_crcbundle_cr.yaml >> deploy/releases/release-v$(RELEASE_VERSION).yaml
	@cat deploy/crds/crc.developer.openshift.io_crcclusters_crd.yaml > deploy/releases/release-v$(RELEASE_VERSION)_crd.yaml
	@echo -e "\n---\n" >> deploy/releases/release-v$(RELEASE_VERSION)_crd.yaml
	@cat deploy/crds/crc.developer.openshift.io_crcbundles_crd.yaml >> deploy/releases/release-v$(RELEASE_VERSION)_crd.yaml

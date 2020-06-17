RELEASE_REGISTRY ?= "quay.io/bbrowning"

release:
	@echo "Checking for RELEASE_VERSION environment variable..."
	[ ! -z "$(RELEASE_VERSION)" ]
	@sed -i -e "s/Version = \".*\"/Version = \"$(RELEASE_VERSION)\"/" version/version.go
	@podman build route-helper -t $(RELEASE_REGISTRY)/crc-operator-routes-helper:v$(RELEASE_VERSION)
	@podman push $(RELEASE_REGISTRY)/crc-operator-routes-helper:v$(RELEASE_VERSION)
	@operator-sdk build $(RELEASE_REGISTRY)/crc-operator:v$(RELEASE_VERSION)
	@podman push $(RELEASE_REGISTRY)/crc-operator:v$(RELEASE_VERSION)
	@cat deploy/service_account.yaml > deploy/release-v$(RELEASE_VERSION).yaml
	@echo -e "\n---\n" >> deploy/release-v$(RELEASE_VERSION).yaml
	@cat deploy/role.yaml >> deploy/release-v$(RELEASE_VERSION).yaml
	@echo -e "\n---\n" >> deploy/release-v$(RELEASE_VERSION).yaml
	@cat deploy/role_binding.yaml >> deploy/release-v$(RELEASE_VERSION).yaml
	@echo -e "\n---\n" >> deploy/release-v$(RELEASE_VERSION).yaml
	@cat deploy/operator.yaml | sed -e "s|REPLACE_IMAGE|quay.io/bbrowning/crc-operator:v$(RELEASE_VERSION)|g" -e "s|REPLACE_ROUTES_HELPER_IMAGE|quay.io/bbrowning/crc-operator-routes-helper:v$(RELEASE_VERSION)|g" >> deploy/release-v$(RELEASE_VERSION).yaml
	@cp deploy/crds/crc.developer.openshift.io_crcclusters_crd.yaml deploy/release-v$(RELEASE_VERSION)_crd.yaml

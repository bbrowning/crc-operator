module github.com/bbrowning/crc-operator

go 1.13

require (
	github.com/code-ready/machine v0.0.0-20191122132905-c31e0b90623d
	github.com/coreos/rkt v1.30.0 // indirect
	github.com/go-logr/logr v0.1.0
	github.com/google/uuid v1.1.1
	github.com/openshift/api v0.0.0-20200205133042-34f0ec8dab87
	github.com/openshift/client-go v0.0.0-20191125132246-f6563a70e19a
	github.com/operator-framework/operator-sdk v0.17.1
	github.com/spf13/pflag v1.0.5
	golang.org/x/crypto v0.0.0-20200414173820-0848c9571904
	k8s.io/api v0.17.4
	k8s.io/apimachinery v0.17.4
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/kube-openapi v0.0.0-20200410145947-bcb3869e6f29 // indirect
	kubevirt.io/client-go v0.30.0
	kubevirt.io/containerized-data-importer v1.10.6
	sigs.k8s.io/controller-runtime v0.5.5
)

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.2+incompatible // Required by OLM
	k8s.io/client-go => k8s.io/client-go v0.17.4 // Required by prometheus-operator
)

module github.com/bbrowning/crc-operator

go 1.13

require (
	github.com/code-ready/machine v0.0.0-20191122132905-c31e0b90623d
	github.com/go-logr/logr v0.1.0
	github.com/google/uuid v1.1.1
	github.com/openshift/api v0.0.0-20191219222812-2987a591a72c
	github.com/operator-framework/operator-sdk v0.18.0
	github.com/spf13/pflag v1.0.5
	golang.org/x/crypto v0.0.0-20200414173820-0848c9571904
	k8s.io/api v0.18.2
	k8s.io/apimachinery v0.18.2
	k8s.io/client-go v12.0.0+incompatible
	kubevirt.io/client-go v0.30.0
	sigs.k8s.io/controller-runtime v0.6.0
)

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.2+incompatible // Required by OLM
	k8s.io/client-go => k8s.io/client-go v0.18.2 // Required by prometheus-operator
)

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// CrcBundleSpec defines the desired state of CrcBundle
type CrcBundleSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html

	// Image is the container image containing the VM image for this
	// bundle
	Image string `json:"image"`

	// DiskSize is the size of the disk in this bundle
	DiskSize string `json:"diskSize"`

	// SSHKey is the base64 encoded SSH key used to connect to the
	// Node in this bundle
	SSHKey string `json:"sshKey"`

	// Kubeconfig is the base64 encoded initial kubeconfig to connect
	// to this bundle
	Kubeconfig string `json:"kubeconfig"`
}

// CrcBundleStatus defines the observed state of CrcBundle
type CrcBundleStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CrcBundle is the Schema for the crcbundles API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=crcbundles,scope=Namespaced
type CrcBundle struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CrcBundleSpec   `json:"spec,omitempty"`
	Status CrcBundleStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CrcBundleList contains a list of CrcBundle
type CrcBundleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CrcBundle `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CrcBundle{}, &CrcBundleList{})
}

package v1alpha1

import (
	"github.com/operator-framework/operator-sdk/pkg/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// CrcClusterSpec defines the desired state of CrcCluster
type CrcClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html

	// TODO: Look for Hive / OpenShift Cluster Manager CRDs here
	// instead of creating our own things

	// CPU is the number of CPUs to allocate to the cluster
	// +kubebuilder:default=4
	CPU int `json:"cpu"`

	// Memory is the amount of memory to allocate to the cluster
	// +kubebuilder:default="12Gi"
	Memory string `json:"memory"`

	// PullSecret is your base64-encoded OpenShift pull secret
	PullSecret string `json:"pullSecret"`
}

const (
	// ConditionTypeVirtualMachineNotReady indicates if the VirtualMachine is not ready
	ConditionTypeVirtualMachineNotReady status.ConditionType = "VirtualMachineNotReady"

	// ConditionTypeNetworkingNotReady indicates if the networking
	// setup to route traffic into the cluster is not ready
	ConditionTypeNetworkingNotReady status.ConditionType = "NetworkingNotReady"

	// ConditionTypeKubeletNotReady indicates if kubelet has been
	// started in the cluster
	ConditionTypeKubeletNotReady status.ConditionType = "KubeletNotReady"

	// ConditionTypeClusterNotConfigured indicates if the OpenShift cluster
	// is not yet configured after first boot
	ConditionTypeClusterNotConfigured status.ConditionType = "ClusterNotConfigured"

	// ConditionTypeReady indicates if the OpenShift cluster is ready
	ConditionTypeReady status.ConditionType = "Ready"
)

// CrcClusterStatus defines the observed state of CrcCluster
type CrcClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html

	// BaseDomain is the base domain of the cluster's URLs
	BaseDomain string `json:"baseDomain,omitempty"`

	// APIURL is the URL of the cluster's API server
	APIURL string `json:"apiUrl,omitempty"`

	// ConsoleURL is the URL of the cluster's web console
	ConsoleURL string `json:"consoleUrl,omitempty"`

	// Kubeconfig is the base64-encoded kubeconfig to connect to the
	// cluster as an administrator
	Kubeconfig string `json:"kubeconfig,omitempty"`

	// KubeAdminPassword is the password to connect to the cluster as an administrator
	KubeAdminPassword string `json:"kubeAdminPassword,omitempty"`

	// Conditions represent the latest available observations of an object's state
	Conditions status.Conditions `json:"conditions"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CrcCluster is the Schema for the crcclusters API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=crcclusters,scope=Namespaced,shortName=crc
//
type CrcCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CrcClusterSpec   `json:"spec,omitempty"`
	Status CrcClusterStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CrcClusterList contains a list of CrcCluster
type CrcClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CrcCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CrcCluster{}, &CrcClusterList{})
}

package v1alpha1

import (
	"github.com/operator-framework/operator-sdk/pkg/status"
	corev1 "k8s.io/api/core/v1"
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
	// +kubebuilder:default="16Gi"
	Memory string `json:"memory"`

	// PullSecret is your base64-encoded OpenShift pull secret
	PullSecret string `json:"pullSecret"`

	// BundleImage is the CRC bundle image to use. If not set, a
	// default will be chosen based on the BundleName. This exists
	// only to allow temporary overriding of new bundle images before
	// a formal API gets created to allow dynamically creating new
	// bundle images. The new bundle image will need to have the same
	// SSH key and initial kubeconfig as the bundle specified in
	// BundleName.
	BundleImage string `json:"bundleImage,omitempty"`

	// BundleName is the CRC bundle name to use. If not set, a default
	// will be chosen by the CRC Operator.
	// +kubebuilder:validation:Enum=ocp445;ocp450rc1;ocp450rc2
	BundleName string `json:"bundleName,omitempty"`

	// Storage is the storage options to use. If not set, a default
	// will be chosen by the CRC Operator.
	Storage CrcStorageSpec `json:"storage,omitempty"`
}

// CrcStorageSpec defines the desired storage of CrcCluster
type CrcStorageSpec struct {
	// Persistent controls whether any data in this cluster should
	// persist if the cluster gets rebooted. Persistent storage takes
	// longer and costs more to provision. If this is false, the
	// cluster will be reset to the original state if the Node its
	// running on reboots or if the cluster itself gets shut
	// down. Defaults to false.
	// +kubebuilder:default=false
	Persistent bool `json:"persistent"`

	// Size is the amount of persistent disk space to allocate to the
	// cluster. This is ignored unless Persistent is set to true.
	Size string `json:"size,omitempty"`
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
	APIURL string `json:"apiURL,omitempty"`

	// ConsoleURL is the URL of the cluster's web console
	ConsoleURL string `json:"consoleURL,omitempty"`

	// ClusterID is the ID of this cluster, only really used if
	// connected cluster features are enabled
	ClusterID string `json:"clusterID,omitempty"`

	// Kubeconfig is the base64-encoded kubeconfig to connect to the
	// cluster as an administrator
	Kubeconfig string `json:"kubeconfig,omitempty"`

	// KubeAdminClientKey is the base64-encoded client key to connect
	// to the cluster as an administrator.
	KubeAdminClientKey string `json:"kubeAdminClientKey,omitempty"`

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

// SetConditionBool is a helper function to set boolean Conditions
func (crc *CrcCluster) SetConditionBool(conditionType status.ConditionType, value bool) {
	conditionValue := corev1.ConditionFalse
	if value {
		conditionValue = corev1.ConditionTrue
	}
	condition := status.Condition{
		Type:   conditionType,
		Status: conditionValue,
	}
	crc.Status.Conditions.SetCondition(condition)
}

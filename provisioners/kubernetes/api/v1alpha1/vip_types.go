package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// VIP is the flock VIP of the same name, its spec converted by ToProto.
type VIP struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of VIP
	// +required
	Spec VIPSpec `json:"spec"`

	// status defines the observed state of VIP
	// +optional
	Status Status `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// VIPList contains a list of VIP
type VIPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []VIP `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &VIP{}, &VIPList{})
		return nil
	})
}

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// VirtualServer is the flock virtual server of the same name, its spec converted by ToProto.
type VirtualServer struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of VirtualServer
	// +required
	Spec VirtualServerSpec `json:"spec"`

	// status defines the observed state of VirtualServer
	// +optional
	Status Status `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// VirtualServerList contains a list of VirtualServer
type VirtualServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []VirtualServer `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &VirtualServer{}, &VirtualServerList{})
		return nil
	})
}

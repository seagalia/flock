package v1alpha1

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Kind names the kind of object an ObjectRef points at.
type Kind string

const (
	KindBackend       Kind = "Backend"
	KindCertificate   Kind = "Certificate"
	KindInstance      Kind = "Instance"
	KindSecret        Kind = "Secret"
	KindVIP           Kind = "VIP"
	KindVirtualServer Kind = "VirtualServer"
	KindZone          Kind = "Zone"
)

// ObjectRef points at another object by name, its kind given by the field holding it. Every kind is cluster-scoped
// but Secret, read in the namespace of the provisioner.
type ObjectRef struct {
	// name of the object.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name string `json:"name"`
}

// Resolver gives the flock uuid of the object a reference points at, which ToProto writes in the proto field. It fails
// while the object doesn't exist yet or isn't created in flock.
// +kubebuilder:object:generate=false
type Resolver interface {
	UUID(ctx context.Context, kind Kind, ref ObjectRef) (string, error)
}

// Status is the observed state shared by every kind.
type Status struct {
	// uuid of the flock resource made from this object, empty until it is created.
	// +optional
	// +kubebuilder:validation:MaxLength=36
	UUID string `json:"uuid,omitempty"`

	// observedGeneration is the generation of the spec last written to flock.
	// +optional
	// +kubebuilder:validation:Minimum=0
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions tell whether flock holds the spec of the object (Ready) and why not when it doesn't.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

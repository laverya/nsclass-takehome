/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// NamespaceClassSpec defines the desired state of NamespaceClass
type NamespaceClassSpec struct {
	// resources contains Kubernetes objects to create for namespaces using this class.
	// Each object should include apiVersion, kind, metadata.name, and any object-specific fields.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Resources []runtime.RawExtension `json:"resources,omitempty"`

	// removalPolicy controls what happens to managed resources when a Namespace stops referencing this class.
	// +kubebuilder:validation:Enum=Retain;Delete
	// +kubebuilder:default=Retain
	// +optional
	RemovalPolicy NamespaceClassRemovalPolicy `json:"removalPolicy,omitempty"`
}

// NamespaceClassRemovalPolicy defines how managed resources are handled when a Namespace stops using a class.
type NamespaceClassRemovalPolicy string

const (
	// NamespaceClassRemovalPolicyRetain leaves managed resources in place and removes them from status.
	NamespaceClassRemovalPolicyRetain NamespaceClassRemovalPolicy = "Retain"

	// NamespaceClassRemovalPolicyDelete deletes managed resources and removes them from status.
	NamespaceClassRemovalPolicyDelete NamespaceClassRemovalPolicy = "Delete"
)

const (
	// NamespaceClassConditionReady reports whether the NamespaceClass resources are reconciled.
	NamespaceClassConditionReady = "Ready"

	// NamespaceClassReasonResourcesApplied is used when resources are successfully reconciled.
	NamespaceClassReasonResourcesApplied = "ResourcesApplied"

	// NamespaceClassReasonResourcePreparationFailed is used when a template resource cannot be prepared.
	NamespaceClassReasonResourcePreparationFailed = "ResourcePreparationFailed"

	// NamespaceClassReasonResourceCleanupFailed is used when stale resources cannot be deleted.
	NamespaceClassReasonResourceCleanupFailed = "ResourceCleanupFailed"

	// NamespaceClassReasonResourceApplyFailed is used when a resource cannot be applied.
	NamespaceClassReasonResourceApplyFailed = "ResourceApplyFailed"
)

// NamespaceClassPhase summarizes the NamespaceClass state for human-readable status output.
type NamespaceClassPhase string

const (
	// NamespaceClassPhaseReady indicates the NamespaceClass is reconciled.
	NamespaceClassPhaseReady NamespaceClassPhase = "Ready"

	// NamespaceClassPhaseNotReady indicates the NamespaceClass is not reconciled.
	NamespaceClassPhaseNotReady NamespaceClassPhase = "NotReady"

	// NamespaceClassPhaseUnknown indicates the NamespaceClass state is unknown.
	NamespaceClassPhaseUnknown NamespaceClassPhase = "Unknown"
)

// NamespaceClassStatus defines the observed state of NamespaceClass.
type NamespaceClassStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// phase summarizes the current NamespaceClass state for human-readable status output.
	// +kubebuilder:validation:Enum=Ready;NotReady;Unknown
	// +optional
	Phase NamespaceClassPhase `json:"phase,omitempty"`

	// resourceCount is the number of resource templates defined in spec.resources.
	// +optional
	ResourceCount int32 `json:"resourceCount,omitempty"`

	// namespaceCount is the number of Namespaces currently selecting this NamespaceClass.
	// +optional
	NamespaceCount int32 `json:"namespaceCount,omitempty"`

	// conditions represent the current state of the NamespaceClass resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// managedResources contains the resources last applied by this NamespaceClass.
	// +listType=map
	// +listMapKey=apiVersion
	// +listMapKey=kind
	// +listMapKey=namespace
	// +listMapKey=name
	// +optional
	ManagedResources []NamespaceClassManagedResource `json:"managedResources,omitempty"`
}

// NamespaceClassManagedResource identifies a resource applied by a NamespaceClass.
type NamespaceClassManagedResource struct {
	// apiVersion is the resource API version.
	// +required
	APIVersion string `json:"apiVersion"`

	// kind is the resource kind.
	// +required
	Kind string `json:"kind"`

	// namespace is the resource namespace.
	// +required
	Namespace string `json:"namespace"`

	// name is the resource name.
	// +required
	Name string `json:"name"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Resources",type=integer,JSONPath=".status.resourceCount"
// +kubebuilder:printcolumn:name="Namespaces",type=integer,JSONPath=".status.namespaceCount"
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].reason",priority=1

// NamespaceClass is the Schema for the namespaceclasses API
type NamespaceClass struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of NamespaceClass
	// +required
	Spec NamespaceClassSpec `json:"spec"`

	// status defines the observed state of NamespaceClass
	// +optional
	Status NamespaceClassStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// NamespaceClassList contains a list of NamespaceClass
type NamespaceClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []NamespaceClass `json:"items"`
}

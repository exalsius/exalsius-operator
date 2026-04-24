/*
Copyright 2025 Exalsius contributors.

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

package v1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResourceShape describes whether a workspace runs on a single node or across
// multiple nodes.
// +kubebuilder:validation:Enum=SingleNode;MultiNode
type ResourceShape string

const (
	ResourceShapeSingleNode ResourceShape = "SingleNode"
	ResourceShapeMultiNode  ResourceShape = "MultiNode"
)

// GPUVendor enumerates supported GPU vendors.
// +kubebuilder:validation:Enum=NVIDIA;AMD
type GPUVendor string

const (
	GPUVendorNVIDIA GPUVendor = "NVIDIA"
	GPUVendorAMD    GPUVendor = "AMD"
)

// ResourceRequirements specifies compute resource requirements for a single node.
type ResourceRequirements struct {
	// CPU is the number of CPU cores requested (e.g. "4", "500m").
	// +optional
	CPU *resource.Quantity `json:"cpu,omitempty"`
	// Memory is the amount of memory requested (e.g. "16Gi").
	// +optional
	Memory *resource.Quantity `json:"memory,omitempty"`
	// Storage is the amount of persistent storage requested (e.g. "100Gi").
	// +optional
	Storage *resource.Quantity `json:"storage,omitempty"`
	// GPUCount is the number of GPUs requested per node.
	// +optional
	// +kubebuilder:validation:Minimum=0
	GPUCount *int32 `json:"gpuCount,omitempty"`
	// GPUVendor specifies the preferred GPU vendor.
	// +optional
	GPUVendor *GPUVendor `json:"gpuVendor,omitempty"`
}

// WorkspaceResourceSpec defines the compute resources for a workspace.
type WorkspaceResourceSpec struct {
	// NodeCount is the number of worker nodes for MultiNode workspaces.
	// Ignored for SingleNode shape.
	// +optional
	// +kubebuilder:validation:Minimum=1
	NodeCount *int32 `json:"nodeCount,omitempty"`
	// PerNode specifies resources requested per node.
	PerNode ResourceRequirements `json:"perNode"`
}

// ServiceTemplateRef references a k0rdent ServiceTemplate CR.
type ServiceTemplateRef struct {
	// Name is the name of the ServiceTemplate CR.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// Namespace of the ServiceTemplate. When unspecified, the system namespace is implied.
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// VersionConstraint is an optional SemVer constraint (e.g. ">=1.2.0").
	// +optional
	VersionConstraint string `json:"versionConstraint,omitempty"`
}

// RouteProtocol is the wire protocol for an access endpoint.
// +kubebuilder:validation:Enum=HTTP;TCP;SSH
type RouteProtocol string

const (
	RouteProtocolHTTP RouteProtocol = "HTTP"
	RouteProtocolTCP  RouteProtocol = "TCP"
	RouteProtocolSSH  RouteProtocol = "SSH"
)

// AccessEndpoint declares a network endpoint that the workspace exposes.
type AccessEndpoint struct {
	// Name is a unique identifier for this endpoint within the class.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`
	// Protocol is the wire protocol for this endpoint.
	Protocol RouteProtocol `json:"protocol"`
	// Port is the container port the workspace listens on.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
	// Description is a human-readable summary shown to users.
	// +optional
	Description string `json:"description,omitempty"`
}

// ConfigFieldType identifies the input type for a user-facing config field.
// +kubebuilder:validation:Enum=string;int;bool;secret;publicKey
type ConfigFieldType string

const (
	ConfigFieldTypeString    ConfigFieldType = "string"
	ConfigFieldTypeInt       ConfigFieldType = "int"
	ConfigFieldTypeBool      ConfigFieldType = "bool"
	ConfigFieldTypeSecret    ConfigFieldType = "secret"
	ConfigFieldTypePublicKey ConfigFieldType = "publicKey"
)

// UserFacingConfigField annotates a single Helm value path that the CLI should
// prompt users for during workspace creation.
type UserFacingConfigField struct {
	// HelmValuePath is a dot-delimited path into values.yaml (e.g. "jupyter.password").
	// +kubebuilder:validation:MinLength=1
	HelmValuePath string `json:"helmValuePath"`
	// DisplayName is the label shown to the user in the CLI prompt.
	// +kubebuilder:validation:MinLength=1
	DisplayName string `json:"displayName"`
	// Description provides additional context shown as help text.
	// +optional
	Description string `json:"description,omitempty"`
	// Type is the input type for validation and display.
	// +kubebuilder:default=string
	Type ConfigFieldType `json:"type,omitempty"`
	// Required indicates the user must provide a value.
	// +optional
	Required bool `json:"required,omitempty"`
	// Default is the default value shown in the prompt.
	// +optional
	Default *apiextensionsv1.JSON `json:"default,omitempty"`
	// Options constrains the input to a closed set of allowed values.
	// +optional
	Options []string `json:"options,omitempty"`
}

// WorkspaceClassSpec defines the catalog entry for a workspace type.
//
// +kubebuilder:validation:XValidation:rule="self.resourceShape == 'MultiNode' ? has(self.defaultResources.nodeCount) : true",message="nodeCount is required for MultiNode shape"
type WorkspaceClassSpec struct {
	// DisplayName is the human-readable name shown in the workspace catalog.
	// +kubebuilder:validation:MinLength=1
	DisplayName string `json:"displayName"`
	// Description provides a user-facing summary of what this workspace type offers.
	// +optional
	Description string `json:"description,omitempty"`

	// ServiceTemplate references the k0rdent ServiceTemplate CR that wraps the
	// Helm chart for this workspace type.
	ServiceTemplate ServiceTemplateRef `json:"serviceTemplate"`

	// ResourceShape declares the topology: SingleNode or MultiNode.
	ResourceShape ResourceShape `json:"resourceShape"`
	// DefaultResources are applied when a WorkspaceDeployment does not override them.
	DefaultResources WorkspaceResourceSpec `json:"defaultResources"`

	// Prerequisites lists additional ServiceTemplates that must be healthy on the
	// target cluster before the workspace can be deployed.
	// +optional
	Prerequisites []ServiceTemplateRef `json:"prerequisites,omitempty"`

	// AccessEndpoints declares the network endpoints this workspace type exposes.
	// +optional
	// +listType=map
	// +listMapKey=name
	AccessEndpoints []AccessEndpoint `json:"accessEndpoints,omitempty"`

	// UserFacingConfig lists Helm value paths that the CLI should prompt for.
	// +optional
	// +listType=map
	// +listMapKey=helmValuePath
	UserFacingConfig []UserFacingConfigField `json:"userFacingConfig,omitempty"`

	// DeployTimeout is the maximum time to wait for the workspace Helm release
	// to become ready. Useful for workspaces with large images that take long to pull.
	// Defaults to 15 minutes if not set.
	// +optional
	DeployTimeout *metav1.Duration `json:"deployTimeout,omitempty"`

	// DefaultValues provides base Helm values that are always applied.
	// +optional
	DefaultValues *apiextensionsv1.JSON `json:"defaultValues,omitempty"`
}

// WorkspaceClassStatus reports the observed state of a WorkspaceClass.
type WorkspaceClassStatus struct {
	// Conditions represent the latest observations.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ObservedGeneration is the .metadata.generation that was last reconciled.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=wsc
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="Shape",type=string,JSONPath=`.spec.resourceShape`
// +kubebuilder:printcolumn:name="ServiceTemplate",type=string,JSONPath=`.spec.serviceTemplate.name`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WorkspaceClass is a cluster-scoped catalog entry that defines a workspace type.
// Users reference it by name when creating a WorkspaceDeployment.
type WorkspaceClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkspaceClassSpec   `json:"spec"`
	Status WorkspaceClassStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WorkspaceClassList contains a list of WorkspaceClass.
type WorkspaceClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkspaceClass `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&WorkspaceClass{}, &WorkspaceClassList{},
	)
}

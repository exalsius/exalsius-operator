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

// GPUVendor enumerates supported GPU vendors.
// +kubebuilder:validation:Enum=NVIDIA;AMD
type GPUVendor string

const (
	GPUVendorNVIDIA GPUVendor = "NVIDIA"
	GPUVendorAMD    GPUVendor = "AMD"
)

// ResourceRequirements specifies compute resource requirements for a single replica.
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
	// GPUCount is the number of GPUs requested per replica.
	// +optional
	// +kubebuilder:validation:Minimum=0
	GPUCount *int32 `json:"gpuCount,omitempty"`
	// GPUVendor constrains the GPU vendor (NVIDIA or AMD). Set on a
	// WorkspaceClass to express chart-level vendor compatibility (e.g. CUDA
	// vs ROCm); inheritable into a WorkspaceDeployment that doesn't override it.
	// +optional
	GPUVendor *GPUVendor `json:"gpuVendor,omitempty"`
	// GPUType is a deployment-time preference for a specific GPU model
	// (e.g. "H100", "A100", "L40"). When unset on a WorkspaceDeployment,
	// feasibility matches GPUs of any model. Cannot be set on a
	// WorkspaceClass — type is a per-deployment cluster-dependent choice.
	// +optional
	GPUType *string `json:"gpuType,omitempty"`
}

// WorkspaceResourceSpec defines the compute resources for a workspace.
// A workspace deploys Replicas pod-shaped instances, each with PerReplica
// resources. Whether replicas are spread across Kubernetes nodes or
// co-located is decided by the chart's affinity rules and the K8s scheduler;
// this spec only describes total demand (Replicas × PerReplica).
type WorkspaceResourceSpec struct {
	// Replicas is the number of pod-shaped instances the workspace deploys.
	// Defaults to 1.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	Replicas *int32 `json:"replicas,omitempty"`
	// PerReplica specifies resources requested per replica.
	PerReplica ResourceRequirements `json:"perReplica"`
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
// +kubebuilder:validation:XValidation:rule="!has(self.defaultResources.perReplica.gpuType)",message="gpuType cannot be set on a WorkspaceClass — it is a per-deployment cluster-dependent choice"
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

	// DefaultResources are applied when a WorkspaceDeployment does not override them.
	DefaultResources WorkspaceResourceSpec `json:"defaultResources"`

	// Prerequisites lists additional ServiceTemplates that must be healthy on the
	// target cluster before the workspace can be deployed.
	// +optional
	Prerequisites []PrerequisiteSpec `json:"prerequisites,omitempty"`

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

	// PrerequisiteDeployTimeout overrides DeployTimeout for prerequisite Helm
	// releases. Useful when prerequisite operators take noticeably longer or
	// shorter to install than the workspace itself. Defaults to DeployTimeout
	// when nil.
	// +optional
	PrerequisiteDeployTimeout *metav1.Duration `json:"prerequisiteDeployTimeout,omitempty"`

	// DefaultValues provides base Helm values that are always applied.
	// +optional
	DefaultValues *apiextensionsv1.JSON `json:"defaultValues,omitempty"`

	// ResourceInjection optionally maps resolved per-replica resource fields
	// to additional Helm value paths in spec.values. The standard
	// `_exalsius.resources` path is always populated automatically — this
	// field adds chart-specific paths, typically subchart paths in
	// umbrella charts. Paths use JSONPath-lite syntax: dot-separated, with
	// quoted bracket segments for keys containing dots or other special
	// characters (e.g. 'jupyterhub.singleuser.extraResource.limits["nvidia.com/gpu"]').
	// User-supplied spec.values at the same paths are overwritten by the
	// operator (a ResourcesInjected condition surfaces the override).
	// +optional
	ResourceInjection *ResourceInjectionMap `json:"resourceInjection,omitempty"`
}

// InjectionPath is a JSONPath-lite path into the chart's Helm values tree.
// Dot-separated, with quoted bracket segments for keys containing dots or
// other special characters (e.g. 'jupyterhub.singleuser.extraResource.limits["nvidia.com/gpu"]').
// +kubebuilder:validation:MinLength=1
// +kubebuilder:validation:MaxLength=1024
type InjectionPath string

// ResourceInjectionMap declares additional Helm value paths in spec.values
// that should receive the resolved per-replica resource fields at deploy time.
// Each list contains JSONPath-lite paths that get the corresponding value.
// All fields are optional; nil-resolved fields are skipped silently.
type ResourceInjectionMap struct {
	// Replicas lists Helm value paths receiving the resolved replica count (int).
	// +optional
	// +listType=set
	Replicas []InjectionPath `json:"replicas,omitempty"`
	// CPU lists Helm value paths receiving the resolved per-replica CPU (string).
	// +optional
	// +listType=set
	CPU []InjectionPath `json:"cpu,omitempty"`
	// Memory lists Helm value paths receiving the resolved per-replica memory (string).
	// +optional
	// +listType=set
	Memory []InjectionPath `json:"memory,omitempty"`
	// Storage lists Helm value paths receiving the resolved per-replica storage (string).
	// +optional
	// +listType=set
	Storage []InjectionPath `json:"storage,omitempty"`
	// GPUCount lists Helm value paths receiving the resolved per-replica GPU count (int).
	// +optional
	// +listType=set
	GPUCount []InjectionPath `json:"gpuCount,omitempty"`
	// GPUVendor lists Helm value paths receiving the resolved GPU vendor (string).
	// +optional
	// +listType=set
	GPUVendor []InjectionPath `json:"gpuVendor,omitempty"`
	// GPUType lists Helm value paths receiving the resolved GPU type (string).
	// +optional
	// +listType=set
	GPUType []InjectionPath `json:"gpuType,omitempty"`
}

// PrerequisiteSpec declares a ServiceTemplate dependency with optional Helm
// value overrides applied when the operator auto-installs the prerequisite
// onto the target cluster.
type PrerequisiteSpec struct {
	// ServiceTemplate references the prerequisite ServiceTemplate.
	ServiceTemplate ServiceTemplateRef `json:"serviceTemplate"`
	// Values are optional Helm value overrides applied when installing this
	// prerequisite. When nil, chart defaults are used.
	// +optional
	Values *apiextensionsv1.JSON `json:"values,omitempty"`
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
	// Valid indicates whether the referenced ServiceTemplate is available and
	// itself reports valid=true.
	Valid bool `json:"valid"`
	// ValidationError describes why the WorkspaceClass is not valid.
	// Empty when Valid is true.
	ValidationError string `json:"validationError,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=wsc
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.defaultResources.replicas`
// +kubebuilder:printcolumn:name="ServiceTemplate",type=string,JSONPath=`.spec.serviceTemplate.name`
// +kubebuilder:printcolumn:name="Valid",type=boolean,JSONPath=`.status.valid`
// +kubebuilder:printcolumn:name="ValidationError",type=string,JSONPath=`.status.validationError`,priority=1
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

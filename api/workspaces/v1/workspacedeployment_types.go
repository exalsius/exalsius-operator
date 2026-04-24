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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	WorkspaceDeploymentKind      = "WorkspaceDeployment"
	WorkspaceDeploymentFinalizer = "workspaces.exalsius.ai/deployment"
)

// WorkspaceDeploymentPhase represents the lifecycle state of a deployment.
// +kubebuilder:validation:Enum=Pending;Deploying;Running;Failed;Deleting
type WorkspaceDeploymentPhase string

const (
	WorkspaceDeploymentPhasePending   WorkspaceDeploymentPhase = "Pending"
	WorkspaceDeploymentPhaseDeploying WorkspaceDeploymentPhase = "Deploying"
	WorkspaceDeploymentPhaseRunning   WorkspaceDeploymentPhase = "Running"
	WorkspaceDeploymentPhaseFailed    WorkspaceDeploymentPhase = "Failed"
	WorkspaceDeploymentPhaseDeleting  WorkspaceDeploymentPhase = "Deleting"
)

// Condition types for WorkspaceDeployment.
const (
	ConditionReady            = "Ready"
	ConditionClassResolved    = "ClassResolved"
	ConditionPrerequisitesMet = "PrerequisitesMet"
	ConditionHelmReleaseReady = "HelmReleaseReady"
)

// Condition reasons for WorkspaceDeployment.
const (
	ReasonClassNotFound         = "ClassNotFound"
	ReasonClassResolved         = "ClassResolved"
	ReasonPrerequisitesNotReady = "PrerequisitesNotReady"
	ReasonPrerequisitesMet      = "PrerequisitesMet"
	ReasonHelmReleaseNotReady   = "HelmReleaseNotReady"
	ReasonHelmReleaseDeployed   = "HelmReleaseDeployed"
	ReasonHelmReleaseFailed     = "HelmReleaseFailed"
	ReasonDeploymentReady       = "DeploymentReady"
	ReasonDeletionInProgress    = "DeletionInProgress"
	ReasonInternalError         = "InternalError"
	ReasonSuspended             = "Suspended"
)

// ClusterDeploymentRef references a k0rdent ClusterDeployment.
type ClusterDeploymentRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`
}

// OwnerInfo identifies the user and organization that owns this workspace.
type OwnerInfo struct {
	// +kubebuilder:validation:MinLength=1
	Username string `json:"username"`
	// +optional
	OrgID string `json:"orgID,omitempty"`
	// +optional
	OrgName string `json:"orgName,omitempty"`
	// +optional
	Teams []string `json:"teams,omitempty"`
}

// WorkspaceDeploymentSpec defines user intent to deploy a workspace instance.
type WorkspaceDeploymentSpec struct {
	// WorkspaceClassRef is the name of the cluster-scoped WorkspaceClass.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	WorkspaceClassRef string `json:"workspaceClassRef"`

	// ClusterDeploymentRef identifies the target child cluster.
	ClusterDeploymentRef ClusterDeploymentRef `json:"clusterDeploymentRef"`

	// Owner identifies the user and organization.
	Owner OwnerInfo `json:"owner"`

	// Resources overrides the default resources from the WorkspaceClass.
	// +optional
	Resources *WorkspaceResourceSpec `json:"resources,omitempty"`

	// Values provides user-specified Helm values merged on top of class defaults.
	// +optional
	Values *apiextensionsv1.JSON `json:"values,omitempty"`

	// Suspend, when true, causes the operator to skip reconciliation.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// WorkspaceDeploymentStatus defines the observed state of WorkspaceDeployment.
type WorkspaceDeploymentStatus struct {
	// Phase is a human-readable summary of the deployment lifecycle.
	// +optional
	Phase WorkspaceDeploymentPhase `json:"phase,omitempty"`

	// Conditions represent the latest observations.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the .metadata.generation that was last reconciled.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ServiceEntryName is the name of the service entry added to the ClusterDeployment.
	// +optional
	ServiceEntryName string `json:"serviceEntryName,omitempty"`

	// ResolvedResources captures the effective resources after merging.
	// +optional
	ResolvedResources *WorkspaceResourceSpec `json:"resolvedResources,omitempty"`

	// Message is a human-readable error or status detail.
	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=wsd
// +kubebuilder:printcolumn:name="Class",type=string,JSONPath=`.spec.workspaceClassRef`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterDeploymentRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WorkspaceDeployment represents user intent to deploy a workspace instance
// onto a child cluster.
type WorkspaceDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkspaceDeploymentSpec   `json:"spec"`
	Status WorkspaceDeploymentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WorkspaceDeploymentList contains a list of WorkspaceDeployment.
type WorkspaceDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkspaceDeployment `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&WorkspaceDeployment{}, &WorkspaceDeploymentList{},
	)
}

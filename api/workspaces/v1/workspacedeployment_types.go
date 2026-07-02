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

const (
	WorkspaceDeploymentKind      = "WorkspaceDeployment"
	WorkspaceDeploymentFinalizer = "workspaces.exalsius.ai/deployment"
)

// WorkspaceDeploymentPhase represents the lifecycle state of a deployment.
// +kubebuilder:validation:Enum=Pending;InstallingPrerequisites;Waiting;Deploying;Running;Failed;Deleting
type WorkspaceDeploymentPhase string

const (
	WorkspaceDeploymentPhasePending                 WorkspaceDeploymentPhase = "Pending"
	WorkspaceDeploymentPhaseInstallingPrerequisites WorkspaceDeploymentPhase = "InstallingPrerequisites"
	// WorkspaceDeploymentPhaseWaiting means the requested GPU Offering exists
	// on the target cluster but none are free right now; the workspace is held
	// (no Helm release created) and retried until capacity frees (ADR-0002).
	// Transient and self-resolving — distinct from the terminal Failed.
	WorkspaceDeploymentPhaseWaiting   WorkspaceDeploymentPhase = "Waiting"
	WorkspaceDeploymentPhaseDeploying WorkspaceDeploymentPhase = "Deploying"
	WorkspaceDeploymentPhaseRunning   WorkspaceDeploymentPhase = "Running"
	WorkspaceDeploymentPhaseFailed    WorkspaceDeploymentPhase = "Failed"
	WorkspaceDeploymentPhaseDeleting  WorkspaceDeploymentPhase = "Deleting"
)

// PrerequisitePhase reports the install state of a single prerequisite.
// +kubebuilder:validation:Enum=Pending;Installing;Satisfied;Failed
type PrerequisitePhase string

const (
	PrerequisitePhasePending    PrerequisitePhase = "Pending"
	PrerequisitePhaseInstalling PrerequisitePhase = "Installing"
	PrerequisitePhaseSatisfied  PrerequisitePhase = "Satisfied"
	PrerequisitePhaseFailed     PrerequisitePhase = "Failed"
)

// PrerequisiteSource identifies who is providing a prerequisite.
// +kubebuilder:validation:Enum=workspace;colony
type PrerequisiteSource string

const (
	// PrerequisiteSourceWorkspace means the workspace controller installed
	// the prerequisite via a wsprereq-* ServiceSet.
	PrerequisiteSourceWorkspace PrerequisiteSource = "workspace"
	// PrerequisiteSourceColony means the prerequisite is satisfied by a
	// service entry on the ClusterDeployment (managed by the colony controller).
	PrerequisiteSourceColony PrerequisiteSource = "colony"
)

// FeasibilityStatus reports whether the cluster has enough free capacity
// to satisfy this workspace's resource demand. Computed by the operator
// during pre-deploy phases against live cluster state. Not updated once
// the workspace is Running.
type FeasibilityStatus struct {
	// Fits is true when the demanded resources can be satisfied.
	Fits bool `json:"fits"`
	// EvaluatedAt is when this feasibility verdict was computed.
	EvaluatedAt metav1.Time `json:"evaluatedAt"`
	// Demanded is the total resource demand that was checked
	// (Replicas × PerReplica from the merged class+deployment spec).
	Demanded ResourceTotals `json:"demanded"`
	// Available is the cluster-wide free capacity at evaluation time.
	Available ResourceTotals `json:"available"`
	// Missing describes what's lacking when Fits=false. Nil when Fits=true.
	// +optional
	Missing *ResourceTotals `json:"missing,omitempty"`
	// Message is a human-readable summary.
	// +optional
	Message string `json:"message,omitempty"`
}

// ResourceTotals summarises a cluster-total resource view used for
// feasibility reporting.
type ResourceTotals struct {
	// CPU in cores (decimal-encoded resource.Quantity).
	// +optional
	CPU *resource.Quantity `json:"cpu,omitempty"`
	// Memory in bytes.
	// +optional
	Memory *resource.Quantity `json:"memory,omitempty"`
	// Storage in bytes.
	// +optional
	Storage *resource.Quantity `json:"storage,omitempty"`
	// GPUCount is the total number of GPUs.
	// +optional
	GPUCount *int32 `json:"gpuCount,omitempty"`
	// GPUVendor identifies the vendor that was matched (when feasibility
	// considered vendor as a constraint). Empty when wildcard.
	// +optional
	GPUVendor *GPUVendor `json:"gpuVendor,omitempty"`
	// GPUType identifies the GPU model that was matched. Empty when wildcard.
	// +optional
	GPUType *string `json:"gpuType,omitempty"`
}

// PrerequisiteStatus reports the per-prerequisite install state.
type PrerequisiteStatus struct {
	// Name is the ServiceTemplate name from WorkspaceClass.spec.prerequisites.
	Name string `json:"name"`
	// Phase is the current state of this prerequisite.
	Phase PrerequisitePhase `json:"phase"`
	// Source identifies who is providing the prerequisite.
	// +optional
	Source PrerequisiteSource `json:"source,omitempty"`
	// Namespace is where the shared prerequisite install actually resides on
	// the child cluster: the incumbent's namespace when one exists, otherwise
	// the namespace this class would install into.
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// Message is a human-readable detail (e.g., the underlying failure reason).
	// +optional
	Message string `json:"message,omitempty"`
}

// FailureContext bundles operator-side context captured when the workspace
// transitioned to Failed. The exalsius API projects it into the error
// envelope's `details` so agents can pattern-match common failures without
// querying operator internals. Pod status from the child cluster is a
// planned (v1.1) addition; the shape accommodates new fields non-breaking.
type FailureContext struct {
	// PhaseWhenFailed is the phase the workspace was in when it failed
	// (e.g. Deploying, InstallingPrerequisites).
	// +optional
	PhaseWhenFailed WorkspaceDeploymentPhase `json:"phaseWhenFailed,omitempty"`
	// Reason is the machine-readable failure reason, mirroring the failing
	// condition's reason (e.g. HelmReleaseFailed, PrerequisitesNotReady,
	// RoutingInfraNotReady).
	// +optional
	Reason string `json:"reason,omitempty"`
	// ServiceSetStatus snapshots the workspace ServiceSet's reported
	// service states at failure time.
	// +optional
	ServiceSetStatus *ServiceSetStatusSummary `json:"serviceSetStatus,omitempty"`
	// RecentEvents lists the most recent Kubernetes events recorded for
	// this WorkspaceDeployment, newest first.
	// +optional
	RecentEvents []EventSummary `json:"recentEvents,omitempty"`
}

// ServiceSetStatusSummary is a compact snapshot of a k0rdent ServiceSet
// status for failure forensics.
type ServiceSetStatusSummary struct {
	// Name of the ServiceSet.
	Name string `json:"name"`
	// Deployed mirrors ServiceSet.status.deployed.
	// +optional
	Deployed bool `json:"deployed,omitempty"`
	// Services lists the per-service states.
	// +optional
	Services []ServiceStateSummary `json:"services,omitempty"`
}

// ServiceStateSummary is one service entry's state inside a ServiceSet.
type ServiceStateSummary struct {
	Name string `json:"name"`
	// State is the k0rdent service state (e.g. Deployed, Failed).
	// +optional
	State string `json:"state,omitempty"`
	// FailureMessage carries the underlying failure detail (e.g. the Helm
	// error).
	// +optional
	FailureMessage string `json:"failureMessage,omitempty"`
}

// EventSummary is a compact view of a Kubernetes event.
type EventSummary struct {
	// LastSeen is when the event last occurred.
	// +optional
	LastSeen metav1.Time `json:"lastSeen,omitempty"`
	// Type is Normal or Warning.
	// +optional
	Type   string `json:"type,omitempty"`
	Reason string `json:"reason"`
	// +optional
	Message string `json:"message,omitempty"`
}

// AccessEntry describes one externally reachable endpoint of a workspace,
// resolved by the routing provider. The exalsius API projects these entries
// into Workspace.access[] unchanged (ADR-0001).
type AccessEntry struct {
	// Name is the access endpoint name from the WorkspaceClass.
	Name string `json:"name"`
	// Protocol is the endpoint's wire protocol.
	Protocol RouteProtocol `json:"protocol"`
	// URL is where the endpoint is reachable, e.g.
	// https://thorsten-nb.dos-lab.ex.ls or ssh://dos-lab.ex.ls:2207.
	// Empty while the route is not yet resolvable.
	// +optional
	URL string `json:"url,omitempty"`
	// Ready reports whether the platform side of routing is programmed.
	// Application-level health remains the chart's readiness probes.
	Ready bool `json:"ready"`
	// Message explains why an entry is not ready (e.g. port pool exhausted).
	// +optional
	Message string `json:"message,omitempty"`
}

// Condition types for WorkspaceDeployment.
const (
	ConditionReady            = "Ready"
	ConditionClassResolved    = "ClassResolved"
	ConditionPrerequisitesMet = "PrerequisitesMet"
	ConditionHelmReleaseReady = "HelmReleaseReady"
	// ConditionClusterDeploymentResolved reports whether the referenced
	// ClusterDeployment exists. Non-terminal: a missing CD keeps the
	// workspace Pending and requeues, so it self-heals if the cluster is
	// still provisioning or applied out of order.
	ConditionClusterDeploymentResolved = "ClusterDeploymentResolved"
	// ConditionRoutesReady reports whether all declared access endpoints
	// have programmed routes. Only set when the WorkspaceClass declares
	// access endpoints and a routing provider is configured.
	ConditionRoutesReady = "RoutesReady"
	// ConditionFeasible reports whether the cluster currently has enough
	// capacity to deploy the workspace. Set during pre-deploy phases; not
	// updated once the workspace is Running.
	ConditionFeasible = "Feasible"
	// ConditionResourcesInjected surfaces the result of resolving spec.resources
	// into Helm values. Status=True is the steady state; Status=False signals
	// that the operator overwrote user-supplied paths in spec.values to align
	// them with the structured spec.resources channel — i.e., a likely user
	// configuration error worth surfacing.
	ConditionResourcesInjected = "ResourcesInjected"
)

// Condition reasons for WorkspaceDeployment.
const (
	ReasonClassNotFound             = "ClassNotFound"
	ReasonClassResolved             = "ClassResolved"
	ReasonClusterDeploymentResolved = "ClusterDeploymentResolved"
	ReasonClusterDeploymentNotFound = "ClusterDeploymentNotFound"
	ReasonPrerequisitesNotReady     = "PrerequisitesNotReady"
	ReasonPrerequisitesMet          = "PrerequisitesMet"
	ReasonInstallingPrerequisites   = "InstallingPrerequisites"
	ReasonHelmReleaseNotReady       = "HelmReleaseNotReady"
	ReasonHelmReleaseDeployed       = "HelmReleaseDeployed"
	ReasonHelmReleaseFailed         = "HelmReleaseFailed"
	ReasonDeploymentReady           = "DeploymentReady"
	ReasonDeletionInProgress        = "DeletionInProgress"
	ReasonInternalError             = "InternalError"
	ReasonSuspended                 = "Suspended"
	ReasonInvalidPrerequisite       = "InvalidPrerequisite"
	// ReasonPrerequisiteNamespaceConflict signals that a class explicitly
	// requested a prerequisite namespace that disagrees with where the shared
	// singleton already lives (installed by another class's ServiceSet or
	// provided by the colony). Terminal: an admin resolves it by aligning the
	// class with the incumbent namespace (ADR-0007).
	ReasonPrerequisiteNamespaceConflict = "PrerequisiteNamespaceConflict"
	ReasonResourcesAvailable            = "ResourcesAvailable"
	ReasonInsufficientResources         = "InsufficientResources"
	ReasonFeasibilityUnknown            = "FeasibilityUnknown"
	ReasonResourcesInjected             = "ResourcesInjected"
	ReasonUserPathsOverwritten          = "UserPathsOverwritten"
	ReasonInvalidWorkspaceClass         = "InvalidWorkspaceClass"
	ReasonRoutesReady                   = "RoutesReady"
	ReasonRoutingInProgress             = "RoutingInProgress"
	ReasonRoutingError                  = "RoutingError"
	// ReasonRoutingInfraNotReady signals that the tenant's routing
	// infrastructure (regional cluster, gateway, listeners) is missing or
	// unprogrammed — an admin-fixable condition (remediation: contact_admin).
	ReasonRoutingInfraNotReady = "RoutingInfraNotReady"
	// ReasonGpuOfferingUnavailable signals that the requested GPU model is not
	// present on the target ClusterDeployment at all (ADR-0002). Terminal: the
	// user fixes the request (or has the GPU provisioned) and recreates.
	ReasonGpuOfferingUnavailable = "GpuOfferingUnavailable"
	// ReasonWaitingForGpuCapacity signals that the requested GPU model exists
	// on the target cluster but none are free right now (ADR-0002). Transient:
	// the workspace is held in the Waiting phase and proceeds once capacity
	// frees.
	ReasonWaitingForGpuCapacity = "WaitingForGpuCapacity"
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

// WorkspaceDeploymentSpec defines user intent to deploy a workspace instance.
type WorkspaceDeploymentSpec struct {
	// WorkspaceClassRef is the name of the cluster-scoped WorkspaceClass.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	WorkspaceClassRef string `json:"workspaceClassRef"`

	// ClusterDeploymentRef identifies the target child cluster.
	ClusterDeploymentRef ClusterDeploymentRef `json:"clusterDeploymentRef"`

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

	// Prerequisites reports the per-prerequisite install state. Only populated
	// when the resolved WorkspaceClass declares prerequisites.
	// +optional
	// +listType=map
	// +listMapKey=name
	Prerequisites []PrerequisiteStatus `json:"prerequisites,omitempty"`

	// Feasibility reports whether the cluster has enough free capacity to
	// deploy this workspace. Refreshed on every reconcile while the
	// workspace is being deployed; not updated once the workspace is Running.
	// +optional
	Feasibility *FeasibilityStatus `json:"feasibility,omitempty"`

	// Access lists the externally reachable endpoints of this workspace,
	// resolved by the routing provider once the workspace is Running.
	// +optional
	// +listType=map
	// +listMapKey=name
	Access []AccessEntry `json:"access,omitempty"`

	// FailureContext bundles operator-side failure forensics, populated on
	// every transition into Failed and cleared when the workspace recovers.
	// +optional
	FailureContext *FailureContext `json:"failureContext,omitempty"`

	// Message is a human-readable error or status detail.
	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=wsd
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 60",message="workspace name must be at most 60 characters so the ws-<name> workspace namespace fits the 63-character namespace name limit"
// +kubebuilder:validation:XValidation:rule="self.metadata.name.matches('^[a-z0-9]([-a-z0-9]*[a-z0-9])?$')",message="workspace name must be a lowercase DNS-1123 label: it becomes the ws-<name> workspace namespace and the workspace hostname"
// +kubebuilder:printcolumn:name="Class",type=string,JSONPath=`.spec.workspaceClassRef`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterDeploymentRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.access[0].url`
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

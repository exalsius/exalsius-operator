/*
Copyright 2025.

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
	"encoding/json"
	"fmt"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ColonySpec defines the desired state of Colony.
type ColonySpec struct {
	// ColonyClusters is the list of clusters to create.
	ColonyClusters []ColonyCluster `json:"colonyClusters,omitempty"`
}

type ColonyCluster struct {
	// Name is the name of the cluster.
	ClusterName string `json:"clusterName"`
	// ClusterDeployment is the specification for the cluster deployment.
	// This is stored as RawExtension to avoid CRD validation issues with k0rdent types
	ClusterDeploymentSpec *runtime.RawExtension `json:"clusterDeploymentSpec,omitempty"`
	// ClusterLabels are the labels to be applied to the ClusterDeployment resource
	ClusterLabels map[string]string `json:"clusterLabels,omitempty"`
	// ClusterAnnotations are the annotations to be applied to the ClusterDeployment resource
	ClusterAnnotations map[string]string `json:"clusterAnnotations,omitempty"`
}

// GetClusterDeploymentSpec returns the ClusterDeploymentSpec as a typed object
func (cc *ColonyCluster) GetClusterDeploymentSpec() (*k0rdentv1beta1.ClusterDeploymentSpec, error) {
	if cc.ClusterDeploymentSpec == nil {
		return nil, nil
	}

	var spec k0rdentv1beta1.ClusterDeploymentSpec
	if err := json.Unmarshal(cc.ClusterDeploymentSpec.Raw, &spec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ClusterDeploymentSpec: %w", err)
	}
	return &spec, nil
}

// SetClusterDeploymentSpec sets the ClusterDeploymentSpec from a typed object
func (cc *ColonyCluster) SetClusterDeploymentSpec(spec *k0rdentv1beta1.ClusterDeploymentSpec) error {
	if spec == nil {
		cc.ClusterDeploymentSpec = nil
		return nil
	}

	raw, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("failed to marshal ClusterDeploymentSpec: %w", err)
	}

	cc.ClusterDeploymentSpec = &runtime.RawExtension{Raw: raw}
	return nil
}

// ColonyStatus defines the observed state of Colony.
type ColonyStatus struct {
	Phase                 string                    `json:"phase,omitempty"`
	Conditions            []metav1.Condition        `json:"conditions,omitempty"`
	ClusterDeploymentRefs []*corev1.ObjectReference `json:"clusterDeploymentRefs,omitempty"`
	TotalClusters         int32                     `json:"totalClusters,omitempty"`
	ReadyClusters         int32                     `json:"readyClusters,omitempty"`

	// GPUInventory maps each child cluster (keyed by ClusterDeployment name) to
	// the GPU Offerings discovered on it (ADR-0002). Populated best-effort on a
	// periodic poll for API discovery ("what GPUs can I pick on this cluster?").
	// It records totals only — never live free capacity, which the workspace
	// gate computes live against the cluster at deploy time.
	// +optional
	GPUInventory map[string]ClusterGPUInventory `json:"gpuInventory,omitempty"`
}

// ClusterGPUInventory is the set of GPU Offerings present on one child cluster.
type ClusterGPUInventory struct {
	// LastUpdated is when this cluster's inventory was last refreshed. A stale
	// timestamp means the cluster was unreachable on recent polls.
	// +optional
	LastUpdated metav1.Time `json:"lastUpdated,omitempty"`
	// Offerings are the distinct GPU kinds available on the cluster.
	// +optional
	Offerings []GPUOffering `json:"offerings,omitempty"`
}

// GPUOffering is a distinct kind of GPU available on a cluster, identified by
// vendor + model + resource name + Selector. The model is best-effort (ADR-0002,
// revised) and may be empty; the remaining fields are aggregated attributes.
type GPUOffering struct {
	// Model is the best-effort short model name (e.g. "H100", "A100-80GB" from
	// the provisioning-set GPU model label, or a vendor product label value like
	// "NVIDIA-L40"). Empty when the node carried no usable GPU model label; the
	// offering is then pickable only via a Selector composed from Labels.
	// +optional
	Model string `json:"model,omitempty"`
	// Selector is the exact node-label requirement that picks this offering — the
	// GPU Selector to put in a workspace's gpuNodeSelector, and the one the gate
	// validates. {exalsius.ai/gpu-model: <model>} when canonically labelled,
	// {<vendor-product-label>: <value>} when derived from a vendor label, empty
	// when the node had no usable GPU label (compose one from Labels instead).
	// +optional
	Selector map[string]string `json:"selector,omitempty"`
	// Vendor is the GPU vendor (e.g. NVIDIA, AMD).
	// +optional
	Vendor string `json:"vendor,omitempty"`
	// ResourceName is the extended resource a pod requests for this offering
	// (e.g. nvidia.com/gpu, amd.com/gpu).
	// +optional
	ResourceName string `json:"resourceName,omitempty"`
	// Total is the total number of GPUs of this offering across the cluster.
	// +optional
	Total int64 `json:"total,omitempty"`
	// Nodes is the number of nodes carrying this offering.
	// +optional
	Nodes int32 `json:"nodes,omitempty"`
	// Labels are the raw GFD/AMD/vendor node labels defining this offering,
	// retained for display and advanced selection (data retention, not identity).
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready Clusters",type=integer,JSONPath=`.status.readyClusters`
// +kubebuilder:printcolumn:name="Total Clusters",type=integer,JSONPath=`.status.totalClusters`

// Colony is the Schema for the colonies API.
type Colony struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ColonySpec   `json:"spec,omitempty"`
	Status ColonyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ColonyList contains a list of Colony.
type ColonyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Colony `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Colony{}, &ColonyList{})
}

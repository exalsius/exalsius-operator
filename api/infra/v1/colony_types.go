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
	// NetBird configuration for VPN networking
	NetBird *NetBirdConfig `json:"netBird,omitempty"`
}

// NetBirdConfig defines the NetBird VPN configuration for the Colony.
type NetBirdConfig struct {
	// Enabled enables NetBird integration for this Colony.
	Enabled bool `json:"enabled"`
	// Image is the NetBird container image to use (default: "netbirdio/netbird:v0.60.2").
	// +optional
	Image string `json:"image,omitempty"`
	// APIKeySecret is the name of the Secret containing the NetBird API key.
	APIKeySecret string `json:"apiKeySecret"`
	// ManagementURL is the NetBird management API URL (default: "https://api.netbird.io").
	// +optional
	ManagementURL string `json:"managementURL,omitempty"`
}

type ColonyCluster struct {
	// Name is the name of the cluster.
	ClusterName string `json:"clusterName"`
	// ClusterDeployment is the specification for the cluster deployment.
	// This is stored as RawExtension to avoid CRD validation issues with k0rdent types
	ClusterDeploymentSpec *runtime.RawExtension `json:"clusterDeploymentSpec,omitempty"`
	// ClusterLabels are the labels to be applied to the ClusterDeployment resource
	ClusterLabels map[string]string `json:"clusterLabels,omitempty"`
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
	// NetBird status for VPN networking
	NetBird *NetBirdStatus `json:"netBird,omitempty"`
}

// NetBirdStatus defines the observed state of NetBird integration.
type NetBirdStatus struct {
	// RouterReady indicates whether the routing peer Deployment is ready.
	RouterReady bool `json:"routerReady,omitempty"`
	// NetworkID is the NetBird network ID.
	NetworkID string `json:"networkID,omitempty"`
	// ColonyMeshPolicyID is the NetBird Policy ID for the shared colony-scoped mesh policy.
	ColonyMeshPolicyID string `json:"colonyMeshPolicyID,omitempty"`
	// ColonyNodesGroupID is the NetBird Group ID for the shared colony-scoped nodes group.
	ColonyNodesGroupID string `json:"colonyNodesGroupID,omitempty"`
	// ColonyRoutersGroupID is the NetBird Group ID for routing peers only.
	ColonyRoutersGroupID string `json:"colonyRoutersGroupID,omitempty"`
	// SetupKeySecretName is the name of the auto-generated Secret containing the setup key.
	SetupKeySecretName string `json:"setupKeySecretName,omitempty"`
	// SetupKeyID is the NetBird setup key ID for tracking/regeneration.
	SetupKeyID string `json:"setupKeyID,omitempty"`
	// RouterSetupKeySecretName is the name of the Secret containing the router-specific setup key.
	RouterSetupKeySecretName string `json:"routerSetupKeySecretName,omitempty"`
	// RouterSetupKeyID is the NetBird setup key ID for the routing peer.
	RouterSetupKeyID string `json:"routerSetupKeyID,omitempty"`
	// ClusterResources tracks NetBird resources per cluster.
	ClusterResources map[string]ClusterNetBirdStatus `json:"clusterResources,omitempty"`
}

// ClusterNetBirdStatus tracks NetBird resources for a specific cluster.
type ClusterNetBirdStatus struct {
	// ControlPlaneResourceID is the NetBird Network Resource ID for the control plane.
	ControlPlaneResourceID string `json:"controlPlaneResourceID,omitempty"`
	// ControlPlaneResourceAddress is the last known address of the resource (for migration/debugging).
	ControlPlaneResourceAddress string `json:"controlPlaneResourceAddress,omitempty"`
	// ExposedEndpoint is the address workers use to join the cluster.
	// Format: "<ip>:<port>" or "<hostname>:<port>"
	ExposedEndpoint string `json:"exposedEndpoint,omitempty"`
	// ServiceName is the control plane service name.
	ServiceName string `json:"serviceName,omitempty"`
	// ServiceNamespace is the control plane service namespace.
	ServiceNamespace string `json:"serviceNamespace,omitempty"`
	// ServiceType is either "LoadBalancer" or "NodePort".
	ServiceType string `json:"serviceType,omitempty"`
	// ExternalAddress is the LoadBalancer external address (if applicable)
	ExternalAddress string `json:"externalAddress,omitempty"`
	// UseDirectConnection indicates if workers connect directly (true for LB)
	// or via NetBird routing peer (false for NodePort)
	UseDirectConnection bool `json:"useDirectConnection,omitempty"`
	// Ready indicates whether this cluster's NetBird resources are ready.
	Ready bool `json:"ready,omitempty"`
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

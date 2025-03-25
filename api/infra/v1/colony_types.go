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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	helmv1alpha1 "sigs.k8s.io/cluster-api-addon-provider-helm/api/v1alpha1"
)

// ColonySpec defines the desired state of Colony.
type ColonySpec struct {
	// K8sVersion is the version of Kubernetes to use for the colony.
	K8sVersion string `json:"k8sVersion"`
	// WorkloadDependencies is the list of workload dependencies to use for the colony.
	// This is a list of already pre-installed HelmChartProxy resources that can be specified by name.
	// +optional
	WorkloadDependencies *[]WorkloadDependency `json:"workloadDependencies,omitempty"`

	// AdditionalDependencies is the list of additional dependencies to use for the colony.
	// This is a list of HelmChartProxy resources that will be installed on the colony.
	// See https://cluster-api-addon-provider-helm.sigs.k8s.io/getting-started/
	// +optional
	AdditionalDependencies *[]HelmChartProxyReference `json:"additionalDependencies,omitempty"`

	// HostedControlPlaneEnabled indicates if the hosted control plane is enabled.
	// If this is true, the colony will create a hosted control plane in the management cluster.
	// If this is false, the colony will create a control plane in the colony cluster.
	// If this is not set, the colony will create a control plane in the colony cluster.
	// +optional
	HostedControlPlaneEnabled *bool `json:"hostedControlPlaneEnabled,omitempty"`

	// ColonyClusters is the list of clusters to create.
	ColonyClusters []ColonyCluster `json:"colonyClusters,omitempty"`
}

type ColonyCluster struct {
	// Name is the name of the cluster.
	ClusterName string `json:"clusterName"`

	// DockerEnabled indicates if the docker provider is enabled.
	// This should be only used for local development and testing.
	DockerEnabled *bool `json:"dockerEnabled,omitempty"`
	// Docker is the specification for the docker resources.
	Docker *DockerSpec `json:"docker,omitempty"`

	// AWSEnabled indicates if the AWS provider is enabled.
	// setting this to true requires also setting the AWS spec.
	AWSEnabled *bool `json:"awsEnabled,omitempty"`
	// AWS is the specification for the AWS resources.
	AWS *AWSSpec `json:"aws,omitempty"`

	// AzureEnabled indicates if the Azure provider is enabled.
	// setting this to true requires also setting the Azure spec.
	AzureEnabled *bool `json:"azureEnabled,omitempty"`
	// Azure is the specification for the Azure resources.
	Azure *AzureSpec `json:"azure,omitempty"`

	// RemoteClusterEnabled indicates that this cluster is a remote cluster.
	// It won't create any resources itself, but arbitrary nodes can be added
	// to the cluster via ssh or token-based authentication.
	RemoteClusterEnabled *bool `json:"remoteClusterEnabled,omitempty"`
}

// WorkloadDependency is a workload dependency to be installed on the colony.
// It currently maps to a HelmChartProxy resource
type WorkloadDependency struct {
	// Name is the name of the workload dependency, it maps to the name of the HelmChartProxy resource that
	// has to exist in the cluster.
	Name string `json:"name"`
}

type HelmChartProxyReference struct {
	Name string                          `json:"name"`
	Spec helmv1alpha1.HelmChartProxySpec `json:"spec"`
}

type AWSSpec struct {
	// Replicas is the number of AWS instances to create.
	Replicas int32 `json:"replicas"`
	// AMI is the ID of the AWS Machine Image.
	AMI string `json:"ami"`
	// Region is the AWS region.
	Region string `json:"region,omitempty"`
	// InstanceType is the type of the AWS instance.
	InstanceType string `json:"instanceType"`
	// SSHKeyName is the name of the SSH key to use for the AWS instances.
	SSHKeyName string `json:"sshKeyName"`
	// IAMInstanceProfile is the IAM instance profile to use for the AWS instances.
	IAMInstanceProfile string `json:"iamInstanceProfile"`
}

type AzureSpec struct {
	// Region is the Azure region.
	Region string `json:"region"`
}

type DockerSpec struct {
	// Replicas is the number of "Docker Nodes" to create.
	Replicas int32 `json:"replicas"`
}

// ColonyStatus defines the observed state of Colony.
type ColonyStatus struct {
	Phase         string                    `json:"phase,omitempty"`
	Conditions    []metav1.Condition        `json:"conditions,omitempty"`
	ClusterRefs   []*corev1.ObjectReference `json:"clusterRefs,omitempty"`
	TotalClusters int32                     `json:"totalClusters"`
	ReadyClusters int32                     `json:"readyClusters"`
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

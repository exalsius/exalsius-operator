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
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ColonySpec defines the desired state of Colony.
type ColonySpec struct {
	// ClusterName is the name of the cluster.
	ClusterName string `json:"clusterName"`
	// K8sVersion is the version of Kubernetes to use for the cluster.
	K8sVersion string `json:"k8sVersion"`
	// WorkloadDependencies is the list of workload dependencies to use for the colony.
	// +optional
	WorkloadDependencies []WorkloadDependency `json:"workloadDependencies,omitempty"`
	// AWS is the specification for the AWS resources.
	AWS *AWSSpec `json:"aws,omitempty"`
	// Azure is the specification for the Azure resources.
	Azure *AzureSpec `json:"azure,omitempty"`
}

type WorkloadDependency struct {
	// Name is the name of the workload dependency.
	Name string `json:"name"`
	// Version is the version of the workload dependency.
	Version string `json:"version"`
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

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DilocoTorchDDPSpec defines the desired state of DilocoTorchDDP.
type DilocoTorchDDPSpec struct {
	Parallelism  int32    `json:"parallelism,omitempty"`  // Number of GPUs (Pods) to use for training
	NProcPerNode int32    `json:"nprocPerNode,omitempty"` // Number of processes per node (GPU per pod)
	Image        string   `json:"image,omitempty"`        // Docker image to use for training
	ScriptPath   string   `json:"scriptPath,omitempty"`   // Path to the training script
	WandBAPIKey  string   `json:"wandbApiKey,omitempty"`  // WandB API key
	Args         []string `json:"args,omitempty"`         // Arguments to pass to the training script
}

// DilocoTorchDDPStatus defines the observed state of DilocoTorchDDP.
type DilocoTorchDDPStatus struct {
	JobName string `json:"jobName,omitempty"` // Name of the training job
	Status  string `json:"status,omitempty"`  // Status of the training job
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DilocoTorchDDP is the Schema for the dilocotorchddps API.
type DilocoTorchDDP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DilocoTorchDDPSpec   `json:"spec,omitempty"`
	Status DilocoTorchDDPStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DilocoTorchDDPList contains a list of DilocoTorchDDP.
type DilocoTorchDDPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DilocoTorchDDP `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DilocoTorchDDP{}, &DilocoTorchDDPList{})
}

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
	GPUTypes      []string `json:"gpuTypes,omitempty"`      // List of GPU types to use for training
	TargetCluster *string  `json:"targetCluster,omitempty"` // Target cluster to run the training job (optional)
	Parallelism   int32    `json:"parallelism,omitempty"`   // Number of GPUs (Pods) to use for training
	NProcPerNode  int32    `json:"nprocPerNode,omitempty"`  // Number of processes per node (GPU per pod)
	Image         string   `json:"image,omitempty"`         // Docker image to use for training
	ScriptPath    string   `json:"scriptPath,omitempty"`    // Path to the training script
	WandBAPIKey   string   `json:"wandbApiKey,omitempty"`   // WandB API key
	Args          []string `json:"args,omitempty"`          // Arguments to pass to the training script
}

// JobPhase defines the phase of the job.
type JobPhase string

const (
	// Pending is the phase that job is pending in the queue, waiting for scheduling decision
	Pending JobPhase = "Pending"
	// Aborting is the phase that job is aborted, waiting for releasing pods
	Aborting JobPhase = "Aborting"
	// Aborted is the phase that job is aborted by user or error handling
	Aborted JobPhase = "Aborted"
	// Running is the phase that minimal available tasks of Job are running
	Running JobPhase = "Running"
	// Restarting is the phase that the Job is restarted, waiting for pod releasing and recreating
	Restarting JobPhase = "Restarting"
	// Completing is the phase that required tasks of job are completed, job starts to clean up
	Completing JobPhase = "Completing"
	// Completed is the phase that all tasks of Job are completed
	Completed JobPhase = "Completed"
	// Terminating is the phase that the Job is terminated, waiting for releasing pods
	Terminating JobPhase = "Terminating"
	// Terminated is the phase that the job is finished unexpected, e.g. events
	Terminated JobPhase = "Terminated"
	// Failed is the phase that the job is restarted failed reached the maximum number of retries.
	Failed JobPhase = "Failed"
)

// DilocoTorchDDPStatus defines the observed state of DilocoTorchDDP.
type DilocoTorchDDPStatus struct {
	JobName string   `json:"jobName,omitempty"` // Name of the training job
	Phase   JobPhase `json:"phase,omitempty"`   // Phase of the training job
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

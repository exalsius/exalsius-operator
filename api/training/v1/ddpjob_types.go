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

// DDPJobSpec defines the desired state of DDPJob.
type DDPJobSpec struct {
	// CPUJob describes if the job should be running on a CPU.
	// Mainly used for testing purposes, defaults to false if not provided
	// +optional
	CPUJob *bool `json:"cpuJob,omitempty"`

	// GPUTypes describes the list of GPU types to use for training
	// Currently, the user can specify different GPU types which the exalsius-cli uses to find the cheapest prices
	// across different cloud providers and regions. When specified in the CRD, it does not have an effect yet-
	// TODO: think about removing this field from the CRD and create a specific exalsius Job description file?
	// +optional
	GPUTypes []string `json:"gpuTypes,omitempty"`

	// TargetColony describes the colony to run the training job on.
	// If not provided, the job will be run on the management cluster.
	// +optional
	TargetColony *string `json:"targetColony,omitempty"`

	// Parallelism describes the number of GPUs (Pods) to use for training
	Parallelism int32 `json:"parallelism,omitempty"`

	// NProcPerNode describes the number of processes per node (GPU per pod)
	NProcPerNode int32 `json:"nprocPerNode,omitempty"`

	// Image describes the docker image with the training code
	Image string `json:"image,omitempty"`

	// ScriptPath describes the path to the training script
	ScriptPath string `json:"scriptPath,omitempty"`

	// WandBAPIKey describes the WandB API key
	WandBAPIKey string `json:"wandbApiKey,omitempty"`

	// Args describes the arguments to pass to the training script
	Args []string `json:"args,omitempty"`
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

// DDPJobStatus defines the observed state of DDPJob.
type DDPJobStatus struct {
	JobName string   `json:"jobName,omitempty"` // Name of the training job
	Phase   JobPhase `json:"phase,omitempty"`   // Phase of the training job
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DDPJob is the Schema for the ddpjobs API.
type DDPJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DDPJobSpec   `json:"spec,omitempty"`
	Status DDPJobStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DDPJobList contains a list of DDPJob.
type DDPJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DDPJob `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DDPJob{}, &DDPJobList{})
}

//go:build !ignore_autogenerated

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

// Code generated by controller-gen. DO NOT EDIT.

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AWSSpec) DeepCopyInto(out *AWSSpec) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AWSSpec.
func (in *AWSSpec) DeepCopy() *AWSSpec {
	if in == nil {
		return nil
	}
	out := new(AWSSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AzureSpec) DeepCopyInto(out *AzureSpec) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AzureSpec.
func (in *AzureSpec) DeepCopy() *AzureSpec {
	if in == nil {
		return nil
	}
	out := new(AzureSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Colony) DeepCopyInto(out *Colony) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Colony.
func (in *Colony) DeepCopy() *Colony {
	if in == nil {
		return nil
	}
	out := new(Colony)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *Colony) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ColonyCluster) DeepCopyInto(out *ColonyCluster) {
	*out = *in
	if in.DockerEnabled != nil {
		in, out := &in.DockerEnabled, &out.DockerEnabled
		*out = new(bool)
		**out = **in
	}
	if in.Docker != nil {
		in, out := &in.Docker, &out.Docker
		*out = new(DockerSpec)
		**out = **in
	}
	if in.AWSEnabled != nil {
		in, out := &in.AWSEnabled, &out.AWSEnabled
		*out = new(bool)
		**out = **in
	}
	if in.AWS != nil {
		in, out := &in.AWS, &out.AWS
		*out = new(AWSSpec)
		**out = **in
	}
	if in.AzureEnabled != nil {
		in, out := &in.AzureEnabled, &out.AzureEnabled
		*out = new(bool)
		**out = **in
	}
	if in.Azure != nil {
		in, out := &in.Azure, &out.Azure
		*out = new(AzureSpec)
		**out = **in
	}
	if in.RemoteClusterEnabled != nil {
		in, out := &in.RemoteClusterEnabled, &out.RemoteClusterEnabled
		*out = new(bool)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ColonyCluster.
func (in *ColonyCluster) DeepCopy() *ColonyCluster {
	if in == nil {
		return nil
	}
	out := new(ColonyCluster)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ColonyList) DeepCopyInto(out *ColonyList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]Colony, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ColonyList.
func (in *ColonyList) DeepCopy() *ColonyList {
	if in == nil {
		return nil
	}
	out := new(ColonyList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ColonyList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ColonySpec) DeepCopyInto(out *ColonySpec) {
	*out = *in
	if in.WorkloadDependencies != nil {
		in, out := &in.WorkloadDependencies, &out.WorkloadDependencies
		*out = new([]WorkloadDependency)
		if **in != nil {
			in, out := *in, *out
			*out = make([]WorkloadDependency, len(*in))
			copy(*out, *in)
		}
	}
	if in.AdditionalDependencies != nil {
		in, out := &in.AdditionalDependencies, &out.AdditionalDependencies
		*out = new([]HelmChartProxyReference)
		if **in != nil {
			in, out := *in, *out
			*out = make([]HelmChartProxyReference, len(*in))
			for i := range *in {
				(*in)[i].DeepCopyInto(&(*out)[i])
			}
		}
	}
	if in.HostedControlPlaneEnabled != nil {
		in, out := &in.HostedControlPlaneEnabled, &out.HostedControlPlaneEnabled
		*out = new(bool)
		**out = **in
	}
	if in.ColonyClusters != nil {
		in, out := &in.ColonyClusters, &out.ColonyClusters
		*out = make([]ColonyCluster, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ColonySpec.
func (in *ColonySpec) DeepCopy() *ColonySpec {
	if in == nil {
		return nil
	}
	out := new(ColonySpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ColonyStatus) DeepCopyInto(out *ColonyStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.ClusterRefs != nil {
		in, out := &in.ClusterRefs, &out.ClusterRefs
		*out = make([]*corev1.ObjectReference, len(*in))
		for i := range *in {
			if (*in)[i] != nil {
				in, out := &(*in)[i], &(*out)[i]
				*out = new(corev1.ObjectReference)
				**out = **in
			}
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ColonyStatus.
func (in *ColonyStatus) DeepCopy() *ColonyStatus {
	if in == nil {
		return nil
	}
	out := new(ColonyStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *DockerSpec) DeepCopyInto(out *DockerSpec) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new DockerSpec.
func (in *DockerSpec) DeepCopy() *DockerSpec {
	if in == nil {
		return nil
	}
	out := new(DockerSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *HelmChartProxyReference) DeepCopyInto(out *HelmChartProxyReference) {
	*out = *in
	in.Spec.DeepCopyInto(&out.Spec)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new HelmChartProxyReference.
func (in *HelmChartProxyReference) DeepCopy() *HelmChartProxyReference {
	if in == nil {
		return nil
	}
	out := new(HelmChartProxyReference)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *WorkloadDependency) DeepCopyInto(out *WorkloadDependency) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new WorkloadDependency.
func (in *WorkloadDependency) DeepCopy() *WorkloadDependency {
	if in == nil {
		return nil
	}
	out := new(WorkloadDependency)
	in.DeepCopyInto(out)
	return out
}

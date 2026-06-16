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

// Package gpu derives the GPU Offerings present on a cluster from its node
// labels and advertised extended resources (ADR-0002). The same derivation
// feeds both the WorkspaceDeployment gate (live, against the target Child
// Cluster) and the Colony GPU Inventory poll, so the offering vocabulary the
// API advertises and the vocabulary the gate matches are identical by
// construction.
package gpu

import (
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
)

const (
	// DefaultModelLabel is the well-known node label, set by provisioning,
	// that carries an offering's canonical short model name (memory baked in,
	// e.g. "H100", "A100-80GB"). It is the offering's identity AND the label a
	// chart's nodeSelector places on, so deriving offerings from it guarantees
	// "passes the gate" implies "is placeable" (ADR-0002).
	DefaultModelLabel = "exalsius.ai/gpu-model"
	// DefaultVendorLabel optionally overrides the inferred vendor.
	DefaultVendorLabel = "exalsius.ai/gpu-vendor"

	// ResourceNvidiaGPU is the whole-GPU extended resource for NVIDIA.
	ResourceNvidiaGPU = "nvidia.com/gpu"
	// ResourceAMDGPU is the whole-GPU extended resource for AMD.
	ResourceAMDGPU = "amd.com/gpu"
)

// gpuLabelPrefixes are the node-label key prefixes captured verbatim onto an
// Offering for display and future selection (memory, MIG, vGPU). Identity is
// never derived from them.
var gpuLabelPrefixes = []string{"nvidia.com/", "amd.com/", "gpu.hami.io/", "exalsius.ai/gpu"}

// vendorResources maps each supported whole-GPU extended resource to its
// vendor.
var vendorResources = []struct {
	vendor       workspacesv1.GPUVendor
	resourceName string
}{
	{workspacesv1.GPUVendorNVIDIA, ResourceNvidiaGPU},
	{workspacesv1.GPUVendorAMD, ResourceAMDGPU},
}

// ResourceForVendor returns the whole-GPU extended resource name a pod must
// request for the given vendor, or "" if unknown. Used for coarse
// "any GPU of this vendor" requests where no concrete offering is resolved.
func ResourceForVendor(vendor workspacesv1.GPUVendor) string {
	for _, vr := range vendorResources {
		if vr.vendor == vendor {
			return vr.resourceName
		}
	}
	return ""
}

// Offering is a distinct kind of GPU available on a cluster (CONTEXT.md: GPU
// Offering). Identity is (Vendor, Model, Profile, ResourceName); the remaining
// fields are attributes aggregated across the cluster's nodes.
type Offering struct {
	// --- identity ---
	Vendor       workspacesv1.GPUVendor
	Model        string // canonical short name from the model label; memory baked in
	Profile      string // partition/MIG profile; empty for whole GPUs (reserved for later phases)
	ResourceName string // extended resource a pod must request (e.g. nvidia.com/gpu)

	// --- attributes ---
	Total  int64             // total GPUs of this offering across the cluster
	Nodes  int32             // number of nodes carrying it
	Labels map[string]string // representative raw vendor labels (data retention)
}

// Options controls how offerings are derived. Zero value uses the defaults.
type Options struct {
	ModelLabel  string
	VendorLabel string
}

func (o Options) modelLabel() string {
	if o.ModelLabel != "" {
		return o.ModelLabel
	}
	return DefaultModelLabel
}

func (o Options) vendorLabel() string {
	if o.VendorLabel != "" {
		return o.VendorLabel
	}
	return DefaultVendorLabel
}

type offeringKey struct {
	vendor, model, profile, resourceName string
}

// DeriveOfferings aggregates the GPU Offerings present on the given nodes,
// keyed on the model label. Nodes that are unschedulable, advertise no
// supported GPU resource, or lack the model label contribute nothing — a node
// without the canonical model label is not a placeable offering (ADR-0002), so
// it is deliberately invisible here. Offerings are returned in a stable order.
func DeriveOfferings(nodes []corev1.Node, opts Options) []Offering {
	modelLabel := opts.modelLabel()
	vendorLabel := opts.vendorLabel()

	byKey := map[offeringKey]*Offering{}
	for i := range nodes {
		n := &nodes[i]
		if !nodeIsSchedulable(n) {
			continue
		}
		model := n.Labels[modelLabel]
		if model == "" {
			continue
		}
		for _, vr := range vendorResources {
			count := gpuAllocatable(n, vr.resourceName)
			if count <= 0 {
				continue
			}
			vendor := vr.vendor
			if v := n.Labels[vendorLabel]; v != "" {
				vendor = workspacesv1.GPUVendor(v)
			}
			key := offeringKey{string(vendor), model, "", vr.resourceName}
			o := byKey[key]
			if o == nil {
				o = &Offering{
					Vendor:       vendor,
					Model:        model,
					ResourceName: vr.resourceName,
					Labels:       gpuLabels(n),
				}
				byKey[key] = o
			}
			o.Total += count
			o.Nodes++
		}
	}

	out := make([]Offering, 0, len(byKey))
	for _, o := range byKey {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].ResourceName < out[j].ResourceName
	})
	return out
}

// FindByModel returns the offering whose canonical model matches the requested
// concrete model name, if present. Used by the existence gate.
func FindByModel(offerings []Offering, model string) (Offering, bool) {
	for _, o := range offerings {
		if o.Model == model {
			return o, true
		}
	}
	return Offering{}, false
}

// AvailableForModel returns how many GPUs of the given canonical model are
// free to schedule right now (CONTEXT.md: Available Capacity): the allocatable
// units of the model's GPU resource on its nodes, minus the units already
// requested by pods running on those nodes. `found` is false when no
// schedulable node carries the model at all (i.e. the offering is absent) —
// callers distinguish "absent" (a gate failure) from "present but 0 free" (a
// wait). Counting is discrete-unit, so it covers whole GPUs and MIG slices
// alike; fractional/HAMi sharing is out of scope (ADR-0002).
func AvailableForModel(nodes []corev1.Node, pods []corev1.Pod, model string, opts Options) (free int64, found bool) {
	modelLabel := opts.modelLabel()

	// matching node name -> the GPU resource it advertises for this model.
	matched := map[string]string{}
	var total int64
	for i := range nodes {
		n := &nodes[i]
		if !nodeIsSchedulable(n) || n.Labels[modelLabel] != model {
			continue
		}
		for _, vr := range vendorResources {
			if c := gpuAllocatable(n, vr.resourceName); c > 0 {
				matched[n.Name] = vr.resourceName
				total += c
				found = true
			}
		}
	}
	if !found {
		return 0, false
	}

	var used int64
	for i := range pods {
		p := &pods[i]
		resourceName, ok := matched[p.Spec.NodeName]
		if !ok || !podConsumes(p) {
			continue
		}
		for ci := range p.Spec.Containers {
			if q, ok := p.Spec.Containers[ci].Resources.Requests[corev1.ResourceName(resourceName)]; ok {
				used += q.Value()
			}
		}
	}

	free = total - used
	if free < 0 {
		free = 0
	}
	return free, true
}

// podConsumes reports whether a pod currently occupies node resources that
// should be subtracted from availability.
func podConsumes(p *corev1.Pod) bool {
	if p.Spec.NodeName == "" {
		return false
	}
	switch p.Status.Phase {
	case corev1.PodSucceeded, corev1.PodFailed:
		return false
	}
	return true
}

// NodeSelectorForModel returns the node-label requirements that pin a pod to
// the given canonical model — the GPU Selector the chart applies and the gate
// validates (one identical selector, ADR-0002).
func NodeSelectorForModel(model string, opts Options) map[string]string {
	return map[string]string{opts.modelLabel(): model}
}

// nodeIsSchedulable filters out nodes that can't accept new workloads.
func nodeIsSchedulable(n *corev1.Node) bool {
	if n.Spec.Unschedulable {
		return false
	}
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady && c.Status != corev1.ConditionTrue {
			return false
		}
	}
	return true
}

// gpuAllocatable returns the node's allocatable count of the given extended
// GPU resource.
func gpuAllocatable(n *corev1.Node, resourceName string) int64 {
	if q, ok := n.Status.Allocatable[corev1.ResourceName(resourceName)]; ok {
		return q.Value()
	}
	return 0
}

// gpuLabels captures the GPU-relevant node labels for retention on the offering.
func gpuLabels(n *corev1.Node) map[string]string {
	out := map[string]string{}
	for k, v := range n.Labels {
		for _, p := range gpuLabelPrefixes {
			if strings.HasPrefix(k, p) {
				out[k] = v
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

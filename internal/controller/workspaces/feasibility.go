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

package workspaces

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
)

// Resource names used across this file.
const (
	resourceCPU              = "cpu"
	resourceMemory           = "memory"
	resourceEphemeralStorage = "ephemeral-storage"
	resourceNvidiaGPU        = "nvidia.com/gpu"
	resourceAMDGPU           = "amd.com/gpu"

	// nodeLabelGPUVendor is a common convention surface for the GPU vendor;
	// not all clusters set it. Used as a hint, not a hard filter.
	nodeLabelGPUVendor = "nvidia.com/gpu.present"
	// Common labels reflecting the GPU model on a node. Different cluster
	// operators populate different labels — we accept any of them.
	nodeLabelGPUProduct = "nvidia.com/gpu.product"
	nodeLabelGPUModel   = "nvidia.com/gpu.model"
)

// computeFeasibility checks whether the cluster reachable through
// regionalClient has enough free capacity to satisfy the demanded resources
// (Replicas × PerReplica). The math is approximate cluster-total: we sum
// allocatable across nodes and subtract pod requests, and compare against
// total demand. We do NOT bin-pack per node — pod placement is the
// scheduler's concern and may co-locate pods if affinity rules allow.
//
// GPU matching honours vendor and type as filters: nil means wildcard,
// concrete values constrain the count to nodes whose GPUs match.
func computeFeasibility(
	ctx context.Context,
	regionalClient client.Client,
	demanded workspacesv1.WorkspaceResourceSpec,
) (*workspacesv1.FeasibilityStatus, error) {
	replicas := int32(1)
	if demanded.Replicas != nil {
		replicas = *demanded.Replicas
	}

	totalDemand := totalsFromPerReplica(demanded.PerReplica, replicas)

	nodes := &corev1.NodeList{}
	if err := regionalClient.List(ctx, nodes); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	pods := &corev1.PodList{}
	if err := regionalClient.List(ctx, pods); err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	// Sum allocatable across nodes, filtered by the GPU constraints in the
	// demand spec (vendor/type). Non-matching nodes' GPU capacity is excluded
	// from the GPU total but their CPU/memory/storage still count.
	var available aggregate
	for i := range nodes.Items {
		n := &nodes.Items[i]
		if !nodeIsSchedulable(n) {
			continue
		}
		alloc := n.Status.Allocatable
		available.cpu.Add(quantityFor(alloc, corev1.ResourceCPU))
		available.memory.Add(quantityFor(alloc, corev1.ResourceMemory))
		available.storage.Add(quantityFor(alloc, corev1.ResourceEphemeralStorage))
		if gpuCount, gpuOk := nodeGPUMatches(n, demanded.PerReplica.GPUVendor, demanded.PerReplica.GPUType); gpuOk {
			available.gpu += gpuCount
		}
	}

	// Subtract pod resource requests (everything that's already running).
	for i := range pods.Items {
		p := &pods.Items[i]
		if !podConsumesResources(p) {
			continue
		}
		for _, c := range p.Spec.Containers {
			req := c.Resources.Requests
			subtract(&available.cpu, quantityFor(req, corev1.ResourceCPU))
			subtract(&available.memory, quantityFor(req, corev1.ResourceMemory))
			subtract(&available.storage, quantityFor(req, corev1.ResourceEphemeralStorage))
			if gpu := podContainerGPUCount(c.Resources.Requests); gpu > 0 {
				if podGPUMatchesNode(p, pods.Items, nodes.Items, demanded.PerReplica.GPUVendor, demanded.PerReplica.GPUType) {
					available.gpu -= gpu
				}
			}
		}
	}
	if available.gpu < 0 {
		available.gpu = 0
	}

	demand := aggregate{
		cpu:     quantityValue(totalDemand.CPU),
		memory:  quantityValue(totalDemand.Memory),
		storage: quantityValue(totalDemand.Storage),
		gpu:     int64(int32Value(totalDemand.GPUCount)),
	}

	missing := computeMissing(demand, available)
	fits := missing == nil

	status := &workspacesv1.FeasibilityStatus{
		Fits:        fits,
		EvaluatedAt: metav1.Now(),
		Demanded:    *totalDemand,
		Available:   aggregateToTotals(available, demanded.PerReplica.GPUVendor, demanded.PerReplica.GPUType),
		Missing:     missing,
		Message:     formatFeasibilityMessage(fits, demanded, totalDemand, available, missing),
	}
	return status, nil
}

// totalsFromPerReplica returns the absolute (cluster-wide) demand.
func totalsFromPerReplica(per workspacesv1.ResourceRequirements, replicas int32) *workspacesv1.ResourceTotals {
	t := &workspacesv1.ResourceTotals{
		GPUVendor: per.GPUVendor,
		GPUType:   per.GPUType,
	}
	if per.CPU != nil {
		q := per.CPU.DeepCopy()
		multiplyQuantity(&q, int64(replicas))
		t.CPU = &q
	}
	if per.Memory != nil {
		q := per.Memory.DeepCopy()
		multiplyQuantity(&q, int64(replicas))
		t.Memory = &q
	}
	if per.Storage != nil {
		q := per.Storage.DeepCopy()
		multiplyQuantity(&q, int64(replicas))
		t.Storage = &q
	}
	if per.GPUCount != nil {
		count := *per.GPUCount * replicas
		t.GPUCount = &count
	}
	return t
}

// aggregate is the working representation while summing/subtracting capacity.
type aggregate struct {
	cpu     resource.Quantity
	memory  resource.Quantity
	storage resource.Quantity
	gpu     int64
}

// computeMissing returns nil when demand fits, otherwise per-resource shortfall.
func computeMissing(demand, available aggregate) *workspacesv1.ResourceTotals {
	missing := &workspacesv1.ResourceTotals{}
	any := false

	if demand.cpu.Cmp(available.cpu) > 0 {
		diff := demand.cpu.DeepCopy()
		diff.Sub(available.cpu)
		missing.CPU = &diff
		any = true
	}
	if demand.memory.Cmp(available.memory) > 0 {
		diff := demand.memory.DeepCopy()
		diff.Sub(available.memory)
		missing.Memory = &diff
		any = true
	}
	if demand.storage.Cmp(available.storage) > 0 {
		diff := demand.storage.DeepCopy()
		diff.Sub(available.storage)
		missing.Storage = &diff
		any = true
	}
	if demand.gpu > available.gpu {
		gap := int32(demand.gpu - available.gpu)
		missing.GPUCount = &gap
		any = true
	}
	if !any {
		return nil
	}
	return missing
}

// aggregateToTotals converts the working aggregate into the API-facing
// ResourceTotals shape, preserving the GPU vendor/type scope used during the
// computation.
func aggregateToTotals(a aggregate, vendor *workspacesv1.GPUVendor, gpuType *string) workspacesv1.ResourceTotals {
	gpuCount := int32(a.gpu)
	cpu := a.cpu.DeepCopy()
	memory := a.memory.DeepCopy()
	storage := a.storage.DeepCopy()
	return workspacesv1.ResourceTotals{
		CPU:       &cpu,
		Memory:    &memory,
		Storage:   &storage,
		GPUCount:  &gpuCount,
		GPUVendor: vendor,
		GPUType:   gpuType,
	}
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

// nodeGPUMatches reports whether the node has GPUs matching the vendor/type
// constraints, and the count of matching GPUs from its allocatable.
//
// Matching rules:
//   - vendor==nil: any vendor is acceptable. We sum nvidia.com/gpu and
//     amd.com/gpu allocatable values.
//   - vendor==NVIDIA: only nvidia.com/gpu allocatable counts.
//   - vendor==AMD: only amd.com/gpu allocatable counts.
//   - gpuType==nil: any GPU model on the node counts.
//   - gpuType set: the node's GPU product/model labels must contain the
//     requested string (case-insensitive substring match).
func nodeGPUMatches(n *corev1.Node, vendor *workspacesv1.GPUVendor, gpuType *string) (int64, bool) {
	if gpuType != nil && *gpuType != "" {
		product := strings.ToLower(n.Labels[nodeLabelGPUProduct])
		model := strings.ToLower(n.Labels[nodeLabelGPUModel])
		needle := strings.ToLower(*gpuType)
		if !strings.Contains(product, needle) && !strings.Contains(model, needle) {
			return 0, false
		}
	}

	count := int64(0)
	if vendor == nil || *vendor == workspacesv1.GPUVendorNVIDIA {
		count += quantityValueInt(n.Status.Allocatable, corev1.ResourceName(resourceNvidiaGPU))
	}
	if vendor == nil || *vendor == workspacesv1.GPUVendorAMD {
		count += quantityValueInt(n.Status.Allocatable, corev1.ResourceName(resourceAMDGPU))
	}
	return count, count > 0 || (vendor == nil && gpuType == nil)
}

// podConsumesResources returns true when the pod is occupying resources
// that should be subtracted from cluster availability.
func podConsumesResources(p *corev1.Pod) bool {
	if p.Spec.NodeName == "" {
		// Unscheduled — not yet consuming a real node's resources.
		return false
	}
	switch p.Status.Phase {
	case corev1.PodSucceeded, corev1.PodFailed:
		return false
	}
	return true
}

// podContainerGPUCount returns the GPU count requested by a single container.
func podContainerGPUCount(req corev1.ResourceList) int64 {
	count := int64(0)
	if v, ok := req[corev1.ResourceName(resourceNvidiaGPU)]; ok {
		count += v.Value()
	}
	if v, ok := req[corev1.ResourceName(resourceAMDGPU)]; ok {
		count += v.Value()
	}
	return count
}

// podGPUMatchesNode checks whether the pod is running on a node that
// satisfies the demanded GPU vendor/type. Used to decide whether the pod's
// GPU consumption should be subtracted from our scope.
func podGPUMatchesNode(p *corev1.Pod, _ []corev1.Pod, nodes []corev1.Node, vendor *workspacesv1.GPUVendor, gpuType *string) bool {
	for i := range nodes {
		if nodes[i].Name != p.Spec.NodeName {
			continue
		}
		_, ok := nodeGPUMatches(&nodes[i], vendor, gpuType)
		return ok
	}
	return false
}

// formatFeasibilityMessage produces a human-readable summary line.
func formatFeasibilityMessage(
	fits bool,
	demanded workspacesv1.WorkspaceResourceSpec,
	totals *workspacesv1.ResourceTotals,
	avail aggregate,
	missing *workspacesv1.ResourceTotals,
) string {
	replicas := int32(1)
	if demanded.Replicas != nil {
		replicas = *demanded.Replicas
	}
	if fits {
		return fmt.Sprintf("Cluster has capacity for %d replica(s)", replicas)
	}
	parts := []string{}
	if missing.CPU != nil {
		parts = append(parts, fmt.Sprintf("%s CPU", missing.CPU.String()))
	}
	if missing.Memory != nil {
		parts = append(parts, fmt.Sprintf("%s memory", missing.Memory.String()))
	}
	if missing.Storage != nil {
		parts = append(parts, fmt.Sprintf("%s storage", missing.Storage.String()))
	}
	if missing.GPUCount != nil {
		gpuLabel := "GPU"
		if demanded.PerReplica.GPUType != nil && *demanded.PerReplica.GPUType != "" {
			gpuLabel = *demanded.PerReplica.GPUType + " GPU"
		} else if demanded.PerReplica.GPUVendor != nil {
			gpuLabel = string(*demanded.PerReplica.GPUVendor) + " GPU"
		}
		parts = append(parts, fmt.Sprintf("%d %s", *missing.GPUCount, gpuLabel))
	}
	return fmt.Sprintf("Insufficient capacity for %d replica(s); missing %s",
		replicas, strings.Join(parts, ", "))
}

// --- small helpers ---

func quantityFor(list corev1.ResourceList, name corev1.ResourceName) resource.Quantity {
	if q, ok := list[name]; ok {
		return q.DeepCopy()
	}
	return resource.Quantity{}
}

func quantityValue(q *resource.Quantity) resource.Quantity {
	if q == nil {
		return resource.Quantity{}
	}
	return q.DeepCopy()
}

func quantityValueInt(list corev1.ResourceList, name corev1.ResourceName) int64 {
	q := quantityFor(list, name)
	return q.Value()
}

func int32Value(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func subtract(dst *resource.Quantity, src resource.Quantity) {
	if src.IsZero() {
		return
	}
	dst.Sub(src)
}

// multiplyQuantity scales a Quantity by an integer factor, preserving
// fractional precision (e.g. "100m" CPU * 3 → "300m"). Uses MilliValue —
// safe for CPU/memory/storage in realistic workspace ranges (overflows at
// ~10 PB × 1000 replicas).
func multiplyQuantity(q *resource.Quantity, factor int64) {
	if factor == 1 {
		return
	}
	if factor == 0 {
		*q = resource.Quantity{}
		return
	}
	scaled := q.MilliValue() * factor
	*q = *resource.NewMilliQuantity(scaled, q.Format)
}

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

package gpu

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
)

// gpuNode builds a schedulable node advertising `count` NVIDIA GPUs, labelled
// with the canonical model (unless model is empty) plus any extra labels.
func gpuNode(name, model string, count int64, extra map[string]string) corev1.Node {
	labels := map[string]string{}
	for k, v := range extra {
		labels[k] = v
	}
	if model != "" {
		labels[DefaultModelLabel] = model
	}
	alloc := corev1.ResourceList{}
	if count > 0 {
		alloc[corev1.ResourceName(ResourceNvidiaGPU)] = *resource.NewQuantity(count, resource.DecimalSI)
	}
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{
			Allocatable: alloc,
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

func TestDeriveOfferings_SingleModel(t *testing.T) {
	offerings := DeriveOfferings([]corev1.Node{
		gpuNode("n1", "H100", 8, nil),
		gpuNode("n2", "H100", 4, nil),
	}, Options{})

	if len(offerings) != 1 {
		t.Fatalf("expected 1 offering, got %d: %+v", len(offerings), offerings)
	}
	o := offerings[0]
	if o.Model != "H100" || o.Vendor != workspacesv1.GPUVendorNVIDIA {
		t.Errorf("unexpected identity: %+v", o)
	}
	if o.ResourceName != ResourceNvidiaGPU {
		t.Errorf("expected resourceName %q, got %q", ResourceNvidiaGPU, o.ResourceName)
	}
	if o.Total != 12 || o.Nodes != 2 {
		t.Errorf("expected total=12 nodes=2, got total=%d nodes=%d", o.Total, o.Nodes)
	}
}

func TestDeriveOfferings_MemoryBakedModelsAreDistinct(t *testing.T) {
	offerings := DeriveOfferings([]corev1.Node{
		gpuNode("n1", "A100-80GB", 4, nil),
		gpuNode("n2", "A100-40GB", 8, nil),
	}, Options{})

	if len(offerings) != 2 {
		t.Fatalf("expected 2 distinct offerings, got %d: %+v", len(offerings), offerings)
	}
	if _, ok := FindByModel(offerings, "A100-80GB"); !ok {
		t.Error("A100-80GB offering missing")
	}
	if _, ok := FindByModel(offerings, "A100-40GB"); !ok {
		t.Error("A100-40GB offering missing")
	}
}

func TestDeriveOfferings_ExcludesNodeWithoutModelLabel(t *testing.T) {
	// A node with GPUs but no canonical model label is not a placeable
	// offering (ADR-0002) — it must be invisible to the gate.
	offerings := DeriveOfferings([]corev1.Node{
		gpuNode("n1", "", 8, map[string]string{"nvidia.com/gpu.product": "NVIDIA-H100-80GB-HBM3"}),
	}, Options{})
	if len(offerings) != 0 {
		t.Fatalf("expected 0 offerings for unlabelled node, got %d: %+v", len(offerings), offerings)
	}
}

func TestDeriveOfferings_ExcludesUnschedulableNode(t *testing.T) {
	n := gpuNode("n1", "H100", 8, nil)
	n.Spec.Unschedulable = true
	offerings := DeriveOfferings([]corev1.Node{n}, Options{})
	if len(offerings) != 0 {
		t.Fatalf("expected unschedulable node excluded, got %d", len(offerings))
	}
}

func TestDeriveOfferings_ExcludesNotReadyNode(t *testing.T) {
	n := gpuNode("n1", "H100", 8, nil)
	n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}
	offerings := DeriveOfferings([]corev1.Node{n}, Options{})
	if len(offerings) != 0 {
		t.Fatalf("expected not-ready node excluded, got %d", len(offerings))
	}
}

func TestDeriveOfferings_RetainsRawGpuLabels(t *testing.T) {
	offerings := DeriveOfferings([]corev1.Node{
		gpuNode("n1", "H100", 8, map[string]string{
			"nvidia.com/gpu.product": "NVIDIA-H100-80GB-HBM3",
			"kubernetes.io/hostname": "n1", // non-GPU label, must be dropped
		}),
	}, Options{})
	if len(offerings) != 1 {
		t.Fatalf("expected 1 offering, got %d", len(offerings))
	}
	labels := offerings[0].Labels
	if labels["nvidia.com/gpu.product"] != "NVIDIA-H100-80GB-HBM3" {
		t.Errorf("expected GFD product label retained, got %v", labels)
	}
	if _, ok := labels["kubernetes.io/hostname"]; ok {
		t.Errorf("non-GPU label should not be retained: %v", labels)
	}
}

func TestDeriveOfferings_VendorLabelOverridesInference(t *testing.T) {
	// An explicit vendor label wins over the resource-inferred vendor.
	offerings := DeriveOfferings([]corev1.Node{
		gpuNode("n1", "H100", 8, map[string]string{DefaultVendorLabel: string(workspacesv1.GPUVendorAMD)}),
	}, Options{})
	if len(offerings) != 1 || offerings[0].Vendor != workspacesv1.GPUVendorAMD {
		t.Fatalf("expected vendor label to win, got %+v", offerings)
	}
}

func TestDeriveOfferings_CustomModelLabel(t *testing.T) {
	const customKey = "acme.example/gpu"
	n := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{customKey: "H100"}},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{corev1.ResourceName(ResourceNvidiaGPU): *resource.NewQuantity(8, resource.DecimalSI)},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	offerings := DeriveOfferings([]corev1.Node{n}, Options{ModelLabel: customKey})
	if len(offerings) != 1 || offerings[0].Model != "H100" {
		t.Fatalf("expected offering via custom label, got %+v", offerings)
	}
}

func TestFindByModel(t *testing.T) {
	offerings := []Offering{{Model: "H100"}, {Model: "A100-80GB"}}
	if _, ok := FindByModel(offerings, "H100"); !ok {
		t.Error("expected H100 found")
	}
	if _, ok := FindByModel(offerings, "L40"); ok {
		t.Error("expected L40 absent")
	}
}

func TestNodeSelectorForModel(t *testing.T) {
	sel := NodeSelectorForModel("H100", Options{})
	if sel[DefaultModelLabel] != "H100" || len(sel) != 1 {
		t.Errorf("unexpected selector: %v", sel)
	}
	custom := NodeSelectorForModel("H100", Options{ModelLabel: "acme.example/gpu"})
	if custom["acme.example/gpu"] != "H100" {
		t.Errorf("expected custom-key selector, got %v", custom)
	}
}

// gpuPod builds a pod scheduled on nodeName requesting `count` NVIDIA GPUs.
func gpuPod(name, nodeName string, count int64) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
			Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceName(ResourceNvidiaGPU): *resource.NewQuantity(count, resource.DecimalSI),
					},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func TestAvailableForModel_NoPodsAllFree(t *testing.T) {
	free, found := AvailableForModel([]corev1.Node{
		gpuNode("n1", "H100", 8, nil),
		gpuNode("n2", "H100", 4, nil),
	}, nil, "H100", Options{})
	if !found {
		t.Fatal("expected found")
	}
	if free != 12 {
		t.Errorf("expected 12 free, got %d", free)
	}
}

func TestAvailableForModel_SubtractsPodsOnMatchingNodes(t *testing.T) {
	nodes := []corev1.Node{gpuNode("n1", "H100", 8, nil)}
	pods := []corev1.Pod{
		gpuPod("p1", "n1", 3),
		gpuPod("p2", "other-node", 2), // not on a matching node -> ignored
	}
	free, found := AvailableForModel(nodes, pods, "H100", Options{})
	if !found || free != 5 {
		t.Errorf("expected found and 5 free, got found=%v free=%d", found, free)
	}
}

func TestAvailableForModel_AbsentModel(t *testing.T) {
	_, found := AvailableForModel([]corev1.Node{gpuNode("n1", "H100", 8, nil)}, nil, "A100-80GB", Options{})
	if found {
		t.Error("expected not found for absent model")
	}
}

func TestAvailableForModel_FullClusterClampsToZero(t *testing.T) {
	nodes := []corev1.Node{gpuNode("n1", "H100", 2, nil)}
	pods := []corev1.Pod{gpuPod("p1", "n1", 2), gpuPod("p2", "n1", 1)} // over-subscribed
	free, found := AvailableForModel(nodes, pods, "H100", Options{})
	if !found || free != 0 {
		t.Errorf("expected found and 0 free (clamped), got found=%v free=%d", found, free)
	}
}

func TestAvailableForModel_IgnoresTerminatedPods(t *testing.T) {
	nodes := []corev1.Node{gpuNode("n1", "H100", 8, nil)}
	p := gpuPod("p1", "n1", 4)
	p.Status.Phase = corev1.PodSucceeded // freed
	free, _ := AvailableForModel(nodes, []corev1.Pod{p}, "H100", Options{})
	if free != 8 {
		t.Errorf("expected succeeded pod ignored (8 free), got %d", free)
	}
}

// amdNode builds a schedulable node advertising `count` AMD GPUs with the model label.
func amdNode(name, model string, count int64) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{DefaultModelLabel: model}},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceName(ResourceAMDGPU): *resource.NewQuantity(count, resource.DecimalSI),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

func TestDeriveOfferings_AMD(t *testing.T) {
	offerings := DeriveOfferings([]corev1.Node{amdNode("n1", "MI300X", 8)}, Options{})
	if len(offerings) != 1 {
		t.Fatalf("expected 1 offering, got %d", len(offerings))
	}
	o := offerings[0]
	if o.Vendor != workspacesv1.GPUVendorAMD {
		t.Errorf("expected AMD vendor, got %q", o.Vendor)
	}
	if o.ResourceName != ResourceAMDGPU {
		t.Errorf("expected %q, got %q", ResourceAMDGPU, o.ResourceName)
	}
	if o.Total != 8 {
		t.Errorf("expected total 8, got %d", o.Total)
	}
}

func TestAvailableForModel_AMD(t *testing.T) {
	nodes := []corev1.Node{amdNode("n1", "MI300X", 4)}
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "n1",
			Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceName(ResourceAMDGPU): *resource.NewQuantity(1, resource.DecimalSI)},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	free, found := AvailableForModel(nodes, []corev1.Pod{pod}, "MI300X", Options{})
	if !found || free != 3 {
		t.Errorf("expected found and 3 free, got found=%v free=%d", found, free)
	}
}

func TestResourceForVendor(t *testing.T) {
	if got := ResourceForVendor(workspacesv1.GPUVendorNVIDIA); got != ResourceNvidiaGPU {
		t.Errorf("NVIDIA -> %q, want %q", got, ResourceNvidiaGPU)
	}
	if got := ResourceForVendor(workspacesv1.GPUVendorAMD); got != ResourceAMDGPU {
		t.Errorf("AMD -> %q, want %q", got, ResourceAMDGPU)
	}
	if got := ResourceForVendor("Bogus"); got != "" {
		t.Errorf("unknown vendor -> %q, want empty", got)
	}
}

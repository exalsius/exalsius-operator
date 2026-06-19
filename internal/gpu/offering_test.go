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

const (
	testModelH100   = "H100"
	testProductH100 = "NVIDIA-H100-80GB-HBM3"
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
		gpuNode("n1", testModelH100, 8, nil),
		gpuNode("n2", testModelH100, 4, nil),
	}, Options{})

	if len(offerings) != 1 {
		t.Fatalf("expected 1 offering, got %d: %+v", len(offerings), offerings)
	}
	o := offerings[0]
	if o.Model != testModelH100 || o.Vendor != workspacesv1.GPUVendorNVIDIA {
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

func TestDeriveOfferings_CanonicalSelector(t *testing.T) {
	// A canonically-labelled node carries a Selector on the canonical label.
	offerings := DeriveOfferings([]corev1.Node{gpuNode("n1", testModelH100, 8, nil)}, Options{})
	if len(offerings) != 1 {
		t.Fatalf("expected 1 offering, got %d", len(offerings))
	}
	if got := offerings[0].Selector[DefaultModelLabel]; got != testModelH100 {
		t.Errorf("expected selector %s=H100, got %v", DefaultModelLabel, offerings[0].Selector)
	}
}

func TestDeriveOfferings_DerivesModelFromProductLabel(t *testing.T) {
	// A node with GPUs but no canonical model label is now discoverable
	// (ADR-0002, revised): its model comes from the vendor product label and the
	// Selector targets that label, so it stays placeable via gpuNodeSelector.
	offerings := DeriveOfferings([]corev1.Node{
		gpuNode("n1", "", 8, map[string]string{"nvidia.com/gpu.product": testProductH100}),
	}, Options{})
	if len(offerings) != 1 {
		t.Fatalf("expected 1 offering for product-labelled node, got %d: %+v", len(offerings), offerings)
	}
	o := offerings[0]
	if o.Model != testProductH100 {
		t.Errorf("expected model from product label, got %q", o.Model)
	}
	if o.Selector["nvidia.com/gpu.product"] != testProductH100 {
		t.Errorf("expected selector on product label, got %v", o.Selector)
	}
}

func TestDeriveOfferings_CanonicalLabelWinsOverProductLabel(t *testing.T) {
	// When both are present, the canonical label is the identity and the Selector.
	offerings := DeriveOfferings([]corev1.Node{
		gpuNode("n1", testModelH100, 8, map[string]string{"nvidia.com/gpu.product": testProductH100}),
	}, Options{})
	if len(offerings) != 1 {
		t.Fatalf("expected 1 offering, got %d", len(offerings))
	}
	o := offerings[0]
	if o.Model != testModelH100 || o.Selector[DefaultModelLabel] != testModelH100 {
		t.Errorf("expected canonical to win, got model=%q selector=%v", o.Model, o.Selector)
	}
}

func TestDeriveOfferings_LabelLessNodeHasEmptyModelAndSelector(t *testing.T) {
	// A GPU node with no canonical and no product label is still discoverable,
	// but with an empty model and nil Selector — the caller composes one from Labels.
	offerings := DeriveOfferings([]corev1.Node{gpuNode("n1", "", 4, nil)}, Options{})
	if len(offerings) != 1 {
		t.Fatalf("expected 1 offering for bare GPU node, got %d", len(offerings))
	}
	o := offerings[0]
	if o.Model != "" || o.Selector != nil {
		t.Errorf("expected empty model and nil selector, got model=%q selector=%v", o.Model, o.Selector)
	}
	if o.Vendor != workspacesv1.GPUVendorNVIDIA || o.Total != 4 {
		t.Errorf("unexpected offering: %+v", o)
	}
}

func TestDeriveOfferings_AMDProductLabelCandidateList(t *testing.T) {
	// AMD has no single product label; the default candidate list falls back to
	// the device-id when the product-name label is absent.
	n := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{"amd.com/gpu.device-id": "74a1"}},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{corev1.ResourceName(ResourceAMDGPU): *resource.NewQuantity(8, resource.DecimalSI)},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	offerings := DeriveOfferings([]corev1.Node{n}, Options{})
	if len(offerings) != 1 {
		t.Fatalf("expected 1 offering, got %d", len(offerings))
	}
	o := offerings[0]
	if o.Vendor != workspacesv1.GPUVendorAMD || o.Model != "74a1" {
		t.Errorf("expected AMD model from device-id, got vendor=%q model=%q", o.Vendor, o.Model)
	}
	if o.Selector["amd.com/gpu.device-id"] != "74a1" {
		t.Errorf("expected selector on device-id, got %v", o.Selector)
	}
}

func TestDeriveOfferings_CanonicalAndProductBackedAreDistinct(t *testing.T) {
	// The same physical GPU surfaces as two offerings when one node is canonically
	// labelled and another only by a vendor label — each needs a different Selector.
	offerings := DeriveOfferings([]corev1.Node{
		gpuNode("n1", testModelH100, 8, nil),
		gpuNode("n2", "", 8, map[string]string{"nvidia.com/gpu.product": testProductH100}),
	}, Options{})
	if len(offerings) != 2 {
		t.Fatalf("expected 2 distinct offerings, got %d: %+v", len(offerings), offerings)
	}
}

func TestDeriveOfferings_ExcludesUnschedulableNode(t *testing.T) {
	n := gpuNode("n1", testModelH100, 8, nil)
	n.Spec.Unschedulable = true
	offerings := DeriveOfferings([]corev1.Node{n}, Options{})
	if len(offerings) != 0 {
		t.Fatalf("expected unschedulable node excluded, got %d", len(offerings))
	}
}

func TestDeriveOfferings_ExcludesNotReadyNode(t *testing.T) {
	n := gpuNode("n1", testModelH100, 8, nil)
	n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}
	offerings := DeriveOfferings([]corev1.Node{n}, Options{})
	if len(offerings) != 0 {
		t.Fatalf("expected not-ready node excluded, got %d", len(offerings))
	}
}

func TestDeriveOfferings_RetainsRawGpuLabels(t *testing.T) {
	offerings := DeriveOfferings([]corev1.Node{
		gpuNode("n1", testModelH100, 8, map[string]string{
			"nvidia.com/gpu.product": testProductH100,
			"kubernetes.io/hostname": "n1", // non-GPU label, must be dropped
		}),
	}, Options{})
	if len(offerings) != 1 {
		t.Fatalf("expected 1 offering, got %d", len(offerings))
	}
	labels := offerings[0].Labels
	if labels["nvidia.com/gpu.product"] != testProductH100 {
		t.Errorf("expected GFD product label retained, got %v", labels)
	}
	if _, ok := labels["kubernetes.io/hostname"]; ok {
		t.Errorf("non-GPU label should not be retained: %v", labels)
	}
}

func TestDeriveOfferings_VendorLabelOverridesInference(t *testing.T) {
	// An explicit vendor label wins over the resource-inferred vendor.
	offerings := DeriveOfferings([]corev1.Node{
		gpuNode("n1", testModelH100, 8, map[string]string{DefaultVendorLabel: string(workspacesv1.GPUVendorAMD)}),
	}, Options{})
	if len(offerings) != 1 || offerings[0].Vendor != workspacesv1.GPUVendorAMD {
		t.Fatalf("expected vendor label to win, got %+v", offerings)
	}
}

func TestDeriveOfferings_CustomModelLabel(t *testing.T) {
	const customKey = "acme.example/gpu"
	n := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{customKey: testModelH100}},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{corev1.ResourceName(ResourceNvidiaGPU): *resource.NewQuantity(8, resource.DecimalSI)},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	offerings := DeriveOfferings([]corev1.Node{n}, Options{ModelLabel: customKey})
	if len(offerings) != 1 || offerings[0].Model != testModelH100 {
		t.Fatalf("expected offering via custom label, got %+v", offerings)
	}
}

func TestFindByModel(t *testing.T) {
	offerings := []Offering{{Model: testModelH100}, {Model: "A100-80GB"}}
	if _, ok := FindByModel(offerings, testModelH100); !ok {
		t.Error("expected H100 found")
	}
	if _, ok := FindByModel(offerings, "L40"); ok {
		t.Error("expected L40 absent")
	}
}

func TestNodeSelectorForModel(t *testing.T) {
	sel := NodeSelectorForModel(testModelH100, Options{})
	if sel[DefaultModelLabel] != testModelH100 || len(sel) != 1 {
		t.Errorf("unexpected selector: %v", sel)
	}
	custom := NodeSelectorForModel(testModelH100, Options{ModelLabel: "acme.example/gpu"})
	if custom["acme.example/gpu"] != testModelH100 {
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
		gpuNode("n1", testModelH100, 8, nil),
		gpuNode("n2", testModelH100, 4, nil),
	}, nil, testModelH100, Options{})
	if !found {
		t.Fatal("expected found")
	}
	if free != 12 {
		t.Errorf("expected 12 free, got %d", free)
	}
}

func TestAvailableForModel_SubtractsPodsOnMatchingNodes(t *testing.T) {
	nodes := []corev1.Node{gpuNode("n1", testModelH100, 8, nil)}
	pods := []corev1.Pod{
		gpuPod("p1", "n1", 3),
		gpuPod("p2", "other-node", 2), // not on a matching node -> ignored
	}
	free, found := AvailableForModel(nodes, pods, testModelH100, Options{})
	if !found || free != 5 {
		t.Errorf("expected found and 5 free, got found=%v free=%d", found, free)
	}
}

func TestAvailableForModel_AbsentModel(t *testing.T) {
	_, found := AvailableForModel([]corev1.Node{gpuNode("n1", testModelH100, 8, nil)}, nil, "A100-80GB", Options{})
	if found {
		t.Error("expected not found for absent model")
	}
}

func TestAvailableForModel_FullClusterClampsToZero(t *testing.T) {
	nodes := []corev1.Node{gpuNode("n1", testModelH100, 2, nil)}
	pods := []corev1.Pod{gpuPod("p1", "n1", 2), gpuPod("p2", "n1", 1)} // over-subscribed
	free, found := AvailableForModel(nodes, pods, testModelH100, Options{})
	if !found || free != 0 {
		t.Errorf("expected found and 0 free (clamped), got found=%v free=%d", found, free)
	}
}

func TestAvailableForModel_IgnoresTerminatedPods(t *testing.T) {
	nodes := []corev1.Node{gpuNode("n1", testModelH100, 8, nil)}
	p := gpuPod("p1", "n1", 4)
	p.Status.Phase = corev1.PodSucceeded // freed
	free, _ := AvailableForModel(nodes, []corev1.Pod{p}, testModelH100, Options{})
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

func TestBuildSelector(t *testing.T) {
	// model only
	if got := BuildSelector(testModelH100, nil, Options{}); got[DefaultModelLabel] != testModelH100 || len(got) != 1 {
		t.Errorf("model-only: %v", got)
	}
	// raw only
	if got := BuildSelector("", map[string]string{"nvidia.com/gpu.product": "X"}, Options{}); got["nvidia.com/gpu.product"] != "X" || len(got) != 1 {
		t.Errorf("raw-only: %v", got)
	}
	// both merged
	got := BuildSelector(testModelH100, map[string]string{"k": "v"}, Options{})
	if got[DefaultModelLabel] != testModelH100 || got["k"] != "v" || len(got) != 2 {
		t.Errorf("merged: %v", got)
	}
	// raw wins on collision
	if got := BuildSelector(testModelH100, map[string]string{DefaultModelLabel: "override"}, Options{}); got[DefaultModelLabel] != "override" {
		t.Errorf("raw should win on collision: %v", got)
	}
	// empty
	if got := BuildSelector("", nil, Options{}); len(got) != 0 {
		t.Errorf("empty: %v", got)
	}
}

func TestAvailabilityForSelector(t *testing.T) {
	// A node lacking the canonical model label but carrying a raw GFD label.
	rawNode := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{"nvidia.com/gpu.product": testProductH100}},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{corev1.ResourceName(ResourceNvidiaGPU): *resource.NewQuantity(4, resource.DecimalSI)},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	sel := map[string]string{"nvidia.com/gpu.product": testProductH100}

	avail := AvailabilityForSelector([]corev1.Node{rawNode}, nil, sel)
	if !avail.Found || avail.Free != 4 || avail.ResourceName != ResourceNvidiaGPU || avail.Vendor != workspacesv1.GPUVendorNVIDIA {
		t.Errorf("raw selector match: %+v", avail)
	}

	// Capacity subtracts pods on matching nodes.
	withPod := AvailabilityForSelector([]corev1.Node{rawNode}, []corev1.Pod{gpuPod("p", "n1", 3)}, sel)
	if withPod.Free != 1 {
		t.Errorf("expected 1 free after pod, got %d", withPod.Free)
	}

	// A non-matching selector finds nothing.
	if avail := AvailabilityForSelector([]corev1.Node{rawNode}, nil, map[string]string{"nvidia.com/gpu.product": "OTHER"}); avail.Found {
		t.Errorf("non-matching selector should not be found: %+v", avail)
	}

	// Empty selector finds nothing.
	if avail := AvailabilityForSelector([]corev1.Node{rawNode}, nil, nil); avail.Found {
		t.Error("empty selector should not be found")
	}
}

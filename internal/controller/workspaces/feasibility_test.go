package workspaces

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
)

func mustQty(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}

func nodeWithGPU(name string, cpu, mem string, vendor, product string, gpuCount int64) *corev1.Node {
	alloc := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(mem),
	}
	labels := map[string]string{}
	if gpuCount > 0 {
		switch vendor {
		case "NVIDIA":
			alloc[corev1.ResourceName(resourceNvidiaGPU)] = *resource.NewQuantity(gpuCount, resource.DecimalSI)
		case "AMD":
			alloc[corev1.ResourceName(resourceAMDGPU)] = *resource.NewQuantity(gpuCount, resource.DecimalSI)
		}
		if product != "" {
			labels[nodeLabelGPUProduct] = product
		}
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{
			Allocatable: alloc,
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func newFakeClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(objs...).Build()
}

func TestFeasibility_FitsWhenClusterHasCapacity(t *testing.T) {
	c := newFakeClient(
		nodeWithGPU("n1", "16", "64Gi", "NVIDIA", "NVIDIA-H100", 4),
	)
	demand := workspacesv1.WorkspaceResourceSpec{
		Replicas: int32Ptr(1),
		PerReplica: workspacesv1.ResourceRequirements{
			CPU:      mustQty("4"),
			Memory:   mustQty("16Gi"),
			GPUCount: int32Ptr(1),
		},
	}
	feas, err := computeFeasibility(context.Background(), c, demand)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !feas.Fits {
		t.Errorf("expected fits=true, got false (message=%q, missing=%+v)", feas.Message, feas.Missing)
	}
}

func TestFeasibility_DoesNotFitOnInsufficientCPU(t *testing.T) {
	c := newFakeClient(
		nodeWithGPU("n1", "2", "64Gi", "", "", 0),
	)
	demand := workspacesv1.WorkspaceResourceSpec{
		Replicas: int32Ptr(2),
		PerReplica: workspacesv1.ResourceRequirements{
			CPU:    mustQty("2"),
			Memory: mustQty("4Gi"),
		},
	}
	feas, err := computeFeasibility(context.Background(), c, demand)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feas.Fits {
		t.Errorf("expected fits=false")
	}
	if feas.Missing == nil || feas.Missing.CPU == nil {
		t.Errorf("expected missing.CPU to be set, got %+v", feas.Missing)
	}
}

func TestFeasibility_GPUTypeWildcardCountsAnyModel(t *testing.T) {
	c := newFakeClient(
		nodeWithGPU("n1", "16", "64Gi", "NVIDIA", "NVIDIA-A100", 2),
		nodeWithGPU("n2", "16", "64Gi", "NVIDIA", "NVIDIA-H100", 2),
	)
	nvidia := workspacesv1.GPUVendorNVIDIA
	demand := workspacesv1.WorkspaceResourceSpec{
		Replicas: int32Ptr(1),
		PerReplica: workspacesv1.ResourceRequirements{
			CPU:       mustQty("2"),
			Memory:    mustQty("4Gi"),
			GPUCount:  int32Ptr(3),
			GPUVendor: &nvidia,
			// GPUType nil → both A100 and H100 should count → 4 GPUs total
		},
	}
	feas, err := computeFeasibility(context.Background(), c, demand)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !feas.Fits {
		t.Errorf("expected fits=true (4 GPUs available, demand 3); got missing=%+v", feas.Missing)
	}
}

func TestFeasibility_GPUTypeStrictMatchOnlyCountsMatchingNodes(t *testing.T) {
	c := newFakeClient(
		nodeWithGPU("a100-node", "16", "64Gi", "NVIDIA", "NVIDIA-A100", 4),
		nodeWithGPU("h100-node", "16", "64Gi", "NVIDIA", "NVIDIA-H100", 1),
	)
	nvidia := workspacesv1.GPUVendorNVIDIA
	h100 := testGPUTypeH100
	demand := workspacesv1.WorkspaceResourceSpec{
		Replicas: int32Ptr(1),
		PerReplica: workspacesv1.ResourceRequirements{
			CPU:       mustQty("2"),
			Memory:    mustQty("4Gi"),
			GPUCount:  int32Ptr(2),
			GPUVendor: &nvidia,
			GPUType:   &h100,
		},
	}
	feas, err := computeFeasibility(context.Background(), c, demand)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feas.Fits {
		t.Errorf("expected fits=false (only 1 H100 available, demand 2); got fits=true")
	}
	if feas.Missing == nil || feas.Missing.GPUCount == nil {
		t.Fatalf("expected missing.GPUCount, got %+v", feas.Missing)
	}
	if *feas.Missing.GPUCount != 1 {
		t.Errorf("expected missing GPU count=1, got %d", *feas.Missing.GPUCount)
	}
}

func TestFeasibility_GPUVendorFiltersOutNonMatching(t *testing.T) {
	c := newFakeClient(
		nodeWithGPU("amd-node", "16", "64Gi", "AMD", "", 2),
	)
	nvidia := workspacesv1.GPUVendorNVIDIA
	demand := workspacesv1.WorkspaceResourceSpec{
		Replicas: int32Ptr(1),
		PerReplica: workspacesv1.ResourceRequirements{
			CPU:       mustQty("2"),
			Memory:    mustQty("4Gi"),
			GPUCount:  int32Ptr(1),
			GPUVendor: &nvidia,
		},
	}
	feas, err := computeFeasibility(context.Background(), c, demand)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feas.Fits {
		t.Errorf("expected fits=false (no NVIDIA GPUs in cluster)")
	}
}

func TestFeasibility_ReplicasMultiplyDemand(t *testing.T) {
	c := newFakeClient(
		nodeWithGPU("n1", "8", "32Gi", "", "", 0),
	)
	// Replicas = 5, each wants 2 CPU. Total demand: 10 CPU. Available: 8.
	demand := workspacesv1.WorkspaceResourceSpec{
		Replicas: int32Ptr(5),
		PerReplica: workspacesv1.ResourceRequirements{
			CPU:    mustQty("2"),
			Memory: mustQty("1Gi"),
		},
	}
	feas, err := computeFeasibility(context.Background(), c, demand)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feas.Fits {
		t.Errorf("expected fits=false (demand 10 CPU > available 8)")
	}
	if feas.Missing == nil || feas.Missing.CPU == nil {
		t.Fatalf("expected missing.CPU, got %+v", feas.Missing)
	}
	expected := resource.MustParse("2")
	if feas.Missing.CPU.Cmp(expected) != 0 {
		t.Errorf("expected missing CPU=2, got %s", feas.Missing.CPU.String())
	}
}

func TestFeasibility_PodRequestsConsumed(t *testing.T) {
	node := nodeWithGPU("n1", "8", "32Gi", "", "", 0)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "running-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "n1",
			Containers: []corev1.Container{
				{
					Name: "c1",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("6"),
							corev1.ResourceMemory: resource.MustParse("16Gi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	c := newFakeClient(node, pod)

	// Available after subtracting pod: 2 CPU, 16Gi mem. Demand: 4 CPU.
	demand := workspacesv1.WorkspaceResourceSpec{
		Replicas: int32Ptr(1),
		PerReplica: workspacesv1.ResourceRequirements{
			CPU:    mustQty("4"),
			Memory: mustQty("4Gi"),
		},
	}
	feas, err := computeFeasibility(context.Background(), c, demand)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feas.Fits {
		t.Errorf("expected fits=false (running pod consumes 6 CPU, only 2 free, demand 4)")
	}
}

func TestFeasibility_DefaultReplicasIsOne(t *testing.T) {
	c := newFakeClient(
		nodeWithGPU("n1", "8", "32Gi", "", "", 0),
	)
	demand := workspacesv1.WorkspaceResourceSpec{
		// Replicas nil → treated as 1
		PerReplica: workspacesv1.ResourceRequirements{
			CPU:    mustQty("2"),
			Memory: mustQty("4Gi"),
		},
	}
	feas, err := computeFeasibility(context.Background(), c, demand)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !feas.Fits {
		t.Errorf("expected fits=true (replicas defaults to 1)")
	}
}

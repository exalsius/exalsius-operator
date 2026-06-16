package workspaces

import (
	"context"
	"testing"
	"time"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func fcfsScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := workspacesv1.AddToScheme(s); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return s
}

func waitingWSD(name string, uid types.UID, created time.Time, phase workspacesv1.WorkspaceDeploymentPhase, cluster, model string) *workspacesv1.WorkspaceDeployment {
	m := model
	return &workspacesv1.WorkspaceDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "default",
			UID:               uid,
			CreationTimestamp: metav1.NewTime(created),
		},
		Spec: workspacesv1.WorkspaceDeploymentSpec{
			ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{Name: cluster, Namespace: "default"},
			Resources:            &workspacesv1.WorkspaceResourceSpec{PerReplica: workspacesv1.ResourceRequirements{GPUType: &m}},
		},
		Status: workspacesv1.WorkspaceDeploymentStatus{
			Phase:             phase,
			ResolvedResources: &workspacesv1.WorkspaceResourceSpec{PerReplica: workspacesv1.ResourceRequirements{GPUType: &m}},
		},
	}
}

func TestHasOlderWaitingSibling(t *testing.T) {
	t0 := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	older := t0
	newer := t0.Add(time.Minute)

	cases := []struct {
		name    string
		current *workspacesv1.WorkspaceDeployment
		others  []*workspacesv1.WorkspaceDeployment
		want    bool
	}{
		{
			name:    "younger defers to older waiting sibling (same cluster+model)",
			current: waitingWSD("b", "uid-b", newer, workspacesv1.WorkspaceDeploymentPhaseWaiting, "cd1", "H100"),
			others:  []*workspacesv1.WorkspaceDeployment{waitingWSD("a", "uid-a", older, workspacesv1.WorkspaceDeploymentPhaseWaiting, "cd1", "H100")},
			want:    true,
		},
		{
			name:    "oldest has no older sibling",
			current: waitingWSD("a", "uid-a", older, workspacesv1.WorkspaceDeploymentPhaseWaiting, "cd1", "H100"),
			others:  []*workspacesv1.WorkspaceDeployment{waitingWSD("b", "uid-b", newer, workspacesv1.WorkspaceDeploymentPhaseWaiting, "cd1", "H100")},
			want:    false,
		},
		{
			name:    "older sibling on a different cluster does not block",
			current: waitingWSD("b", "uid-b", newer, workspacesv1.WorkspaceDeploymentPhaseWaiting, "cd1", "H100"),
			others:  []*workspacesv1.WorkspaceDeployment{waitingWSD("a", "uid-a", older, workspacesv1.WorkspaceDeploymentPhaseWaiting, "cd2", "H100")},
			want:    false,
		},
		{
			name:    "older sibling requesting a different model does not block",
			current: waitingWSD("b", "uid-b", newer, workspacesv1.WorkspaceDeploymentPhaseWaiting, "cd1", "H100"),
			others:  []*workspacesv1.WorkspaceDeployment{waitingWSD("a", "uid-a", older, workspacesv1.WorkspaceDeploymentPhaseWaiting, "cd1", "A100-80GB")},
			want:    false,
		},
		{
			name:    "older sibling that is not Waiting does not block",
			current: waitingWSD("b", "uid-b", newer, workspacesv1.WorkspaceDeploymentPhaseWaiting, "cd1", "H100"),
			others:  []*workspacesv1.WorkspaceDeployment{waitingWSD("a", "uid-a", older, workspacesv1.WorkspaceDeploymentPhaseDeploying, "cd1", "H100")},
			want:    false,
		},
		{
			name:    "equal timestamps break by UID (lower UID is older)",
			current: waitingWSD("b", "uid-b", t0, workspacesv1.WorkspaceDeploymentPhaseWaiting, "cd1", "H100"),
			others:  []*workspacesv1.WorkspaceDeployment{waitingWSD("a", "uid-a", t0, workspacesv1.WorkspaceDeploymentPhaseWaiting, "cd1", "H100")},
			want:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			objs := []runtime.Object{tc.current}
			for _, o := range tc.others {
				objs = append(objs, o)
			}
			cl := fake.NewClientBuilder().WithScheme(fcfsScheme(t)).WithRuntimeObjects(objs...).Build()
			r := &WorkspaceDeploymentReconciler{Client: cl}

			got, err := r.hasOlderWaitingSibling(context.Background(), tc.current, gpuSelectorOf(*tc.current.Spec.Resources))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("hasOlderWaitingSibling = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEffectiveGPUSelector(t *testing.T) {
	const modelLabel = "exalsius.ai/gpu-model"
	statusModel := "H100"
	specModel := "A100-80GB"

	// Stamped status wins over spec.
	withStatus := &workspacesv1.WorkspaceDeployment{
		Status: workspacesv1.WorkspaceDeploymentStatus{
			ResolvedResources: &workspacesv1.WorkspaceResourceSpec{PerReplica: workspacesv1.ResourceRequirements{GPUType: &statusModel}},
		},
		Spec: workspacesv1.WorkspaceDeploymentSpec{
			Resources: &workspacesv1.WorkspaceResourceSpec{PerReplica: workspacesv1.ResourceRequirements{GPUType: &specModel}},
		},
	}
	if got := effectiveGPUSelector(withStatus); got[modelLabel] != "H100" {
		t.Errorf("expected status model to win, got %v", got)
	}

	// Spec fallback when no status, with a raw override merged in.
	specOnly := &workspacesv1.WorkspaceDeployment{
		Spec: workspacesv1.WorkspaceDeploymentSpec{
			Resources: &workspacesv1.WorkspaceResourceSpec{PerReplica: workspacesv1.ResourceRequirements{
				GPUType:         &specModel,
				GPUNodeSelector: map[string]string{"nvidia.com/gpu.product": "NVIDIA-A100-SXM4-80GB"},
			}},
		},
	}
	got := effectiveGPUSelector(specOnly)
	if got[modelLabel] != "A100-80GB" || got["nvidia.com/gpu.product"] != "NVIDIA-A100-SXM4-80GB" {
		t.Errorf("expected merged spec selector, got %v", got)
	}

	none := &workspacesv1.WorkspaceDeployment{}
	if got := effectiveGPUSelector(none); len(got) != 0 {
		t.Errorf("expected empty selector, got %v", got)
	}
}

func TestIsOlderWorkspace(t *testing.T) {
	t0 := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	a := waitingWSD("a", "uid-a", t0, workspacesv1.WorkspaceDeploymentPhaseWaiting, "cd1", "H100")
	bLater := waitingWSD("b", "uid-b", t0.Add(time.Second), workspacesv1.WorkspaceDeploymentPhaseWaiting, "cd1", "H100")
	bSameTime := waitingWSD("b", "uid-b", t0, workspacesv1.WorkspaceDeploymentPhaseWaiting, "cd1", "H100")

	if !isOlderWorkspace(a, bLater) {
		t.Error("a created earlier should be older than bLater")
	}
	if isOlderWorkspace(bLater, a) {
		t.Error("bLater should not be older than a")
	}
	if !isOlderWorkspace(a, bSameTime) {
		t.Error("equal timestamp: uid-a < uid-b, so a is older")
	}
}

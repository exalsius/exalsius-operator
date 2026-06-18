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

package infra

import (
	"context"
	"errors"
	"testing"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	"github.com/exalsius/exalsius-operator/internal/gpu"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func invGPUNode(name, model string, count int64) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{gpu.DefaultModelLabel: model}},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceName(gpu.ResourceNvidiaGPU): *resource.NewQuantity(count, resource.DecimalSI),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

func TestScanClusterGPUOfferings(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(
		invGPUNode("n1", "H100", 8),
		invGPUNode("n2", "H100", 4),
	).Build()

	offerings, err := scanClusterGPUOfferings(context.Background(), cl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(offerings) != 1 {
		t.Fatalf("expected 1 offering, got %d: %+v", len(offerings), offerings)
	}
	o := offerings[0]
	if o.Model != "H100" || o.Vendor != "NVIDIA" || o.ResourceName != gpu.ResourceNvidiaGPU {
		t.Errorf("unexpected offering identity: %+v", o)
	}
	if o.Total != 12 || o.Nodes != 2 {
		t.Errorf("expected total=12 nodes=2, got total=%d nodes=%d", o.Total, o.Nodes)
	}
}

func TestRefreshGPUInventory_BestEffortKeepsPreviousOnError(t *testing.T) {
	goodClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(
		invGPUNode("g1", "H100", 8),
	).Build()

	r := &ColonyReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
		Scheme: scheme.Scheme,
		childClientForCD: func(_ context.Context, _ client.Client, _, name string, _ *runtime.Scheme) (client.Client, error) {
			if name == "good-cd" {
				return goodClient, nil
			}
			return nil, errors.New("cluster unreachable")
		},
	}

	colony := &infrav1.Colony{
		Status: infrav1.ColonyStatus{
			ClusterDeploymentRefs: []*corev1.ObjectReference{
				{Name: "good-cd", Namespace: "default"},
				{Name: "bad-cd", Namespace: "default"},
			},
			// bad-cd already has a prior inventory entry from an earlier poll.
			GPUInventory: map[string]infrav1.ClusterGPUInventory{
				"bad-cd": {Offerings: []infrav1.GPUOffering{{Model: "OLD-A100"}}},
			},
		},
	}

	r.refreshGPUInventory(context.Background(), colony)

	good, ok := colony.Status.GPUInventory["good-cd"]
	if !ok || len(good.Offerings) != 1 || good.Offerings[0].Model != "H100" {
		t.Errorf("good-cd should be freshly scanned, got: %+v", good)
	}
	if good.LastUpdated.IsZero() {
		t.Error("good-cd should have a LastUpdated timestamp")
	}
	bad, ok := colony.Status.GPUInventory["bad-cd"]
	if !ok || len(bad.Offerings) != 1 || bad.Offerings[0].Model != "OLD-A100" {
		t.Errorf("bad-cd should keep its previous entry, got: %+v", bad)
	}
}

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

package preflight

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// noRetryOpts disables retries for tests that don't need them.
var noRetryOpts = CheckOptions{
	MaxRetries:     0,
	InitialBackoff: time.Millisecond,
	MaxBackoff:     time.Millisecond,
}

// newFakeDiscovery creates a fake discovery client with the specified API resources.
func newFakeDiscovery(resources []*metav1.APIResourceList) *fakediscovery.FakeDiscovery {
	fakeClient := fakeclientset.NewClientset()
	fd := fakeClient.Discovery().(*fakediscovery.FakeDiscovery)
	fd.Resources = resources
	return fd
}

// allResources returns API resource lists covering all CRDs used by the operator.
func allResources() []*metav1.APIResourceList {
	return []*metav1.APIResourceList{
		{
			GroupVersion: "k0rdent.mirantis.com/v1beta1",
			APIResources: []metav1.APIResource{
				{Name: "clusterdeployments", Kind: "ClusterDeployment"},
			},
		},
		{
			GroupVersion: "infrastructure.cluster.x-k8s.io/v1beta1",
			APIResources: []metav1.APIResource{
				{Name: "remotemachines", Kind: "RemoteMachine"},
			},
		},
	}
}

func TestAllCRDsPresent(t *testing.T) {
	logger := zap.New(zap.UseDevMode(true))
	fd := newFakeDiscovery(allResources())

	results, err := checkCRDsWithClient(context.Background(), fd, OperatorDependencies, noRetryOpts, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, r := range results {
		if !r.Available {
			t.Errorf("expected controller %q to be available, but it has missing deps: %v", r.Name, r.Missing)
		}
	}
}

func TestK0rdentMissing(t *testing.T) {
	logger := zap.New(zap.UseDevMode(true))
	// Only K0smotron present, no K0rdent.
	fd := newFakeDiscovery([]*metav1.APIResourceList{
		{
			GroupVersion: "infrastructure.cluster.x-k8s.io/v1beta1",
			APIResources: []metav1.APIResource{
				{Name: "remotemachines", Kind: "RemoteMachine"},
			},
		},
	})

	results, err := checkCRDsWithClient(context.Background(), fd, OperatorDependencies, noRetryOpts, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := map[string]bool{
		"Colony":               false, // needs K0rdent clusterdeployments
		"RemoteMachineCleanup": true,
		"RemoteMachineWebhook": true,
	}
	for _, r := range results {
		if want, ok := expected[r.Name]; ok {
			if r.Available != want {
				t.Errorf("controller %q: got Available=%v, want %v", r.Name, r.Available, want)
			}
		}
	}
}

func TestOnlyK0smotronMissing(t *testing.T) {
	logger := zap.New(zap.UseDevMode(true))
	// K0rdent present, no K0smotron.
	fd := newFakeDiscovery([]*metav1.APIResourceList{
		{
			GroupVersion: "k0rdent.mirantis.com/v1beta1",
			APIResources: []metav1.APIResource{
				{Name: "clusterdeployments", Kind: "ClusterDeployment"},
			},
		},
	})

	results, err := checkCRDsWithClient(context.Background(), fd, OperatorDependencies, noRetryOpts, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := map[string]bool{
		"Colony":               true,
		"RemoteMachineCleanup": false, // needs K0smotron remotemachines
		"RemoteMachineWebhook": false,
	}
	for _, r := range results {
		if want, ok := expected[r.Name]; ok {
			if r.Available != want {
				t.Errorf("controller %q: got Available=%v, want %v", r.Name, r.Available, want)
			}
		}
	}
}

func TestNothingInstalled(t *testing.T) {
	logger := zap.New(zap.UseDevMode(true))
	fd := newFakeDiscovery(nil)

	results, err := checkCRDsWithClient(context.Background(), fd, OperatorDependencies, noRetryOpts, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, r := range results {
		if r.Available {
			t.Errorf("expected controller %q to be unavailable, but it was available", r.Name)
		}
	}
}

func TestCRDsAppearOnRetry(t *testing.T) {
	logger := zap.New(zap.UseDevMode(true))

	fakeClient := fakeclientset.NewClientset()
	fd := fakeClient.Discovery().(*fakediscovery.FakeDiscovery)
	// Start with nothing.
	fd.Resources = nil

	callCount := 0
	fd.PrependReactor("*", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		callCount++
		// After 2 discovery calls, install K0rdent CRDs.
		if callCount >= 2 {
			fd.Resources = []*metav1.APIResourceList{
				{
					GroupVersion: "k0rdent.mirantis.com/v1beta1",
					APIResources: []metav1.APIResource{
						{Name: "clusterdeployments", Kind: "ClusterDeployment"},
					},
				},
			}
		}
		return false, nil, nil
	})

	opts := CheckOptions{
		MaxRetries:     3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	}

	results, err := checkCRDsWithClient(context.Background(), fd, OperatorDependencies, opts, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Colony should be available after retry.
	for _, r := range results {
		if r.Name == "Colony" && !r.Available {
			t.Error("expected Colony to become available after retry")
		}
	}
}

func TestContextCancellation(t *testing.T) {
	logger := zap.New(zap.UseDevMode(true))
	fd := newFakeDiscovery(nil) // nothing installed, will need retries

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	opts := CheckOptions{
		MaxRetries:     5,
		InitialBackoff: time.Second,
		MaxBackoff:     5 * time.Second,
	}

	_, err := checkCRDsWithClient(ctx, fd, OperatorDependencies, opts, logger)
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

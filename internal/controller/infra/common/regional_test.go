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

package common

import (
	"context"
	"testing"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIsRegionalChild(t *testing.T) {
	tests := []struct {
		name             string
		colonyCluster    *infrav1.ColonyCluster
		wantIsRegional   bool
		wantRegionalName string
	}{
		{
			name: "Regional child with label",
			colonyCluster: &infrav1.ColonyCluster{
				ClusterName: "testchild",
				ClusterLabels: map[string]string{
					LabelKOFRegionalClusterName: "regional-cluster",
				},
			},
			wantIsRegional:   true,
			wantRegionalName: "regional-cluster",
		},
		{
			name: "Non-regional cluster (no label)",
			colonyCluster: &infrav1.ColonyCluster{
				ClusterName: "testchild",
				ClusterLabels: map[string]string{
					"some-other-label": "value",
				},
			},
			wantIsRegional:   false,
			wantRegionalName: "",
		},
		{
			name: "Nil labels",
			colonyCluster: &infrav1.ColonyCluster{
				ClusterName: "testchild",
			},
			wantIsRegional:   false,
			wantRegionalName: "",
		},
		{
			name: "Empty regional label value",
			colonyCluster: &infrav1.ColonyCluster{
				ClusterName: "testchild",
				ClusterLabels: map[string]string{
					LabelKOFRegionalClusterName: "",
				},
			},
			wantIsRegional:   false,
			wantRegionalName: "",
		},
		{
			name: "Regional child with additional labels",
			colonyCluster: &infrav1.ColonyCluster{
				ClusterName: "testchild",
				ClusterLabels: map[string]string{
					LabelKOFRegionalClusterName: "my-regional",
					"environment":               "staging",
				},
			},
			wantIsRegional:   true,
			wantRegionalName: "my-regional",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isRegional, regionalName := IsRegionalChild(tt.colonyCluster)
			if isRegional != tt.wantIsRegional {
				t.Errorf("IsRegionalChild() isRegional = %v, want %v", isRegional, tt.wantIsRegional)
			}
			if regionalName != tt.wantRegionalName {
				t.Errorf("IsRegionalChild() regionalName = %q, want %q", regionalName, tt.wantRegionalName)
			}
		})
	}
}

func TestFindRegionalClusterDeployment(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = k0rdentv1beta1.AddToScheme(scheme)

	tests := []struct {
		name                string
		namespace           string
		regionalClusterName string
		clusterDeployments  []k0rdentv1beta1.ClusterDeployment
		wantErr             bool
		errContains         string
		wantCDName          string
	}{
		{
			name:                "Found regional ClusterDeployment by label",
			namespace:           "default",
			regionalClusterName: "regional-cluster",
			clusterDeployments: []k0rdentv1beta1.ClusterDeployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default-regional-cluster",
						Namespace: "default",
						Labels: map[string]string{
							LabelKOFClusterName: "regional-cluster",
						},
					},
				},
			},
			wantErr:    false,
			wantCDName: "default-regional-cluster",
		},
		{
			name:                "No matching ClusterDeployment",
			namespace:           "default",
			regionalClusterName: "nonexistent-regional",
			clusterDeployments:  []k0rdentv1beta1.ClusterDeployment{},
			wantErr:             true,
			errContains:         "no ClusterDeployment found",
		},
		{
			name:                "ClusterDeployment in different namespace (should not match)",
			namespace:           "default",
			regionalClusterName: "regional-cluster",
			clusterDeployments: []k0rdentv1beta1.ClusterDeployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "other-regional-cluster",
						Namespace: "other-namespace",
						Labels: map[string]string{
							LabelKOFClusterName: "regional-cluster",
						},
					},
				},
			},
			wantErr:     true,
			errContains: "no ClusterDeployment found",
		},
		{
			name:                "Multiple matching ClusterDeployments (returns first)",
			namespace:           "default",
			regionalClusterName: "regional-cluster",
			clusterDeployments: []k0rdentv1beta1.ClusterDeployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "first-regional",
						Namespace: "default",
						Labels: map[string]string{
							LabelKOFClusterName: "regional-cluster",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "second-regional",
						Namespace: "default",
						Labels: map[string]string{
							LabelKOFClusterName: "regional-cluster",
						},
					},
				},
			},
			wantErr: false,
			// Returns first match
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			for i := range tt.clusterDeployments {
				builder = builder.WithObjects(&tt.clusterDeployments[i])
			}
			c := builder.Build()

			ctx := context.Background()
			cd, err := FindRegionalClusterDeployment(ctx, c, tt.namespace, tt.regionalClusterName)

			if tt.wantErr {
				if err == nil {
					t.Errorf("FindRegionalClusterDeployment() expected error but got none")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("FindRegionalClusterDeployment() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("FindRegionalClusterDeployment() unexpected error = %v", err)
				return
			}

			if tt.wantCDName != "" && cd.Name != tt.wantCDName {
				t.Errorf("FindRegionalClusterDeployment() CD name = %q, want %q", cd.Name, tt.wantCDName)
			}
		})
	}
}

func TestGetColonyClusterByName(t *testing.T) {
	colony := &infrav1.Colony{
		Spec: infrav1.ColonySpec{
			ColonyClusters: []infrav1.ColonyCluster{
				{ClusterName: "cluster-a"},
				{ClusterName: "cluster-b", ClusterLabels: map[string]string{"env": "prod"}},
				{ClusterName: "cluster-c"},
			},
		},
	}

	tests := []struct {
		name        string
		clusterName string
		wantFound   bool
		wantName    string
	}{
		{
			name:        "Found first cluster",
			clusterName: "cluster-a",
			wantFound:   true,
			wantName:    "cluster-a",
		},
		{
			name:        "Found middle cluster",
			clusterName: "cluster-b",
			wantFound:   true,
			wantName:    "cluster-b",
		},
		{
			name:        "Found last cluster",
			clusterName: "cluster-c",
			wantFound:   true,
			wantName:    "cluster-c",
		},
		{
			name:        "Cluster not found",
			clusterName: "nonexistent",
			wantFound:   false,
		},
		{
			name:        "Empty cluster name",
			clusterName: "",
			wantFound:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetColonyClusterByName(colony, tt.clusterName)
			if tt.wantFound {
				if result == nil {
					t.Errorf("GetColonyClusterByName() returned nil, want %q", tt.wantName)
					return
				}
				if result.ClusterName != tt.wantName {
					t.Errorf("GetColonyClusterByName() name = %q, want %q", result.ClusterName, tt.wantName)
				}
			} else {
				if result != nil {
					t.Errorf("GetColonyClusterByName() returned %q, want nil", result.ClusterName)
				}
			}
		})
	}
}

func TestGetColonyClusterByName_EmptyColony(t *testing.T) {
	colony := &infrav1.Colony{
		Spec: infrav1.ColonySpec{
			ColonyClusters: []infrav1.ColonyCluster{},
		},
	}

	result := GetColonyClusterByName(colony, "any-name")
	if result != nil {
		t.Errorf("GetColonyClusterByName() on empty colony returned %v, want nil", result)
	}
}

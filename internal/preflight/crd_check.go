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
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

// CRDDependency describes a single CRD that a controller requires.
type CRDDependency struct {
	GroupVersion string // e.g. "k0rdent.mirantis.com/v1beta1"
	Resource     string // e.g. "clusterdeployments"
	Description  string // human-readable name, e.g. "K0rdent ClusterDeployment"
}

// ControllerDependencies maps a controller name to the CRDs it requires.
type ControllerDependencies struct {
	Name         string
	Dependencies []CRDDependency
}

// CheckResult reports whether a controller's CRD dependencies are satisfied.
type CheckResult struct {
	Name      string
	Available bool
	Missing   []CRDDependency
}

// CheckOptions configures the retry behavior for CRD discovery.
type CheckOptions struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// DefaultCheckOptions returns sensible defaults: 5 retries, 2s→15s backoff.
func DefaultCheckOptions() CheckOptions {
	return CheckOptions{
		MaxRetries:     5,
		InitialBackoff: 2 * time.Second,
		MaxBackoff:     15 * time.Second,
	}
}

// OperatorDependencies defines the CRD requirements for each controller.
var OperatorDependencies = []ControllerDependencies{
	{
		Name: "Colony",
		Dependencies: []CRDDependency{
			{GroupVersion: "k0rdent.mirantis.com/v1beta1", Resource: "clusterdeployments", Description: "K0rdent ClusterDeployment"},
		},
	},
	{
		Name: "RemoteMachineCleanup",
		Dependencies: []CRDDependency{
			{GroupVersion: "infrastructure.cluster.x-k8s.io/v1beta1", Resource: "remotemachines", Description: "K0smotron RemoteMachine"},
		},
	},
	{
		Name: "RemoteMachineWebhook",
		Dependencies: []CRDDependency{
			{GroupVersion: "infrastructure.cluster.x-k8s.io/v1beta1", Resource: "remotemachines", Description: "K0smotron RemoteMachine"},
		},
	},
}

// CheckCRDsAvailable uses the Kubernetes Discovery API to verify which CRDs
// are present in the cluster. It retries with backoff to handle CRDs that are
// still being installed (e.g. by Helm subcharts).
func CheckCRDsAvailable(
	ctx context.Context,
	cfg *rest.Config,
	deps []ControllerDependencies,
	opts CheckOptions,
	logger logr.Logger,
) ([]CheckResult, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating discovery client: %w", err)
	}
	return checkCRDsWithClient(ctx, dc, deps, opts, logger)
}

// checkCRDsWithClient is the testable core that accepts a discovery.DiscoveryInterface.
func checkCRDsWithClient(
	ctx context.Context,
	dc discovery.DiscoveryInterface,
	deps []ControllerDependencies,
	opts CheckOptions,
	logger logr.Logger,
) ([]CheckResult, error) {
	// Collect all unique GroupVersion/Resource pairs we need to check.
	type gvr struct {
		groupVersion string
		resource     string
	}
	needed := make(map[gvr]struct{})
	for _, cd := range deps {
		for _, d := range cd.Dependencies {
			needed[gvr{d.GroupVersion, d.Resource}] = struct{}{}
		}
	}

	// available tracks which GVRs have been found.
	available := make(map[gvr]bool)

	backoff := opts.InitialBackoff
	for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
		if attempt > 0 {
			logger.Info("retrying CRD discovery", "attempt", attempt, "backoff", backoff.String())
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			// Invalidate cached discovery data if the client supports it.
			if cacheable, ok := dc.(interface{ Invalidate() }); ok {
				cacheable.Invalidate()
			}
			// Exponential backoff capped at MaxBackoff.
			backoff *= 2
			if backoff > opts.MaxBackoff {
				backoff = opts.MaxBackoff
			}
		}

		// Check each GVR that hasn't been found yet.
		for g := range needed {
			if available[g] {
				continue
			}
			resources, err := dc.ServerResourcesForGroupVersion(g.groupVersion)
			if err != nil {
				// GroupVersion not registered at all — CRD not installed.
				continue
			}
			for _, r := range resources.APIResources {
				if r.Name == g.resource {
					available[g] = true
					break
				}
			}
		}

		// Check if all needed GVRs are available.
		allFound := true
		for g := range needed {
			if !available[g] {
				allFound = false
				break
			}
		}
		if allFound {
			logger.Info("all CRD dependencies satisfied")
			break
		}
	}

	// Build per-controller results.
	results := make([]CheckResult, len(deps))
	for i, cd := range deps {
		result := CheckResult{
			Name:      cd.Name,
			Available: true,
		}
		for _, d := range cd.Dependencies {
			if !available[gvr{d.GroupVersion, d.Resource}] {
				result.Available = false
				result.Missing = append(result.Missing, d)
			}
		}
		results[i] = result
	}

	return results, nil
}

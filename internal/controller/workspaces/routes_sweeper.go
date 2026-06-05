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
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	"github.com/exalsius/exalsius-operator/internal/controller/workspaces/routing"
)

// defaultSweepInterval paces the orphan-route sweep. The sweep is a
// backstop, not the primary cleanup — finalizers handle the normal path.
const defaultSweepInterval = 10 * time.Minute

// orphanRouteSweeper periodically reclaims provider-created route objects
// whose WorkspaceDeployment no longer exists (e.g. workspaces deleted after
// their child ClusterDeployment was already torn down — the regional
// cluster outlives the child, so finalizer cleanup rightly skipped it).
type orphanRouteSweeper struct {
	client   client.Client
	scheme   *runtime.Scheme
	sweeper  routing.OrphanSweeper
	interval time.Duration
}

// NeedLeaderElection makes the sweeper run only on the active manager —
// concurrent sweeps from standby replicas would be wasted work.
func (s *orphanRouteSweeper) NeedLeaderElection() bool {
	return true
}

// Start implements manager.Runnable: one sweep at startup, then ticking.
func (s *orphanRouteSweeper) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("orphan-route-sweeper")
	ctx = log.IntoContext(ctx, logger)

	interval := s.interval
	if interval <= 0 {
		interval = defaultSweepInterval
	}

	s.sweepOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.sweepOnce(ctx)
		}
	}
}

func (s *orphanRouteSweeper) sweepOnce(ctx context.Context) {
	logger := log.FromContext(ctx)

	// Conservative active set: a workspace name that exists in ANY
	// namespace counts as active — the sweep must never remove live routes.
	wsds := &workspacesv1.WorkspaceDeploymentList{}
	if err := s.client.List(ctx, wsds); err != nil {
		logger.Info("Skipping orphan sweep, failed to list WorkspaceDeployments", "error", err.Error())
		return
	}
	active := make(map[string]bool, len(wsds.Items))
	for i := range wsds.Items {
		active[wsds.Items[i].Name] = true
	}

	if err := s.sweeper.SweepOrphans(ctx, routing.SweepRequest{
		ManagementClient:  s.client,
		Scheme:            s.scheme,
		IsActiveWorkspace: func(name string) bool { return active[name] },
	}); err != nil {
		logger.Info("Orphan route sweep failed, will retry next interval", "error", err.Error())
	}
}

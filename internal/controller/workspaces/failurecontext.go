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
	"sort"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
)

// maxRecentEvents caps the events embedded in failureContext.
const maxRecentEvents = 5

// markFailed transitions the workspace to Failed, capturing operator-side
// failure forensics into status.failureContext for the API's error envelope
// (ADR-0001 / agent-interface design decision 12). Must run BEFORE the phase
// is overwritten so phaseWhenFailed records where the failure happened.
// Callers still set their condition and write status afterwards.
func (r *WorkspaceDeploymentReconciler) markFailed(
	ctx context.Context,
	wsd *workspacesv1.WorkspaceDeployment,
	reason, message string,
) {
	// First capture wins: failure paths that run before the Failed-phase
	// early return (e.g. class resolution) re-fire on subsequent
	// reconciles, and the forensics must describe the ORIGINAL failure.
	if wsd.Status.Phase == workspacesv1.WorkspaceDeploymentPhaseFailed &&
		wsd.Status.FailureContext != nil {
		wsd.Status.Message = message
		return
	}
	if r.Recorder != nil {
		r.Recorder.Eventf(wsd, nil, corev1.EventTypeWarning, reason, "Reconcile", "%s", message)
	}
	wsd.Status.FailureContext = r.buildFailureContext(ctx, wsd, reason, message)
	wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhaseFailed
	wsd.Status.Message = message
}

func (r *WorkspaceDeploymentReconciler) buildFailureContext(
	ctx context.Context,
	wsd *workspacesv1.WorkspaceDeployment,
	reason, message string,
) *workspacesv1.FailureContext {
	phase := wsd.Status.Phase
	if phase == "" {
		phase = workspacesv1.WorkspaceDeploymentPhasePending
	}
	fc := &workspacesv1.FailureContext{
		PhaseWhenFailed: phase,
		Reason:          reason,
	}

	// ServiceSet snapshot — best effort; the ServiceSet may not exist yet
	// (e.g. failures before deployment).
	ss := &k0rdentv1beta1.ServiceSet{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      serviceSetName(wsd),
		Namespace: wsd.Spec.ClusterDeploymentRef.Namespace,
	}, ss); err == nil {
		summary := &workspacesv1.ServiceSetStatusSummary{
			Name:     ss.Name,
			Deployed: ss.Status.Deployed,
		}
		for _, svc := range ss.Status.Services {
			summary.Services = append(summary.Services, workspacesv1.ServiceStateSummary{
				Name:           svc.Name,
				State:          svc.State,
				FailureMessage: svc.FailureMessage,
			})
		}
		fc.ServiceSetStatus = summary
	}

	// Seed RecentEvents with the failure event we just emitted. The event
	// broadcaster records it asynchronously, so a read-back in this same
	// reconcile can't see it; and since the operator is the only emitter of
	// events on a WorkspaceDeployment, a plain read would leave RecentEvents
	// empty in every real failure. Older events (if any) follow, newest first.
	current := workspacesv1.EventSummary{
		LastSeen: metav1.Now(),
		Type:     corev1.EventTypeWarning,
		Reason:   reason,
		Message:  message,
	}
	fc.RecentEvents = append([]workspacesv1.EventSummary{current}, r.recentEvents(ctx, wsd)...)
	if len(fc.RecentEvents) > maxRecentEvents {
		fc.RecentEvents = fc.RecentEvents[:maxRecentEvents]
	}
	return fc
}

// recentEvents fetches the newest events recorded for this
// WorkspaceDeployment. Reads bypass the manager cache (APIReader) so the
// cache never has to hold all cluster events.
func (r *WorkspaceDeploymentReconciler) recentEvents(
	ctx context.Context,
	wsd *workspacesv1.WorkspaceDeployment,
) []workspacesv1.EventSummary {
	if r.APIReader == nil {
		return nil
	}

	events := &corev1.EventList{}
	if err := r.APIReader.List(ctx, events,
		client.InNamespace(wsd.Namespace),
		client.MatchingFields{
			"involvedObject.name": wsd.Name,
			"involvedObject.kind": workspacesv1.WorkspaceDeploymentKind,
		},
	); err != nil {
		return nil
	}

	sort.Slice(events.Items, func(i, j int) bool {
		return events.Items[j].LastTimestamp.Before(&events.Items[i].LastTimestamp)
	})

	n := len(events.Items)
	if n > maxRecentEvents {
		n = maxRecentEvents
	}
	summaries := make([]workspacesv1.EventSummary, 0, n)
	for _, ev := range events.Items[:n] {
		summaries = append(summaries, workspacesv1.EventSummary{
			LastSeen: ev.LastTimestamp,
			Type:     ev.Type,
			Reason:   ev.Reason,
			Message:  ev.Message,
		})
	}
	return summaries
}

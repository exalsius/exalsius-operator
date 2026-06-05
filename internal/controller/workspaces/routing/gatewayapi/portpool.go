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

package gatewayapi

import (
	"context"
	"fmt"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	"github.com/exalsius/exalsius-operator/internal/controller/workspaces/routing"
)

// The Port Pool (ADR-0001): raw TCP carries no hostname, so SSH/TCP
// endpoints get a dedicated high port on the tenant domain instead. The
// admin pre-provisions a fixed range of TCP listeners on the tenant Gateway;
// the operator only ever ATTACHES TCPRoutes to them — never mutates the
// Gateway. The set of existing TCPRoutes attached to the gateway IS the
// allocation table: no separate state store, and deleting a route frees its
// port by construction.

// poolListener is one allocatable TCP listener on the tenant Gateway.
type poolListener struct {
	section gatewayv1.SectionName
	port    int32
}

// ensureTCPRoute materializes port-pool access for one SSH/TCP endpoint and
// returns its access entry. Pool exhaustion and missing pool infrastructure
// are per-endpoint conditions (the workspace's HTTP endpoints keep working),
// not provider errors.
func (p *Provider) ensureTCPRoute(
	ctx context.Context,
	gwCtx *gatewayContext,
	nsName, svcName, workspaceName string,
	ep workspacesv1.AccessEndpoint,
) (workspacesv1.AccessEntry, error) {
	entry := workspacesv1.AccessEntry{Name: ep.Name, Protocol: ep.Protocol}

	pool := tcpListeners(gwCtx.gateway)
	if len(pool) == 0 {
		entry.Message = fmt.Sprintf(
			"tenant Gateway %s/%s has no TCP listeners (port pool) — ask an administrator to provision one",
			p.cfg.GatewayNamespace, p.cfg.GatewayName)
		return entry, nil
	}

	desiredBackends := []gatewayv1alpha2.BackendRef{{
		BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
			Name: gatewayv1alpha2.ObjectName(svcName),
			Port: ptr.To(ep.Port),
		},
	}}

	route := &gatewayv1alpha2.TCPRoute{}
	err := gwCtx.regionalClient.Get(ctx, client.ObjectKey{Name: ep.Name, Namespace: nsName}, route)
	if apimeta.IsNoMatchError(err) {
		entry.Message = "TCPRoute support (Gateway API experimental channel) is not installed on the regional cluster"
		return entry, nil
	}

	if apierrors.IsNotFound(err) {
		// New endpoint — allocate the lowest free pool port. A port stays
		// with its workspace for the workspace's lifetime; deleting the
		// TCPRoute (workspace deletion) releases it.
		allocated, listErr := p.allocatedSections(ctx, gwCtx.regionalClient)
		if listErr != nil {
			return entry, listErr
		}
		var chosen *poolListener
		for i := range pool {
			if !allocated[pool[i].section] {
				chosen = &pool[i]
				break
			}
		}
		if chosen == nil {
			entry.Message = fmt.Sprintf(
				"port pool exhausted: all %d TCP listeners on the tenant Gateway are in use — ask an administrator to widen the pool",
				len(pool))
			return entry, nil
		}

		gwNs := gatewayv1alpha2.Namespace(p.cfg.GatewayNamespace)
		section := chosen.section
		route = &gatewayv1alpha2.TCPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ep.Name,
				Namespace: nsName,
				Labels:    map[string]string{routing.LabelWorkspace: workspaceName},
			},
			Spec: gatewayv1alpha2.TCPRouteSpec{
				CommonRouteSpec: gatewayv1alpha2.CommonRouteSpec{
					ParentRefs: []gatewayv1alpha2.ParentReference{{
						Name:        gatewayv1alpha2.ObjectName(p.cfg.GatewayName),
						Namespace:   &gwNs,
						SectionName: &section,
					}},
				},
				Rules: []gatewayv1alpha2.TCPRouteRule{{BackendRefs: desiredBackends}},
			},
		}
		if err := gwCtx.regionalClient.Create(ctx, route); err != nil && !apierrors.IsAlreadyExists(err) {
			return entry, fmt.Errorf("failed to create TCPRoute %q: %w", ep.Name, err)
		}
		entry.URL = poolURL(ep.Protocol, gwCtx.domain, chosen.port)
		entry.Message = msgAwaitingAcceptance
		return entry, nil
	}
	if err != nil {
		return entry, fmt.Errorf("failed to get TCPRoute %q: %w", ep.Name, err)
	}

	// Existing route — the allocation is sticky: reuse its listener.
	section := allocatedSection(route, p.cfg.GatewayName, p.cfg.GatewayNamespace)
	if section == "" {
		entry.Message = "TCPRoute exists but is not attached to the tenant Gateway — delete and recreate the workspace"
		return entry, nil
	}
	port, ok := poolPort(pool, section)
	if !ok {
		entry.Message = fmt.Sprintf(
			"allocated listener %q no longer exists on the tenant Gateway — the pool shrank under a live workspace",
			section)
		return entry, nil
	}

	if !routeRulesEqual(route.Spec.Rules, desiredBackends) {
		route.Spec.Rules = []gatewayv1alpha2.TCPRouteRule{{BackendRefs: desiredBackends}}
		if err := gwCtx.regionalClient.Update(ctx, route); err != nil {
			return entry, fmt.Errorf("failed to update TCPRoute %q: %w", ep.Name, err)
		}
	}

	entry.URL = poolURL(ep.Protocol, gwCtx.domain, port)
	entry.Ready = parentsProgrammed(route.Status.Parents, p.cfg.GatewayName, p.cfg.GatewayNamespace)
	if !entry.Ready {
		entry.Message = msgAwaitingAcceptance
	}
	return entry, nil
}

// allocatedSections returns the pool listeners currently held by ANY
// TCPRoute attached to the tenant Gateway, cluster-wide. The routes are the
// allocation table.
func (p *Provider) allocatedSections(ctx context.Context, c client.Client) (map[gatewayv1.SectionName]bool, error) {
	routes := &gatewayv1alpha2.TCPRouteList{}
	if err := c.List(ctx, routes); err != nil {
		return nil, fmt.Errorf("failed to list TCPRoutes for port allocation: %w", err)
	}
	allocated := map[gatewayv1.SectionName]bool{}
	for i := range routes.Items {
		if s := allocatedSection(&routes.Items[i], p.cfg.GatewayName, p.cfg.GatewayNamespace); s != "" {
			allocated[s] = true
		}
	}
	return allocated, nil
}

// allocatedSection returns the listener section a TCPRoute holds on the
// tenant Gateway, or "" if it is not attached to it.
func allocatedSection(route *gatewayv1alpha2.TCPRoute, gwName, gwNamespace string) gatewayv1.SectionName {
	for _, ref := range route.Spec.ParentRefs {
		if string(ref.Name) != gwName || ref.SectionName == nil {
			continue
		}
		ns := route.Namespace
		if ref.Namespace != nil {
			ns = string(*ref.Namespace)
		}
		if ns == gwNamespace {
			return *ref.SectionName
		}
	}
	return ""
}

// tcpListeners returns the Gateway's TCP listeners (the Port Pool) sorted by
// port so allocation is deterministic: lowest free port wins.
func tcpListeners(gw *gatewayv1.Gateway) []poolListener {
	pool := make([]poolListener, 0, len(gw.Spec.Listeners))
	for _, l := range gw.Spec.Listeners {
		if l.Protocol != gatewayv1.TCPProtocolType {
			continue
		}
		pool = append(pool, poolListener{section: l.Name, port: l.Port})
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].port < pool[j].port })
	return pool
}

func poolPort(pool []poolListener, section gatewayv1.SectionName) (int32, bool) {
	for _, l := range pool {
		if l.section == section {
			return l.port, true
		}
	}
	return 0, false
}

func poolURL(protocol workspacesv1.RouteProtocol, domain string, port int32) string {
	return fmt.Sprintf("%s://%s:%d", strings.ToLower(string(protocol)), domain, port)
}

func routeRulesEqual(rules []gatewayv1alpha2.TCPRouteRule, desired []gatewayv1alpha2.BackendRef) bool {
	if len(rules) != 1 {
		return false
	}
	if len(rules[0].BackendRefs) != len(desired) {
		return false
	}
	for i := range desired {
		got, want := rules[0].BackendRefs[i], desired[i]
		if got.Name != want.Name || !equalPortPtr(got.Port, want.Port) {
			return false
		}
	}
	return true
}

func equalPortPtr(a, b *gatewayv1.PortNumber) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

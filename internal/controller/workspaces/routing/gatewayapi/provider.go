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

// Package gatewayapi implements routing.RouteProvider on top of Gateway API
// resources on the tenant's regional cluster (ADR-0001).
//
// Data path: the tenant Gateway terminates TLS for the tenant domain →
// HTTPRoute (hostname-matched) → mirror Service in the workspace namespace
// (same name + namespace as the child-cluster Service, no local endpoints) →
// Istio multi-cluster endpoint discovery carries traffic to the workspace
// pods on the child cluster.
//
// The provider never mutates the Gateway — it only attaches routes. The
// Gateway, wildcard cert, DNS, and mesh wiring are admin-installed tenant
// infrastructure; when they are missing the provider reports
// routing.InfraNotReadyError.
package gatewayapi

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	"github.com/exalsius/exalsius-operator/internal/controller/infra/common"
	"github.com/exalsius/exalsius-operator/internal/controller/workspaces/routing"
)

const (
	// DefaultGatewayName is the well-known name of the tenant Gateway on the
	// regional cluster. The Gateway's HTTPS listener hostname is the single
	// source of truth for the tenant domain.
	DefaultGatewayName = "exalsius-workspaces"
	// DefaultGatewayNamespace is the well-known namespace of the tenant Gateway.
	DefaultGatewayNamespace = "exalsius-gateway"

	// maxHostnameLabel is the DNS limit for a single hostname label.
	maxHostnameLabel = 63

	// labelIstioGlobal marks a Service as an Istio ambient multi-cluster
	// "global service" — the explicit opt-in for istiod to federate its
	// endpoints across clusters. Required on BOTH the regional mirror Service
	// and the child workspace Service for the cross-cluster data path to form.
	labelIstioGlobal      = "istio.io/global"
	labelIstioGlobalValue = "true"

	// msgAwaitingAcceptance is the not-ready message while the gateway
	// controller has not (yet) accepted a freshly written route.
	msgAwaitingAcceptance = "waiting for the gateway to accept the route"
)

// Config locates the tenant Gateway by convention. Operator-level defaults,
// overridable via manager flags — deliberately NOT per-tenant config: the
// gateway location is an installation convention, the tenant domain is
// derived from the Gateway itself.
type Config struct {
	GatewayName      string
	GatewayNamespace string
	// MeshNamespaceLabels are the Istio mesh-enrollment labels stamped on the
	// regional mirror namespace (from --workspace-mesh-mode). The mirror
	// namespace holds no pods, so this is typically a no-op; applied for
	// parity with the child namespace.
	MeshNamespaceLabels map[string]string
}

// Provider implements routing.RouteProvider via Gateway API resources.
type Provider struct {
	cfg Config
}

// New returns a Gateway API route provider. Empty config fields fall back
// to the well-known defaults.
func New(cfg Config) *Provider {
	if cfg.GatewayName == "" {
		cfg.GatewayName = DefaultGatewayName
	}
	if cfg.GatewayNamespace == "" {
		cfg.GatewayNamespace = DefaultGatewayNamespace
	}
	return &Provider{cfg: cfg}
}

// gatewayContext is the resolved per-call routing context.
type gatewayContext struct {
	regionalClient client.Client
	domain         string
	// scheme is the URL scheme HTTP endpoints are reachable on — "https"
	// when the tenant domain came from an HTTPS wildcard listener (the
	// production default), "http" when only an HTTP wildcard listener exists
	// (dev/test).
	scheme string
	// gateway is the tenant Gateway — read-only; its TCP listeners form the
	// port pool for SSH/TCP endpoints.
	gateway *gatewayv1.Gateway
}

// EnsureRoutes implements routing.RouteProvider.
func (p *Provider) EnsureRoutes(ctx context.Context, req routing.RouteRequest) ([]workspacesv1.AccessEntry, error) {
	gwCtx, err := p.resolveGatewayContext(ctx, req)
	if err != nil {
		return nil, err
	}

	nsName := routing.WorkspaceNamespaceName(req.Workspace)
	if err := p.ensureMirrorNamespace(ctx, gwCtx.regionalClient, nsName, req.Workspace.Name); err != nil {
		return nil, err
	}

	// Client for the child cluster, where the chart's real Services live. The
	// child Service needs the istio.io/global label too (the regional mirror
	// alone is not enough — federation requires it on both sides). The
	// workspace is Running by the time routes are reconciled, so the child is
	// reachable; treat failure as retryable.
	cdRef := req.Workspace.Spec.ClusterDeploymentRef
	childClient, err := common.GetRegionalClusterClient(ctx, req.ManagementClient, cdRef.Namespace, cdRef.Name, req.Scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to reach child cluster to mark services global: %w", err)
	}

	entries := make([]workspacesv1.AccessEntry, 0, len(req.Endpoints))
	primaryAssigned := false
	for _, ep := range req.Endpoints {
		svcName := routing.EndpointServiceName(req.Workspace, ep)
		if len(svcName) > maxHostnameLabel {
			// The chart-convention Service name is a DNS label too. A name
			// this long could not exist on the child cluster either —
			// surface it instead of failing the whole provider call.
			// (The per-endpoint serviceName override is the escape hatch.)
			entries = append(entries, workspacesv1.AccessEntry{
				Name:     ep.Name,
				Protocol: ep.Protocol,
				Ready:    false,
				Message: fmt.Sprintf(
					"conventional Service name %q exceeds %d characters; shorten the workspace or endpoint name",
					svcName, maxHostnameLabel),
			})
			continue
		}

		if ep.Protocol != workspacesv1.RouteProtocolHTTP {
			// SSH/TCP: no hostname routing on raw TCP — these get a
			// dedicated high port on the tenant domain from the Port Pool.
			if err := p.ensureMirrorService(ctx, gwCtx.regionalClient, nsName, svcName, req.Workspace.Name, ep); err != nil {
				return nil, err
			}
			if err := markServiceGlobal(ctx, childClient, nsName, svcName); err != nil {
				return nil, err
			}
			entry, err := p.ensureTCPRoute(ctx, gwCtx, nsName, svcName, req.Workspace.Name, ep)
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
			continue
		}

		// Hostname scheme (ADR-0001): the first declared HTTP endpoint is the
		// primary and gets the bare <workspace>.<domain>; the rest get
		// <workspace>-<endpoint>.<domain>. One label deep keeps every
		// hostname under the tenant's wildcard certificate.
		label := req.Workspace.Name
		if primaryAssigned {
			label = req.Workspace.Name + "-" + ep.Name
		}
		primaryAssigned = true

		if len(label) > maxHostnameLabel {
			entries = append(entries, workspacesv1.AccessEntry{
				Name:     ep.Name,
				Protocol: ep.Protocol,
				Ready:    false,
				Message: fmt.Sprintf(
					"hostname label %q exceeds %d characters; shorten the workspace or endpoint name",
					label, maxHostnameLabel),
			})
			continue
		}
		hostname := label + "." + gwCtx.domain

		if err := p.ensureMirrorService(ctx, gwCtx.regionalClient, nsName, svcName, req.Workspace.Name, ep); err != nil {
			return nil, err
		}
		if err := markServiceGlobal(ctx, childClient, nsName, svcName); err != nil {
			return nil, err
		}
		ready, err := p.ensureHTTPRoute(ctx, gwCtx.regionalClient, nsName, hostname, svcName, req.Workspace.Name, ep)
		if err != nil {
			return nil, err
		}

		entry := workspacesv1.AccessEntry{
			Name:     ep.Name,
			Protocol: ep.Protocol,
			URL:      gwCtx.scheme + "://" + hostname,
			Ready:    ready,
		}
		if !ready {
			entry.Message = msgAwaitingAcceptance
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// CleanupRoutes implements routing.RouteProvider. Everything the provider
// creates lives in the workspace's mirror namespace on the regional cluster,
// so cleanup is namespace deletion. Cluster teardown counts as cleanup: a
// gone ClusterDeployment or unresolvable regional topology means there is
// nothing left to clean.
func (p *Provider) CleanupRoutes(ctx context.Context, req routing.RouteRequest) error {
	cdRef := req.Workspace.Spec.ClusterDeploymentRef

	cd, err := p.getClusterDeployment(ctx, req)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	regionalName := cd.Labels[common.LabelKOFRegionalClusterName]
	if regionalName == "" {
		// No regional parent — no routes were ever materialized.
		return nil
	}
	regionalCD, err := common.FindRegionalClusterDeployment(ctx, req.ManagementClient, cdRef.Namespace, regionalName)
	if err != nil {
		// Regional CD gone (teardown) — its destruction is the cleanup.
		log.FromContext(ctx).Info("Regional cluster not resolvable during route cleanup, skipping",
			"regionalCluster", regionalName, "error", err.Error())
		return nil
	}
	regionalClient, err := common.GetRegionalClusterClient(
		ctx, req.ManagementClient, cdRef.Namespace, regionalCD.Name, req.Scheme)
	if err != nil {
		return fmt.Errorf("failed to reach regional cluster for route cleanup: %w", err)
	}

	nsName := routing.WorkspaceNamespaceName(req.Workspace)

	// Delete TCPRoutes explicitly BEFORE the namespace: namespace
	// termination can take a while, and the routes ARE the port-allocation
	// table — removing them returns the pool ports immediately.
	if err := regionalClient.DeleteAllOf(ctx, &gatewayv1alpha2.TCPRoute{},
		client.InNamespace(nsName),
		client.MatchingLabels{routing.LabelWorkspace: req.Workspace.Name},
	); err != nil && !apierrors.IsNotFound(err) && !apimeta.IsNoMatchError(err) {
		return fmt.Errorf("failed to delete TCPRoutes on regional cluster: %w", err)
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: nsName},
	}
	if err := regionalClient.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete mirror namespace on regional cluster: %w", err)
	}
	return nil
}

// resolveGatewayContext resolves child CD → regional cluster → tenant
// Gateway → tenant domain. Topology and gateway gaps are admin-fixable and
// reported as InfraNotReadyError; transport problems are plain errors.
func (p *Provider) resolveGatewayContext(ctx context.Context, req routing.RouteRequest) (*gatewayContext, error) {
	cdRef := req.Workspace.Spec.ClusterDeploymentRef

	cd, err := p.getClusterDeployment(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get ClusterDeployment %s/%s: %w", cdRef.Namespace, cdRef.Name, err)
	}

	regionalName := cd.Labels[common.LabelKOFRegionalClusterName]
	if regionalName == "" {
		return nil, &routing.InfraNotReadyError{Reason: fmt.Sprintf(
			"cluster %q has no regional parent (label %s missing) — workspace routing requires a regional cluster",
			cdRef.Name, common.LabelKOFRegionalClusterName)}
	}

	regionalCD, err := common.FindRegionalClusterDeployment(ctx, req.ManagementClient, cdRef.Namespace, regionalName)
	if err != nil {
		return nil, &routing.InfraNotReadyError{Reason: fmt.Sprintf(
			"regional cluster %q for cluster %q is not resolvable: %v", regionalName, cdRef.Name, err)}
	}

	regionalClient, err := common.GetRegionalClusterClient(
		ctx, req.ManagementClient, cdRef.Namespace, regionalCD.Name, req.Scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to build regional cluster client: %w", err)
	}

	gw := &gatewayv1.Gateway{}
	if err := regionalClient.Get(ctx, client.ObjectKey{
		Name: p.cfg.GatewayName, Namespace: p.cfg.GatewayNamespace,
	}, gw); err != nil {
		if apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err) {
			return nil, &routing.InfraNotReadyError{Reason: fmt.Sprintf(
				"tenant Gateway %s/%s not found on regional cluster %q — install the tenant routing stack",
				p.cfg.GatewayNamespace, p.cfg.GatewayName, regionalName)}
		}
		return nil, fmt.Errorf("failed to get tenant Gateway: %w", err)
	}

	if !apimeta.IsStatusConditionTrue(gw.Status.Conditions, string(gatewayv1.GatewayConditionProgrammed)) {
		return nil, &routing.InfraNotReadyError{Reason: fmt.Sprintf(
			"tenant Gateway %s/%s is not programmed yet", p.cfg.GatewayNamespace, p.cfg.GatewayName)}
	}

	domain, scheme := tenantDomainFromGateway(gw)
	if domain == "" {
		return nil, &routing.InfraNotReadyError{Reason: fmt.Sprintf(
			"tenant Gateway %s/%s has no HTTP(S) listener with a wildcard hostname (*.<tenant-domain>)",
			p.cfg.GatewayNamespace, p.cfg.GatewayName)}
	}

	return &gatewayContext{regionalClient: regionalClient, domain: domain, scheme: scheme, gateway: gw}, nil
}

func (p *Provider) getClusterDeployment(ctx context.Context, req routing.RouteRequest) (*metav1.PartialObjectMetadata, error) {
	cdRef := req.Workspace.Spec.ClusterDeploymentRef
	// PartialObjectMetadata keeps the provider decoupled from the k0rdent
	// types — only the labels are needed.
	cd := &metav1.PartialObjectMetadata{}
	cd.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "k0rdent.mirantis.com", Version: "v1beta1", Kind: "ClusterDeployment",
	})
	err := req.ManagementClient.Get(ctx, client.ObjectKey{Name: cdRef.Name, Namespace: cdRef.Namespace}, cd)
	return cd, err
}

// tenantDomainFromGateway derives the tenant domain and URL scheme from the
// Gateway's wildcard listener — the single source of truth; no config field
// can drift from what the certificate and DNS actually serve (ADR-0001).
//
// An HTTPS wildcard listener is preferred (the production default → "https").
// An HTTP wildcard listener is accepted as a fallback (dev/test → "http"),
// so a local Gateway needs no TLS cert. Returns ("", "") when no wildcard
// listener exists.
func tenantDomainFromGateway(gw *gatewayv1.Gateway) (domain, scheme string) {
	var httpDomain string
	for _, l := range gw.Spec.Listeners {
		if l.Hostname == nil {
			continue
		}
		host := string(*l.Hostname)
		if !strings.HasPrefix(host, "*.") {
			continue
		}
		switch l.Protocol {
		case gatewayv1.HTTPSProtocolType:
			// HTTPS wins immediately.
			return strings.TrimPrefix(host, "*."), "https"
		case gatewayv1.HTTPProtocolType:
			if httpDomain == "" {
				httpDomain = strings.TrimPrefix(host, "*.")
			}
		}
	}
	if httpDomain != "" {
		return httpDomain, "http"
	}
	return "", ""
}

// ensureMirrorNamespace creates/labels the workspace namespace on the
// regional cluster. Same name as on the child cluster — Istio multi-cluster
// discovery requires namespace sameness for the mirror Service to resolve
// child-cluster endpoints.
func (p *Provider) ensureMirrorNamespace(ctx context.Context, c client.Client, nsName, workspaceName string) error {
	desired := map[string]string{routing.LabelWorkspace: workspaceName}
	for k, v := range p.cfg.MeshNamespaceLabels {
		desired[k] = v
	}

	ns := &corev1.Namespace{}
	err := c.Get(ctx, client.ObjectKey{Name: nsName}, ns)
	if apierrors.IsNotFound(err) {
		ns = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   nsName,
			Labels: desired,
		}}
		if err := c.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create mirror namespace: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get mirror namespace: %w", err)
	}
	if !ns.DeletionTimestamp.IsZero() {
		return fmt.Errorf("mirror namespace %q is terminating", nsName)
	}
	// Heal workspace + mesh-enrollment labels, leaving others untouched.
	changed := false
	if ns.Labels == nil {
		ns.Labels = map[string]string{}
	}
	for k, v := range desired {
		if ns.Labels[k] != v {
			ns.Labels[k] = v
			changed = true
		}
	}
	if changed {
		if err := c.Update(ctx, ns); err != nil {
			return fmt.Errorf("failed to label mirror namespace: %w", err)
		}
	}
	return nil
}

// ensureMirrorService creates/updates the selector-less mirror Service. It
// carries the same name + namespace as the chart's Service on the child
// cluster; Istio resolves its endpoints through the east-west gateway.
func (p *Provider) ensureMirrorService(
	ctx context.Context, c client.Client,
	nsName, svcName, workspaceName string,
	ep workspacesv1.AccessEndpoint,
) error {
	desiredPorts := []corev1.ServicePort{{
		Name:     ep.Name,
		Port:     ep.Port,
		Protocol: corev1.ProtocolTCP,
	}}

	desiredLabels := map[string]string{
		routing.LabelWorkspace: workspaceName,
		// Mark the mirror as an Istio ambient global service so istiod
		// federates the child cluster's endpoints into it.
		labelIstioGlobal: labelIstioGlobalValue,
	}

	svc := &corev1.Service{}
	err := c.Get(ctx, client.ObjectKey{Name: svcName, Namespace: nsName}, svc)
	if apierrors.IsNotFound(err) {
		svc = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      svcName,
				Namespace: nsName,
				Labels:    desiredLabels,
			},
			Spec: corev1.ServiceSpec{
				// No selector: there are no local pods. Endpoints come from
				// the child cluster via mesh discovery.
				Ports: desiredPorts,
			},
		}
		if err := c.Create(ctx, svc); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create mirror Service %q: %w", svcName, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get mirror Service %q: %w", svcName, err)
	}

	changed := false
	if svc.Labels == nil {
		svc.Labels = map[string]string{}
	}
	for k, v := range desiredLabels {
		if svc.Labels[k] != v {
			svc.Labels[k] = v
			changed = true
		}
	}
	if !apiequality.Semantic.DeepDerivative(desiredPorts, svc.Spec.Ports) {
		svc.Spec.Ports = desiredPorts
		changed = true
	}
	if changed {
		if err := c.Update(ctx, svc); err != nil {
			return fmt.Errorf("failed to update mirror Service %q: %w", svcName, err)
		}
	}
	return nil
}

// markServiceGlobal adds the Istio ambient "global service" label to the
// child cluster's Service for an access endpoint, so istiod federates its
// endpoints to the regional mirror. Idempotent. A missing Service (chart not
// yet reconciled the Service) is tolerated — it'll be labeled on a later
// reconcile.
func markServiceGlobal(ctx context.Context, childClient client.Client, nsName, svcName string) error {
	svc := &corev1.Service{}
	err := childClient.Get(ctx, client.ObjectKey{Name: svcName, Namespace: nsName}, svc)
	if apierrors.IsNotFound(err) {
		log.FromContext(ctx).V(1).Info("child Service not present yet, will label on next reconcile",
			"namespace", nsName, "service", svcName)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get child Service %q: %w", svcName, err)
	}
	if svc.Labels[labelIstioGlobal] == labelIstioGlobalValue {
		return nil
	}
	if svc.Labels == nil {
		svc.Labels = map[string]string{}
	}
	svc.Labels[labelIstioGlobal] = labelIstioGlobalValue
	if err := childClient.Update(ctx, svc); err != nil {
		return fmt.Errorf("failed to label child Service %q global: %w", svcName, err)
	}
	return nil
}

// ensureHTTPRoute creates/updates the HTTPRoute attaching the hostname to
// the tenant Gateway and reports whether the route is programmed.
// Readiness = Accepted + ResolvedRefs on our parentRef status (the
// route-level equivalents of the ADR's "Accepted + Programmed").
func (p *Provider) ensureHTTPRoute(
	ctx context.Context, c client.Client,
	nsName, hostname, svcName, workspaceName string,
	ep workspacesv1.AccessEndpoint,
) (bool, error) {
	gwNamespace := gatewayv1.Namespace(p.cfg.GatewayNamespace)
	desiredSpec := gatewayv1.HTTPRouteSpec{
		CommonRouteSpec: gatewayv1.CommonRouteSpec{
			ParentRefs: []gatewayv1.ParentReference{{
				Name:      gatewayv1.ObjectName(p.cfg.GatewayName),
				Namespace: &gwNamespace,
			}},
		},
		Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(hostname)},
		Rules: []gatewayv1.HTTPRouteRule{{
			BackendRefs: []gatewayv1.HTTPBackendRef{{
				BackendRef: gatewayv1.BackendRef{
					BackendObjectReference: gatewayv1.BackendObjectReference{
						Name: gatewayv1.ObjectName(svcName),
						// gatewayv1.PortNumber is an alias of int32.
						Port: ptr.To(ep.Port),
					},
				},
			}},
		}},
	}

	route := &gatewayv1.HTTPRoute{}
	err := c.Get(ctx, client.ObjectKey{Name: ep.Name, Namespace: nsName}, route)
	if apierrors.IsNotFound(err) {
		route = &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ep.Name,
				Namespace: nsName,
				Labels:    map[string]string{routing.LabelWorkspace: workspaceName},
			},
			Spec: desiredSpec,
		}
		if err := c.Create(ctx, route); err != nil && !apierrors.IsAlreadyExists(err) {
			return false, fmt.Errorf("failed to create HTTPRoute %q: %w", ep.Name, err)
		}
		// Fresh route — the gateway hasn't accepted it yet.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to get HTTPRoute %q: %w", ep.Name, err)
	}

	if !apiequality.Semantic.DeepEqual(route.Spec, desiredSpec) {
		route.Spec = desiredSpec
		if err := c.Update(ctx, route); err != nil {
			return false, fmt.Errorf("failed to update HTTPRoute %q: %w", ep.Name, err)
		}
	}

	return parentsProgrammed(route.Status.Parents, p.cfg.GatewayName, p.cfg.GatewayNamespace), nil
}

// parentsProgrammed checks a route's parent statuses for our gateway
// reporting Accepted=True and ResolvedRefs=True — the route-level
// equivalents of the ADR's "Accepted + Programmed". Shared by HTTPRoute and
// TCPRoute (identical status shape).
func parentsProgrammed(parents []gatewayv1.RouteParentStatus, gwName, gwNamespace string) bool {
	for _, parent := range parents {
		ref := parent.ParentRef
		if string(ref.Name) != gwName {
			continue
		}
		if ref.Namespace != nil && string(*ref.Namespace) != gwNamespace {
			continue
		}
		accepted := apimeta.IsStatusConditionTrue(parent.Conditions, string(gatewayv1.RouteConditionAccepted))
		resolved := apimeta.IsStatusConditionTrue(parent.Conditions, string(gatewayv1.RouteConditionResolvedRefs))
		return accepted && resolved
	}
	return false
}

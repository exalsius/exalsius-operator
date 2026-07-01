package workspaces

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/exalsius/exalsius-operator/internal/controller/infra/common"
	"github.com/exalsius/exalsius-operator/internal/controller/workspaces/routing"
	"github.com/exalsius/exalsius-operator/internal/controller/workspaces/routing/gatewayapi"
)

// Shared fixtures for the Gateway API provider specs. The provider is
// exercised directly (not through the suite's reconciler, which runs the
// fake provider): envtest doubles as both the management cluster (CDs,
// kubeconfig secrets) and the regional cluster (Gateway, routes, mirror
// Services). Route status conditions are set manually, the same way the
// suite flips ServiceSet status.

const gwTestDomain = "test.ex.ls"

func gwMakeRequest(wsdName, cdName string, endpoints ...workspacesv1.AccessEndpoint) routing.RouteRequest {
	return routing.RouteRequest{
		Workspace: &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: wsdName, Namespace: "default"},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "irrelevant",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name: cdName, Namespace: "default",
				},
			},
		},
		Endpoints:        endpoints,
		ManagementClient: k8sClient,
		Scheme:           scheme.Scheme,
	}
}

// gwSetupTopology creates the child CD (with regional label), the regional
// CD, and kubeconfig secrets pointing back at envtest.
func gwSetupTopology(prefix string) (childCD string) {
	GinkgoHelper()
	childCD = prefix + "-cd"
	regionalName := prefix + "-regional"
	regionalCD := prefix + "-regional-cd"

	Expect(k8sClient.Create(ctx, &k0rdentv1beta1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: childCD, Namespace: "default",
			Labels: map[string]string{common.LabelKOFRegionalClusterName: regionalName},
		},
		Spec: k0rdentv1beta1.ClusterDeploymentSpec{Template: "tpl"},
	})).To(Succeed())
	Expect(k8sClient.Create(ctx, &k0rdentv1beta1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: regionalCD, Namespace: "default",
			Labels: map[string]string{
				common.LabelKOFClusterName: regionalName,
				common.LabelKOFClusterRole: "regional",
			},
		},
		Spec: k0rdentv1beta1.ClusterDeploymentSpec{Template: "tpl"},
	})).To(Succeed())
	ensureChildKubeconfigSecret(childCD, "default")
	ensureChildKubeconfigSecret(regionalCD, "default")
	gwSetupWaypoint(childCD)
	return childCD
}

func gwHTTPSListener() gatewayv1.Listener {
	tlsMode := gatewayv1.TLSModeTerminate
	return gatewayv1.Listener{
		Name:     "https",
		Port:     443,
		Protocol: gatewayv1.HTTPSProtocolType,
		Hostname: hostnamePtr("*." + gwTestDomain),
		TLS: &gatewayv1.ListenerTLSConfig{
			Mode: &tlsMode,
			CertificateRefs: []gatewayv1.SecretObjectReference{
				{Name: "wildcard-cert"},
			},
		},
	}
}

func gwHTTPWildcardListener() gatewayv1.Listener {
	return gatewayv1.Listener{
		Name:     "http",
		Port:     80,
		Protocol: gatewayv1.HTTPProtocolType,
		Hostname: hostnamePtr("*." + gwTestDomain),
	}
}

func gwTCPListener(name string, port int32) gatewayv1.Listener {
	return gatewayv1.Listener{
		Name:     gatewayv1.SectionName(name),
		Port:     port,
		Protocol: gatewayv1.TCPProtocolType,
	}
}

// gwSetupGateway creates the tenant Gateway in its own namespace and
// (optionally) marks it Programmed. Default listeners: one HTTPS wildcard.
func gwSetupGateway(gwNamespace string, programmed bool, listeners ...gatewayv1.Listener) {
	GinkgoHelper()
	Expect(k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: gwNamespace},
	})).To(Succeed())

	if listeners == nil {
		listeners = []gatewayv1.Listener{gwHTTPSListener()}
	}

	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "exalsius-workspaces", Namespace: gwNamespace},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "istio",
			Listeners:        listeners,
		},
	}
	Expect(k8sClient.Create(ctx, gw)).To(Succeed())

	if programmed {
		gw.Status.Conditions = []metav1.Condition{{
			Type:               string(gatewayv1.GatewayConditionProgrammed),
			Status:             metav1.ConditionTrue,
			Reason:             "Programmed",
			LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, gw)).To(Succeed())
	}
}

func gwNewProvider(gwNamespace string) *gatewayapi.Provider {
	return gatewayapi.New(gatewayapi.Config{
		GatewayName:      "exalsius-workspaces",
		GatewayNamespace: gwNamespace,
		Mesh: routing.MeshConfig{
			Mode:              routing.MeshModeAmbient,
			WaypointEnabled:   true,
			WaypointNamespace: "istio-system",
		},
	})
}

// gwSetupWaypoint creates the per-child waypoint Gateway (<cd>-waypoint in
// istio-system), marked Programmed — the per-child waypoint the provider
// requires on both the regional and child clusters (ADR-0005). envtest is both,
// so one Gateway satisfies both checks. Idempotent on the shared istio-system.
func gwSetupWaypoint(cdName string) {
	GinkgoHelper()
	istioNs := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "istio-system"}}
	if err := k8sClient.Create(ctx, istioNs); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
	wp := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name: routing.WaypointNameForClusterDeployment(cdName), Namespace: "istio-system",
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "istio-waypoint",
			Listeners: []gatewayv1.Listener{{
				Name: "mesh", Port: 15008, Protocol: gatewayv1.TCPProtocolType,
			}},
		},
	}
	if err := k8sClient.Create(ctx, wp); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
	wp.Status.Conditions = []metav1.Condition{{
		Type:               string(gatewayv1.GatewayConditionProgrammed),
		Status:             metav1.ConditionTrue,
		Reason:             "Programmed",
		LastTransitionTime: metav1.Now(),
	}}
	Expect(k8sClient.Status().Update(ctx, wp)).To(Succeed())
}

func gwAcceptedParentStatus(gwNamespace string) []gatewayv1.RouteParentStatus {
	gwNs := gatewayv1.Namespace(gwNamespace)
	now := metav1.Now()
	return []gatewayv1.RouteParentStatus{{
		ParentRef: gatewayv1.ParentReference{
			Name:      "exalsius-workspaces",
			Namespace: &gwNs,
		},
		ControllerName: "istio.io/gateway-controller",
		Conditions: []metav1.Condition{
			{
				Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue,
				Reason: "Accepted", LastTransitionTime: now,
			},
			{
				Type: string(gatewayv1.RouteConditionResolvedRefs), Status: metav1.ConditionTrue,
				Reason: "ResolvedRefs", LastTransitionTime: now,
			},
		},
	}}
}

func gwMarkHTTPRouteAccepted(routeName, routeNamespace, gwNamespace string) {
	GinkgoHelper()
	route := &gatewayv1.HTTPRoute{}
	Expect(k8sClient.Get(ctx, client.ObjectKey{Name: routeName, Namespace: routeNamespace}, route)).To(Succeed())
	route.Status.Parents = gwAcceptedParentStatus(gwNamespace)
	Expect(k8sClient.Status().Update(ctx, route)).To(Succeed())
}

func hostnamePtr(s string) *gatewayv1.Hostname {
	h := gatewayv1.Hostname(s)
	return &h
}

var _ = Describe("Gateway API route provider", func() {
	It("creates mirror namespace, Services, and HTTPRoutes with the hostname scheme", func() {
		cd := gwSetupTopology("gwhttp")
		gwSetupGateway("gwhttp-gwns", true)
		provider := gwNewProvider("gwhttp-gwns")

		req := gwMakeRequest("gwhttp-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888},
			workspacesv1.AccessEndpoint{Name: "api", Protocol: workspacesv1.RouteProtocolHTTP, Port: 6006},
		)
		entries, err := provider.EnsureRoutes(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(2))

		// Primary endpoint gets the bare hostname; the second gets the suffix.
		Expect(entries[0].URL).To(Equal("https://gwhttp-wsd." + gwTestDomain))
		Expect(entries[0].Ready).To(BeFalse(), "fresh route is not accepted yet")
		Expect(entries[1].URL).To(Equal("https://gwhttp-wsd-api." + gwTestDomain))

		// Mirror namespace with the workspace label.
		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ws-gwhttp-wsd"}, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue(LabelWorkspace, "gwhttp-wsd"))
		// Mirror namespace gets the mesh-enrollment label (provider runs ambient)
		// but NOT the waypoint-routing labels — those are scoped to the child
		// namespace alone (ADR-0005).
		Expect(ns.Labels).To(HaveKeyWithValue("istio.io/dataplane-mode", "ambient"))
		Expect(ns.Labels).NotTo(HaveKey("istio.io/use-waypoint"))
		Expect(ns.Labels).NotTo(HaveKey("istio.io/use-waypoint-namespace"))
		Expect(ns.Labels).NotTo(HaveKey("istio.io/ingress-use-waypoint"))

		// Selector-less mirror Service named by the chart convention.
		svc := &corev1.Service{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Name: "wsd-gwhttp-cd-gwhttp-wsd-ide", Namespace: "ws-gwhttp-wsd",
		}, svc)).To(Succeed())
		Expect(svc.Spec.Selector).To(BeEmpty())
		Expect(svc.Spec.Ports).To(HaveLen(1))
		Expect(svc.Spec.Ports[0].Port).To(Equal(int32(8888)))
		// Marked as an Istio ambient global service so istiod federates the
		// child cluster's endpoints into the mirror.
		Expect(svc.Labels).To(HaveKeyWithValue("istio.io/global", "true"))

		// HTTPRoute attached to the tenant Gateway with the right hostname
		// and backend.
		route := &gatewayv1.HTTPRoute{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ide", Namespace: "ws-gwhttp-wsd"}, route)).To(Succeed())
		Expect(route.Spec.ParentRefs).To(HaveLen(1))
		Expect(string(route.Spec.ParentRefs[0].Name)).To(Equal("exalsius-workspaces"))
		Expect(string(*route.Spec.ParentRefs[0].Namespace)).To(Equal("gwhttp-gwns"))
		Expect(route.Spec.Hostnames).To(ConsistOf(gatewayv1.Hostname("gwhttp-wsd." + gwTestDomain)))
		Expect(route.Spec.Rules).To(HaveLen(1))
		Expect(string(route.Spec.Rules[0].BackendRefs[0].Name)).To(Equal("wsd-gwhttp-cd-gwhttp-wsd-ide"))
	})

	It("derives the domain from an HTTP wildcard listener and uses the http scheme", func() {
		cd := gwSetupTopology("gwhttponly")
		// Gateway with only an HTTP wildcard listener — no TLS at all.
		gwSetupGateway("gwhttponly-gwns", true, gwHTTPWildcardListener())
		provider := gwNewProvider("gwhttponly-gwns")

		entries, err := provider.EnsureRoutes(ctx, gwMakeRequest("gwhttponly-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888}))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].URL).To(Equal("http://gwhttponly-wsd." + gwTestDomain))
	})

	It("prefers the HTTPS listener scheme when both HTTP and HTTPS wildcards exist", func() {
		cd := gwSetupTopology("gwboth")
		gwSetupGateway("gwboth-gwns", true, gwHTTPWildcardListener(), gwHTTPSListener())
		provider := gwNewProvider("gwboth-gwns")

		entries, err := provider.EnsureRoutes(ctx, gwMakeRequest("gwboth-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888}))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries[0].URL).To(Equal("https://gwboth-wsd." + gwTestDomain))
	})

	It("reports ready and is idempotent once the gateway accepts the routes", func() {
		cd := gwSetupTopology("gwrdy")
		gwSetupGateway("gwrdy-gwns", true)
		provider := gwNewProvider("gwrdy-gwns")

		req := gwMakeRequest("gwrdy-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888},
		)
		entries, err := provider.EnsureRoutes(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries[0].Ready).To(BeFalse())

		gwMarkHTTPRouteAccepted("ide", "ws-gwrdy-wsd", "gwrdy-gwns")

		entries, err = provider.EnsureRoutes(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries[0].Ready).To(BeTrue())
		Expect(entries[0].Message).To(BeEmpty())

		// Idempotency: a further call must not rewrite the route object.
		route := &gatewayv1.HTTPRoute{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ide", Namespace: "ws-gwrdy-wsd"}, route)).To(Succeed())
		rvBefore := route.ResourceVersion

		_, err = provider.EnsureRoutes(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ide", Namespace: "ws-gwrdy-wsd"}, route)).To(Succeed())
		Expect(route.ResourceVersion).To(Equal(rvBefore))
	})

	It("returns InfraNotReady when the tenant Gateway is missing", func() {
		cd := gwSetupTopology("gwmiss")
		// No gateway installed.
		provider := gwNewProvider("gwmiss-gwns")

		_, err := provider.EnsureRoutes(ctx, gwMakeRequest("gwmiss-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888}))
		Expect(err).To(HaveOccurred())
		Expect(routing.IsInfraNotReady(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("not found"))
	})

	It("returns InfraNotReady when the Gateway is not programmed", func() {
		cd := gwSetupTopology("gwunprog")
		gwSetupGateway("gwunprog-gwns", false)
		provider := gwNewProvider("gwunprog-gwns")

		_, err := provider.EnsureRoutes(ctx, gwMakeRequest("gwunprog-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888}))
		Expect(err).To(HaveOccurred())
		Expect(routing.IsInfraNotReady(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("not programmed"))
	})

	It("returns InfraNotReady when the per-child waypoint is missing", func() {
		cd := gwSetupTopology("gwnowp")
		gwSetupGateway("gwnowp-gwns", true)
		// Remove the per-child waypoint the topology helper provisioned, to
		// exercise the ADR-0005 waypoint-existence check.
		Expect(k8sClient.Delete(ctx, &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{
			Name: routing.WaypointNameForClusterDeployment(cd), Namespace: "istio-system",
		}})).To(Succeed())
		provider := gwNewProvider("gwnowp-gwns")

		_, err := provider.EnsureRoutes(ctx, gwMakeRequest("gwnowp-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888}))
		Expect(err).To(HaveOccurred())
		Expect(routing.IsInfraNotReady(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("per-child waypoint"))
	})

	It("returns InfraNotReady when no wildcard listener exists", func() {
		cd := gwSetupTopology("gwnotls")
		// An HTTP listener WITHOUT a wildcard hostname doesn't yield a domain.
		gwSetupGateway("gwnotls-gwns", true, gatewayv1.Listener{
			Name:     "http",
			Port:     80,
			Protocol: gatewayv1.HTTPProtocolType,
		})
		provider := gwNewProvider("gwnotls-gwns")

		_, err := provider.EnsureRoutes(ctx, gwMakeRequest("gwnotls-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888}))
		Expect(err).To(HaveOccurred())
		Expect(routing.IsInfraNotReady(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("wildcard hostname"))
	})

	It("returns InfraNotReady when the child cluster has no regional parent", func() {
		// CD without the KOF regional label.
		Expect(k8sClient.Create(ctx, &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "gwnoreg-cd", Namespace: "default"},
			Spec:       k0rdentv1beta1.ClusterDeploymentSpec{Template: "tpl"},
		})).To(Succeed())
		provider := gwNewProvider("gwnoreg-gwns")

		_, err := provider.EnsureRoutes(ctx, gwMakeRequest("gwnoreg-wsd", "gwnoreg-cd",
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888}))
		Expect(err).To(HaveOccurred())
		Expect(routing.IsInfraNotReady(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("no regional parent"))
	})

	It("routes to an explicit serviceName instead of the convention when the class declares one", func() {
		cd := gwSetupTopology("gwsvc")
		gwSetupGateway("gwsvc-gwns", true)
		provider := gwNewProvider("gwsvc-gwns")

		entries, err := provider.EnsureRoutes(ctx, gwMakeRequest("gwsvc-wsd", cd,
			workspacesv1.AccessEndpoint{
				Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 80,
				ServiceName: "proxy-public",
			}))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries[0].URL).To(Equal("https://gwsvc-wsd." + gwTestDomain))

		// Mirror Service carries the fixed name — safe because the workspace
		// namespace disambiguates.
		svc := &corev1.Service{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Name: "proxy-public", Namespace: "ws-gwsvc-wsd",
		}, svc)).To(Succeed())
		Expect(svc.Spec.Ports[0].Port).To(Equal(int32(80)))

		// And the HTTPRoute backend references it.
		route := &gatewayv1.HTTPRoute{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ide", Namespace: "ws-gwsvc-wsd"}, route)).To(Succeed())
		Expect(string(route.Spec.Rules[0].BackendRefs[0].Name)).To(Equal("proxy-public"))

		// The conventional name must NOT have been created.
		err = k8sClient.Get(ctx, client.ObjectKey{
			Name: "wsd-gwsvc-cd-gwsvc-wsd-ide", Namespace: "ws-gwsvc-wsd",
		}, &corev1.Service{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("marks endpoints whose hostname label would exceed 63 characters as not ready", func() {
		cd := gwSetupTopology("gwlong")
		gwSetupGateway("gwlong-gwns", true)
		provider := gwNewProvider("gwlong-gwns")

		// The suffixed hostname label of the second endpoint
		// (<workspace>-<endpoint>) exceeds 63 chars; the primary stays valid.
		longEndpoint := strings.Repeat("e", 56)
		entries, err := provider.EnsureRoutes(ctx, gwMakeRequest("gwlong-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888},
			workspacesv1.AccessEndpoint{Name: longEndpoint, Protocol: workspacesv1.RouteProtocolHTTP, Port: 6006},
		))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(2))
		Expect(entries[0].URL).To(Equal("https://gwlong-wsd." + gwTestDomain))
		Expect(entries[0].Ready).To(BeFalse()) // fresh route, not accepted yet
		Expect(entries[1].Ready).To(BeFalse())
		Expect(entries[1].Message).To(ContainSubstring("exceeds 63 characters"))
		Expect(entries[1].URL).To(BeEmpty())
	})

	It("cleans up the mirror namespace and tolerates repeat and CD-gone calls", func() {
		cd := gwSetupTopology("gwclean")
		gwSetupGateway("gwclean-gwns", true)
		provider := gwNewProvider("gwclean-gwns")

		req := gwMakeRequest("gwclean-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888})
		_, err := provider.EnsureRoutes(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ws-gwclean-wsd"}, ns)).To(Succeed())

		Expect(provider.CleanupRoutes(ctx, req)).To(Succeed())
		// envtest namespaces stay Terminating — deletion underway is the assertion.
		err = k8sClient.Get(ctx, client.ObjectKey{Name: "ws-gwclean-wsd"}, ns)
		if err == nil {
			Expect(ns.DeletionTimestamp.IsZero()).To(BeFalse())
		} else {
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}

		// Idempotent.
		Expect(provider.CleanupRoutes(ctx, req)).To(Succeed())

		// CD gone → cleanup is a no-op success (teardown owns it).
		Expect(k8sClient.Delete(ctx, &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: cd, Namespace: "default"},
		})).To(Succeed())
		Expect(provider.CleanupRoutes(ctx, req)).To(Succeed())
	})
})

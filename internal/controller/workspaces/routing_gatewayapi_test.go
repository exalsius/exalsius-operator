package workspaces

import (
	"strings"
	"time"

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

// The Gateway API provider is exercised directly (not through the suite's
// reconciler, which runs the fake provider): envtest doubles as both the
// management cluster (CDs, kubeconfig secrets) and the regional cluster
// (Gateway, HTTPRoutes, mirror Services). Route status conditions are set
// manually, the same way the suite flips ServiceSet status.
var _ = Describe("Gateway API route provider", func() {
	const (
		timeout      = 30 * time.Second
		interval     = 250 * time.Millisecond
		tenantDomain = "test.ex.ls"
	)

	makeRequest := func(wsdName, cdName string, endpoints ...workspacesv1.AccessEndpoint) routing.RouteRequest {
		return routing.RouteRequest{
			Workspace: &workspacesv1.WorkspaceDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: wsdName, Namespace: "default"},
				Spec: workspacesv1.WorkspaceDeploymentSpec{
					WorkspaceClassRef: "irrelevant",
					ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
						Name: cdName, Namespace: "default",
					},
					Owner: workspacesv1.OwnerInfo{Username: "tester"},
				},
			},
			Endpoints:        endpoints,
			ManagementClient: k8sClient,
			Scheme:           scheme.Scheme,
		}
	}

	// setupTopology creates the child CD (with regional label), the regional
	// CD, and kubeconfig secrets pointing back at envtest.
	setupTopology := func(prefix string) (childCD string) {
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
				Labels: map[string]string{common.LabelKOFClusterName: regionalName},
			},
			Spec: k0rdentv1beta1.ClusterDeploymentSpec{Template: "tpl"},
		})).To(Succeed())
		ensureChildKubeconfigSecret(childCD, "default")
		ensureChildKubeconfigSecret(regionalCD, "default")
		return childCD
	}

	// setupGateway creates the tenant Gateway in its own namespace with an
	// HTTPS wildcard listener and (optionally) a Programmed condition.
	setupGateway := func(gwNamespace string, programmed bool, listeners ...gatewayv1.Listener) {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: gwNamespace},
		})).To(Succeed())

		if listeners == nil {
			tlsMode := gatewayv1.TLSModeTerminate
			listeners = []gatewayv1.Listener{{
				Name:     "https",
				Port:     443,
				Protocol: gatewayv1.HTTPSProtocolType,
				Hostname: hostnamePtr("*." + tenantDomain),
				TLS: &gatewayv1.ListenerTLSConfig{
					Mode: &tlsMode,
					CertificateRefs: []gatewayv1.SecretObjectReference{
						{Name: "wildcard-cert"},
					},
				},
			}}
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

	newProvider := func(gwNamespace string) *gatewayapi.Provider {
		return gatewayapi.New(gatewayapi.Config{
			GatewayName:      "exalsius-workspaces",
			GatewayNamespace: gwNamespace,
		})
	}

	markRouteAccepted := func(routeName, routeNamespace, gwNamespace string) {
		GinkgoHelper()
		route := &gatewayv1.HTTPRoute{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: routeName, Namespace: routeNamespace}, route)).To(Succeed())
		gwNs := gatewayv1.Namespace(gwNamespace)
		now := metav1.Now()
		route.Status.Parents = []gatewayv1.RouteParentStatus{{
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
		Expect(k8sClient.Status().Update(ctx, route)).To(Succeed())
	}

	It("creates mirror namespace, Services, and HTTPRoutes with the hostname scheme", func() {
		cd := setupTopology("gwhttp")
		setupGateway("gwhttp-gwns", true)
		provider := newProvider("gwhttp-gwns")

		req := makeRequest("gwhttp-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888},
			workspacesv1.AccessEndpoint{Name: "api", Protocol: workspacesv1.RouteProtocolHTTP, Port: 6006},
			workspacesv1.AccessEndpoint{Name: "shell", Protocol: workspacesv1.RouteProtocolSSH, Port: 22},
		)
		entries, err := provider.EnsureRoutes(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(3))

		// Primary endpoint gets the bare hostname; the second gets the suffix.
		Expect(entries[0].URL).To(Equal("https://gwhttp-wsd." + tenantDomain))
		Expect(entries[0].Ready).To(BeFalse(), "fresh route is not accepted yet")
		Expect(entries[1].URL).To(Equal("https://gwhttp-wsd-api." + tenantDomain))
		// SSH is honestly reported as not yet routable.
		Expect(entries[2].Ready).To(BeFalse())
		Expect(entries[2].Message).To(ContainSubstring("not yet available"))
		Expect(entries[2].URL).To(BeEmpty())

		// Mirror namespace with the workspace label.
		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ws-gwhttp-wsd"}, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue(LabelWorkspace, "gwhttp-wsd"))

		// Selector-less mirror Service named by the chart convention.
		svc := &corev1.Service{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Name: "wsd-gwhttp-cd-gwhttp-wsd-ide", Namespace: "ws-gwhttp-wsd",
		}, svc)).To(Succeed())
		Expect(svc.Spec.Selector).To(BeEmpty())
		Expect(svc.Spec.Ports).To(HaveLen(1))
		Expect(svc.Spec.Ports[0].Port).To(Equal(int32(8888)))

		// HTTPRoute attached to the tenant Gateway with the right hostname
		// and backend.
		route := &gatewayv1.HTTPRoute{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ide", Namespace: "ws-gwhttp-wsd"}, route)).To(Succeed())
		Expect(route.Spec.ParentRefs).To(HaveLen(1))
		Expect(string(route.Spec.ParentRefs[0].Name)).To(Equal("exalsius-workspaces"))
		Expect(string(*route.Spec.ParentRefs[0].Namespace)).To(Equal("gwhttp-gwns"))
		Expect(route.Spec.Hostnames).To(ConsistOf(gatewayv1.Hostname("gwhttp-wsd." + tenantDomain)))
		Expect(route.Spec.Rules).To(HaveLen(1))
		Expect(string(route.Spec.Rules[0].BackendRefs[0].Name)).To(Equal("wsd-gwhttp-cd-gwhttp-wsd-ide"))

		// No SSH route object — non-HTTP endpoints are port-pool territory.
		err = k8sClient.Get(ctx, client.ObjectKey{Name: "shell", Namespace: "ws-gwhttp-wsd"}, &gatewayv1.HTTPRoute{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("reports ready and is idempotent once the gateway accepts the routes", func() {
		cd := setupTopology("gwrdy")
		setupGateway("gwrdy-gwns", true)
		provider := newProvider("gwrdy-gwns")

		req := makeRequest("gwrdy-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888},
		)
		entries, err := provider.EnsureRoutes(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries[0].Ready).To(BeFalse())

		markRouteAccepted("ide", "ws-gwrdy-wsd", "gwrdy-gwns")

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
		cd := setupTopology("gwmiss")
		// No gateway installed.
		provider := newProvider("gwmiss-gwns")

		_, err := provider.EnsureRoutes(ctx, makeRequest("gwmiss-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888}))
		Expect(err).To(HaveOccurred())
		Expect(routing.IsInfraNotReady(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("not found"))
	})

	It("returns InfraNotReady when the Gateway is not programmed", func() {
		cd := setupTopology("gwunprog")
		setupGateway("gwunprog-gwns", false)
		provider := newProvider("gwunprog-gwns")

		_, err := provider.EnsureRoutes(ctx, makeRequest("gwunprog-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888}))
		Expect(err).To(HaveOccurred())
		Expect(routing.IsInfraNotReady(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("not programmed"))
	})

	It("returns InfraNotReady when no HTTPS wildcard listener exists", func() {
		cd := setupTopology("gwnotls")
		setupGateway("gwnotls-gwns", true, gatewayv1.Listener{
			Name:     "http",
			Port:     80,
			Protocol: gatewayv1.HTTPProtocolType,
		})
		provider := newProvider("gwnotls-gwns")

		_, err := provider.EnsureRoutes(ctx, makeRequest("gwnotls-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888}))
		Expect(err).To(HaveOccurred())
		Expect(routing.IsInfraNotReady(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("no HTTPS listener"))
	})

	It("returns InfraNotReady when the child cluster has no regional parent", func() {
		// CD without the KOF regional label.
		Expect(k8sClient.Create(ctx, &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "gwnoreg-cd", Namespace: "default"},
			Spec:       k0rdentv1beta1.ClusterDeploymentSpec{Template: "tpl"},
		})).To(Succeed())
		provider := newProvider("gwnoreg-gwns")

		_, err := provider.EnsureRoutes(ctx, makeRequest("gwnoreg-wsd", "gwnoreg-cd",
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888}))
		Expect(err).To(HaveOccurred())
		Expect(routing.IsInfraNotReady(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("no regional parent"))
	})

	It("marks endpoints whose hostname label would exceed 63 characters as not ready", func() {
		cd := setupTopology("gwlong")
		setupGateway("gwlong-gwns", true)
		provider := newProvider("gwlong-gwns")

		// The suffixed hostname label of the second endpoint
		// (<workspace>-<endpoint>) exceeds 63 chars; the primary stays valid.
		longEndpoint := strings.Repeat("e", 56)
		entries, err := provider.EnsureRoutes(ctx, makeRequest("gwlong-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888},
			workspacesv1.AccessEndpoint{Name: longEndpoint, Protocol: workspacesv1.RouteProtocolHTTP, Port: 6006},
		))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(2))
		Expect(entries[0].URL).To(Equal("https://gwlong-wsd." + tenantDomain))
		Expect(entries[0].Ready).To(BeFalse()) // fresh route, not accepted yet
		Expect(entries[1].Ready).To(BeFalse())
		Expect(entries[1].Message).To(ContainSubstring("exceeds 63 characters"))
		Expect(entries[1].URL).To(BeEmpty())
	})

	It("cleans up the mirror namespace and tolerates repeat and CD-gone calls", func() {
		cd := setupTopology("gwclean")
		setupGateway("gwclean-gwns", true)
		provider := newProvider("gwclean-gwns")

		req := makeRequest("gwclean-wsd", cd,
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

func hostnamePtr(s string) *gatewayv1.Hostname {
	h := gatewayv1.Hostname(s)
	return &h
}

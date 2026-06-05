package workspaces

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

var _ = Describe("Gateway API port pool (SSH/TCP)", func() {
	sshEndpoint := workspacesv1.AccessEndpoint{
		Name: "ssh", Protocol: workspacesv1.RouteProtocolSSH, Port: 22,
	}

	markTCPRouteAccepted := func(routeName, routeNamespace, gwNamespace string) {
		GinkgoHelper()
		route := &gatewayv1alpha2.TCPRoute{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: routeName, Namespace: routeNamespace}, route)).To(Succeed())
		route.Status.Parents = gwAcceptedParentStatus(gwNamespace)
		Expect(k8sClient.Status().Update(ctx, route)).To(Succeed())
	}

	It("allocates the lowest free pool port and reports ready once accepted", func() {
		cd := gwSetupTopology("pp1")
		// Listeners deliberately declared out of order — allocation must
		// still pick the lowest port.
		gwSetupGateway("pp1-gwns", true,
			gwHTTPSListener(),
			gwTCPListener("ssh-2201", 2201),
			gwTCPListener("ssh-2200", 2200),
		)
		provider := gwNewProvider("pp1-gwns")

		req := gwMakeRequest("pp1-wsd", cd, sshEndpoint)
		entries, err := provider.EnsureRoutes(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].URL).To(Equal("ssh://" + gwTestDomain + ":2200"))
		Expect(entries[0].Ready).To(BeFalse(), "fresh route is not accepted yet")

		// TCPRoute bound to the lowest listener, backend = mirror Service.
		route := &gatewayv1alpha2.TCPRoute{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ssh", Namespace: "ws-pp1-wsd"}, route)).To(Succeed())
		Expect(route.Spec.ParentRefs).To(HaveLen(1))
		Expect(string(*route.Spec.ParentRefs[0].SectionName)).To(Equal("ssh-2200"))
		Expect(string(route.Spec.Rules[0].BackendRefs[0].Name)).To(Equal("wsd-pp1-cd-pp1-wsd-ssh"))

		// Mirror Service exists for the SSH endpoint too.
		svc := &corev1.Service{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Name: "wsd-pp1-cd-pp1-wsd-ssh", Namespace: "ws-pp1-wsd",
		}, svc)).To(Succeed())
		Expect(svc.Spec.Ports[0].Port).To(Equal(int32(22)))

		markTCPRouteAccepted("ssh", "ws-pp1-wsd", "pp1-gwns")
		entries, err = provider.EnsureRoutes(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries[0].Ready).To(BeTrue())
		Expect(entries[0].Message).To(BeEmpty())
	})

	It("gives a second workspace the next free port and keeps allocations sticky", func() {
		cd := gwSetupTopology("pp2")
		gwSetupGateway("pp2-gwns", true,
			gwHTTPSListener(),
			gwTCPListener("ssh-2200", 2200),
			gwTCPListener("ssh-2201", 2201),
		)
		provider := gwNewProvider("pp2-gwns")

		reqA := gwMakeRequest("pp2-wsd-a", cd, sshEndpoint)
		entriesA, err := provider.EnsureRoutes(ctx, reqA)
		Expect(err).NotTo(HaveOccurred())
		Expect(entriesA[0].URL).To(HaveSuffix(":2200"))

		reqB := gwMakeRequest("pp2-wsd-b", cd, sshEndpoint)
		entriesB, err := provider.EnsureRoutes(ctx, reqB)
		Expect(err).NotTo(HaveOccurred())
		Expect(entriesB[0].URL).To(HaveSuffix(":2201"))

		// Stickiness: repeat reconciles keep the same port and do not
		// rewrite the route.
		route := &gatewayv1alpha2.TCPRoute{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ssh", Namespace: "ws-pp2-wsd-a"}, route)).To(Succeed())
		rvBefore := route.ResourceVersion

		entriesA, err = provider.EnsureRoutes(ctx, reqA)
		Expect(err).NotTo(HaveOccurred())
		Expect(entriesA[0].URL).To(HaveSuffix(":2200"))
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ssh", Namespace: "ws-pp2-wsd-a"}, route)).To(Succeed())
		Expect(route.ResourceVersion).To(Equal(rvBefore))
	})

	It("reports exhaustion per endpoint, leaves HTTP unaffected, and recovers after release", func() {
		cd := gwSetupTopology("pp3")
		// Pool of exactly one port.
		gwSetupGateway("pp3-gwns", true,
			gwHTTPSListener(),
			gwTCPListener("ssh-2200", 2200),
		)
		provider := gwNewProvider("pp3-gwns")

		reqA := gwMakeRequest("pp3-wsd-a", cd, sshEndpoint)
		entriesA, err := provider.EnsureRoutes(ctx, reqA)
		Expect(err).NotTo(HaveOccurred())
		Expect(entriesA[0].URL).To(HaveSuffix(":2200"))

		// Second workspace: HTTP endpoint works, SSH is exhausted.
		reqB := gwMakeRequest("pp3-wsd-b", cd,
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888},
			sshEndpoint,
		)
		entriesB, err := provider.EnsureRoutes(ctx, reqB)
		Expect(err).NotTo(HaveOccurred())
		Expect(entriesB).To(HaveLen(2))
		Expect(entriesB[0].URL).To(Equal("https://pp3-wsd-b." + gwTestDomain))
		Expect(entriesB[1].Ready).To(BeFalse())
		Expect(entriesB[1].Message).To(ContainSubstring("port pool exhausted"))
		Expect(entriesB[1].URL).To(BeEmpty())

		// Releasing workspace A's routes frees the port immediately — even
		// though the mirror namespace lingers in Terminating (envtest has no
		// namespace controller), the TCPRoute itself is deleted explicitly.
		Expect(provider.CleanupRoutes(ctx, reqA)).To(Succeed())
		err = k8sClient.Get(ctx, client.ObjectKey{Name: "ssh", Namespace: "ws-pp3-wsd-a"}, &gatewayv1alpha2.TCPRoute{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		entriesB, err = provider.EnsureRoutes(ctx, reqB)
		Expect(err).NotTo(HaveOccurred())
		Expect(entriesB[1].URL).To(Equal("ssh://" + gwTestDomain + ":2200"))
	})

	It("uses the tcp:// scheme for plain TCP endpoints", func() {
		cd := gwSetupTopology("pp4")
		gwSetupGateway("pp4-gwns", true,
			gwHTTPSListener(),
			gwTCPListener("tcp-2300", 2300),
		)
		provider := gwNewProvider("pp4-gwns")

		entries, err := provider.EnsureRoutes(ctx, gwMakeRequest("pp4-wsd", cd,
			workspacesv1.AccessEndpoint{Name: "db", Protocol: workspacesv1.RouteProtocolTCP, Port: 5432}))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries[0].URL).To(Equal("tcp://" + gwTestDomain + ":2300"))
	})

	It("reports a missing port pool when the gateway has no TCP listeners", func() {
		cd := gwSetupTopology("pp5")
		gwSetupGateway("pp5-gwns", true) // HTTPS only
		provider := gwNewProvider("pp5-gwns")

		entries, err := provider.EnsureRoutes(ctx, gwMakeRequest("pp5-wsd", cd, sshEndpoint))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries[0].Ready).To(BeFalse())
		Expect(entries[0].Message).To(ContainSubstring("no TCP listeners"))
		// No route object was created.
		err = k8sClient.Get(ctx, client.ObjectKey{Name: "ssh", Namespace: "ws-pp5-wsd"}, &gatewayv1alpha2.TCPRoute{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
})

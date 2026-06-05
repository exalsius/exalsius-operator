package workspaces

import (
	"context"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"github.com/exalsius/exalsius-operator/internal/controller/workspaces/routing"
)

var _ = Describe("Deletion resilience", func() {
	const (
		timeout  = 30 * time.Second
		interval = 250 * time.Millisecond
	)

	makeClass := func(name string) *workspacesv1.WorkspaceClass {
		return &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName:     "Resilience Test",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "res-template"},
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerReplica: workspacesv1.ResourceRequirements{CPU: resourceQuantityPtr("100m")},
				},
			},
		}
	}

	makeCD := func(name string) *k0rdentv1beta1.ClusterDeployment {
		return &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       k0rdentv1beta1.ClusterDeploymentSpec{Template: "tpl"},
		}
	}

	makeWSD := func(name, classRef, cdName string) *workspacesv1.WorkspaceDeployment {
		return &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: classRef,
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name: cdName, Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{Username: "tester"},
			},
		}
	}

	waitForDeploying := func(wsd *workspacesv1.WorkspaceDeployment) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
		}, timeout, interval).Should(Succeed())
	}

	It("blocks deletion while route cleanup fails, holding the ServiceSet (ordering)", func() {
		Expect(k8sClient.Create(ctx, makeClass("res-rt-class"))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("res-rt-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("res-rt-cd", "default")

		wsd := makeWSD("res-rt-wsd", "res-rt-class", "res-rt-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())
		waitForDeploying(wsd)

		// Route cleanup fails (e.g. regional cluster unreachable) — the CD
		// still exists, so the operator must hold on, NOT give up: a
		// time-boxed give-up would leak a pool port and a live route.
		testRouteProvider.setFailCleanup("res-rt-wsd", "regional unreachable")
		Expect(k8sClient.Delete(ctx, wsd)).To(Succeed())

		// First deletion reconcile marks the phase.
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeleting))
		}, timeout, interval).Should(Succeed())

		Consistently(func(g Gomega) {
			// WSD held by the finalizer...
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			// ...and the ServiceSet must still exist: backends only go away
			// AFTER the front door is gone.
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name: "wsd-res-rt-cd-res-rt-wsd", Namespace: "default",
			}, ss)).To(Succeed())
		}, 3*time.Second, interval).Should(Succeed())

		// Reachability returns → deletion completes end-to-end.
		testRouteProvider.clearFailCleanup("res-rt-wsd")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), &workspacesv1.WorkspaceDeployment{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Name: "wsd-res-rt-cd-res-rt-wsd", Namespace: "default",
			}, &k0rdentv1beta1.ServiceSet{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}, timeout, interval).Should(Succeed())
	})

	It("blocks deletion while the child cluster is unreachable, after the ServiceSet is gone", func() {
		Expect(k8sClient.Create(ctx, makeClass("res-ns-class"))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("res-ns-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("res-ns-cd", "default")

		wsd := makeWSD("res-ns-wsd", "res-ns-class", "res-ns-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())
		waitForDeploying(wsd)

		// Child cluster becomes unreachable while the CD still exists.
		Expect(k8sClient.Delete(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "res-ns-cd-kubeconfig", Namespace: "default"},
		})).To(Succeed())

		Expect(k8sClient.Delete(ctx, wsd)).To(Succeed())

		// Routes + ServiceSet cleanup proceed; namespace cleanup blocks —
		// proving the order (SS before namespace, finalizer last).
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, client.ObjectKey{
				Name: "wsd-res-ns-cd-res-ns-wsd", Namespace: "default",
			}, &k0rdentv1beta1.ServiceSet{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}, timeout, interval).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), &workspacesv1.WorkspaceDeployment{})).To(Succeed())
		}, 3*time.Second, interval).Should(Succeed())

		// Reachability returns → namespace cleanup completes, WSD goes away.
		ensureChildKubeconfigSecret("res-ns-cd", "default")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), &workspacesv1.WorkspaceDeployment{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}, timeout, interval).Should(Succeed())
	})

	It("sweeps orphaned mirror namespaces and their TCPRoutes on the regional cluster", func() {
		// Regional cluster with role label (the sweep's discovery key).
		gwSetupTopology("sweep")
		provider := gwNewProvider("sweep-gwns")

		// An orphaned mirror namespace (workspace long gone) holding a
		// TCPRoute — i.e. a leaked pool port.
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "ws-sweep-orphan",
				Labels: map[string]string{LabelWorkspace: "sweep-orphan"},
			},
		})).To(Succeed())
		gwNs := gatewayv1alpha2.Namespace("sweep-gwns")
		section := gatewayv1alpha2.SectionName("ssh-2200")
		Expect(k8sClient.Create(ctx, &gatewayv1alpha2.TCPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ssh", Namespace: "ws-sweep-orphan",
				Labels: map[string]string{LabelWorkspace: "sweep-orphan"},
			},
			Spec: gatewayv1alpha2.TCPRouteSpec{
				CommonRouteSpec: gatewayv1alpha2.CommonRouteSpec{
					ParentRefs: []gatewayv1alpha2.ParentReference{{
						Name: "exalsius-workspaces", Namespace: &gwNs, SectionName: &section,
					}},
				},
				Rules: []gatewayv1alpha2.TCPRouteRule{{
					BackendRefs: []gatewayv1alpha2.BackendRef{{
						BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
							Name: "svc",
							Port: ptr.To(int32(22)),
						},
					}},
				}},
			},
		})).To(Succeed())

		// A live workspace's mirror namespace must survive.
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "ws-sweep-live",
				Labels: map[string]string{LabelWorkspace: "sweep-live"},
			},
		})).To(Succeed())

		// Everything except the orphan counts as active — keeps the sweep
		// from touching namespaces created by other specs.
		Expect(provider.SweepOrphans(ctx, routing.SweepRequest{
			ManagementClient:  k8sClient,
			Scheme:            scheme.Scheme,
			IsActiveWorkspace: func(name string) bool { return name != "sweep-orphan" },
		})).To(Succeed())

		// Orphan: TCPRoute gone (port freed), namespace deletion underway.
		err := k8sClient.Get(ctx, client.ObjectKey{Name: "ssh", Namespace: "ws-sweep-orphan"}, &gatewayv1alpha2.TCPRoute{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		ns := &corev1.Namespace{}
		err = k8sClient.Get(ctx, client.ObjectKey{Name: "ws-sweep-orphan"}, ns)
		if err == nil {
			Expect(ns.DeletionTimestamp.IsZero()).To(BeFalse())
		} else {
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}

		// Live namespace untouched.
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ws-sweep-live"}, ns)).To(Succeed())
		Expect(ns.DeletionTimestamp.IsZero()).To(BeTrue())
	})

	It("runs the sweep at startup and then periodically", func() {
		var calls atomic.Int32
		runner := &orphanRouteSweeper{
			client:   k8sClient,
			scheme:   scheme.Scheme,
			interval: 50 * time.Millisecond,
			sweeper:  stubSweeper{calls: &calls},
		}

		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			Expect(runner.Start(runCtx)).To(Succeed())
			close(done)
		}()

		// One immediate sweep plus at least one tick.
		Eventually(func() int32 { return calls.Load() }, timeout, interval).Should(BeNumerically(">=", 2))
		cancel()
		Eventually(done, timeout).Should(BeClosed())
	})
})

// stubSweeper counts sweep invocations for the runner spec.
type stubSweeper struct {
	calls *atomic.Int32
}

func (s stubSweeper) SweepOrphans(_ context.Context, req routing.SweepRequest) error {
	// The runner must hand over a usable active-set callback.
	Expect(req.IsActiveWorkspace).NotTo(BeNil())
	s.calls.Add(1)
	return nil
}

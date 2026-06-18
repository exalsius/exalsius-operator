package workspaces

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/exalsius/exalsius-operator/internal/controller/workspaces/routing"
)

// fakeRouteProvider is the in-memory test double at the RouteProvider seam.
// It is a test artifact only — never registered in the operator binary.
// Behavior is keyed per workspace name so specs can run against the shared
// suite manager without interfering.
type fakeRouteProvider struct {
	mu sync.Mutex
	// ensureCalls / cleanupCalls count provider invocations per workspace.
	ensureCalls  map[string]int
	cleanupCalls map[string]int
	// failEnsure makes EnsureRoutes return an error for a workspace.
	failEnsure map[string]string
	// failCleanup makes CleanupRoutes return an error for a workspace.
	failCleanup map[string]string
	// ssExistedAtCleanup records whether the workspace's ServiceSet still
	// existed when CleanupRoutes first ran — the reconciler contract is
	// routes-before-ServiceSet.
	ssExistedAtCleanup map[string]bool
}

func newFakeRouteProvider() *fakeRouteProvider {
	return &fakeRouteProvider{
		ensureCalls:        map[string]int{},
		cleanupCalls:       map[string]int{},
		failEnsure:         map[string]string{},
		failCleanup:        map[string]string{},
		ssExistedAtCleanup: map[string]bool{},
	}
}

func (f *fakeRouteProvider) EnsureRoutes(_ context.Context, req routing.RouteRequest) ([]workspacesv1.AccessEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	name := req.Workspace.Name
	f.ensureCalls[name]++
	if msg, ok := f.failEnsure[name]; ok {
		return nil, errors.New(msg)
	}

	entries := make([]workspacesv1.AccessEntry, 0, len(req.Endpoints))
	for _, ep := range req.Endpoints {
		entries = append(entries, workspacesv1.AccessEntry{
			Name:     ep.Name,
			Protocol: ep.Protocol,
			URL:      fmt.Sprintf("https://%s-%s.test.invalid", name, ep.Name),
			Ready:    true,
		})
	}
	return entries, nil
}

func (f *fakeRouteProvider) CleanupRoutes(ctx context.Context, req routing.RouteRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	name := req.Workspace.Name
	f.cleanupCalls[name]++

	// Record the ordering evidence only on the first call — later deletion
	// passes legitimately run after the ServiceSet is gone.
	if _, seen := f.ssExistedAtCleanup[name]; !seen {
		ss := &k0rdentv1beta1.ServiceSet{}
		err := k8sClient.Get(ctx, client.ObjectKey{
			Name:      serviceSetName(req.Workspace),
			Namespace: req.Workspace.Spec.ClusterDeploymentRef.Namespace,
		}, ss)
		f.ssExistedAtCleanup[name] = err == nil && ss.DeletionTimestamp.IsZero()
	}
	if msg, ok := f.failCleanup[name]; ok {
		return errors.New(msg)
	}
	return nil
}

func (f *fakeRouteProvider) setFailCleanup(workspaceName, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failCleanup[workspaceName] = msg
}

func (f *fakeRouteProvider) clearFailCleanup(workspaceName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.failCleanup, workspaceName)
}

func (f *fakeRouteProvider) setFailEnsure(workspaceName, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failEnsure[workspaceName] = msg
}

func (f *fakeRouteProvider) clearFailEnsure(workspaceName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.failEnsure, workspaceName)
}

func (f *fakeRouteProvider) ensureCount(workspaceName string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ensureCalls[workspaceName]
}

func (f *fakeRouteProvider) cleanupEvidence(workspaceName string) (calls int, ssExisted bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cleanupCalls[workspaceName], f.ssExistedAtCleanup[workspaceName]
}

var _ = Describe("RouteProvider integration", func() {
	const (
		timeout  = 30 * time.Second
		interval = 250 * time.Millisecond
	)

	makeClassWithEndpoints := func(name string, endpoints ...workspacesv1.AccessEndpoint) *workspacesv1.WorkspaceClass {
		return &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName:     "Routing Test",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "routing-template"},
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerReplica: workspacesv1.ResourceRequirements{CPU: resourceQuantityPtr("100m")},
				},
				AccessEndpoints: endpoints,
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
			},
		}
	}

	flipServiceSetDeployed := func(wsd *workspacesv1.WorkspaceDeployment) {
		GinkgoHelper()
		ssName := serviceSetName(wsd)
		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ssName, Namespace: "default"}, ss)).To(Succeed())
			now := metav1.Now()
			ss.Status.Services = []k0rdentv1beta1.ServiceState{{
				Name:                    serviceEntryName(wsd),
				Namespace:               workspaceNamespaceName(wsd),
				Template:                "routing-template",
				State:                   k0rdentv1beta1.ServiceStateDeployed,
				Type:                    "Helm",
				LastStateTransitionTime: &now,
			}}
			ss.Status.Deployed = true
			g.Expect(k8sClient.Status().Update(ctx, ss)).To(Succeed())
		}, timeout, interval).Should(Succeed())
	}

	It("populates status.access once the workspace is Running", func() {
		Expect(k8sClient.Create(ctx, makeClassWithEndpoints("rt-class",
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888},
			workspacesv1.AccessEndpoint{Name: "api", Protocol: workspacesv1.RouteProtocolHTTP, Port: 6006},
		))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("rt-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("rt-cd", "default")

		wsd := makeWSD("rt-wsd", "rt-class", "rt-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// While Deploying, no routes exist yet.
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
			g.Expect(fetched.Status.Access).To(BeEmpty())
		}, timeout, interval).Should(Succeed())
		Expect(testRouteProvider.ensureCount("rt-wsd")).To(BeZero())

		flipServiceSetDeployed(wsd)

		// Running → provider invoked → access entries + RoutesReady=True.
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseRunning))
			g.Expect(fetched.Status.Access).To(HaveLen(2))
			g.Expect(fetched.Status.Access[0].Name).To(Equal("ide"))
			g.Expect(fetched.Status.Access[0].URL).To(Equal("https://rt-wsd-ide.test.invalid"))
			g.Expect(fetched.Status.Access[0].Ready).To(BeTrue())
			g.Expect(fetched.Status.Access[1].Name).To(Equal("api"))
			g.Expect(fetched.Status.Access[1].Ready).To(BeTrue())

			cond := findCondition(fetched.Status.Conditions, workspacesv1.ConditionRoutesReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(workspacesv1.ReasonRoutesReady))
		}, timeout, interval).Should(Succeed())
	})

	It("does not call the provider when the class declares no endpoints", func() {
		Expect(k8sClient.Create(ctx, makeClassWithEndpoints("rt-noep-class"))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("rt-noep-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("rt-noep-cd", "default")

		wsd := makeWSD("rt-noep-wsd", "rt-noep-class", "rt-noep-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())
		flipServiceSetDeployed(wsd)

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseRunning))
		}, timeout, interval).Should(Succeed())

		// No provider calls, no access entries, no RoutesReady condition.
		fetched := &workspacesv1.WorkspaceDeployment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
		Expect(fetched.Status.Access).To(BeEmpty())
		Expect(findCondition(fetched.Status.Conditions, workspacesv1.ConditionRoutesReady)).To(BeNil())
		Expect(testRouteProvider.ensureCount("rt-noep-wsd")).To(BeZero())
	})

	It("surfaces provider errors as RoutesReady=False without failing the workspace, then recovers", func() {
		testRouteProvider.setFailEnsure("rt-err-wsd", "gateway exploded")

		Expect(k8sClient.Create(ctx, makeClassWithEndpoints("rt-err-class",
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888},
		))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("rt-err-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("rt-err-cd", "default")

		wsd := makeWSD("rt-err-wsd", "rt-err-class", "rt-err-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())
		flipServiceSetDeployed(wsd)

		// Degraded, not failed: Running phase with RoutesReady=False.
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseRunning))
			cond := findCondition(fetched.Status.Conditions, workspacesv1.ConditionRoutesReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(workspacesv1.ReasonRoutingError))
			g.Expect(cond.Message).To(ContainSubstring("gateway exploded"))
		}, timeout, interval).Should(Succeed())

		// Provider recovers → routes converge without any user action.
		testRouteProvider.clearFailEnsure("rt-err-wsd")
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			cond := findCondition(fetched.Status.Conditions, workspacesv1.ConditionRoutesReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(fetched.Status.Access).To(HaveLen(1))
			g.Expect(fetched.Status.Access[0].Ready).To(BeTrue())
		}, timeout, interval).Should(Succeed())
	})

	It("cleans up routes before deleting the ServiceSet", func() {
		Expect(k8sClient.Create(ctx, makeClassWithEndpoints("rt-del-class",
			workspacesv1.AccessEndpoint{Name: "ide", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8888},
		))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("rt-del-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("rt-del-cd", "default")

		wsd := makeWSD("rt-del-wsd", "rt-del-class", "rt-del-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())
		flipServiceSetDeployed(wsd)

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Access).To(HaveLen(1))
		}, timeout, interval).Should(Succeed())

		Expect(k8sClient.Delete(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), &workspacesv1.WorkspaceDeployment{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}, timeout, interval).Should(Succeed())

		// CleanupRoutes ran, and the ServiceSet was still alive when it did.
		calls, ssExisted := testRouteProvider.cleanupEvidence("rt-del-wsd")
		Expect(calls).To(BeNumerically(">=", 1))
		Expect(ssExisted).To(BeTrue(), "routes must be cleaned up BEFORE the ServiceSet is deleted")
	})
})

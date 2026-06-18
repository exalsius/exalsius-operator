package workspaces

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("FailureContext", func() {
	const (
		timeout  = 30 * time.Second
		interval = 250 * time.Millisecond
	)

	makeClass := func(name string) *workspacesv1.WorkspaceClass {
		return &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName:     "FailureContext Test",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "fc-template"},
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
			},
		}
	}

	patchSSFailed := func(wsd *workspacesv1.WorkspaceDeployment, failureMsg string) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: serviceSetName(wsd), Namespace: "default"}, ss)).To(Succeed())
			now := metav1.Now()
			ss.Status.Services = []k0rdentv1beta1.ServiceState{{
				Name:                    serviceEntryName(wsd),
				Namespace:               workspaceNamespaceName(wsd),
				Template:                "fc-template",
				State:                   k0rdentv1beta1.ServiceStateFailed,
				FailureMessage:          failureMsg,
				Type:                    "Helm",
				LastStateTransitionTime: &now,
			}}
			g.Expect(k8sClient.Status().Update(ctx, ss)).To(Succeed())
		}, timeout, interval).Should(Succeed())
	}

	It("captures phase, reason, ServiceSet snapshot, and recent events on Helm failure", func() {
		Expect(k8sClient.Create(ctx, makeClass("fc-helm-class"))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("fc-helm-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("fc-helm-cd", "default")

		wsd := makeWSD("fc-helm-wsd", "fc-helm-class", "fc-helm-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// A pre-existing event for this workspace must surface in the
		// forensics alongside the controller's own failure event (which is
		// seeded directly, since it is recorded asynchronously and won't be
		// readable from the API during the same reconcile).
		Expect(k8sClient.Create(ctx, &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "fc-helm-wsd.prior", Namespace: "default"},
			InvolvedObject: corev1.ObjectReference{
				Kind: "WorkspaceDeployment", Namespace: "default", Name: "fc-helm-wsd",
			},
			Type: corev1.EventTypeWarning, Reason: "PriorWarning",
			Message:       "image pull slow",
			LastTimestamp: metav1.Now(),
		})).To(Succeed())

		patchSSFailed(wsd, "context deadline exceeded")

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseFailed))

			fc := fetched.Status.FailureContext
			g.Expect(fc).NotTo(BeNil())
			g.Expect(fc.PhaseWhenFailed).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
			g.Expect(fc.Reason).To(Equal(workspacesv1.ReasonHelmReleaseFailed))

			g.Expect(fc.ServiceSetStatus).NotTo(BeNil())
			g.Expect(fc.ServiceSetStatus.Name).To(Equal("wsd-fc-helm-cd-fc-helm-wsd"))
			g.Expect(fc.ServiceSetStatus.Services).To(HaveLen(1))
			g.Expect(fc.ServiceSetStatus.Services[0].State).To(Equal(k0rdentv1beta1.ServiceStateFailed))
			g.Expect(fc.ServiceSetStatus.Services[0].FailureMessage).To(Equal("context deadline exceeded"))

			reasons := make([]string, 0, len(fc.RecentEvents))
			for _, ev := range fc.RecentEvents {
				reasons = append(reasons, ev.Reason)
			}
			// The controller's own failure event is captured (newest first)...
			g.Expect(fc.RecentEvents).NotTo(BeEmpty())
			g.Expect(fc.RecentEvents[0].Reason).To(Equal(workspacesv1.ReasonHelmReleaseFailed))
			g.Expect(fc.RecentEvents[0].Message).To(Equal("Helm release failed: context deadline exceeded"))
			// ...alongside the pre-existing event.
			g.Expect(reasons).To(ContainElement("PriorWarning"))
		}, timeout, interval).Should(Succeed())
	})

	It("captures pre-deploy failures with phaseWhenFailed=Pending", func() {
		wsd := makeWSD("fc-noclass-wsd", "fc-no-such-class", "fc-any-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseFailed))

			fc := fetched.Status.FailureContext
			g.Expect(fc).NotTo(BeNil())
			g.Expect(fc.PhaseWhenFailed).To(Equal(workspacesv1.WorkspaceDeploymentPhasePending))
			g.Expect(fc.Reason).To(Equal(workspacesv1.ReasonClassNotFound))
			// No ServiceSet was ever created — the snapshot is absent, not fabricated.
			g.Expect(fc.ServiceSetStatus).To(BeNil())
		}, timeout, interval).Should(Succeed())
	})

	It("clears failureContext when the workspace recovers", func() {
		Expect(k8sClient.Create(ctx, makeClass("fc-rec-class"))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("fc-rec-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("fc-rec-cd", "default")

		wsd := makeWSD("fc-rec-wsd", "fc-rec-class", "fc-rec-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
		}, timeout, interval).Should(Succeed())

		// Stale forensics from an earlier failed incarnation.
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			fetched.Status.FailureContext = &workspacesv1.FailureContext{
				PhaseWhenFailed: workspacesv1.WorkspaceDeploymentPhaseDeploying,
				Reason:          "Stale",
			}
			g.Expect(k8sClient.Status().Update(ctx, fetched)).To(Succeed())
		}, timeout, interval).Should(Succeed())

		// Recovery: the ServiceSet reports Deployed → Running clears it.
		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: serviceSetName(wsd), Namespace: "default"}, ss)).To(Succeed())
			now := metav1.Now()
			ss.Status.Services = []k0rdentv1beta1.ServiceState{{
				Name:                    serviceEntryName(wsd),
				Namespace:               workspaceNamespaceName(wsd),
				Template:                "fc-template",
				State:                   k0rdentv1beta1.ServiceStateDeployed,
				Type:                    "Helm",
				LastStateTransitionTime: &now,
			}}
			ss.Status.Deployed = true
			g.Expect(k8sClient.Status().Update(ctx, ss)).To(Succeed())
		}, timeout, interval).Should(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseRunning))
			g.Expect(fetched.Status.FailureContext).To(BeNil())
		}, timeout, interval).Should(Succeed())
	})
})

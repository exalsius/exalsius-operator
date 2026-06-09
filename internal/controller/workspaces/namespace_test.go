package workspaces

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Workspace namespace lifecycle", func() {
	const (
		timeout  = 30 * time.Second
		interval = 250 * time.Millisecond
	)

	makeClass := func(name string) *workspacesv1.WorkspaceClass {
		return &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName:     "NS Test",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "ns-template"},
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

	It("creates the labeled workspace namespace and targets it in the ServiceSet", func() {
		Expect(k8sClient.Create(ctx, makeClass("nslc-class"))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("nslc-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("nslc-cd", "default")

		wsd := makeWSD("nslc-wsd", "nslc-class", "nslc-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// The workspace namespace must exist on the child cluster with the
		// mesh-discovery label, and the ServiceSet entry must install into it.
		Eventually(func(g Gomega) {
			ns := &corev1.Namespace{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ws-nslc-wsd"}, ns)).To(Succeed())
			g.Expect(ns.Labels).To(HaveKeyWithValue(LabelWorkspace, "nslc-wsd"))
			// Mesh enrollment (suite runs ambient mode).
			g.Expect(ns.Labels).To(HaveKeyWithValue("istio.io/dataplane-mode", "ambient"))

			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name: "wsd-nslc-cd-nslc-wsd", Namespace: "default",
			}, ss)).To(Succeed())
			g.Expect(ss.Spec.Services).To(HaveLen(1))
			g.Expect(ss.Spec.Services[0].Namespace).To(Equal("ws-nslc-wsd"))
		}, timeout, interval).Should(Succeed())
	})

	It("keeps prerequisite ServiceSets in the default namespace", func() {
		wsc := makeClass("nsprereq-class")
		wsc.Spec.Prerequisites = []workspacesv1.PrerequisiteSpec{
			{ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "nsprereq-template"}},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("nsprereq-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("nsprereq-cd", "default")

		wsd := makeWSD("nsprereq-wsd", "nsprereq-class", "nsprereq-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// Prerequisites are shared cluster-local infrastructure — they never
		// move into workspace namespaces (ADR-0001).
		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name: "wsprereq-nsprereq-cd-nsprereq-template", Namespace: "default",
			}, ss)).To(Succeed())
			g.Expect(ss.Spec.Services).To(HaveLen(1))
			g.Expect(ss.Spec.Services[0].Namespace).To(Equal("default"))
		}, timeout, interval).Should(Succeed())
	})

	It("deletes the workspace namespace on the child cluster when the WSD is deleted", func() {
		Expect(k8sClient.Create(ctx, makeClass("nsdel-class"))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("nsdel-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("nsdel-cd", "default")

		wsd := makeWSD("nsdel-wsd", "nsdel-class", "nsdel-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			ns := &corev1.Namespace{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ws-nsdel-wsd"}, ns)).To(Succeed())
		}, timeout, interval).Should(Succeed())

		Expect(k8sClient.Delete(ctx, wsd)).To(Succeed())

		// ServiceSet goes first, then the namespace; the WSD finalizer is
		// removed only after namespace deletion is underway.
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), &workspacesv1.WorkspaceDeployment{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

			ss := &k0rdentv1beta1.ServiceSet{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-nsdel-cd-nsdel-wsd", Namespace: "default"}, ss)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

			// envtest has no namespace controller, so the namespace stays
			// Terminating forever — deletion being underway is the assertion.
			ns := &corev1.Namespace{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: "ws-nsdel-wsd"}, ns)
			if err == nil {
				g.Expect(ns.DeletionTimestamp.IsZero()).To(BeFalse())
			} else {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}
		}, timeout, interval).Should(Succeed())
	})

	It("skips namespace cleanup when the ClusterDeployment is gone", func() {
		Expect(k8sClient.Create(ctx, makeClass("nsskip-class"))).To(Succeed())
		cd := makeCD("nsskip-cd")
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())
		ensureChildKubeconfigSecret("nsskip-cd", "default")

		wsd := makeWSD("nsskip-wsd", "nsskip-class", "nsskip-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			ns := &corev1.Namespace{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ws-nsskip-wsd"}, ns)).To(Succeed())
		}, timeout, interval).Should(Succeed())

		// Cluster teardown: the CD disappears before the workspace is deleted.
		Expect(k8sClient.Delete(ctx, cd)).To(Succeed())
		Expect(k8sClient.Delete(ctx, wsd)).To(Succeed())

		// Deletion must complete without child-cluster cleanup.
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), &workspacesv1.WorkspaceDeployment{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}, timeout, interval).Should(Succeed())

		// The namespace was deliberately left alone — teardown owns it.
		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "ws-nsskip-wsd"}, ns)).To(Succeed())
		Expect(ns.DeletionTimestamp.IsZero()).To(BeTrue())
	})

	It("waits while a terminating predecessor namespace blocks deployment", func() {
		// Simulate a same-name predecessor still cleaning up: envtest has no
		// namespace controller, so a deleted namespace is Terminating forever.
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ws-nswait-wsd"}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		Expect(k8sClient.Delete(ctx, ns)).To(Succeed())

		Expect(k8sClient.Create(ctx, makeClass("nswait-class"))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("nswait-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("nswait-cd", "default")

		wsd := makeWSD("nswait-wsd", "nswait-class", "nswait-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// The workspace must hold in Pending — installing into a terminating
		// namespace would resurrect stale state.
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhasePending))
			g.Expect(fetched.Status.Message).To(ContainSubstring("ws-nswait-wsd"))
		}, timeout, interval).Should(Succeed())

		// And no ServiceSet may exist yet.
		ss := &k0rdentv1beta1.ServiceSet{}
		err := k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-nswait-cd-nswait-wsd", Namespace: "default"}, ss)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	Describe("name validation", func() {
		makeNamedWSD := func(name string) *workspacesv1.WorkspaceDeployment {
			return makeWSD(name, "any-class", "any-cd")
		}

		It("rejects names longer than 60 characters", func() {
			longName := strings.Repeat("a", 61)
			err := k8sClient.Create(ctx, makeNamedWSD(longName))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("60 characters"))
		})

		It("rejects names that are not DNS-1123 labels", func() {
			err := k8sClient.Create(ctx, makeNamedWSD("dotted.name"))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("DNS-1123"))
		})

		It("accepts a 60-character name", func() {
			name := fmt.Sprintf("a%s", strings.Repeat("b", 59))
			wsd := makeNamedWSD(name)
			Expect(k8sClient.Create(ctx, wsd)).To(Succeed())
			Expect(k8sClient.Delete(ctx, wsd)).To(Succeed())
		})
	})
})

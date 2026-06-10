package workspaces

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("ClusterDeployment resolution", func() {
	const (
		timeout  = 30 * time.Second
		interval = 250 * time.Millisecond
	)

	makeClass := func(name string) *workspacesv1.WorkspaceClass {
		return &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName:     "CD Resolve Test",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "cdres-template"},
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerReplica: workspacesv1.ResourceRequirements{CPU: resourceQuantityPtr("100m")},
				},
			},
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

	It("reports ClusterDeploymentNotFound and stays Pending when the CD is missing, then heals", func() {
		Expect(k8sClient.Create(ctx, makeClass("cdres-class"))).To(Succeed())

		// WSD references a CD that does not exist yet.
		wsd := makeWSD("cdres-wsd", "cdres-class", "cdres-missing-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// Pending + ClusterDeploymentResolved=False/ClusterDeploymentNotFound.
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhasePending))
			cond := findCondition(fetched.Status.Conditions, workspacesv1.ConditionClusterDeploymentResolved)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(workspacesv1.ReasonClusterDeploymentNotFound))
			g.Expect(cond.Message).To(ContainSubstring("cdres-missing-cd"))
			// Short-circuited: ClassResolved was set, but no ServiceSet exists.
			g.Expect(findCondition(fetched.Status.Conditions, workspacesv1.ConditionClassResolved)).NotTo(BeNil())
		}, timeout, interval).Should(Succeed())

		// No ServiceSet should have been created while the CD is missing.
		Consistently(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-cdres-missing-cd-cdres-wsd", Namespace: "default"}, ss)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}, 2*time.Second, interval).Should(Succeed())

		// Create the CD → the workspace heals and proceeds past the check.
		Expect(k8sClient.Create(ctx, &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "cdres-missing-cd", Namespace: "default"},
			Spec:       k0rdentv1beta1.ClusterDeploymentSpec{Template: "tpl"},
		})).To(Succeed())
		ensureChildKubeconfigSecret("cdres-missing-cd", "default")

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			cond := findCondition(fetched.Status.Conditions, workspacesv1.ConditionClusterDeploymentResolved)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(workspacesv1.ReasonClusterDeploymentResolved))
			// Now proceeds to Deploying.
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
		}, timeout, interval).Should(Succeed())
	})

	It("sets ClusterDeploymentResolved=True on the happy path", func() {
		Expect(k8sClient.Create(ctx, makeClass("cdres-ok-class"))).To(Succeed())
		Expect(k8sClient.Create(ctx, &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "cdres-ok-cd", Namespace: "default"},
			Spec:       k0rdentv1beta1.ClusterDeploymentSpec{Template: "tpl"},
		})).To(Succeed())
		ensureChildKubeconfigSecret("cdres-ok-cd", "default")

		wsd := makeWSD("cdres-ok-wsd", "cdres-ok-class", "cdres-ok-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			cond := findCondition(fetched.Status.Conditions, workspacesv1.ConditionClusterDeploymentResolved)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
		}, timeout, interval).Should(Succeed())
	})
})

package workspaces

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Prerequisite auto-install", func() {
	const (
		timeout  = 30 * time.Second
		interval = 250 * time.Millisecond
	)

	makeClass := func(name, prereqTemplate string) *workspacesv1.WorkspaceClass {
		spec := workspacesv1.WorkspaceClassSpec{
			DisplayName:     "Prereq Test",
			ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "ws-template"},
			DefaultResources: workspacesv1.WorkspaceResourceSpec{
				PerReplica: workspacesv1.ResourceRequirements{CPU: resourceQuantityPtr("100m")},
			},
		}
		if prereqTemplate != "" {
			spec.Prerequisites = []workspacesv1.PrerequisiteSpec{
				{ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: prereqTemplate}},
			}
		}
		return &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       spec,
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

	patchSSStatus := func(ssKey client.ObjectKey, templateName, state, failureMsg string) {
		ss := &k0rdentv1beta1.ServiceSet{}
		Expect(k8sClient.Get(ctx, ssKey, ss)).To(Succeed())
		now := metav1.Now()
		ss.Status.Services = []k0rdentv1beta1.ServiceState{{
			Name:                    sanitizeServiceEntryName(templateName),
			Namespace:               "default",
			Template:                templateName,
			State:                   state,
			FailureMessage:          failureMsg,
			Type:                    "Helm",
			LastStateTransitionTime: &now,
		}}
		Expect(k8sClient.Status().Update(ctx, ss)).To(Succeed())
	}

	It("should skip prereq logic entirely when class has no prereqs", func() {
		Expect(k8sClient.Create(ctx, makeClass("noprereq-class", ""))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("noprereq-cd"))).To(Succeed())
		wsd := makeWSD("noprereq-wsd", "noprereq-class", "noprereq-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// Should reach Deploying directly, never enter InstallingPrerequisites.
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
			g.Expect(fetched.Status.Prerequisites).To(BeEmpty())
		}, timeout, interval).Should(Succeed())
	})

	It("should transition to Deploying when a prereq SS reports Deployed", func() {
		Expect(k8sClient.Create(ctx, makeClass("flip-class", "flip-prereq"))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("flip-cd"))).To(Succeed())
		wsd := makeWSD("flip-wsd", "flip-class", "flip-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		ssKey := client.ObjectKey{Name: "wsprereq-flip-cd-flip-prereq", Namespace: "default"}

		// Wait for the controller to create the prereq SS.
		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, ssKey, ss)).To(Succeed())
		}, timeout, interval).Should(Succeed())

		// Flip prereq SS to Deployed.
		patchSSStatus(ssKey, "flip-prereq", k0rdentv1beta1.ServiceStateDeployed, "")

		// WSD should advance through to Deploying (prereq satisfied).
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
			prereqCondition := findCondition(fetched.Status.Conditions, workspacesv1.ConditionPrerequisitesMet)
			g.Expect(prereqCondition).NotTo(BeNil())
			g.Expect(prereqCondition.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(fetched.Status.Prerequisites).To(HaveLen(1))
			g.Expect(fetched.Status.Prerequisites[0].Phase).To(Equal(workspacesv1.PrerequisitePhaseSatisfied))
		}, timeout, interval).Should(Succeed())
	})

	It("should fail-fast when a prereq SS reports Failed", func() {
		Expect(k8sClient.Create(ctx, makeClass("failfast-class", "failfast-prereq"))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("failfast-cd"))).To(Succeed())
		wsd := makeWSD("failfast-wsd", "failfast-class", "failfast-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		ssKey := client.ObjectKey{Name: "wsprereq-failfast-cd-failfast-prereq", Namespace: "default"}
		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, ssKey, ss)).To(Succeed())
		}, timeout, interval).Should(Succeed())

		patchSSStatus(ssKey, "failfast-prereq", k0rdentv1beta1.ServiceStateFailed, "chart pull failed")

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseFailed))
			g.Expect(fetched.Status.Message).To(ContainSubstring("failfast-prereq"))
			g.Expect(fetched.Status.Message).To(ContainSubstring("chart pull failed"))
		}, timeout, interval).Should(Succeed())
	})

	It("should accept colony-managed prereqs without creating wsprereq SS", func() {
		Expect(k8sClient.Create(ctx, makeClass("colony-class", "colony-prereq"))).To(Succeed())
		cd := makeCD("colony-cd")
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())

		// Colony has already deployed the prereq — reflected in cd.status.services[].
		Eventually(func(g Gomega) {
			fresh := &k0rdentv1beta1.ClusterDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cd), fresh)).To(Succeed())
			now := metav1.Now()
			fresh.Status.Services = []k0rdentv1beta1.ServiceState{{
				Name: "colony-prereq-svc", Namespace: "default",
				Template: "colony-prereq", State: k0rdentv1beta1.ServiceStateDeployed,
				Type: "Helm", LastStateTransitionTime: &now,
			}}
			g.Expect(k8sClient.Status().Update(ctx, fresh)).To(Succeed())
		}, timeout, interval).Should(Succeed())

		wsd := makeWSD("colony-wsd", "colony-class", "colony-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
			g.Expect(fetched.Status.Prerequisites).To(HaveLen(1))
			g.Expect(fetched.Status.Prerequisites[0].Source).To(Equal(workspacesv1.PrerequisiteSourceColony))
			g.Expect(fetched.Status.Prerequisites[0].Phase).To(Equal(workspacesv1.PrerequisitePhaseSatisfied))
		}, timeout, interval).Should(Succeed())

		// No wsprereq SS should have been created.
		ss := &k0rdentv1beta1.ServiceSet{}
		err := k8sClient.Get(ctx, client.ObjectKey{
			Name:      "wsprereq-colony-cd-colony-prereq",
			Namespace: "default",
		}, ss)
		Expect(err).To(HaveOccurred())
	})

	It("should fail loudly when versionConstraint is set on a prereq", func() {
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{Name: "vc-class"},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName:     "VC test",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "ws-template"},
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerReplica: workspacesv1.ResourceRequirements{CPU: resourceQuantityPtr("100m")},
				},
				Prerequisites: []workspacesv1.PrerequisiteSpec{{
					ServiceTemplate: workspacesv1.ServiceTemplateRef{
						Name:              "vc-prereq",
						VersionConstraint: ">=1.0.0",
					},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("vc-cd"))).To(Succeed())

		wsd := makeWSD("vc-wsd", "vc-class", "vc-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseFailed))
			g.Expect(fetched.Status.Message).To(ContainSubstring("versionConstraint"))
			cond := findCondition(fetched.Status.Conditions, workspacesv1.ConditionPrerequisitesMet)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal(workspacesv1.ReasonInvalidPrerequisite))
		}, timeout, interval).Should(Succeed())
	})

	It("should share a single prereq SS between two WSDs and reconcile both on flip", func() {
		Expect(k8sClient.Create(ctx, makeClass("share-class", "share-prereq"))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("share-cd"))).To(Succeed())

		wsdA := makeWSD("share-a", "share-class", "share-cd")
		wsdB := makeWSD("share-b", "share-class", "share-cd")
		Expect(k8sClient.Create(ctx, wsdA)).To(Succeed())
		Expect(k8sClient.Create(ctx, wsdB)).To(Succeed())

		ssKey := client.ObjectKey{Name: "wsprereq-share-cd-share-prereq", Namespace: "default"}
		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, ssKey, ss)).To(Succeed())
			// Only one — both WSDs converged on the shared name.
			var ssList k0rdentv1beta1.ServiceSetList
			g.Expect(k8sClient.List(ctx, &ssList, client.InNamespace("default"))).To(Succeed())
			matchingPrereqs := 0
			for _, item := range ssList.Items {
				if item.Labels["workspaces.exalsius.ai/cluster-deployment"] == "share-cd" &&
					item.Labels["workspaces.exalsius.ai/template"] == "share-prereq" {
					matchingPrereqs++
				}
			}
			g.Expect(matchingPrereqs).To(Equal(1))
		}, timeout, interval).Should(Succeed())

		patchSSStatus(ssKey, "share-prereq", k0rdentv1beta1.ServiceStateDeployed, "")

		// Both WSDs should advance to Deploying via the mapper.
		for _, wsd := range []*workspacesv1.WorkspaceDeployment{wsdA, wsdB} {
			Eventually(func(g Gomega) {
				fetched := &workspacesv1.WorkspaceDeployment{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
				g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
			}, timeout, interval).Should(Succeed())
		}
	})
})

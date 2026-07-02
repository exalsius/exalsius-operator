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
		ensureChildKubeconfigSecret("noprereq-cd", "default")
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
		ensureChildKubeconfigSecret("flip-cd", "default")
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
		ensureChildKubeconfigSecret("failfast-cd", "default")
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
		ensureChildKubeconfigSecret(cd.Name, cd.Namespace)

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

	It("should install a prereq into the namespace declared on the class and report it", func() {
		wsc := makeClass("nsx-class", "nsx-prereq")
		wsc.Spec.Prerequisites[0].Namespace = "monitoring"
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())
		Expect(k8sClient.Create(ctx, makeCD("nsx-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("nsx-cd", "default")
		wsd := makeWSD("nsx-wsd", "nsx-class", "nsx-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		ssKey := client.ObjectKey{Name: "wsprereq-nsx-cd-nsx-prereq", Namespace: "default"}
		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, ssKey, ss)).To(Succeed())
			g.Expect(ss.Spec.Services).To(HaveLen(1))
			g.Expect(ss.Spec.Services[0].Namespace).To(Equal("monitoring"))
			g.Expect(ss.Spec.Services[0].HelmOptions).NotTo(BeNil())
			g.Expect(ss.Spec.Services[0].HelmOptions.InstallOptions).NotTo(BeNil())
			g.Expect(ss.Spec.Services[0].HelmOptions.InstallOptions.CreateNamespace).To(BeTrue())
		}, timeout, interval).Should(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Prerequisites).To(HaveLen(1))
			g.Expect(fetched.Status.Prerequisites[0].Namespace).To(Equal("monitoring"))
		}, timeout, interval).Should(Succeed())
	})

	It("should fail with a namespace conflict when an explicit namespace disagrees with the existing install", func() {
		Expect(k8sClient.Create(ctx, makeCD("conf-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("conf-cd", "default")

		// Incumbent shared prereq SS already pins the template to ns-a.
		incumbent := &k0rdentv1beta1.ServiceSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "wsprereq-conf-cd-conf-prereq",
				Namespace: "default",
				Labels: map[string]string{
					labelPrerequisiteRole:     labelPrerequisiteRoleValue,
					labelPrerequisiteCluster:  "conf-cd",
					labelPrerequisiteTemplate: "conf-prereq",
				},
			},
			Spec: k0rdentv1beta1.ServiceSetSpec{
				Cluster:  "conf-cd",
				Provider: k0rdentv1beta1.StateManagementProviderConfig{Name: defaultStateManagementProvider},
				Services: []k0rdentv1beta1.ServiceWithValues{{
					Name: "conf-prereq", Namespace: "ns-a", Template: "conf-prereq",
				}},
			},
		}
		Expect(k8sClient.Create(ctx, incumbent)).To(Succeed())

		// A class explicitly asks for ns-b — a conflict with the incumbent.
		wsc := makeClass("conf-class", "conf-prereq")
		wsc.Spec.Prerequisites[0].Namespace = "ns-b"
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())
		wsd := makeWSD("conf-wsd", "conf-class", "conf-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseFailed))
			g.Expect(fetched.Status.Message).To(ContainSubstring("ns-a"))
			g.Expect(fetched.Status.Message).To(ContainSubstring("ns-b"))
			cond := findCondition(fetched.Status.Conditions, workspacesv1.ConditionPrerequisitesMet)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal(workspacesv1.ReasonPrerequisiteNamespaceConflict))
			g.Expect(fetched.Status.Prerequisites).To(HaveLen(1))
			g.Expect(fetched.Status.Prerequisites[0].Phase).To(Equal(workspacesv1.PrerequisitePhaseFailed))
			g.Expect(fetched.Status.Prerequisites[0].Namespace).To(Equal("ns-a"))
		}, timeout, interval).Should(Succeed())
	})

	It("should accept the incumbent namespace when the class leaves namespace unset", func() {
		Expect(k8sClient.Create(ctx, makeCD("perm-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("perm-cd", "default")

		ssKey := client.ObjectKey{Name: "wsprereq-perm-cd-perm-prereq", Namespace: "default"}
		incumbent := &k0rdentv1beta1.ServiceSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ssKey.Name,
				Namespace: "default",
				Labels: map[string]string{
					labelPrerequisiteRole:     labelPrerequisiteRoleValue,
					labelPrerequisiteCluster:  "perm-cd",
					labelPrerequisiteTemplate: "perm-prereq",
				},
			},
			Spec: k0rdentv1beta1.ServiceSetSpec{
				Cluster:  "perm-cd",
				Provider: k0rdentv1beta1.StateManagementProviderConfig{Name: defaultStateManagementProvider},
				Services: []k0rdentv1beta1.ServiceWithValues{{
					Name: "perm-prereq", Namespace: "ns-a", Template: "perm-prereq",
				}},
			},
		}
		Expect(k8sClient.Create(ctx, incumbent)).To(Succeed())
		patchSSStatus(ssKey, "perm-prereq", k0rdentv1beta1.ServiceStateDeployed, "")

		// Class leaves prereq namespace unset — must accept ns-a, not conflict.
		wsc := makeClass("perm-class", "perm-prereq")
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())
		wsd := makeWSD("perm-wsd", "perm-class", "perm-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
			g.Expect(fetched.Status.Prerequisites).To(HaveLen(1))
			g.Expect(fetched.Status.Prerequisites[0].Phase).To(Equal(workspacesv1.PrerequisitePhaseSatisfied))
			g.Expect(fetched.Status.Prerequisites[0].Namespace).To(Equal("ns-a"))
		}, timeout, interval).Should(Succeed())
	})

	It("should fail with a conflict when an explicit namespace disagrees with a colony-provided prereq", func() {
		wsc := makeClass("colconf-class", "colconf-prereq")
		wsc.Spec.Prerequisites[0].Namespace = "ns-b"
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())
		cd := makeCD("colconf-cd")
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())
		ensureChildKubeconfigSecret(cd.Name, cd.Namespace)

		// Colony already deployed the prereq in ns-a.
		Eventually(func(g Gomega) {
			fresh := &k0rdentv1beta1.ClusterDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cd), fresh)).To(Succeed())
			now := metav1.Now()
			fresh.Status.Services = []k0rdentv1beta1.ServiceState{{
				Name: "colconf-svc", Namespace: "ns-a",
				Template: "colconf-prereq", State: k0rdentv1beta1.ServiceStateDeployed,
				Type: "Helm", LastStateTransitionTime: &now,
			}}
			g.Expect(k8sClient.Status().Update(ctx, fresh)).To(Succeed())
		}, timeout, interval).Should(Succeed())

		wsd := makeWSD("colconf-wsd", "colconf-class", "colconf-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseFailed))
			g.Expect(fetched.Status.Message).To(ContainSubstring("ns-a"))
			g.Expect(fetched.Status.Message).To(ContainSubstring("ns-b"))
			cond := findCondition(fetched.Status.Conditions, workspacesv1.ConditionPrerequisitesMet)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal(workspacesv1.ReasonPrerequisiteNamespaceConflict))
			g.Expect(fetched.Status.Prerequisites).To(HaveLen(1))
			g.Expect(fetched.Status.Prerequisites[0].Source).To(Equal(workspacesv1.PrerequisiteSourceColony))
			g.Expect(fetched.Status.Prerequisites[0].Namespace).To(Equal("ns-a"))
		}, timeout, interval).Should(Succeed())

		// No wsprereq SS should have been created for the conflicting prereq.
		ss := &k0rdentv1beta1.ServiceSet{}
		err := k8sClient.Get(ctx, client.ObjectKey{
			Name: "wsprereq-colconf-cd-colconf-prereq", Namespace: "default",
		}, ss)
		Expect(err).To(HaveOccurred())
	})

	It("should report the conflict message when another prereq also failed", func() {
		Expect(k8sClient.Create(ctx, makeCD("multi-cd"))).To(Succeed())
		ensureChildKubeconfigSecret("multi-cd", "default")

		// Prereq A: an ordinary install failure, listed first.
		failKey := client.ObjectKey{Name: "wsprereq-multi-cd-afail", Namespace: "default"}
		failSS := &k0rdentv1beta1.ServiceSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: failKey.Name, Namespace: "default",
				Labels: map[string]string{
					labelPrerequisiteRole:     labelPrerequisiteRoleValue,
					labelPrerequisiteCluster:  "multi-cd",
					labelPrerequisiteTemplate: "afail",
				},
			},
			Spec: k0rdentv1beta1.ServiceSetSpec{
				Cluster:  "multi-cd",
				Provider: k0rdentv1beta1.StateManagementProviderConfig{Name: defaultStateManagementProvider},
				Services: []k0rdentv1beta1.ServiceWithValues{{
					Name: "afail", Namespace: "default", Template: "afail",
				}},
			},
		}
		Expect(k8sClient.Create(ctx, failSS)).To(Succeed())
		patchSSStatus(failKey, "afail", k0rdentv1beta1.ServiceStateFailed, "chart boom")

		// Prereq B: incumbent in ns-a, but the class asks for ns-b — a conflict.
		confSS := &k0rdentv1beta1.ServiceSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "wsprereq-multi-cd-bconf", Namespace: "default",
				Labels: map[string]string{
					labelPrerequisiteRole:     labelPrerequisiteRoleValue,
					labelPrerequisiteCluster:  "multi-cd",
					labelPrerequisiteTemplate: "bconf",
				},
			},
			Spec: k0rdentv1beta1.ServiceSetSpec{
				Cluster:  "multi-cd",
				Provider: k0rdentv1beta1.StateManagementProviderConfig{Name: defaultStateManagementProvider},
				Services: []k0rdentv1beta1.ServiceWithValues{{
					Name: "bconf", Namespace: "ns-a", Template: "bconf",
				}},
			},
		}
		Expect(k8sClient.Create(ctx, confSS)).To(Succeed())

		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{Name: "multi-class"},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName:     "multi",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "ws-template"},
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerReplica: workspacesv1.ResourceRequirements{CPU: resourceQuantityPtr("100m")},
				},
				Prerequisites: []workspacesv1.PrerequisiteSpec{
					{ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "afail"}},
					{ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "bconf"}, Namespace: "ns-b"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())
		wsd := makeWSD("multi-wsd", "multi-class", "multi-cd")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseFailed))
			cond := findCondition(fetched.Status.Conditions, workspacesv1.ConditionPrerequisitesMet)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal(workspacesv1.ReasonPrerequisiteNamespaceConflict))
			// The reported message must be the conflict, not the unrelated failure.
			g.Expect(fetched.Status.Message).To(ContainSubstring("ns-a"))
			g.Expect(fetched.Status.Message).To(ContainSubstring("ns-b"))
			g.Expect(fetched.Status.Message).NotTo(ContainSubstring("chart boom"))
		}, timeout, interval).Should(Succeed())
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
		ensureChildKubeconfigSecret("vc-cd", "default")

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
		ensureChildKubeconfigSecret("share-cd", "default")

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

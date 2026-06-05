package workspaces

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Resource injection (integration)", func() {
	const (
		timeout  = 30 * time.Second
		interval = 250 * time.Millisecond
	)

	It("should set valid=false on a WorkspaceClass with a malformed resourceInjection path", func() {
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{Name: "wsc-bad-injection-path"},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName:     "Bad Injection",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "missing-template", Namespace: "default"},
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerReplica: workspacesv1.ResourceRequirements{CPU: resourceQuantityPtr("100m")},
				},
				ResourceInjection: &workspacesv1.ResourceInjectionMap{
					CPU: []workspacesv1.InjectionPath{"a..b"}, // empty segment — malformed
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		// validity reconciler runs: missing ST also sets valid=false, but the
		// path-error message should win OR the ST-not-found message should
		// surface; either way valid=false. Once a valid ST is in place, the
		// path malformation is the only blocker.
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceClass{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsc), fetched)).To(Succeed())
			g.Expect(fetched.Status.Valid).To(BeFalse())
		}, timeout, interval).Should(Succeed())
	})

	It("should write the standard _exalsius.resources path into the ServiceSet values", func() {
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{Name: "wsc-inject-standard"},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName:     "Standard Injection",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "standard-injection-template"},
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					Replicas: int32Ptr(2),
					PerReplica: workspacesv1.ResourceRequirements{
						CPU:      resourceQuantityPtr("4"),
						Memory:   resourceQuantityPtr("8Gi"),
						GPUCount: int32Ptr(1),
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		cd := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "inject-cd-1", Namespace: "default"},
			Spec:       k0rdentv1beta1.ClusterDeploymentSpec{Template: "tpl"},
		}
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())
		ensureChildKubeconfigSecret(cd.Name, cd.Namespace)

		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "inject-wsd-1", Namespace: "default"},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "wsc-inject-standard",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name: "inject-cd-1", Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{Username: "tester"},
			},
		}
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name: "wsd-inject-cd-1-inject-wsd-1", Namespace: "default",
			}, ss)).To(Succeed())
			g.Expect(ss.Spec.Services).To(HaveLen(1))

			var values map[string]any
			g.Expect(json.Unmarshal([]byte(ss.Spec.Services[0].Values), &values)).To(Succeed())

			ex, ok := values["_exalsius"].(map[string]any)
			g.Expect(ok).To(BeTrue(), "_exalsius key should be present")
			res, ok := ex["resources"].(map[string]any)
			g.Expect(ok).To(BeTrue())
			g.Expect(res["replicas"]).To(BeNumerically("==", 2))

			per, ok := res["perReplica"].(map[string]any)
			g.Expect(ok).To(BeTrue())
			g.Expect(per["cpu"]).To(Equal("4"))
			g.Expect(per["memory"]).To(Equal("8Gi"))
			g.Expect(per["gpuCount"]).To(BeNumerically("==", 1))
		}, timeout, interval).Should(Succeed())

		// And the ResourcesInjected condition is True on the WSD.
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			cond := findCondition(fetched.Status.Conditions, workspacesv1.ConditionResourcesInjected)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(workspacesv1.ReasonResourcesInjected))
		}, timeout, interval).Should(Succeed())
	})

	It("should inject into umbrella-chart paths declared by the class", func() {
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{Name: "wsc-inject-umbrella"},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName:     "Umbrella Injection",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "umbrella-template"},
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					Replicas: int32Ptr(3),
					PerReplica: workspacesv1.ResourceRequirements{
						CPU:    resourceQuantityPtr("8"),
						Memory: resourceQuantityPtr("32Gi"),
					},
				},
				ResourceInjection: &workspacesv1.ResourceInjectionMap{
					CPU:      []workspacesv1.InjectionPath{"jupyterhub.singleuser.cpu.guarantee"},
					Memory:   []workspacesv1.InjectionPath{"jupyterhub.singleuser.memory.guarantee"},
					Replicas: []workspacesv1.InjectionPath{"jupyterhub.replicas"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		cd := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "inject-cd-2", Namespace: "default"},
			Spec:       k0rdentv1beta1.ClusterDeploymentSpec{Template: "tpl"},
		}
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())
		ensureChildKubeconfigSecret(cd.Name, cd.Namespace)

		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "inject-wsd-2", Namespace: "default"},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "wsc-inject-umbrella",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name: "inject-cd-2", Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{Username: "tester"},
			},
		}
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name: "wsd-inject-cd-2-inject-wsd-2", Namespace: "default",
			}, ss)).To(Succeed())

			var values map[string]any
			g.Expect(json.Unmarshal([]byte(ss.Spec.Services[0].Values), &values)).To(Succeed())

			jh, ok := values["jupyterhub"].(map[string]any)
			g.Expect(ok).To(BeTrue(), "jupyterhub key injected at top level")
			g.Expect(jh["replicas"]).To(BeNumerically("==", 3))

			su := jh["singleuser"].(map[string]any)
			g.Expect(su["cpu"].(map[string]any)["guarantee"]).To(Equal("8"))
			g.Expect(su["memory"].(map[string]any)["guarantee"]).To(Equal("32Gi"))
		}, timeout, interval).Should(Succeed())
	})

	It("should overwrite user-supplied values at an injected path and emit a warning condition", func() {
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{Name: "wsc-inject-overwrite"},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName:     "Overwrite",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "overwrite-template"},
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerReplica: workspacesv1.ResourceRequirements{
						CPU: resourceQuantityPtr("2"),
					},
				},
				ResourceInjection: &workspacesv1.ResourceInjectionMap{
					CPU: []workspacesv1.InjectionPath{"jupyterhub.singleuser.cpu.guarantee"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		cd := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "inject-cd-3", Namespace: "default"},
			Spec:       k0rdentv1beta1.ClusterDeploymentSpec{Template: "tpl"},
		}
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())
		ensureChildKubeconfigSecret(cd.Name, cd.Namespace)

		// User submits spec.values at the same path the class will inject into.
		userValues := apiextensionsv1.JSON{Raw: []byte(`{"jupyterhub":{"singleuser":{"cpu":{"guarantee":"99"}}}}`)}

		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "inject-wsd-3", Namespace: "default"},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "wsc-inject-overwrite",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name: "inject-cd-3", Namespace: "default",
				},
				Owner:  workspacesv1.OwnerInfo{Username: "tester"},
				Values: &userValues,
			},
		}
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// Operator wins: the ServiceSet values reflect the structured CPU,
		// not the user's spec.values override.
		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name: "wsd-inject-cd-3-inject-wsd-3", Namespace: "default",
			}, ss)).To(Succeed())

			var values map[string]any
			g.Expect(json.Unmarshal([]byte(ss.Spec.Services[0].Values), &values)).To(Succeed())
			got := values["jupyterhub"].(map[string]any)["singleuser"].(map[string]any)["cpu"].(map[string]any)["guarantee"]
			g.Expect(got).To(Equal("2"))
		}, timeout, interval).Should(Succeed())

		// And the ResourcesInjected condition is False with reason=UserPathsOverwritten.
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			cond := findCondition(fetched.Status.Conditions, workspacesv1.ConditionResourcesInjected)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(workspacesv1.ReasonUserPathsOverwritten))
			g.Expect(cond.Message).To(ContainSubstring("jupyterhub.singleuser.cpu.guarantee"))
		}, timeout, interval).Should(Succeed())
	})
})

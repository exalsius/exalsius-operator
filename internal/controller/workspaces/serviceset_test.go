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

var _ = Describe("ServiceSet-based workspace deployment", func() {
	const (
		timeout  = 30 * time.Second
		interval = 250 * time.Millisecond
	)

	It("should create a ServiceSet when a WorkspaceDeployment is created", func() {
		// Create WorkspaceClass
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-ss-jupyter",
			},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName: "Jupyter Notebook",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{
					Name: "jupyter-ss-template",
				},
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerReplica: workspacesv1.ResourceRequirements{
						CPU: resourceQuantityPtr("2"),
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		// Create ClusterDeployment (must exist for ServiceSet to reference)
		cd := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ss-cluster",
				Namespace: "default",
			},
			Spec: k0rdentv1beta1.ClusterDeploymentSpec{
				Template: "some-template",
			},
		}
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())
		ensureChildKubeconfigSecret(cd.Name, cd.Namespace)

		// Create WorkspaceDeployment
		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ss-deploy",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "test-ss-jupyter",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "test-ss-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{Username: "testuser"},
			},
		}
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// A ServiceSet should be created
		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      serviceSetName(wsd),
				Namespace: "default",
			}, ss)).To(Succeed())

			// Verify ServiceSet spec
			g.Expect(ss.Spec.Cluster).To(Equal("test-ss-cluster"))
			g.Expect(ss.Spec.Provider.Name).To(Equal(defaultStateManagementProvider))
			g.Expect(ss.Spec.Services).To(HaveLen(1))
			g.Expect(ss.Spec.Services[0].Name).To(Equal("wsd-test-ss-cluster-test-ss-deploy"))
			g.Expect(ss.Spec.Services[0].Template).To(Equal("jupyter-ss-template"))

			// Verify adapter label for StateManagementProvider matching
			g.Expect(ss.Labels).To(HaveKeyWithValue("ksm.k0rdent.mirantis.com/adapter", "kcm-controller-manager"))

			// Verify owner reference points to the WorkspaceDeployment
			g.Expect(ss.OwnerReferences).To(HaveLen(1))
			g.Expect(ss.OwnerReferences[0].Kind).To(Equal("WorkspaceDeployment"))
			g.Expect(ss.OwnerReferences[0].Name).To(Equal("test-ss-deploy"))
		}, timeout, interval).Should(Succeed())

		// WSD should be in Deploying phase
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
			g.Expect(fetched.Status.ServiceEntryName).To(Equal("wsd-test-ss-cluster-test-ss-deploy"))
		}, timeout, interval).Should(Succeed())
	})
})

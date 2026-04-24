package workspaces

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("WorkspaceClass", func() {
	It("should create a SingleNode WorkspaceClass and read it back", func() {
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "jupyter-notebook",
			},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName: "Jupyter Notebook",
				Description: "Interactive Python notebook with JupyterLab UI",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{
					Name: "jupyter-workspace-1.0.0",
				},
				ResourceShape: workspacesv1.ResourceShapeSingleNode,
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerNode: workspacesv1.ResourceRequirements{
						CPU:      resourceQuantityPtr("2"),
						Memory:   resourceQuantityPtr("8Gi"),
						GPUCount: int32Ptr(0),
					},
				},
				AccessEndpoints: []workspacesv1.AccessEndpoint{
					{
						Name:        "web",
						Protocol:    workspacesv1.RouteProtocolHTTP,
						Port:        8888,
						Description: "JupyterLab web interface",
					},
				},
			},
		}

		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		// Read it back
		fetched := &workspacesv1.WorkspaceClass{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "jupyter-notebook"}, fetched)).To(Succeed())
		Expect(fetched.Spec.DisplayName).To(Equal("Jupyter Notebook"))
		Expect(fetched.Spec.ResourceShape).To(Equal(workspacesv1.ResourceShapeSingleNode))
		Expect(fetched.Spec.AccessEndpoints).To(HaveLen(1))
		Expect(fetched.Spec.AccessEndpoints[0].Name).To(Equal("web"))
	})

	It("should reject MultiNode WorkspaceClass without nodeCount", func() {
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "bad-multinode",
			},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName: "Bad MultiNode",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{
					Name: "some-template",
				},
				ResourceShape: workspacesv1.ResourceShapeMultiNode,
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					// Missing nodeCount — should fail CEL validation
					PerNode: workspacesv1.ResourceRequirements{
						CPU: resourceQuantityPtr("4"),
					},
				},
			},
		}

		err := k8sClient.Create(ctx, wsc)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("nodeCount"))
	})

	It("should accept MultiNode WorkspaceClass with nodeCount", func() {
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "slurm-cluster",
			},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName: "Slurm Cluster",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{
					Name: "slurm-workspace-1.0.0",
				},
				ResourceShape: workspacesv1.ResourceShapeMultiNode,
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					NodeCount: int32Ptr(3),
					PerNode: workspacesv1.ResourceRequirements{
						CPU:      resourceQuantityPtr("8"),
						Memory:   resourceQuantityPtr("32Gi"),
						GPUCount: int32Ptr(1),
					},
				},
				Prerequisites: []workspacesv1.ServiceTemplateRef{
					{Name: "slurm-operator", VersionConstraint: ">=0.5.0"},
				},
				AccessEndpoints: []workspacesv1.AccessEndpoint{
					{Name: "login", Protocol: workspacesv1.RouteProtocolSSH, Port: 22},
					{Name: "dashboard", Protocol: workspacesv1.RouteProtocolHTTP, Port: 8080},
				},
			},
		}

		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		fetched := &workspacesv1.WorkspaceClass{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "slurm-cluster"}, fetched)).To(Succeed())
		Expect(*fetched.Spec.DefaultResources.NodeCount).To(Equal(int32(3)))
		Expect(fetched.Spec.Prerequisites).To(HaveLen(1))
		Expect(fetched.Spec.AccessEndpoints).To(HaveLen(2))
	})
})

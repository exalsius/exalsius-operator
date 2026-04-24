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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var _ = Describe("WorkspaceDeploymentReconciler", func() {
	const (
		timeout  = 30 * time.Second
		interval = 250 * time.Millisecond
	)

	It("should set phase=Failed when WorkspaceClass does not exist", func() {
		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-missing-class",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "nonexistent-class",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "some-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{
					Username: "testuser",
				},
			},
		}

		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// The controller should reconcile and set phase=Failed
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseFailed))
			// Should have ClassResolved=False condition
			classCondition := findCondition(fetched.Status.Conditions, workspacesv1.ConditionClassResolved)
			g.Expect(classCondition).NotTo(BeNil())
			g.Expect(classCondition.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(classCondition.Reason).To(Equal(workspacesv1.ReasonClassNotFound))
		}, timeout, interval).Should(Succeed())
	})

	It("should apply a service entry to ClusterDeployment when class is resolved", func() {
		// Create WorkspaceClass
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-jupyter-deploy",
			},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName: "Jupyter Notebook",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{
					Name: "jupyter-workspace-1.0.0",
				},
				ResourceShape: workspacesv1.ResourceShapeSingleNode,
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerNode: workspacesv1.ResourceRequirements{
						CPU:    resourceQuantityPtr("2"),
						Memory: resourceQuantityPtr("8Gi"),
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		// Create target ClusterDeployment
		cd := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-colony-cluster-1",
				Namespace: "default",
			},
			Spec: k0rdentv1beta1.ClusterDeploymentSpec{
				Template: "some-template",
			},
		}
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())

		// Create WorkspaceDeployment
		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-deploy-svc",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "test-jupyter-deploy",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "test-colony-cluster-1",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{
					Username: "testuser",
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// The controller should create a ServiceSet
		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-test-colony-cluster-1-test-deploy-svc", Namespace: "default"}, ss)).To(Succeed())
			g.Expect(ss.Spec.Cluster).To(Equal("test-colony-cluster-1"))
			g.Expect(ss.Spec.Services).To(HaveLen(1))
			g.Expect(ss.Spec.Services[0].Name).To(Equal("wsd-test-colony-cluster-1-test-deploy-svc"))
			g.Expect(ss.Spec.Services[0].Template).To(Equal("jupyter-workspace-1.0.0"))
		}, timeout, interval).Should(Succeed())

		// Status should reflect the service entry name
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.ServiceEntryName).To(Equal("wsd-test-colony-cluster-1-test-deploy-svc"))
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
		}, timeout, interval).Should(Succeed())
	})

	It("should skip reconciliation when suspend=true", func() {
		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-suspended",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "nonexistent-class-for-suspend",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "some-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{
					Username: "testuser",
				},
				Suspend: true,
			},
		}

		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// Wait a bit and verify no phase is set — the controller should skip
		Consistently(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			// Phase should remain empty (controller didn't run)
			g.Expect(fetched.Status.Phase).To(BeEmpty())
			// No conditions should be set
			g.Expect(fetched.Status.Conditions).To(BeEmpty())
		}, 2*time.Second, interval).Should(Succeed())
	})

	It("should transition to Running when ClusterDeployment reports service as Deployed", func() {
		// Create WorkspaceClass
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-jupyter-running",
			},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName: "Jupyter Notebook",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{
					Name: "jupyter-workspace-1.0.0",
				},
				ResourceShape: workspacesv1.ResourceShapeSingleNode,
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerNode: workspacesv1.ResourceRequirements{
						CPU: resourceQuantityPtr("2"),
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		// Create target ClusterDeployment
		cd := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-colony-running-cluster",
				Namespace: "default",
			},
			Spec: k0rdentv1beta1.ClusterDeploymentSpec{
				Template: "some-template",
			},
		}
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())

		// Create WorkspaceDeployment
		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-running",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "test-jupyter-running",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "test-colony-running-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{
					Username: "testuser",
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// Wait for it to reach Deploying phase
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
		}, timeout, interval).Should(Succeed())

		// Simulate k0rdent reporting the service as Deployed
		// by updating the ClusterDeployment status using the typed Go client.
		Eventually(func(g Gomega) {
			cdFetched := &k0rdentv1beta1.ClusterDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "test-colony-running-cluster", Namespace: "default"}, cdFetched)).To(Succeed())

			now := metav1.Now()
			cdFetched.Status.Services = []k0rdentv1beta1.ServiceState{
				{
					Name:                    "wsd-test-colony-running-cluster-test-running",
					Namespace:               "default",
					Template:                "jupyter-workspace-1.0.0",
					State:                   k0rdentv1beta1.ServiceStateDeployed,
					Type:                    "Helm",
					LastStateTransitionTime: &now,
				},
			}
			g.Expect(k8sClient.Status().Update(ctx, cdFetched)).To(Succeed())

			// Verify the status was written
			cdVerify := &k0rdentv1beta1.ClusterDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "test-colony-running-cluster", Namespace: "default"}, cdVerify)).To(Succeed())
			g.Expect(cdVerify.Status.Services).To(HaveLen(1))
			g.Expect(cdVerify.Status.Services[0].State).To(Equal(k0rdentv1beta1.ServiceStateDeployed))
		}, timeout, interval).Should(Succeed())

		// The controller should detect the Deployed state and transition to Running
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseRunning))
			// HelmReleaseReady should be True
			helmCondition := findCondition(fetched.Status.Conditions, workspacesv1.ConditionHelmReleaseReady)
			g.Expect(helmCondition).NotTo(BeNil())
			g.Expect(helmCondition.Status).To(Equal(metav1.ConditionTrue))
		}, timeout, interval).Should(Succeed())
	})

	It("should set phase=Pending and resolve class when WorkspaceClass exists", func() {
		// Create the WorkspaceClass first
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-jupyter",
			},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName: "Jupyter Notebook",
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
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		// Create the WorkspaceDeployment
		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-resolved-class",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "test-jupyter",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "some-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{
					Username: "testuser",
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// The controller should resolve the class and add the finalizer
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			// ClassResolved should be True
			classCondition := findCondition(fetched.Status.Conditions, workspacesv1.ConditionClassResolved)
			g.Expect(classCondition).NotTo(BeNil())
			g.Expect(classCondition.Status).To(Equal(metav1.ConditionTrue))
			// Finalizer should be present
			g.Expect(controllerutil.ContainsFinalizer(fetched, workspacesv1.WorkspaceDeploymentFinalizer)).To(BeTrue())
		}, timeout, interval).Should(Succeed())
	})

	It("should remove service entry from ClusterDeployment when WSD is deleted", func() {
		// Create WorkspaceClass
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-jupyter-delete",
			},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName: "Jupyter Notebook",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{
					Name: "jupyter-delete-template",
				},
				ResourceShape: workspacesv1.ResourceShapeSingleNode,
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerNode: workspacesv1.ResourceRequirements{
						CPU: resourceQuantityPtr("2"),
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		// Create ClusterDeployment
		cd := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-delete-cluster",
				Namespace: "default",
			},
			Spec: k0rdentv1beta1.ClusterDeploymentSpec{
				Template: "some-template",
			},
		}
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())

		// Create WSD
		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-delete-wsd",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "test-jupyter-delete",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "test-delete-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{Username: "testuser"},
			},
		}
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// Wait for ServiceSet to be created
		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-test-delete-cluster-test-delete-wsd", Namespace: "default"}, ss)).To(Succeed())
			g.Expect(ss.Spec.Services).To(HaveLen(1))
		}, timeout, interval).Should(Succeed())

		// Delete the WSD
		Expect(k8sClient.Delete(ctx, wsd)).To(Succeed())

		// ServiceSet should be deleted
		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-test-delete-cluster-test-delete-wsd", Namespace: "default"}, ss)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}, timeout, interval).Should(Succeed())

		// WSD should be gone (finalizer removed)
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), &workspacesv1.WorkspaceDeployment{})
			g.Expect(err).To(HaveOccurred())
			g.Expect(client.IgnoreNotFound(err)).To(Succeed())
		}, timeout, interval).Should(Succeed())
	})

	It("should not affect other workspaces when one is deleted from the same ClusterDeployment", func() {
		// Create WorkspaceClass
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-jupyter-multi",
			},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName: "Jupyter Notebook",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{
					Name: "jupyter-multi-template",
				},
				ResourceShape: workspacesv1.ResourceShapeSingleNode,
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerNode: workspacesv1.ResourceRequirements{
						CPU: resourceQuantityPtr("2"),
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		// Create shared ClusterDeployment
		cd := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-multi-cluster",
				Namespace: "default",
			},
			Spec: k0rdentv1beta1.ClusterDeploymentSpec{
				Template: "some-template",
			},
		}
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())

		// Create two WSDs on the same cluster
		wsdAlice := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "alice-nb",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "test-jupyter-multi",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "test-multi-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{Username: "alice"},
			},
		}
		wsdBob := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bob-nb",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "test-jupyter-multi",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "test-multi-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{Username: "bob"},
			},
		}
		Expect(k8sClient.Create(ctx, wsdAlice)).To(Succeed())
		Expect(k8sClient.Create(ctx, wsdBob)).To(Succeed())

		// Wait for both ServiceSets to be created
		Eventually(func(g Gomega) {
			ssAlice := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-test-multi-cluster-alice-nb", Namespace: "default"}, ssAlice)).To(Succeed())
			ssBob := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-test-multi-cluster-bob-nb", Namespace: "default"}, ssBob)).To(Succeed())
		}, timeout, interval).Should(Succeed())

		// Delete Alice's workspace
		Expect(k8sClient.Delete(ctx, wsdAlice)).To(Succeed())

		// Alice's ServiceSet should be deleted, Bob's should remain
		Eventually(func(g Gomega) {
			ssAlice := &k0rdentv1beta1.ServiceSet{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-test-multi-cluster-alice-nb", Namespace: "default"}, ssAlice)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

			ssBob := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-test-multi-cluster-bob-nb", Namespace: "default"}, ssBob)).To(Succeed())
			g.Expect(ssBob.Spec.Services).To(HaveLen(1))
		}, timeout, interval).Should(Succeed())
	})

	It("should transition to Failed when ClusterDeployment reports service as Failed", func() {
		// Create WorkspaceClass
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-jupyter-fail",
			},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName: "Jupyter Notebook",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{
					Name: "jupyter-fail-template",
				},
				ResourceShape: workspacesv1.ResourceShapeSingleNode,
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerNode: workspacesv1.ResourceRequirements{
						CPU: resourceQuantityPtr("2"),
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		cd := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-fail-cluster",
				Namespace: "default",
			},
			Spec: k0rdentv1beta1.ClusterDeploymentSpec{
				Template: "some-template",
			},
		}
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())

		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-fail-wsd",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "test-jupyter-fail",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "test-fail-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{Username: "testuser"},
			},
		}
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// Wait for Deploying phase
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
		}, timeout, interval).Should(Succeed())

		// Simulate k0rdent reporting the service as Failed
		Eventually(func(g Gomega) {
			cdFetched := &k0rdentv1beta1.ClusterDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "test-fail-cluster", Namespace: "default"}, cdFetched)).To(Succeed())
			now := metav1.Now()
			cdFetched.Status.Services = []k0rdentv1beta1.ServiceState{
				{
					Name:                    "wsd-test-fail-cluster-test-fail-wsd",
					Namespace:               "default",
					Template:                "jupyter-fail-template",
					State:                   k0rdentv1beta1.ServiceStateFailed,
					FailureMessage:          "context deadline exceeded",
					Type:                    "Helm",
					LastStateTransitionTime: &now,
				},
			}
			g.Expect(k8sClient.Status().Update(ctx, cdFetched)).To(Succeed())
		}, timeout, interval).Should(Succeed())

		// Controller should transition to Failed
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseFailed))
			g.Expect(fetched.Status.Message).To(ContainSubstring("context deadline exceeded"))
			helmCondition := findCondition(fetched.Status.Conditions, workspacesv1.ConditionHelmReleaseReady)
			g.Expect(helmCondition).NotTo(BeNil())
			g.Expect(helmCondition.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(helmCondition.Reason).To(Equal(workspacesv1.ReasonHelmReleaseFailed))
		}, timeout, interval).Should(Succeed())
	})

	It("should fail when prerequisite is missing from ClusterDeployment", func() {
		// Create WorkspaceClass with a prerequisite
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-llm-prereq",
			},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName: "LLM Inference",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{
					Name: "llm-template",
				},
				ResourceShape: workspacesv1.ResourceShapeSingleNode,
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerNode: workspacesv1.ResourceRequirements{
						CPU: resourceQuantityPtr("4"),
					},
				},
				Prerequisites: []workspacesv1.ServiceTemplateRef{
					{Name: "llm-d-stack"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		// Create ClusterDeployment WITHOUT the prerequisite installed
		cd := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-prereq-cluster",
				Namespace: "default",
			},
			Spec: k0rdentv1beta1.ClusterDeploymentSpec{
				Template: "some-template",
			},
		}
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())

		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-prereq-wsd",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "test-llm-prereq",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "test-prereq-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{Username: "testuser"},
			},
		}
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// Should fail with PrerequisitesMet=False
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseFailed))
			g.Expect(fetched.Status.Message).To(ContainSubstring("llm-d-stack"))
			prereqCondition := findCondition(fetched.Status.Conditions, workspacesv1.ConditionPrerequisitesMet)
			g.Expect(prereqCondition).NotTo(BeNil())
			g.Expect(prereqCondition.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(prereqCondition.Reason).To(Equal(workspacesv1.ReasonPrerequisitesNotReady))
		}, timeout, interval).Should(Succeed())
	})

	It("should deploy two workspaces on the same cluster in parallel", func() {
		// Create WorkspaceClass
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-jupyter-parallel",
			},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName: "Jupyter Notebook",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{
					Name: "jupyter-parallel-template",
				},
				ResourceShape: workspacesv1.ResourceShapeSingleNode,
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerNode: workspacesv1.ResourceRequirements{
						CPU: resourceQuantityPtr("2"),
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		cd := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-parallel-cluster",
				Namespace: "default",
			},
			Spec: k0rdentv1beta1.ClusterDeploymentSpec{
				Template: "some-template",
			},
		}
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())

		// Create both WSDs at the same time
		wsd1 := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "parallel-1",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "test-jupyter-parallel",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "test-parallel-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{Username: "user1"},
			},
		}
		wsd2 := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "parallel-2",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "test-jupyter-parallel",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "test-parallel-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{Username: "user2"},
			},
		}
		Expect(k8sClient.Create(ctx, wsd1)).To(Succeed())
		Expect(k8sClient.Create(ctx, wsd2)).To(Succeed())

		// Both ServiceSets should be created independently
		// without waiting for either workspace to reach Running first.
		// This proves they deploy in parallel, not sequentially.
		Eventually(func(g Gomega) {
			ss1 := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-test-parallel-cluster-parallel-1", Namespace: "default"}, ss1)).To(Succeed())
			ss2 := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-test-parallel-cluster-parallel-2", Namespace: "default"}, ss2)).To(Succeed())
			// Both should reference the same cluster
			g.Expect(ss1.Spec.Cluster).To(Equal("test-parallel-cluster"))
			g.Expect(ss2.Spec.Cluster).To(Equal("test-parallel-cluster"))
		}, timeout, interval).Should(Succeed())

		// Both should be in Deploying (not one blocked waiting for the other)
		Eventually(func(g Gomega) {
			fetched1 := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd1), fetched1)).To(Succeed())
			g.Expect(fetched1.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))

			fetched2 := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd2), fetched2)).To(Succeed())
			g.Expect(fetched2.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
		}, timeout, interval).Should(Succeed())
	})

	It("should cleanly remove both workspaces when deleted concurrently", func() {
		// Create WorkspaceClass
		wsc := &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-jupyter-concurrent-del",
			},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName: "Jupyter Notebook",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{
					Name: "jupyter-concurrent-template",
				},
				ResourceShape: workspacesv1.ResourceShapeSingleNode,
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerNode: workspacesv1.ResourceRequirements{
						CPU: resourceQuantityPtr("2"),
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		cd := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-concurrent-del-cluster",
				Namespace: "default",
			},
			Spec: k0rdentv1beta1.ClusterDeploymentSpec{
				Template: "some-template",
			},
		}
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())

		// Create two WSDs
		wsdA := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "concurrent-del-a",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "test-jupyter-concurrent-del",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "test-concurrent-del-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{Username: "userA"},
			},
		}
		wsdB := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "concurrent-del-b",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "test-jupyter-concurrent-del",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "test-concurrent-del-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{Username: "userB"},
			},
		}
		Expect(k8sClient.Create(ctx, wsdA)).To(Succeed())
		Expect(k8sClient.Create(ctx, wsdB)).To(Succeed())

		// Wait for both ServiceSets to be created
		Eventually(func(g Gomega) {
			ssA := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-test-concurrent-del-cluster-concurrent-del-a", Namespace: "default"}, ssA)).To(Succeed())
			ssB := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-test-concurrent-del-cluster-concurrent-del-b", Namespace: "default"}, ssB)).To(Succeed())
		}, timeout, interval).Should(Succeed())

		// Delete both at the same time
		Expect(k8sClient.Delete(ctx, wsdA)).To(Succeed())
		Expect(k8sClient.Delete(ctx, wsdB)).To(Succeed())

		// Both ServiceSets should be deleted
		Eventually(func(g Gomega) {
			ssA := &k0rdentv1beta1.ServiceSet{}
			errA := k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-test-concurrent-del-cluster-concurrent-del-a", Namespace: "default"}, ssA)
			g.Expect(apierrors.IsNotFound(errA)).To(BeTrue())
			ssB := &k0rdentv1beta1.ServiceSet{}
			errB := k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-test-concurrent-del-cluster-concurrent-del-b", Namespace: "default"}, ssB)
			g.Expect(apierrors.IsNotFound(errB)).To(BeTrue())
		}, timeout, interval).Should(Succeed())

		// Both WSDs should be gone
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(wsdA), &workspacesv1.WorkspaceDeployment{})
			g.Expect(client.IgnoreNotFound(err)).To(Succeed())
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}, timeout, interval).Should(Succeed())
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(wsdB), &workspacesv1.WorkspaceDeployment{})
			g.Expect(client.IgnoreNotFound(err)).To(Succeed())
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}, timeout, interval).Should(Succeed())
	})

	It("should not re-reconcile when phase is Failed", func() {
		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-failed-stable",
				Namespace: "default",
			},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef: "nonexistent-class-stable",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{
					Name:      "some-cluster",
					Namespace: "default",
				},
				Owner: workspacesv1.OwnerInfo{Username: "testuser"},
			},
		}
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// Wait for Failed
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseFailed))
		}, timeout, interval).Should(Succeed())

		// Get the resource version after failure
		failed := &workspacesv1.WorkspaceDeployment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), failed)).To(Succeed())
		rvAfterFail := failed.ResourceVersion

		// Wait and verify it stays stable — no further reconciles should change it
		Consistently(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseFailed))
			g.Expect(fetched.ResourceVersion).To(Equal(rvAfterFail))
		}, 3*time.Second, interval).Should(Succeed())
	})
})

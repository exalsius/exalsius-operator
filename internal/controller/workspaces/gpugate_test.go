package workspaces

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	"github.com/exalsius/exalsius-operator/internal/gpu"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// createGPUNode registers a schedulable node carrying `count` NVIDIA GPUs
// labelled with the canonical model, in the (shared) envtest cluster that the
// child kubeconfig points at. Cleaned up after the spec. Node status is set via
// the status subresource.
func createGPUNode(name, model string, count int64) {
	GinkgoHelper()
	createGPUNodeRes(name, model, gpu.ResourceNvidiaGPU, count)
}

// createGPUNodeRes registers a schedulable node advertising `count` GPUs of the
// given extended resource (NVIDIA or AMD), labelled with the canonical model.
func createGPUNodeRes(name, model, resourceName string, count int64) {
	GinkgoHelper()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{gpu.DefaultModelLabel: model},
		},
	}
	Expect(k8sClient.Create(ctx, node)).To(Succeed())
	node.Status = corev1.NodeStatus{
		Allocatable: corev1.ResourceList{
			corev1.ResourceName(resourceName): *resource.NewQuantity(count, resource.DecimalSI),
		},
		Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
	}
	Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())
	DeferCleanup(func() {
		_ = k8sClient.Delete(ctx, node)
	})
}

var _ = Describe("WorkspaceDeployment GPU offering gate", func() {
	const (
		timeout  = 30 * time.Second
		interval = 250 * time.Millisecond
	)

	gpuClass := func(name string) *workspacesv1.WorkspaceClass {
		return &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName:     "GPU Workspace",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{Name: "gpu-workspace-1.0.0"},
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerReplica: workspacesv1.ResourceRequirements{
						CPU:    resourceQuantityPtr("2"),
						Memory: resourceQuantityPtr("8Gi"),
					},
				},
			},
		}
	}

	gpuWSD := func(name, class, cd, model string) *workspacesv1.WorkspaceDeployment {
		return &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef:    class,
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{Name: cd, Namespace: "default"},
				Resources: &workspacesv1.WorkspaceResourceSpec{
					PerReplica: workspacesv1.ResourceRequirements{GPUType: &model},
				},
			},
		}
	}

	mkCD := func(name string) {
		GinkgoHelper()
		cd := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       k0rdentv1beta1.ClusterDeploymentSpec{Template: "some-template"},
		}
		Expect(k8sClient.Create(ctx, cd)).To(Succeed())
		ensureChildKubeconfigSecret(name, "default")
	}

	It("fails terminally when the requested GPU model is absent from the cluster", func() {
		Expect(k8sClient.Create(ctx, gpuClass("gpugate-absent-class"))).To(Succeed())
		mkCD("gpugate-absent-cd")

		// A model no node will ever carry → static infeasibility.
		wsd := gpuWSD("gpugate-absent", "gpugate-absent-class", "gpugate-absent-cd", "GATETEST-NOSUCHGPU")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseFailed))
			g.Expect(fetched.Status.FailureContext).NotTo(BeNil())
			g.Expect(fetched.Status.FailureContext.Reason).To(Equal(workspacesv1.ReasonGpuOfferingUnavailable))
			g.Expect(fetched.Status.Message).To(ContainSubstring("GATETEST-NOSUCHGPU"))
		}, timeout, interval).Should(Succeed())

		// Terminal: a ServiceSet must never be created for a failed gate.
		Consistently(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-gpugate-absent-cd-gpugate-absent", Namespace: "default"}, ss)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}, 2*time.Second, interval).Should(Succeed())
	})

	It("proceeds to deploy when the requested GPU model is present", func() {
		Expect(k8sClient.Create(ctx, gpuClass("gpugate-present-class"))).To(Succeed())
		mkCD("gpugate-present-cd")
		createGPUNode("gpugate-present-node", "GATETEST-PRESENTGPU", 4)

		wsd := gpuWSD("gpugate-present", "gpugate-present-class", "gpugate-present-cd", "GATETEST-PRESENTGPU")
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
		}, timeout, interval).Should(Succeed())
	})

	It("holds in Waiting when the model exists but is full, then proceeds once it frees", func() {
		Expect(k8sClient.Create(ctx, gpuClass("gpugate-cap-class"))).To(Succeed())
		mkCD("gpugate-cap-cd")
		createGPUNode("gpugate-cap-node", "GATETEST-CAPGPU", 1)
		// Occupy the single GPU so none are free.
		occupant := occupyGPU("gpugate-cap-occupant", "gpugate-cap-node", 1)

		// Request 1 GPU of the (now full) model.
		one := int32(1)
		model := "GATETEST-CAPGPU"
		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "gpugate-cap", Namespace: "default"},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef:    "gpugate-cap-class",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{Name: "gpugate-cap-cd", Namespace: "default"},
				Resources: &workspacesv1.WorkspaceResourceSpec{
					PerReplica: workspacesv1.ResourceRequirements{GPUType: &model, GPUCount: &one},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// Held in Waiting (no ServiceSet) while the GPU is occupied.
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseWaiting))
			g.Expect(fetched.Status.Message).To(ContainSubstring("GATETEST-CAPGPU"))
		}, timeout, interval).Should(Succeed())
		Consistently(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-gpugate-cap-cd-gpugate-cap", Namespace: "default"}, ss)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}, 2*time.Second, interval).Should(Succeed())

		// Free the GPU — the workspace should leave Waiting and deploy.
		// Force-delete (grace 0): envtest has no kubelet, so a graceful delete
		// would leave the pod Terminating and still counted as occupying.
		Expect(k8sClient.Delete(ctx, occupant, client.GracePeriodSeconds(0))).To(Succeed())
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
		}, timeout, interval).Should(Succeed())
	})

	It("deploys an AMD workspace, injecting the AMD GPU resource and selector", func() {
		Expect(k8sClient.Create(ctx, gpuClass("gpugate-amd-class"))).To(Succeed())
		mkCD("gpugate-amd-cd")
		createGPUNodeRes("gpugate-amd-node", "GATETEST-AMDGPU", gpu.ResourceAMDGPU, 4)

		one := int32(1)
		model := "GATETEST-AMDGPU"
		wsd := &workspacesv1.WorkspaceDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "gpugate-amd", Namespace: "default"},
			Spec: workspacesv1.WorkspaceDeploymentSpec{
				WorkspaceClassRef:    "gpugate-amd-class",
				ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{Name: "gpugate-amd-cd", Namespace: "default"},
				Resources: &workspacesv1.WorkspaceResourceSpec{
					PerReplica: workspacesv1.ResourceRequirements{GPUType: &model, GPUCount: &one},
				},
			},
		}
		Expect(k8sClient.Create(ctx, wsd)).To(Succeed())

		// Proceeds to deploy, and the vendor is inferred as AMD.
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsd), fetched)).To(Succeed())
			g.Expect(fetched.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
			g.Expect(fetched.Status.ResolvedResources).NotTo(BeNil())
			g.Expect(fetched.Status.ResolvedResources.PerReplica.GPUVendor).NotTo(BeNil())
			g.Expect(*fetched.Status.ResolvedResources.PerReplica.GPUVendor).To(Equal(workspacesv1.GPUVendorAMD))
		}, timeout, interval).Should(Succeed())

		// The ServiceSet's Helm values carry the AMD resource name + node selector.
		Eventually(func(g Gomega) {
			ss := &k0rdentv1beta1.ServiceSet{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "wsd-gpugate-amd-cd-gpugate-amd", Namespace: "default"}, ss)).To(Succeed())
			g.Expect(ss.Spec.Services).To(HaveLen(1))
			values := ss.Spec.Services[0].Values
			g.Expect(values).To(ContainSubstring("gpuResourceName"))
			g.Expect(values).To(ContainSubstring(gpu.ResourceAMDGPU))
			g.Expect(values).To(ContainSubstring(gpu.DefaultModelLabel))
		}, timeout, interval).Should(Succeed())
	})

	It("serves Waiting workspaces oldest-first when capacity frees", func() {
		Expect(k8sClient.Create(ctx, gpuClass("gpugate-fcfs-class"))).To(Succeed())
		mkCD("gpugate-fcfs-cd")
		createGPUNode("gpugate-fcfs-node", "GATETEST-FCFSGPU", 1)
		occupant := occupyGPU("gpugate-fcfs-occupant", "gpugate-fcfs-node", 1) // 0 free

		one := int32(1)
		model := "GATETEST-FCFSGPU"
		mkWSD := func(name string) *workspacesv1.WorkspaceDeployment {
			return &workspacesv1.WorkspaceDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: workspacesv1.WorkspaceDeploymentSpec{
					WorkspaceClassRef:    "gpugate-fcfs-class",
					ClusterDeploymentRef: workspacesv1.ClusterDeploymentRef{Name: "gpugate-fcfs-cd", Namespace: "default"},
					Resources: &workspacesv1.WorkspaceResourceSpec{
						PerReplica: workspacesv1.ResourceRequirements{GPUType: &model, GPUCount: &one},
					},
				},
			}
		}

		older := mkWSD("gpugate-fcfs-older")
		Expect(k8sClient.Create(ctx, older)).To(Succeed())
		Eventually(func(g Gomega) {
			f := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(older), f)).To(Succeed())
			g.Expect(f.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseWaiting))
		}, timeout, interval).Should(Succeed())

		// Guarantee a strictly later creation second for the younger one.
		time.Sleep(1100 * time.Millisecond)
		younger := mkWSD("gpugate-fcfs-younger")
		Expect(k8sClient.Create(ctx, younger)).To(Succeed())
		Eventually(func(g Gomega) {
			f := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(younger), f)).To(Succeed())
			g.Expect(f.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseWaiting))
		}, timeout, interval).Should(Succeed())

		// Free the single GPU — FCFS dictates the older workspace proceeds.
		Expect(k8sClient.Delete(ctx, occupant, client.GracePeriodSeconds(0))).To(Succeed())
		Eventually(func(g Gomega) {
			f := &workspacesv1.WorkspaceDeployment{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(older), f)).To(Succeed())
			g.Expect(f.Status.Phase).To(Equal(workspacesv1.WorkspaceDeploymentPhaseDeploying))
		}, timeout, interval).Should(Succeed())
	})
})

// occupyGPU creates a pod bound to nodeName that requests `count` NVIDIA GPUs,
// so AvailableForModel counts them as used. Returns the pod for explicit
// deletion mid-spec; a DeferCleanup is registered as a safety net.
func occupyGPU(name, nodeName string, count int64) *corev1.Pod {
	GinkgoHelper()
	q := *resource.NewQuantity(count, resource.DecimalSI)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
			Containers: []corev1.Container{{
				Name:  "c",
				Image: "busybox",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceName(gpu.ResourceNvidiaGPU): q},
					Limits:   corev1.ResourceList{corev1.ResourceName(gpu.ResourceNvidiaGPU): q},
				},
			}},
		},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	DeferCleanup(func() { _ = k8sClient.Delete(ctx, pod) })
	return pod
}

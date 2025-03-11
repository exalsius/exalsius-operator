/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package training

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	volcanoalpha1 "volcano.sh/apis/pkg/apis/batch/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	trainingv1 "github.com/exalsius/exalsius-operator/api/training/v1"
)

var _ = Describe("DilocoTorchDDP Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		dilocotorchddp := &trainingv1.DilocoTorchDDP{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind DilocoTorchDDP")
			err := k8sClient.Get(ctx, typeNamespacedName, dilocotorchddp)
			if err != nil && errors.IsNotFound(err) {
				resource := &trainingv1.DilocoTorchDDP{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: trainingv1.DilocoTorchDDPSpec{
						GPUTypes:     []string{"A100"},
						Parallelism:  2,
						NProcPerNode: 1,
						Image:        "test/test-image:latest",
						ScriptPath:   "/app/test-script.py",
						WandBAPIKey:  "xxxxxxx",
						Args:         []string{"--epochs", "10"},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &trainingv1.DilocoTorchDDP{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())
			By("Cleanup the specific resource instance DilocoTorchDDP")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource and create a volcano job", func() {
			By("Reconciling the created resource")
			controllerReconciler := &DilocoTorchDDPReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Ensure that a Volcano Job is created
			job := &volcanoalpha1.Job{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "diloco-job-" + resourceName, Namespace: "default"}, job)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.Spec.MinAvailable).To(Equal(int32(2))) // Should match Parallelism
			Expect(job.Spec.SchedulerName).To(Equal("volcano"))
		})

		It("should update the CR status when the volcano job status changes", func() {
			By("Reconciling the created resource")
			controllerReconciler := &DilocoTorchDDPReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Simulate a running volcano job
			job := &volcanoalpha1.Job{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "diloco-job-" + resourceName, Namespace: "default"}, job)
			Expect(err).NotTo(HaveOccurred())

			job.Status.State.Phase = volcanoalpha1.Running
			Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

			// Reconcile the resource again to update the CR status
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			updatedDilocoTorchDDPJob := &trainingv1.DilocoTorchDDP{}
			err = k8sClient.Get(ctx, typeNamespacedName, updatedDilocoTorchDDPJob)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedDilocoTorchDDPJob.Status.Phase).To(Equal(trainingv1.JobPhase(volcanoalpha1.Running)))
		})

		It("should not create another TorchDDP Job if one already exists", func() {
			By("Creating a second TorchDDP resource")
			dilocoTraining := &trainingv1.DilocoTorchDDP{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: trainingv1.DilocoTorchDDPSpec{
					Image:       "test-image",
					ScriptPath:  "test.py",
					Parallelism: 2,
				},
			}
			err := k8sClient.Create(ctx, dilocoTraining)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsAlreadyExists(err)).To(BeTrue())
		})

		It("should return an error when a non-existing TargetCluster is used", func() {
			By("Creating a DilocoTorchDDP with a missing TargetCluster")
			resource := &trainingv1.DilocoTorchDDP{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "missing-cluster-resource",
					Namespace: "default",
				},
				Spec: trainingv1.DilocoTorchDDPSpec{
					TargetCluster: pointerTo("non-existent-cluster"),
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &DilocoTorchDDPReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "missing-cluster-resource", Namespace: "default"},
			})
			Expect(err).To(HaveOccurred()) // Expect an error due to missing cluster
		})

	})
})

// Helper function for optional TargetCluster values
func pointerTo(s string) *string {
	return &s
}

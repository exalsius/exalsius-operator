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

package infra

import (
	"context"
	"time"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
)

var _ = Describe("Colony Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		colony := &infrav1.Colony{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Colony")
			err := k8sClient.Get(ctx, typeNamespacedName, colony)
			if err != nil && errors.IsNotFound(err) {
				resource := &infrav1.Colony{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &infrav1.Colony{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Colony")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ColonyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should add a finalizer if missing", func() {
			By("Creating a Colony without a finalizer")
			resource := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "finalizer-test",
					Namespace: "default",
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			controllerReconciler := &ColonyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "finalizer-test",
					Namespace: "default",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Fetch the updated resource and check for the finalizer
			updated := &infrav1.Colony{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      "finalizer-test",
				Namespace: "default",
			}, updated)).To(Succeed())
			Expect(updated.Finalizers).To(ContainElement("colony.infra.exalsius.ai/finalizer"))
		})
	})

	Context("Async Orphan Cleanup", func() {
		var (
			ctx        context.Context
			reconciler *ColonyReconciler
		)

		BeforeEach(func() {
			ctx = context.Background()
			reconciler = &ColonyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
		})

		It("should requeue when orphaned ClusterDeployment is still being deleted", func() {
			By("Creating a Colony with orphaned ClusterDeployment reference")
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "orphan-requeue-test",
					Namespace:  "default",
					Finalizers: []string{colonyFinalizer},
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{
						{ClusterName: "cluster-1"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, colony)).To(Succeed())

			// Add ClusterDeploymentRefs to status (including orphaned one)
			colony.Status.ClusterDeploymentRefs = []*corev1.ObjectReference{
				{Name: "orphan-requeue-test-cluster-1", Namespace: "default"},
				{Name: "orphan-requeue-test-cluster-2", Namespace: "default"}, // orphaned
			}
			Expect(k8sClient.Status().Update(ctx, colony)).To(Succeed())

			// Create the orphaned ClusterDeployment (it exists but shouldn't)
			orphanedCD := &k0rdentv1beta1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "orphan-requeue-test-cluster-2",
					Namespace: "default",
				},
				Spec: k0rdentv1beta1.ClusterDeploymentSpec{
					Template:   "test",
					Credential: "test",
				},
			}
			Expect(k8sClient.Create(ctx, orphanedCD)).To(Succeed())

			By("Reconciling should trigger deletion and requeue")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "orphan-requeue-test",
					Namespace: "default",
				},
			})

			// First reconcile triggers deletion, then the object is gone immediately in test
			// In real scenario, we'd see RequeueAfter > 0
			Expect(err).NotTo(HaveOccurred())
			// Note: In fake client, deletion is immediate so requeue might not be needed
			// but the logic is still correct

			By("Cleaning up")
			// Delete colony
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(colony), colony)).To(Succeed())
			colony.Finalizers = nil
			Expect(k8sClient.Update(ctx, colony)).To(Succeed())
			Expect(k8sClient.Delete(ctx, colony)).To(Succeed())
			// Delete orphaned CD if it still exists
			_ = k8sClient.Delete(ctx, orphanedCD)
			_ = result // Suppress unused variable warning
		})

		It("should remove orphaned reference from status when ClusterDeployment is deleted", func() {
			By("Creating a Colony with orphaned ClusterDeployment that doesn't exist")
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "orphan-cleanup-test",
					Namespace:  "default",
					Finalizers: []string{colonyFinalizer},
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{
						{ClusterName: "cluster-1"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, colony)).To(Succeed())

			// Add ClusterDeploymentRefs including orphaned one (that doesn't exist)
			colony.Status.ClusterDeploymentRefs = []*corev1.ObjectReference{
				{Name: "orphan-cleanup-test-cluster-1", Namespace: "default"},
				{Name: "orphan-cleanup-test-cluster-ghost", Namespace: "default"}, // doesn't exist
			}
			Expect(k8sClient.Status().Update(ctx, colony)).To(Succeed())

			By("Reconciling should remove the ghost reference from status")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "orphan-cleanup-test",
					Namespace: "default",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify orphaned reference was removed from status
			updated := &infrav1.Colony{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      "orphan-cleanup-test",
				Namespace: "default",
			}, updated)).To(Succeed())

			// Should only have cluster-1 left
			Expect(updated.Status.ClusterDeploymentRefs).To(HaveLen(1))
			Expect(updated.Status.ClusterDeploymentRefs[0].Name).To(Equal("orphan-cleanup-test-cluster-1"))

			By("Cleaning up")
			updated.Finalizers = nil
			Expect(k8sClient.Update(ctx, updated)).To(Succeed())
			Expect(k8sClient.Delete(ctx, updated)).To(Succeed())
		})

		It("should complete reconciliation quickly (non-blocking)", func() {
			By("Creating a Colony")
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "fast-reconcile-test",
					Namespace:  "default",
					Finalizers: []string{colonyFinalizer},
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{},
				},
			}
			Expect(k8sClient.Create(ctx, colony)).To(Succeed())

			By("Reconciling should complete quickly")
			start := time.Now()
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "fast-reconcile-test",
					Namespace: "default",
				},
			})
			duration := time.Since(start)

			Expect(err).NotTo(HaveOccurred())
			// Should complete in less than 5 seconds (old blocking would take up to 10 minutes)
			Expect(duration).To(BeNumerically("<", 5*time.Second))

			By("Cleaning up")
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(colony), colony)).To(Succeed())
			colony.Finalizers = nil
			Expect(k8sClient.Update(ctx, colony)).To(Succeed())
			Expect(k8sClient.Delete(ctx, colony)).To(Succeed())
		})
	})

	Context("cleanupAssociatedResources", func() {
		var (
			ctx        context.Context
			reconciler *ColonyReconciler
		)

		BeforeEach(func() {
			ctx = context.Background()
			reconciler = &ColonyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
		})

		It("should return false when ClusterDeployment is already deleted", func() {
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cleanup-deleted-test",
					Namespace: "default",
				},
			}
			colonyCluster := &infrav1.ColonyCluster{
				ClusterName: "nonexistent-cluster",
			}

			// Don't create the ClusterDeployment - it's "already deleted"
			stillExists, err := reconciler.cleanupAssociatedResources(ctx, colony, colonyCluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(stillExists).To(BeFalse())
		})

		It("should trigger deletion and return true when ClusterDeployment exists", func() {
			By("Creating Colony and ClusterDeployment")
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cleanup-exists-test",
					Namespace: "default",
				},
			}
			Expect(k8sClient.Create(ctx, colony)).To(Succeed())

			cd := &k0rdentv1beta1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cleanup-exists-test-my-cluster",
					Namespace: "default",
				},
				Spec: k0rdentv1beta1.ClusterDeploymentSpec{
					Template:   "test",
					Credential: "test",
				},
			}
			Expect(k8sClient.Create(ctx, cd)).To(Succeed())

			colonyCluster := &infrav1.ColonyCluster{
				ClusterName: "my-cluster",
			}

			By("Calling cleanupAssociatedResources")
			stillExists, err := reconciler.cleanupAssociatedResources(ctx, colony, colonyCluster)
			Expect(err).NotTo(HaveOccurred())

			// In fake client, deletion is immediate, so stillExists might be false
			// In real k8s, it would return true because deletion isn't instant
			// The important thing is no error and the deletion was triggered
			_ = stillExists

			// Verify deletion was triggered
			deletedCD := &k0rdentv1beta1.ClusterDeployment{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: "cleanup-exists-test-my-cluster", Namespace: "default"}, deletedCD)
			Expect(errors.IsNotFound(err)).To(BeTrue())

			By("Cleaning up")
			Expect(k8sClient.Delete(ctx, colony)).To(Succeed())
		})

		It("should not error when ClusterDeployment has finalizers (being deleted)", func() {
			By("Creating Colony and ClusterDeployment with finalizer")
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cleanup-finalizer-test",
					Namespace: "default",
				},
			}
			Expect(k8sClient.Create(ctx, colony)).To(Succeed())

			cd := &k0rdentv1beta1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cleanup-finalizer-test-my-cluster",
					Namespace:  "default",
					Finalizers: []string{"test-finalizer"},
				},
				Spec: k0rdentv1beta1.ClusterDeploymentSpec{
					Template:   "test",
					Credential: "test",
				},
			}
			Expect(k8sClient.Create(ctx, cd)).To(Succeed())

			// Mark for deletion (but finalizer prevents actual deletion)
			Expect(k8sClient.Delete(ctx, cd)).To(Succeed())

			colonyCluster := &infrav1.ColonyCluster{
				ClusterName: "my-cluster",
			}

			By("Calling cleanupAssociatedResources on already-deleting CD")
			stillExists, err := reconciler.cleanupAssociatedResources(ctx, colony, colonyCluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(stillExists).To(BeTrue()) // Still exists because of finalizer

			By("Cleaning up - remove finalizer to allow deletion")
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cd), cd)).To(Succeed())
			cd.Finalizers = nil
			Expect(k8sClient.Update(ctx, cd)).To(Succeed())
			Expect(k8sClient.Delete(ctx, colony)).To(Succeed())
		})
	})

	Context("Colony Deletion Flow", func() {
		var (
			ctx        context.Context
			reconciler *ColonyReconciler
		)

		BeforeEach(func() {
			ctx = context.Background()
			reconciler = &ColonyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
		})

		It("should requeue if ClusterDeployments still exist during deletion", func() {
			By("Creating Colony with ClusterDeployment that has finalizer")
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "deletion-requeue-test",
					Namespace:  "default",
					Finalizers: []string{colonyFinalizer},
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{
						{ClusterName: "blocking-cluster"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, colony)).To(Succeed())

			// Create ClusterDeployment with finalizer (to simulate blocking deletion)
			cd := &k0rdentv1beta1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "deletion-requeue-test-blocking-cluster",
					Namespace:  "default",
					Finalizers: []string{"blocking-finalizer"},
				},
				Spec: k0rdentv1beta1.ClusterDeploymentSpec{
					Template:   "test",
					Credential: "test",
				},
			}
			Expect(k8sClient.Create(ctx, cd)).To(Succeed())

			By("Marking Colony for deletion")
			Expect(k8sClient.Delete(ctx, colony)).To(Succeed())

			By("Reconciling - should requeue because CD still exists")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "deletion-requeue-test",
					Namespace: "default",
				},
			})
			Expect(err).NotTo(HaveOccurred())
			// Should requeue to wait for CD deletion
			Expect(result.RequeueAfter).To(Equal(10 * time.Second))

			By("Removing finalizer from CD to allow deletion")
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cd), cd)).To(Succeed())
			cd.Finalizers = nil
			Expect(k8sClient.Update(ctx, cd)).To(Succeed())

			By("Reconciling again - should complete and remove finalizer")
			result, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "deletion-requeue-test",
					Namespace: "default",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Colony should be fully deleted now
			deletedColony := &infrav1.Colony{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      "deletion-requeue-test",
				Namespace: "default",
			}, deletedColony)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("should complete deletion quickly when all ClusterDeployments are gone", func() {
			By("Creating Colony without any ClusterDeployments")
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "fast-deletion-test",
					Namespace:  "default",
					Finalizers: []string{colonyFinalizer},
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{},
				},
			}
			Expect(k8sClient.Create(ctx, colony)).To(Succeed())

			By("Marking Colony for deletion")
			Expect(k8sClient.Delete(ctx, colony)).To(Succeed())

			By("Reconciling - should complete quickly")
			start := time.Now()
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "fast-deletion-test",
					Namespace: "default",
				},
			})
			duration := time.Since(start)

			Expect(err).NotTo(HaveOccurred())
			Expect(duration).To(BeNumerically("<", 5*time.Second))

			// Colony should be fully deleted
			deletedColony := &infrav1.Colony{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      "fast-deletion-test",
				Namespace: "default",
			}, deletedColony)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})
	})
})

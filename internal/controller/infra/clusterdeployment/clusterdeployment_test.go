package clusterdeployment

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("EnsureClusterDeployment", func() {
	var (
		scheme        *runtime.Scheme
		colony        *infrav1.Colony
		colonyCluster *infrav1.ColonyCluster
		ctx           context.Context
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		_ = k0rdentv1beta1.AddToScheme(scheme)
		_ = infrav1.AddToScheme(scheme)
		_ = corev1.AddToScheme(scheme)

		colonyCluster = &infrav1.ColonyCluster{
			ClusterName: "test-cluster",
			ClusterLabels: map[string]string{
				"environment": "test",
				"team":        "platform",
			},
		}

		spec := &k0rdentv1beta1.ClusterDeploymentSpec{
			Template:   "test-template",
			Credential: "test-credential",
			Config: &apiextensionsv1.JSON{
				Raw: []byte(`{"test": "test"}`),
			},
		}
		Expect(colonyCluster.SetClusterDeploymentSpec(spec)).To(Succeed())

		colony = &infrav1.Colony{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-colony",
				Namespace: "default",
			},
			Spec: infrav1.ColonySpec{
				ColonyClusters: []infrav1.ColonyCluster{
					*colonyCluster,
				},
			},
			Status: infrav1.ColonyStatus{},
		}

		colony.SetGroupVersionKind(infrav1.GroupVersion.WithKind("Colony"))
		ctx = context.TODO()
	})

	It("should create a new ClusterDeployment and update colony status", func() {
		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&infrav1.Colony{}).
			WithObjects(colony).
			Build()

		err := EnsureClusterDeployment(ctx, c, colony, colonyCluster, scheme)
		Expect(err).NotTo(HaveOccurred())

		// Check that the ClusterDeployment was created
		cd := &k0rdentv1beta1.ClusterDeployment{}
		err = c.Get(ctx, client.ObjectKey{Name: "test-colony-test-cluster", Namespace: "default"}, cd)
		Expect(err).NotTo(HaveOccurred())
		Expect(cd.Name).To(Equal("test-colony-test-cluster"))

		// Check that the labels were applied
		Expect(cd.Labels).To(Equal(map[string]string{
			"environment": "test",
			"team":        "platform",
		}))

		// Check that the colony status was updated
		Expect(colony.Status.ClusterDeploymentRefs).To(HaveLen(1))
		Expect(colony.Status.ClusterDeploymentRefs[0].Name).To(Equal("test-colony-test-cluster"))
	})

	It("should not create a duplicate if ClusterDeployment already exists", func() {
		existing := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-colony-test-cluster",
				Namespace: "default",
			},
		}
		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&infrav1.Colony{}).
			WithObjects(colony, existing).
			Build()

		err := EnsureClusterDeployment(ctx, c, colony, colonyCluster, scheme)
		Expect(err).NotTo(HaveOccurred())
		Expect(colony.Status.ClusterDeploymentRefs).To(HaveLen(1))
		Expect(colony.Status.ClusterDeploymentRefs[0].Name).To(Equal("test-colony-test-cluster"))
	})

	It("should update labels when they change", func() {
		// Create initial ClusterDeployment
		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&infrav1.Colony{}).
			WithObjects(colony).
			Build()

		err := EnsureClusterDeployment(ctx, c, colony, colonyCluster, scheme)
		Expect(err).NotTo(HaveOccurred())

		// Verify initial labels
		cd := &k0rdentv1beta1.ClusterDeployment{}
		err = c.Get(ctx, client.ObjectKey{Name: "test-colony-test-cluster", Namespace: "default"}, cd)
		Expect(err).NotTo(HaveOccurred())
		Expect(cd.Labels).To(Equal(map[string]string{
			"environment": "test",
			"team":        "platform",
		}))

		// Update the labels
		colonyCluster.ClusterLabels = map[string]string{
			"environment": "production",
			"team":        "platform",
			"region":      "us-west-2",
		}

		// Run the controller again
		err = EnsureClusterDeployment(ctx, c, colony, colonyCluster, scheme)
		Expect(err).NotTo(HaveOccurred())

		// Verify updated labels
		err = c.Get(ctx, client.ObjectKey{Name: "test-colony-test-cluster", Namespace: "default"}, cd)
		Expect(err).NotTo(HaveOccurred())
		Expect(cd.Labels).To(Equal(map[string]string{
			"environment": "production",
			"team":        "platform",
			"region":      "us-west-2",
		}))
	})

	It("should preserve existing labels from other operators when updating", func() {
		// Create initial ClusterDeployment with some existing labels from another operator
		existing := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-colony-test-cluster",
				Namespace: "default",
				Labels: map[string]string{
					"other-operator-label": "other-value",
					"environment":          "test", // This will be updated by our operator
					"team":                 "platform",
				},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&infrav1.Colony{}).
			WithObjects(colony, existing).
			Build()

		err := EnsureClusterDeployment(ctx, c, colony, colonyCluster, scheme)
		Expect(err).NotTo(HaveOccurred())

		// Verify that our labels are applied and existing labels are preserved
		cd := &k0rdentv1beta1.ClusterDeployment{}
		err = c.Get(ctx, client.ObjectKey{Name: "test-colony-test-cluster", Namespace: "default"}, cd)
		Expect(err).NotTo(HaveOccurred())

		// Should have our labels AND the existing label from other operator
		expectedLabels := map[string]string{
			"other-operator-label": "other-value", // Preserved from other operator
			"environment":          "test",        // Our label
			"team":                 "platform",    // Our label
		}
		Expect(cd.Labels).To(Equal(expectedLabels))
	})

})

var _ = Describe("WaitForClusterDeletion", func() {
	var (
		scheme            *runtime.Scheme
		clusterDeployment *k0rdentv1beta1.ClusterDeployment
		ctx               context.Context
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		_ = k0rdentv1beta1.AddToScheme(scheme)
		_ = corev1.AddToScheme(scheme)

		clusterDeployment = &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		ctx = context.TODO()
	})

	It("should delete PVC after ClusterDeployment is deleted", func() {
		// Create a PVC that should be deleted
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "etcd-data-kmc-test-cluster-etcd-0",
				Namespace: "default",
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(pvc).
			Build()

		// Since ClusterDeployment doesn't exist, the function should immediately detect it's "deleted" and delete the PVC
		err := WaitForClusterDeletion(ctx, c, clusterDeployment, 1*time.Second, 100*time.Millisecond)
		Expect(err).NotTo(HaveOccurred())

		// Verify PVC was deleted
		deletedPVC := &corev1.PersistentVolumeClaim{}
		err = c.Get(ctx, client.ObjectKey{Name: "etcd-data-kmc-test-cluster-etcd-0", Namespace: "default"}, deletedPVC)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found"))
	})

	It("should handle PVC not found gracefully", func() {
		c := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		// Since ClusterDeployment doesn't exist, the function should immediately detect it's "deleted" and try to delete PVC
		// But since PVC doesn't exist either, it should handle the NotFound error gracefully
		err := WaitForClusterDeletion(ctx, c, clusterDeployment, 1*time.Second, 100*time.Millisecond)
		Expect(err).NotTo(HaveOccurred())
		// Should not have any additional errors related to PVC deletion
	})

	It("should return immediately when ClusterDeployment is already deleted", func() {
		c := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		// Create and immediately delete the ClusterDeployment
		cd := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}
		Expect(c.Create(ctx, cd)).To(Succeed())
		Expect(c.Delete(ctx, cd)).To(Succeed())

		// Create a PVC that should be deleted
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "etcd-data-kmc-test-cluster-etcd-0",
				Namespace: "default",
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		}
		Expect(c.Create(ctx, pvc)).To(Succeed())

		// Wait for deletion with a short timeout
		err := WaitForClusterDeletion(ctx, c, clusterDeployment, 2*time.Second, 50*time.Millisecond)
		Expect(err).NotTo(HaveOccurred())

		// Verify PVC was deleted
		deletedPVC := &corev1.PersistentVolumeClaim{}
		err = c.Get(ctx, client.ObjectKey{Name: "etcd-data-kmc-test-cluster-etcd-0", Namespace: "default"}, deletedPVC)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found"))
	})
})

var _ = Describe("CleanupOrphanedClusterDeployments", func() {
	var (
		scheme *runtime.Scheme
		ctx    context.Context
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		_ = k0rdentv1beta1.AddToScheme(scheme)
		_ = infrav1.AddToScheme(scheme)
		_ = corev1.AddToScheme(scheme)
		ctx = context.TODO()
	})

	Context("when there are no orphaned ClusterDeployments", func() {
		It("should return false with no error", func() {
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-colony",
					Namespace: "default",
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{
						{ClusterName: "cluster-1"},
						{ClusterName: "cluster-2"},
					},
				},
				Status: infrav1.ColonyStatus{
					ClusterDeploymentRefs: []*corev1.ObjectReference{
						{Name: "test-colony-cluster-1", Namespace: "default"},
						{Name: "test-colony-cluster-2", Namespace: "default"},
					},
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&infrav1.Colony{}).
				WithObjects(colony).
				Build()

			hasPending, err := CleanupOrphanedClusterDeployments(ctx, c, colony)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasPending).To(BeFalse())
			// Status should be unchanged
			Expect(colony.Status.ClusterDeploymentRefs).To(HaveLen(2))
		})
	})

	Context("when orphaned ClusterDeployment is already deleted", func() {
		It("should remove from status and return false (no pending)", func() {
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-colony",
					Namespace: "default",
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{
						{ClusterName: "cluster-1"},
					},
				},
				Status: infrav1.ColonyStatus{
					ClusterDeploymentRefs: []*corev1.ObjectReference{
						{Name: "test-colony-cluster-1", Namespace: "default"},
						{Name: "test-colony-cluster-2", Namespace: "default"}, // orphaned and doesn't exist
					},
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&infrav1.Colony{}).
				WithObjects(colony).
				Build()

			hasPending, err := CleanupOrphanedClusterDeployments(ctx, c, colony)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasPending).To(BeFalse())
			// Orphaned ref should be removed from status
			Expect(colony.Status.ClusterDeploymentRefs).To(HaveLen(1))
			Expect(colony.Status.ClusterDeploymentRefs[0].Name).To(Equal("test-colony-cluster-1"))
		})

		It("should delete associated PVC when ClusterDeployment is gone", func() {
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-colony",
					Namespace: "default",
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{
						{ClusterName: "cluster-1"},
					},
				},
				Status: infrav1.ColonyStatus{
					ClusterDeploymentRefs: []*corev1.ObjectReference{
						{Name: "test-colony-cluster-1", Namespace: "default"},
						{Name: "test-colony-cluster-2", Namespace: "default"}, // orphaned
					},
				},
			}

			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd-data-kmc-test-colony-cluster-2-etcd-0",
					Namespace: "default",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&infrav1.Colony{}).
				WithObjects(colony, pvc).
				Build()

			hasPending, err := CleanupOrphanedClusterDeployments(ctx, c, colony)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasPending).To(BeFalse())

			// Verify PVC was deleted
			deletedPVC := &corev1.PersistentVolumeClaim{}
			err = c.Get(ctx, client.ObjectKey{Name: "etcd-data-kmc-test-colony-cluster-2-etcd-0", Namespace: "default"}, deletedPVC)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})
	})

	Context("when orphaned ClusterDeployment exists and is not being deleted", func() {
		It("should trigger deletion and return true (pending)", func() {
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-colony",
					Namespace: "default",
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{
						{ClusterName: "cluster-1"},
					},
				},
				Status: infrav1.ColonyStatus{
					ClusterDeploymentRefs: []*corev1.ObjectReference{
						{Name: "test-colony-cluster-1", Namespace: "default"},
						{Name: "test-colony-cluster-2", Namespace: "default"}, // orphaned
					},
				},
			}

			// Create the orphaned ClusterDeployment (exists but shouldn't)
			orphanedCD := &k0rdentv1beta1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-colony-cluster-2",
					Namespace: "default",
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&infrav1.Colony{}).
				WithObjects(colony, orphanedCD).
				Build()

			hasPending, err := CleanupOrphanedClusterDeployments(ctx, c, colony)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasPending).To(BeTrue()) // Should indicate pending deletion

			// Status should NOT be updated yet (CD still exists)
			Expect(colony.Status.ClusterDeploymentRefs).To(HaveLen(2))

			// Verify deletion was triggered (in fake client, Delete removes immediately unless there are finalizers)
			cd := &k0rdentv1beta1.ClusterDeployment{}
			err = c.Get(ctx, client.ObjectKey{Name: "test-colony-cluster-2", Namespace: "default"}, cd)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})
	})

	Context("when orphaned ClusterDeployment is being deleted (has DeletionTimestamp)", func() {
		It("should return true (pending) without triggering another delete", func() {
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-colony",
					Namespace: "default",
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{
						{ClusterName: "cluster-1"},
					},
				},
				Status: infrav1.ColonyStatus{
					ClusterDeploymentRefs: []*corev1.ObjectReference{
						{Name: "test-colony-cluster-1", Namespace: "default"},
						{Name: "test-colony-cluster-2", Namespace: "default"}, // orphaned, being deleted
					},
				},
			}

			now := metav1.Now()
			// Create orphaned ClusterDeployment with DeletionTimestamp (being deleted)
			orphanedCD := &k0rdentv1beta1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-colony-cluster-2",
					Namespace:         "default",
					DeletionTimestamp: &now,
					Finalizers:        []string{"some-finalizer"}, // Must have finalizer to have DeletionTimestamp
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&infrav1.Colony{}).
				WithObjects(colony, orphanedCD).
				Build()

			hasPending, err := CleanupOrphanedClusterDeployments(ctx, c, colony)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasPending).To(BeTrue()) // Deletion is pending

			// Status should NOT be updated yet
			Expect(colony.Status.ClusterDeploymentRefs).To(HaveLen(2))

			// ClusterDeployment should still exist (not deleted again)
			cd := &k0rdentv1beta1.ClusterDeployment{}
			err = c.Get(ctx, client.ObjectKey{Name: "test-colony-cluster-2", Namespace: "default"}, cd)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when there are multiple orphaned ClusterDeployments in various states", func() {
		It("should handle mixed states correctly", func() {
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-colony",
					Namespace: "default",
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{
						{ClusterName: "cluster-1"},
					},
				},
				Status: infrav1.ColonyStatus{
					ClusterDeploymentRefs: []*corev1.ObjectReference{
						{Name: "test-colony-cluster-1", Namespace: "default"}, // Not orphaned
						{Name: "test-colony-cluster-2", Namespace: "default"}, // Orphaned, already deleted
						{Name: "test-colony-cluster-3", Namespace: "default"}, // Orphaned, exists
						{Name: "test-colony-cluster-4", Namespace: "default"}, // Orphaned, being deleted
					},
				},
			}

			now := metav1.Now()
			// cluster-3: exists, needs deletion
			cd3 := &k0rdentv1beta1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-colony-cluster-3",
					Namespace: "default",
				},
			}
			// cluster-4: exists with DeletionTimestamp
			cd4 := &k0rdentv1beta1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-colony-cluster-4",
					Namespace:         "default",
					DeletionTimestamp: &now,
					Finalizers:        []string{"some-finalizer"},
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&infrav1.Colony{}).
				WithObjects(colony, cd3, cd4).
				Build()

			hasPending, err := CleanupOrphanedClusterDeployments(ctx, c, colony)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasPending).To(BeTrue()) // cluster-4 is still pending

			// cluster-2 should be removed from status (was already deleted)
			// cluster-3 and cluster-4 still pending
			Expect(colony.Status.ClusterDeploymentRefs).To(HaveLen(3))

			// Verify cluster-2 was removed
			found := false
			for _, ref := range colony.Status.ClusterDeploymentRefs {
				if ref.Name == "test-colony-cluster-2" {
					found = true
				}
			}
			Expect(found).To(BeFalse())
		})
	})

	Context("when all orphaned ClusterDeployments are already deleted", func() {
		It("should remove all from status and return false", func() {
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-colony",
					Namespace: "default",
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{
						{ClusterName: "cluster-1"},
					},
				},
				Status: infrav1.ColonyStatus{
					ClusterDeploymentRefs: []*corev1.ObjectReference{
						{Name: "test-colony-cluster-1", Namespace: "default"},
						{Name: "test-colony-cluster-2", Namespace: "default"}, // orphaned, deleted
						{Name: "test-colony-cluster-3", Namespace: "default"}, // orphaned, deleted
					},
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&infrav1.Colony{}).
				WithObjects(colony).
				Build()

			hasPending, err := CleanupOrphanedClusterDeployments(ctx, c, colony)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasPending).To(BeFalse())

			// Only cluster-1 should remain in status
			Expect(colony.Status.ClusterDeploymentRefs).To(HaveLen(1))
			Expect(colony.Status.ClusterDeploymentRefs[0].Name).To(Equal("test-colony-cluster-1"))
		})
	})

	Context("when Colony has empty ClusterDeploymentRefs", func() {
		It("should handle gracefully", func() {
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-colony",
					Namespace: "default",
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{
						{ClusterName: "cluster-1"},
					},
				},
				Status: infrav1.ColonyStatus{
					ClusterDeploymentRefs: []*corev1.ObjectReference{},
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&infrav1.Colony{}).
				WithObjects(colony).
				Build()

			hasPending, err := CleanupOrphanedClusterDeployments(ctx, c, colony)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasPending).To(BeFalse())
		})
	})

	Context("when Colony has nil ClusterDeploymentRefs", func() {
		It("should handle gracefully", func() {
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-colony",
					Namespace: "default",
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{
						{ClusterName: "cluster-1"},
					},
				},
				Status: infrav1.ColonyStatus{
					ClusterDeploymentRefs: nil,
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&infrav1.Colony{}).
				WithObjects(colony).
				Build()

			hasPending, err := CleanupOrphanedClusterDeployments(ctx, c, colony)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasPending).To(BeFalse())
		})
	})

	Context("when Colony spec has no ColonyClusters (all should be orphaned)", func() {
		It("should mark all ClusterDeploymentRefs as orphaned", func() {
			colony := &infrav1.Colony{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-colony",
					Namespace: "default",
				},
				Spec: infrav1.ColonySpec{
					ColonyClusters: []infrav1.ColonyCluster{}, // Empty spec
				},
				Status: infrav1.ColonyStatus{
					ClusterDeploymentRefs: []*corev1.ObjectReference{
						{Name: "test-colony-cluster-1", Namespace: "default"},
						{Name: "test-colony-cluster-2", Namespace: "default"},
					},
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&infrav1.Colony{}).
				WithObjects(colony).
				Build()

			hasPending, err := CleanupOrphanedClusterDeployments(ctx, c, colony)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasPending).To(BeFalse()) // Both were already deleted (not found)

			// Both should be removed from status
			Expect(colony.Status.ClusterDeploymentRefs).To(BeEmpty())
		})
	})
})

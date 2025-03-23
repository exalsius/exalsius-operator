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
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	volcanoalpha1 "volcano.sh/apis/pkg/apis/batch/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	trainingv1 "github.com/exalsius/exalsius-operator/api/training/v1"
)

var _ = Describe("DDPJob Controller", func() {
	ctx := context.Background()

	// Helper functions used across all contexts
	createDDPJob := func(name string, spec trainingv1.DDPJobSpec) *trainingv1.DDPJob {
		return &trainingv1.DDPJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: spec,
		}
	}

	// verifyDDPJobIsCreated verifies that a DDPJob exists in the given namespace
	// verifyDDPJobIsCreated := func(name string, namespace string) {
	// 	By("Verifying DDPJob exists")
	// 	ddpJob := &trainingv1.DDPJob{}
	// 	Expect(k8sClient.Get(ctx, types.NamespacedName{
	// 		Name:      name,
	// 		Namespace: namespace,
	// 	}, ddpJob)).To(Succeed())
	// }

	// verifyResourcesAreCreated verifies that a DDPJob and Volcano job exist in the given namespace
	verifyResourcesAreCreated := func(name string, namespace string) {
		By("Verifying DDPJob exists")
		var ddpJob *trainingv1.DDPJob
		Eventually(func() error {
			ddpJob = &trainingv1.DDPJob{}
			return k8sClient.Get(ctx, types.NamespacedName{
				Name:      name,
				Namespace: namespace,
			}, ddpJob)
		}, "30s", "1s").Should(Succeed(), "DDPJob should exist")

		By("Verifying Volcano job exists")
		var volcanoJob *volcanoalpha1.Job
		Eventually(func() error {
			volcanoJob = &volcanoalpha1.Job{}
			return k8sClient.Get(ctx, types.NamespacedName{
				Name:      "ddp-job-" + name,
				Namespace: namespace,
			}, volcanoJob)
		}, "30s", "1s").Should(Succeed(), "Volcano job should exist")

		By("Verifying Volcano job configuration")
		Expect(volcanoJob.Spec.MinAvailable).To(Equal(int32(ddpJob.Spec.Parallelism)),
			"Volcano job parallelism should match DDPJob spec")
		Expect(volcanoJob.Spec.SchedulerName).To(Equal("volcano"),
			"Scheduler name should be volcano")

	}

	verifyResourcesAreDeleted := func(name string, namespace string) {
		By("Verifying DDPJob is deleted")
		Eventually(func() error {
			ddpJob := &trainingv1.DDPJob{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      name,
				Namespace: namespace,
			}, ddpJob)
			if err == nil {
				return fmt.Errorf("DDPJob still exists")
			}
			if !errors.IsNotFound(err) {
				return fmt.Errorf("unexpected error checking DDPJob: %v", err)
			}
			return nil
		}, "30s", "1s").Should(Succeed(), "DDPJob should be deleted")

		By("Verifying Volcano job is deleted")
		Eventually(func() error {
			volcanoJob := &volcanoalpha1.Job{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      "ddp-job-" + name,
				Namespace: namespace,
			}, volcanoJob)
			if err == nil {
				return fmt.Errorf("Volcano job still exists")
			}
			if !errors.IsNotFound(err) {
				return fmt.Errorf("unexpected error checking Volcano job: %v", err)
			}
			return nil
		}, "30s", "1s").Should(Succeed(), "Volcano job should be deleted")
	}

	reconcileAndExpectSuccess := func(name string, namespace string) {
		By("Reconciling the created resource")
		reconciler := &DDPJobReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      name,
				Namespace: namespace,
			},
		})
		Expect(err).NotTo(HaveOccurred())
	}

	reconcileAndGetError := func(name string, namespace string) error {
		reconciler := &DDPJobReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      name,
				Namespace: namespace,
			},
		})
		return err
	}

	cleanupAllResources := func() {
		By("Cleaning up all test resources")

		By("Deleting all DDPJobs")
		ddpJobList := &trainingv1.DDPJobList{}
		Expect(k8sClient.List(ctx, ddpJobList)).To(Succeed())

		for _, job := range ddpJobList.Items {
			By(fmt.Sprintf("Deleting DDPJob %s", job.Name))
			Expect(k8sClient.Delete(ctx, &job)).To(Succeed())

			// reconcile to process finalizer
			reconcileAndExpectSuccess(job.Name, "default")
		}

		By("Deleting all Volcano jobs")
		volcanoJobList := &volcanoalpha1.JobList{}
		Expect(k8sClient.List(ctx, volcanoJobList)).To(Succeed())

		for _, job := range volcanoJobList.Items {
			Expect(k8sClient.Delete(ctx, &job)).To(Succeed())
		}

		By("Deleting all colonies")
		colonyList := &infrav1.ColonyList{}
		Expect(k8sClient.List(ctx, colonyList)).To(Succeed())

		for _, colony := range colonyList.Items {
			Expect(k8sClient.Delete(ctx, &colony)).To(Succeed())
		}

		By("Waiting for all resources to be deleted")
		Eventually(func() error {
			// Check for remaining DDPJobs
			ddpJobs := &trainingv1.DDPJobList{}
			if err := k8sClient.List(ctx, ddpJobs); err != nil {
				return err
			}
			if len(ddpJobs.Items) > 0 {
				return fmt.Errorf("found %d remaining DDPJobs", len(ddpJobs.Items))
			}

			// Check for remaining Volcano jobs
			volcanoJobs := &volcanoalpha1.JobList{}
			if err := k8sClient.List(ctx, volcanoJobs); err != nil {
				return err
			}
			if len(volcanoJobs.Items) > 0 {
				return fmt.Errorf("found %d remaining Volcano jobs", len(volcanoJobs.Items))
			}

			return nil
		}, "60s", "2s").Should(Succeed(), "All resources should be deleted")
	}

	// createTestColonyWithSecrets creates a colony with a kubeconfig secret
	// TODO: this should be moved to a helper functions file
	createTestColonyWithSecrets := func(name string, namespace string) error {
		By("Creating a colony")
		colony := &infrav1.Colony{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: infrav1.ColonySpec{
				ClusterName:   name,
				K8sVersion:    "v1.28.0",
				DockerEnabled: pointerTo(true),
				Docker: &infrav1.DockerSpec{
					Replicas: 1,
				},
			},
		}
		if err := k8sClient.Create(ctx, colony); err != nil {
			return fmt.Errorf("failed to create colony: %w", err)
		}

		colony.Status = infrav1.ColonyStatus{
			Phase:         "Ready",
			ReadyClusters: 1,
			TotalClusters: 1,
			ClusterRefs: []*corev1.ObjectReference{{
				Name:      name,
				Namespace: namespace,
			}},
		}
		if err := k8sClient.Status().Update(ctx, colony); err != nil {
			return fmt.Errorf("failed to update colony status: %w", err)
		}

		By("Creating kubeconfig secrets")
		kubeconfig := clientcmdapi.NewConfig()
		kubeconfig.Clusters["test-cluster"] = &clientcmdapi.Cluster{
			Server:                "https://localhost:6443",
			InsecureSkipTLSVerify: true,
		}
		kubeconfig.AuthInfos["test-user"] = &clientcmdapi.AuthInfo{
			Token: "dummy-token",
		}
		kubeconfig.Contexts["test-context"] = &clientcmdapi.Context{
			Cluster:  "test-cluster",
			AuthInfo: "test-user",
		}
		kubeconfig.CurrentContext = "test-context"

		kubeconfigBytes, err := clientcmd.Write(*kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to marshal kubeconfig: %w", err)
		}

		clusterSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name + "-kubeconfig",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"value": kubeconfigBytes,
			},
		}
		if err := k8sClient.Create(ctx, clusterSecret); err != nil {
			return fmt.Errorf("failed to create cluster secret: %w", err)
		}

		objRef := corev1.ObjectReference{
			APIVersion: clusterSecret.APIVersion,
			Kind:       clusterSecret.Kind,
			Namespace:  clusterSecret.Namespace,
			Name:       clusterSecret.Name,
		}

		refBytes, err := json.Marshal(objRef)
		if err != nil {
			return fmt.Errorf("failed to marshal reference: %w", err)
		}

		colonySecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name + "-kubeconfigs",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				name: refBytes,
			},
		}
		if err := k8sClient.Create(ctx, colonySecret); err != nil {
			return fmt.Errorf("failed to create colony secret: %w", err)
		}

		return nil
	}

	Context("When creating new DDPJobs", func() {
		AfterEach(func() {
			// Clean up any leftover resources before each test
			cleanupAllResources()
		})

		It("should create a DDPJob with a specified number of GPUs", func() {
			jobName := "test-job"
			ddpJob := createDDPJob(jobName, trainingv1.DDPJobSpec{
				Parallelism:  2,
				NProcPerNode: 1,
				Image:        "test/test-image:latest",
				ScriptPath:   "/app/test-script.py",
				WandBAPIKey:  "xxxxxxx",
				Args:         []string{"--epochs", "10"},
			})
			Expect(k8sClient.Create(ctx, ddpJob)).To(Succeed())
			reconcileAndExpectSuccess(jobName, "default")
			verifyResourcesAreCreated(jobName, "default")
		})

		It("should create a CPU job", func() {
			// Test CPU job creation
			jobName := "test-job"
			ddpJob := createDDPJob(jobName, trainingv1.DDPJobSpec{
				CPUJob:       pointerTo(true),
				Parallelism:  2,
				NProcPerNode: 1,
				Image:        "test/test-image:latest",
				ScriptPath:   "/app/test-script.py",
				WandBAPIKey:  "xxxxxxx",
			})
			Expect(k8sClient.Create(ctx, ddpJob)).To(Succeed())
			reconcileAndExpectSuccess(jobName, "default")
			verifyResourcesAreCreated(jobName, "default")
		})
	})

	Context("When handling colony-targeted jobs", func() {
		colonyName := "target-colony"

		BeforeEach(func() {
			Expect(createTestColonyWithSecrets(colonyName, "default")).To(Succeed())
		})

		AfterEach(func() {
			cleanupAllResources()
		})

		It("should create a DDPJob with a targetColony", func() {
			jobName := "test-job"

			ddpJob := createDDPJob(jobName, trainingv1.DDPJobSpec{
				TargetColony: pointerTo(colonyName),
				Parallelism:  2,
				NProcPerNode: 1,
				Image:        "test/test-image:latest",
				ScriptPath:   "/app/test-script.py",
				WandBAPIKey:  "xxxxxxx",
			})
			Expect(k8sClient.Create(ctx, ddpJob)).To(Succeed())
			// For this checks to work we can't use a mocked colony
			// reconcileAndExpectSuccess(jobName, "default")
			// verifyDDPJobIsCreated(jobName, "default")
		})

	})

	Context("When handling job lifecycle", func() {
		jobName := "test-job"

		BeforeEach(func() {
			ddpJob := createDDPJob(jobName, trainingv1.DDPJobSpec{
				Parallelism:  2,
				NProcPerNode: 1,
				Image:        "test/test-image:latest",
				ScriptPath:   "/app/test-script.py",
			})
			Expect(k8sClient.Create(ctx, ddpJob)).To(Succeed())
			reconcileAndExpectSuccess(jobName, "default")
			verifyResourcesAreCreated(jobName, "default")
		})

		AfterEach(func() {
			cleanupAllResources()
		})

		It("should update status when volcano job status changes", func() {

			By("Verifying the Volcano job was created")
			volcanoJob := &volcanoalpha1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      "ddp-job-" + jobName,
					Namespace: "default",
				}, volcanoJob)
			}).Should(Succeed())

			By("Simulating a running volcano job")
			volcanoJob.Status.State.Phase = volcanoalpha1.Running
			Expect(k8sClient.Status().Update(ctx, volcanoJob)).To(Succeed())

			By("Reconciling the DDPJob")
			reconcileAndExpectSuccess(jobName, "default")

			By("Verifying the DDPJob status was updated")
			Eventually(func() trainingv1.JobPhase {
				ddpJob := &trainingv1.DDPJob{}
				k8sClient.Get(ctx, types.NamespacedName{
					Name:      jobName,
					Namespace: "default",
				}, ddpJob)
				return ddpJob.Status.Phase
			}).Should(Equal(trainingv1.JobPhase(volcanoalpha1.Running)))
		})

		It("should delete the volcano job when the DDPJob is deleted", func() {
			By("Deleting the DDPJob")
			ddpJob := &trainingv1.DDPJob{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      jobName,
				Namespace: "default",
			}, ddpJob)).To(Succeed())

			Expect(k8sClient.Delete(ctx, ddpJob)).To(Succeed())
			reconcileAndExpectSuccess(jobName, "default")
			verifyResourcesAreDeleted(jobName, "default")
		})
	})

	Context("Error scenarios", func() {
		AfterEach(func() {
			cleanupAllResources()
		})

		Context("Validation errors", func() {
			It("should reject invalid target-colony configuration", func() {
				By("Creating a job with a non-existent target-colony")
				jobName := "test-job"
				ddpJob := createDDPJob(jobName, trainingv1.DDPJobSpec{
					TargetColony: pointerTo("non-existent-colony"),
				})
				Expect(k8sClient.Create(ctx, ddpJob)).To(Succeed())
				err := reconcileAndGetError(jobName, "default")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not found"))
			})

		})

		Context("Runtime errors", func() {
		})
	})
})

// Helper function for optional values
func pointerTo[T any](s T) *T {
	return &s
}

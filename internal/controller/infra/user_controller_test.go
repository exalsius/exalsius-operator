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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
)

var _ = Describe("User Controller", func() {
	Context("When reconciling a User resource", func() {
		const (
			resourceName  = "test-user"
			userNamespace = "test-user-namespace"
		)

		ctx := context.Background()
		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		BeforeEach(func() {
			// Ensure cleanup from previous tests
			By("Cleaning up any existing resources from previous tests")

			// Cleanup User resource
			user := &infrav1.User{}
			err := k8sClient.Get(ctx, typeNamespacedName, user)
			if err == nil {
				Expect(k8sClient.Delete(ctx, user)).To(Succeed())
			}

			// Cleanup namespace
			namespace := &corev1.Namespace{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: userNamespace}, namespace)
			if err == nil {
				Expect(k8sClient.Delete(ctx, namespace)).To(Succeed())
			}

			// Cleanup AccessManagement
			accessManagement := &k0rdentv1beta1.AccessManagement{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, accessManagement)
			if err == nil {
				Expect(k8sClient.Delete(ctx, accessManagement)).To(Succeed())
			}

			// Wait for resources to be fully deleted with shorter timeout
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, user)
				return errors.IsNotFound(err)
			}, time.Second*5, time.Millisecond*250).Should(BeTrue(), "User should be deleted")

			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: userNamespace}, namespace)
				return errors.IsNotFound(err)
			}, time.Second*5, time.Millisecond*250).Should(BeTrue(), "Namespace should be deleted")

			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, accessManagement)
				return errors.IsNotFound(err)
			}, time.Second*5, time.Millisecond*250).Should(BeTrue(), "AccessManagement should be deleted")
		})

		AfterEach(func() {
			// Cleanup after each test
			By("Cleaning up resources after test")

			// Cleanup User resource
			user := &infrav1.User{}
			err := k8sClient.Get(ctx, typeNamespacedName, user)
			if err == nil {
				Expect(k8sClient.Delete(ctx, user)).To(Succeed())
			}

			// Cleanup namespace
			namespace := &corev1.Namespace{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: userNamespace}, namespace)
			if err == nil {
				Expect(k8sClient.Delete(ctx, namespace)).To(Succeed())
			}

			// Cleanup AccessManagement
			accessManagement := &k0rdentv1beta1.AccessManagement{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, accessManagement)
			if err == nil {
				Expect(k8sClient.Delete(ctx, accessManagement)).To(Succeed())
			}
		})

		It("should create namespace and update AccessManagement when User is created", func() {
			By("Creating a User resource")
			user := &infrav1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: infrav1.UserSpec{
					UserId:        "test-user-id",
					UserName:      "Test User",
					UserEmail:     "test@example.com",
					UserNamespace: userNamespace,
					UserRoles:     []string{"user"},
				},
			}
			Expect(k8sClient.Create(ctx, user)).To(Succeed())

			By("Creating an AccessManagement resource")
			accessManagement := &k0rdentv1beta1.AccessManagement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kcm",
				},
				Spec: k0rdentv1beta1.AccessManagementSpec{
					AccessRules: []k0rdentv1beta1.AccessRule{
						{
							TargetNamespaces: k0rdentv1beta1.TargetNamespaces{
								List: []string{"default"},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, accessManagement)).To(Succeed())

			By("Reconciling the User resource")
			controllerReconciler := &UserReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying that the namespace was created")
			namespace := &corev1.Namespace{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: userNamespace}, namespace)
			}, time.Second*10, time.Millisecond*250).Should(Succeed())
			Expect(namespace.Name).To(Equal(userNamespace))

			By("Verifying that the namespace was added to AccessManagement")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, accessManagement)
				if err != nil {
					return false
				}
				if len(accessManagement.Spec.AccessRules) == 0 {
					return false
				}
				for _, ns := range accessManagement.Spec.AccessRules[0].TargetNamespaces.List {
					if ns == userNamespace {
						return true
					}
				}
				return false
			}, time.Second*10, time.Millisecond*250).Should(BeTrue())

			By("Verifying that the User status was updated")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, user)
				if err != nil {
					return false
				}
				return user.Status.Ready
			}, time.Second*10, time.Millisecond*250).Should(BeTrue())
		})

		It("should handle existing namespace gracefully", func() {
			By("Creating the namespace first")
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: userNamespace,
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

			By("Creating a User resource")
			user := &infrav1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: infrav1.UserSpec{
					UserId:        "test-user-id",
					UserName:      "Test User",
					UserEmail:     "test@example.com",
					UserNamespace: userNamespace,
					UserRoles:     []string{"user"},
				},
			}
			Expect(k8sClient.Create(ctx, user)).To(Succeed())

			By("Creating an AccessManagement resource")
			accessManagement := &k0rdentv1beta1.AccessManagement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kcm",
				},
				Spec: k0rdentv1beta1.AccessManagementSpec{
					AccessRules: []k0rdentv1beta1.AccessRule{
						{
							TargetNamespaces: k0rdentv1beta1.TargetNamespaces{
								List: []string{"default", userNamespace},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, accessManagement)).To(Succeed())

			By("Reconciling the User resource")
			controllerReconciler := &UserReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying that the namespace still exists")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: userNamespace}, namespace)).To(Succeed())

			By("Verifying that the User status was updated")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, user)
				if err != nil {
					return false
				}
				return user.Status.Ready
			}, time.Second*10, time.Millisecond*250).Should(BeTrue())
		})

		It("should handle missing AccessManagement gracefully", func() {
			By("Creating a User resource")
			user := &infrav1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: infrav1.UserSpec{
					UserId:        "test-user-id",
					UserName:      "Test User",
					UserEmail:     "test@example.com",
					UserNamespace: userNamespace,
					UserRoles:     []string{"user"},
				},
			}
			Expect(k8sClient.Create(ctx, user)).To(Succeed())

			By("Reconciling the User resource without AccessManagement")
			controllerReconciler := &UserReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying that the namespace was created")
			namespace := &corev1.Namespace{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: userNamespace}, namespace)
			}, time.Second*10, time.Millisecond*250).Should(Succeed())

			By("Verifying that the User status was updated")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, user)
				if err != nil {
					return false
				}
				return user.Status.Ready
			}, time.Second*10, time.Millisecond*250).Should(BeTrue())
		})

		It("should create AccessManagement with new access rule when none exists", func() {
			By("Creating a User resource")
			user := &infrav1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: infrav1.UserSpec{
					UserId:        "test-user-id",
					UserName:      "Test User",
					UserEmail:     "test@example.com",
					UserNamespace: userNamespace,
					UserRoles:     []string{"user"},
				},
			}
			Expect(k8sClient.Create(ctx, user)).To(Succeed())

			By("Creating an empty AccessManagement resource")
			accessManagement := &k0rdentv1beta1.AccessManagement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kcm",
				},
				Spec: k0rdentv1beta1.AccessManagementSpec{
					AccessRules: []k0rdentv1beta1.AccessRule{},
				},
			}
			Expect(k8sClient.Create(ctx, accessManagement)).To(Succeed())

			By("Reconciling the User resource")
			controllerReconciler := &UserReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying that the namespace was created")
			namespace := &corev1.Namespace{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: userNamespace}, namespace)
			}, time.Second*10, time.Millisecond*250).Should(Succeed())

			By("Verifying that AccessManagement was updated with new access rule")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, accessManagement)
				if err != nil {
					return false
				}
				if len(accessManagement.Spec.AccessRules) == 0 {
					return false
				}
				for _, ns := range accessManagement.Spec.AccessRules[0].TargetNamespaces.List {
					if ns == userNamespace {
						return true
					}
				}
				return false
			}, time.Second*10, time.Millisecond*250).Should(BeTrue())

			By("Verifying that the User status was updated")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, user)
				if err != nil {
					return false
				}
				return user.Status.Ready
			}, time.Second*10, time.Millisecond*250).Should(BeTrue())
		})

		It("should handle User deletion gracefully", func() {
			By("Creating a User resource")
			user := &infrav1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: infrav1.UserSpec{
					UserId:        "test-user-id",
					UserName:      "Test User",
					UserEmail:     "test@example.com",
					UserNamespace: userNamespace,
					UserRoles:     []string{"user"},
				},
			}
			Expect(k8sClient.Create(ctx, user)).To(Succeed())

			By("Deleting the User resource")
			Expect(k8sClient.Delete(ctx, user)).To(Succeed())

			By("Reconciling the deleted User resource")
			controllerReconciler := &UserReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying that the User no longer exists")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, user)
				return errors.IsNotFound(err)
			}, time.Second*10, time.Millisecond*250).Should(BeTrue())
		})
	})
})

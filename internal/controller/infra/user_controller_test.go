package infra

import (
	"time"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("User Controller", func() {
	const (
		userNamespace = "test-user-ns"
		userName      = "test-user"
	)

	var mgr ctrl.Manager

	BeforeEach(func() {
		var err error
		mgr, err = ctrl.NewManager(cfg, ctrl.Options{
			Scheme: scheme.Scheme,
		})
		Expect(err).ToNot(HaveOccurred())

		reconciler := &UserReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		}
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(ctx)).To(Succeed())
		}()
	})

	It("should reconcile User by creating namespace and updating AccessManagement", func() {
		By("Creating AccessManagement object")
		access := &k0rdentv1beta1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kcm",
			},
			Spec: k0rdentv1beta1.AccessManagementSpec{
				AccessRules: []k0rdentv1beta1.AccessRule{{
					TargetNamespaces: k0rdentv1beta1.TargetNamespaces{
						List: []string{},
					},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, access)).To(Succeed())

		By("Creating User resource")
		user := &infrav1.User{
			ObjectMeta: metav1.ObjectMeta{
				Name:      userName,
				Namespace: "default",
			},
			Spec: infrav1.UserSpec{
				UserNamespace: userNamespace,
			},
		}
		Expect(k8sClient.Create(ctx, user)).To(Succeed())

		By("Expecting Namespace to be created")
		Eventually(func() bool {
			ns := &corev1.Namespace{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: userNamespace}, ns)
			return err == nil
		}, time.Second*10, time.Millisecond*250).Should(BeTrue())

		By("Expecting User status to be ready")
		Eventually(func() bool {
			u := &infrav1.User{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: userName, Namespace: "default"}, u)
			return err == nil && u.Status.Ready
		}, time.Second*10, time.Millisecond*250).Should(BeTrue())

		By("Expecting AccessManagement to contain the namespace")
		Eventually(func() []string {
			am := &k0rdentv1beta1.AccessManagement{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, am)
			if err != nil || len(am.Spec.AccessRules) == 0 {
				return nil
			}
			return am.Spec.AccessRules[0].TargetNamespaces.List
		}, time.Second*10, time.Millisecond*250).Should(ContainElement(userNamespace))
	})

})

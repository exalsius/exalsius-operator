package infra

import (
	"context"
	"time"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	capsulev1beta2 "github.com/projectcapsule/capsule/api/v1beta2"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
)

var _ = Describe("Tenant Controller", Ordered, func() {
	const (
		tenantName  = "test-tenant"
		tenantName2 = "test-tenant-2"
		timeout     = time.Second * 10
		interval    = time.Millisecond * 250
	)

	var (
		mgrCtx    context.Context
		mgrCancel context.CancelFunc
	)

	cleanupAccessManagement := func() {
		am := &k0rdentv1beta1.AccessManagement{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, am)
		if err == nil {
			Expect(k8sClient.Delete(ctx, am)).To(Succeed())
			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, &k0rdentv1beta1.AccessManagement{}))
			}, timeout, interval).Should(BeTrue())
		}
	}

	cleanupTenant := func(name string) {
		tenant := &capsulev1beta2.Tenant{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, tenant)
		if err != nil {
			return
		}
		// Remove finalizer to allow deletion
		tenant.Finalizers = nil
		Expect(k8sClient.Update(ctx, tenant)).To(Succeed())
		Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())
		Eventually(func() bool {
			return errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &capsulev1beta2.Tenant{}))
		}, timeout, interval).Should(BeTrue())
	}

	BeforeAll(func() {
		mgrCtx, mgrCancel = context.WithCancel(ctx)

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme: scheme.Scheme,
		})
		Expect(err).ToNot(HaveOccurred())

		reconciler := &TenantReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		}
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()
	})

	AfterAll(func() {
		mgrCancel()
	})

	AfterEach(func() {
		cleanupTenant(tenantName)
		cleanupTenant(tenantName2)
		cleanupAccessManagement()
	})

	It("should create an AccessRule when a Tenant is created", func() {
		By("Creating AccessManagement with a default AccessRule")
		access := &k0rdentv1beta1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kcm",
			},
			Spec: k0rdentv1beta1.AccessManagementSpec{
				AccessRules: []k0rdentv1beta1.AccessRule{{
					TargetNamespaces: k0rdentv1beta1.TargetNamespaces{
						List: []string{"default"},
					},
					ClusterTemplateChains: []string{"default-chain"},
					ServiceTemplateChains: []string{"default-svc-chain"},
					Credentials:           []string{"default-cred"},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, access)).To(Succeed())

		By("Creating a Capsule Tenant")
		tenant := &capsulev1beta2.Tenant{
			ObjectMeta: metav1.ObjectMeta{
				Name: tenantName,
			},
			Spec: capsulev1beta2.TenantSpec{},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		By("Expecting an AccessRule with label selector for the tenant")
		Eventually(func() bool {
			am := &k0rdentv1beta1.AccessManagement{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, am); err != nil {
				return false
			}
			for _, rule := range am.Spec.AccessRules {
				if rule.TargetNamespaces.Selector != nil {
					if val, ok := rule.TargetNamespaces.Selector.MatchLabels[capsuleTenantLabel]; ok && val == tenantName {
						return true
					}
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())

		By("Verifying the new rule inherits templates and credentials from the default rule")
		am := &k0rdentv1beta1.AccessManagement{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, am)).To(Succeed())
		var tenantRule *k0rdentv1beta1.AccessRule
		for i, rule := range am.Spec.AccessRules {
			if rule.TargetNamespaces.Selector != nil {
				if val, ok := rule.TargetNamespaces.Selector.MatchLabels[capsuleTenantLabel]; ok && val == tenantName {
					tenantRule = &am.Spec.AccessRules[i]
					break
				}
			}
		}
		Expect(tenantRule).NotTo(BeNil())
		Expect(tenantRule.ClusterTemplateChains).To(Equal([]string{"default-chain"}))
		Expect(tenantRule.ServiceTemplateChains).To(Equal([]string{"default-svc-chain"}))
		Expect(tenantRule.Credentials).To(Equal([]string{"default-cred"}))
	})

	It("should remove the AccessRule when a Tenant is deleted", func() {
		By("Creating AccessManagement with a default AccessRule")
		access := &k0rdentv1beta1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kcm",
			},
			Spec: k0rdentv1beta1.AccessManagementSpec{
				AccessRules: []k0rdentv1beta1.AccessRule{{
					TargetNamespaces: k0rdentv1beta1.TargetNamespaces{
						List: []string{"default"},
					},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, access)).To(Succeed())

		By("Creating a Capsule Tenant")
		tenant := &capsulev1beta2.Tenant{
			ObjectMeta: metav1.ObjectMeta{
				Name: tenantName,
			},
			Spec: capsulev1beta2.TenantSpec{},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		By("Waiting for the AccessRule to be created")
		Eventually(func() bool {
			am := &k0rdentv1beta1.AccessManagement{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, am); err != nil {
				return false
			}
			for _, rule := range am.Spec.AccessRules {
				if rule.TargetNamespaces.Selector != nil {
					if val, ok := rule.TargetNamespaces.Selector.MatchLabels[capsuleTenantLabel]; ok && val == tenantName {
						return true
					}
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())

		By("Deleting the Tenant")
		Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())

		By("Expecting the AccessRule to be removed")
		Eventually(func() bool {
			am := &k0rdentv1beta1.AccessManagement{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, am); err != nil {
				return false
			}
			for _, rule := range am.Spec.AccessRules {
				if rule.TargetNamespaces.Selector != nil {
					if val, ok := rule.TargetNamespaces.Selector.MatchLabels[capsuleTenantLabel]; ok && val == tenantName {
						return false
					}
				}
			}
			return true
		}, timeout, interval).Should(BeTrue())

		By("Verifying the finalizer is removed")
		Eventually(func() bool {
			return errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName}, &capsulev1beta2.Tenant{}))
		}, timeout, interval).Should(BeTrue())
	})

	It("should handle multiple tenants with separate AccessRules", func() {
		By("Creating AccessManagement with a default AccessRule")
		access := &k0rdentv1beta1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kcm",
			},
			Spec: k0rdentv1beta1.AccessManagementSpec{
				AccessRules: []k0rdentv1beta1.AccessRule{{
					TargetNamespaces: k0rdentv1beta1.TargetNamespaces{
						List: []string{"default"},
					},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, access)).To(Succeed())

		By("Creating two Tenants")
		for _, name := range []string{tenantName, tenantName2} {
			tenant := &capsulev1beta2.Tenant{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: capsulev1beta2.TenantSpec{},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
		}

		By("Expecting two AccessRules with distinct selectors")
		Eventually(func() int {
			am := &k0rdentv1beta1.AccessManagement{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, am); err != nil {
				return 0
			}
			count := 0
			for _, rule := range am.Spec.AccessRules {
				if rule.TargetNamespaces.Selector != nil {
					if _, ok := rule.TargetNamespaces.Selector.MatchLabels[capsuleTenantLabel]; ok {
						count++
					}
				}
			}
			return count
		}, timeout, interval).Should(Equal(2))

		By("Deleting one Tenant and verifying only its rule is removed")
		t := &capsulev1beta2.Tenant{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName}, t)).To(Succeed())
		Expect(k8sClient.Delete(ctx, t)).To(Succeed())

		Eventually(func() int {
			am := &k0rdentv1beta1.AccessManagement{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, am); err != nil {
				return -1
			}
			count := 0
			for _, rule := range am.Spec.AccessRules {
				if rule.TargetNamespaces.Selector != nil {
					if _, ok := rule.TargetNamespaces.Selector.MatchLabels[capsuleTenantLabel]; ok {
						count++
					}
				}
			}
			return count
		}, timeout, interval).Should(Equal(1))

		By("Verifying the remaining rule belongs to the second tenant")
		am := &k0rdentv1beta1.AccessManagement{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, am)).To(Succeed())
		found := false
		for _, rule := range am.Spec.AccessRules {
			if rule.TargetNamespaces.Selector != nil {
				if val, ok := rule.TargetNamespaces.Selector.MatchLabels[capsuleTenantLabel]; ok && val == tenantName2 {
					found = true
				}
			}
		}
		Expect(found).To(BeTrue())
	})

	It("should handle missing AccessManagement gracefully", func() {
		By("Creating a Tenant without AccessManagement existing")
		tenant := &capsulev1beta2.Tenant{
			ObjectMeta: metav1.ObjectMeta{
				Name: tenantName,
			},
			Spec: capsulev1beta2.TenantSpec{},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		By("Expecting the finalizer to be added without error")
		Eventually(func() bool {
			t := &capsulev1beta2.Tenant{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: tenantName}, t); err != nil {
				return false
			}
			for _, f := range t.Finalizers {
				if f == tenantFinalizerName {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())
	})

	It("should be idempotent and not create duplicate AccessRules", func() {
		By("Creating AccessManagement with a default AccessRule")
		access := &k0rdentv1beta1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kcm",
			},
			Spec: k0rdentv1beta1.AccessManagementSpec{
				AccessRules: []k0rdentv1beta1.AccessRule{{
					TargetNamespaces: k0rdentv1beta1.TargetNamespaces{
						List: []string{"default"},
					},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, access)).To(Succeed())

		By("Creating a Tenant")
		tenant := &capsulev1beta2.Tenant{
			ObjectMeta: metav1.ObjectMeta{
				Name: tenantName,
			},
			Spec: capsulev1beta2.TenantSpec{},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		By("Waiting for the AccessRule to be created")
		Eventually(func() bool {
			am := &k0rdentv1beta1.AccessManagement{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, am); err != nil {
				return false
			}
			for _, rule := range am.Spec.AccessRules {
				if rule.TargetNamespaces.Selector != nil {
					if val, ok := rule.TargetNamespaces.Selector.MatchLabels[capsuleTenantLabel]; ok && val == tenantName {
						return true
					}
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())

		By("Triggering another reconcile by updating the Tenant")
		t := &capsulev1beta2.Tenant{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tenantName}, t)).To(Succeed())
		if t.Labels == nil {
			t.Labels = map[string]string{}
		}
		t.Labels["test"] = "trigger-reconcile"
		Expect(k8sClient.Update(ctx, t)).To(Succeed())

		By("Verifying there is still only one AccessRule for the tenant")
		Consistently(func() int {
			am := &k0rdentv1beta1.AccessManagement{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, am); err != nil {
				return -1
			}
			count := 0
			for _, rule := range am.Spec.AccessRules {
				if rule.TargetNamespaces.Selector != nil {
					if val, ok := rule.TargetNamespaces.Selector.MatchLabels[capsuleTenantLabel]; ok && val == tenantName {
						count++
					}
				}
			}
			return count
		}, time.Second*3, interval).Should(Equal(1))
	})

	It("should add finalizer to the Tenant", func() {
		By("Creating a Tenant")
		tenant := &capsulev1beta2.Tenant{
			ObjectMeta: metav1.ObjectMeta{
				Name: tenantName,
			},
			Spec: capsulev1beta2.TenantSpec{},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		By("Expecting the finalizer to be present")
		Eventually(func() bool {
			t := &capsulev1beta2.Tenant{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: tenantName}, t); err != nil {
				return false
			}
			for _, f := range t.Finalizers {
				if f == tenantFinalizerName {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())
	})

	It("should create an AccessRule with only selector when no default rule exists", func() {
		By("Creating empty AccessManagement (no AccessRules)")
		access := &k0rdentv1beta1.AccessManagement{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kcm",
			},
			Spec: k0rdentv1beta1.AccessManagementSpec{
				AccessRules: []k0rdentv1beta1.AccessRule{},
			},
		}
		Expect(k8sClient.Create(ctx, access)).To(Succeed())

		By("Creating a Tenant")
		tenant := &capsulev1beta2.Tenant{
			ObjectMeta: metav1.ObjectMeta{
				Name: tenantName,
			},
			Spec: capsulev1beta2.TenantSpec{},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		By("Expecting an AccessRule with label selector and empty templates/credentials")
		Eventually(func() bool {
			am := &k0rdentv1beta1.AccessManagement{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, am); err != nil {
				return false
			}
			for _, rule := range am.Spec.AccessRules {
				if rule.TargetNamespaces.Selector != nil {
					if val, ok := rule.TargetNamespaces.Selector.MatchLabels[capsuleTenantLabel]; ok && val == tenantName {
						return true
					}
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())

		By("Verifying the rule has empty templates and credentials")
		am := &k0rdentv1beta1.AccessManagement{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "kcm"}, am)).To(Succeed())
		for _, rule := range am.Spec.AccessRules {
			if rule.TargetNamespaces.Selector != nil {
				if val, ok := rule.TargetNamespaces.Selector.MatchLabels[capsuleTenantLabel]; ok && val == tenantName {
					Expect(rule.ClusterTemplateChains).To(BeNil())
					Expect(rule.ServiceTemplateChains).To(BeNil())
					Expect(rule.Credentials).To(BeNil())
				}
			}
		}
	})
})

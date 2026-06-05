package workspaces

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("WorkspaceClassReconciler", func() {
	const (
		timeout  = 30 * time.Second
		interval = 250 * time.Millisecond
	)

	makeServiceTemplate := func(name string) *k0rdentv1beta1.ServiceTemplate {
		return &k0rdentv1beta1.ServiceTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: k0rdentv1beta1.ServiceTemplateSpec{
				Helm: &k0rdentv1beta1.HelmSpec{
					ChartSpec: &sourcev1.HelmChartSpec{
						Chart:    "test-chart",
						Version:  "0.1.0",
						Interval: metav1.Duration{Duration: time.Minute},
						SourceRef: sourcev1.LocalHelmChartSourceReference{
							Kind: "HelmRepository",
							Name: "test-repo",
						},
					},
				},
			},
		}
	}

	patchSTValid := func(st *k0rdentv1beta1.ServiceTemplate, valid bool, errMsg string) {
		fresh := &k0rdentv1beta1.ServiceTemplate{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(st), fresh)).To(Succeed())
		fresh.Status.Valid = valid
		fresh.Status.ValidationError = errMsg
		Expect(k8sClient.Status().Update(ctx, fresh)).To(Succeed())
	}

	makeClass := func(name, stName string) *workspacesv1.WorkspaceClass {
		return &workspacesv1.WorkspaceClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: workspacesv1.WorkspaceClassSpec{
				DisplayName: "Test",
				ServiceTemplate: workspacesv1.ServiceTemplateRef{
					Name:      stName,
					Namespace: "default",
				},
				DefaultResources: workspacesv1.WorkspaceResourceSpec{
					PerReplica: workspacesv1.ResourceRequirements{
						CPU:    resourceQuantityPtr("100m"),
						Memory: resourceQuantityPtr("128Mi"),
					},
				},
			},
		}
	}

	It("should set valid=false when the ServiceTemplate is missing", func() {
		wsc := makeClass("wsc-missing-st", "missing-template")
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceClass{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsc), fetched)).To(Succeed())
			g.Expect(fetched.Status.Valid).To(BeFalse())
			g.Expect(fetched.Status.ValidationError).To(ContainSubstring("not found"))
			g.Expect(fetched.Status.ValidationError).To(ContainSubstring("missing-template"))
			g.Expect(fetched.Status.ObservedGeneration).To(Equal(fetched.Generation))
		}, timeout, interval).Should(Succeed())
	})

	It("should set valid=true when the ServiceTemplate reports valid=true", func() {
		st := makeServiceTemplate("st-valid-true")
		Expect(k8sClient.Create(ctx, st)).To(Succeed())
		patchSTValid(st, true, "")

		wsc := makeClass("wsc-st-valid", "st-valid-true")
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceClass{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsc), fetched)).To(Succeed())
			g.Expect(fetched.Status.Valid).To(BeTrue())
			g.Expect(fetched.Status.ValidationError).To(BeEmpty())
		}, timeout, interval).Should(Succeed())
	})

	It("should propagate the ServiceTemplate's validationError when valid=false", func() {
		st := makeServiceTemplate("st-valid-false")
		Expect(k8sClient.Create(ctx, st)).To(Succeed())
		patchSTValid(st, false, "chart pull failed")

		wsc := makeClass("wsc-st-invalid", "st-valid-false")
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceClass{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsc), fetched)).To(Succeed())
			g.Expect(fetched.Status.Valid).To(BeFalse())
			g.Expect(fetched.Status.ValidationError).To(Equal("chart pull failed"))
		}, timeout, interval).Should(Succeed())
	})

	It("should fall back to a default error when ST is invalid with no message", func() {
		st := makeServiceTemplate("st-invalid-no-msg")
		Expect(k8sClient.Create(ctx, st)).To(Succeed())
		// status.valid defaults to false on creation; no ValidationError set

		wsc := makeClass("wsc-invalid-no-msg", "st-invalid-no-msg")
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceClass{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsc), fetched)).To(Succeed())
			g.Expect(fetched.Status.Valid).To(BeFalse())
			g.Expect(fetched.Status.ValidationError).To(Equal(sourceNotValidMessage))
		}, timeout, interval).Should(Succeed())
	})

	It("should re-reconcile the WorkspaceClass when the ServiceTemplate status flips", func() {
		st := makeServiceTemplate("st-flipper")
		Expect(k8sClient.Create(ctx, st)).To(Succeed())
		patchSTValid(st, false, "not yet")

		wsc := makeClass("wsc-flipper", "st-flipper")
		Expect(k8sClient.Create(ctx, wsc)).To(Succeed())

		// Initially invalid
		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceClass{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsc), fetched)).To(Succeed())
			g.Expect(fetched.Status.Valid).To(BeFalse())
			g.Expect(fetched.Status.ValidationError).To(Equal("not yet"))
		}, timeout, interval).Should(Succeed())

		// Flip the ST to valid — secondary watch should pick this up
		patchSTValid(st, true, "")

		Eventually(func(g Gomega) {
			fetched := &workspacesv1.WorkspaceClass{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(wsc), fetched)).To(Succeed())
			g.Expect(fetched.Status.Valid).To(BeTrue())
			g.Expect(fetched.Status.ValidationError).To(BeEmpty())
		}, timeout, interval).Should(Succeed())
	})
})

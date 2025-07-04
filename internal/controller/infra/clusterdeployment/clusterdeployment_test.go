package clusterdeployment

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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

		cdSpec := &k0rdentv1beta1.ClusterDeploymentSpec{
			Template:   "test-template",
			Credential: "test-credential",
			Config: &apiextensionsv1.JSON{
				Raw: []byte(`{"test": "test"}`),
			},
		}
		cdSpecRaw, err := json.Marshal(cdSpec)
		Expect(err).NotTo(HaveOccurred())

		colonyCluster = &infrav1.ColonyCluster{
			ClusterName: "test-cluster",
			ClusterDeploymentSpec: &runtime.RawExtension{
				Raw: cdSpecRaw,
			},
		}

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

})

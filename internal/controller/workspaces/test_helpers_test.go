package workspaces

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// testGPUTypeH100 is the GPU model string shared across specs.
const testGPUTypeH100 = "H100"

func resourceQuantityPtr(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}

func int32Ptr(i int32) *int32 {
	return &i
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

// ensureChildKubeconfigSecret creates the `<cd-name>-kubeconfig` secret the
// controller uses to reach the child cluster — pointed at the envtest API
// server itself, so "child cluster" operations land in the same test cluster
// and can be asserted with k8sClient. Idempotent: specs sharing a CD name can
// call it freely.
func ensureChildKubeconfigSecret(cdName, namespace string) {
	GinkgoHelper()

	kc := clientcmdapi.NewConfig()
	kc.Clusters["envtest"] = &clientcmdapi.Cluster{
		Server:                   cfg.Host,
		CertificateAuthorityData: cfg.CAData,
	}
	kc.AuthInfos["envtest"] = &clientcmdapi.AuthInfo{
		ClientCertificateData: cfg.CertData,
		ClientKeyData:         cfg.KeyData,
		Token:                 cfg.BearerToken,
	}
	kc.Contexts["envtest"] = &clientcmdapi.Context{Cluster: "envtest", AuthInfo: "envtest"}
	kc.CurrentContext = "envtest"

	data, err := clientcmd.Write(*kc)
	Expect(err).NotTo(HaveOccurred())

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cdName + "-kubeconfig",
			Namespace: namespace,
		},
		Data: map[string][]byte{"value": data},
	}
	if err := k8sClient.Create(ctx, secret); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

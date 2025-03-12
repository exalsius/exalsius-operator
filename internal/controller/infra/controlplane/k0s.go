package controlplane

import (
	"context"

	bootstrapv1beta1 "github.com/k0sproject/k0smotron/api/bootstrap/v1beta1"
	k0sv1beta1 "github.com/k0sproject/k0smotron/api/controlplane/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// EnsureK0sControlPlane ensures that the K0sControlPlane CR exists.
// It creates the resource based on the provided Colony object.
func EnsureK0sControlPlane(ctx context.Context, c client.Client, colony *infrav1.Colony, scheme *runtime.Scheme) error {
	logger := log.FromContext(ctx)

	kcp := &k0sv1beta1.K0sControlPlane{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "controlplane.cluster.x-k8s.io/v1beta1",
			Kind:       "K0sControlPlane",
		},
		ObjectMeta: metav1.ObjectMeta{
			// Derive the name from the Colony spec (e.g. "aws-test")
			Name:      colony.Spec.ClusterName,
			Namespace: colony.Namespace,
		},
		Spec: k0sv1beta1.K0sControlPlaneSpec{
			Replicas:       colony.Spec.AWS.Replicas,
			Version:        colony.Spec.K8sVersion,
			UpdateStrategy: "Recreate",
			K0sConfigSpec: bootstrapv1beta1.K0sConfigSpec{
				Args: []string{"--enable-worker", "--no-taints"},
				K0s: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "k0s.k0sproject.io/v1beta1",
						"kind":       "ClusterConfig",
						"metadata": map[string]interface{}{
							"name": "k0s",
						},
						"spec": map[string]interface{}{
							"api": map[string]interface{}{
								"extraArgs": map[string]string{
									"anonymous-auth": "true",
								},
							},
							"telemetry": map[string]interface{}{
								"enabled": false,
							},
						},
					},
				},
			},
			MachineTemplate: &k0sv1beta1.K0sControlPlaneMachineTemplate{
				InfrastructureRef: corev1.ObjectReference{
					APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
					Kind:       "AWSMachineTemplate",
					Name:       colony.Spec.ClusterName + "-mt",
					Namespace:  colony.Namespace,
				},
			},
		},
	}

	// Check if the K0sControlPlane already exists.
	existing := &k0sv1beta1.K0sControlPlane{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: kcp.Namespace, Name: kcp.Name}, existing); err != nil {
		if errors.IsNotFound(err) {
			if err := c.Create(ctx, kcp); err != nil {
				logger.Error(err, "failed to create K0sControlPlane", "namespace", kcp.Namespace, "name", kcp.Name)
				return err
			}
			logger.Info("Created K0sControlPlane", "namespace", kcp.Namespace, "name", kcp.Name)
		} else {
			return err
		}
	} else {
		logger.Info("K0sControlPlane already exists", "namespace", kcp.Namespace, "name", kcp.Name)
	}

	return nil
}

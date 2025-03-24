package bootstrap

import (
	"context"

	bootstrapv1beta1 "github.com/k0sproject/k0smotron/api/bootstrap/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func EnsureK0sWorkerConfigTemplate(ctx context.Context, c client.Client, colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster, scheme *runtime.Scheme) error {
	log := log.FromContext(ctx)

	k0sWorkerConfigTemplate := &bootstrapv1beta1.K0sWorkerConfigTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "bootstrap.cluster.x-k8s.io/v1beta1",
			Kind:       "K0sWorkerConfigTemplate",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Name + "-" + colonyCluster.ClusterName + "-machine-config",
			Namespace: colony.Namespace,
		},
		Spec: bootstrapv1beta1.K0sWorkerConfigTemplateSpec{
			Template: bootstrapv1beta1.K0sWorkerConfigTemplateResource{
				Spec: bootstrapv1beta1.K0sWorkerConfigSpec{
					Version: colony.Spec.K8sVersion + "+k0s.0",
				},
			},
		},
	}

	existing := &bootstrapv1beta1.K0sWorkerConfigTemplate{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: k0sWorkerConfigTemplate.Namespace, Name: k0sWorkerConfigTemplate.Name}, existing); err != nil {
		if errors.IsNotFound(err) {
			if err := c.Create(ctx, k0sWorkerConfigTemplate); err != nil {
				log.Error(err, "failed to create K0sWorkerConfigTemplate", "Namespace", k0sWorkerConfigTemplate.Namespace, "Name", k0sWorkerConfigTemplate.Name)
				return err
			}
			log.Info("Created K0sWorkerConfigTemplate", "Namespace", k0sWorkerConfigTemplate.Namespace, "Name", k0sWorkerConfigTemplate.Name)
		} else {
			return err
		}
	} else {
		log.Info("K0sWorkerConfigTemplate already exists", "Namespace", k0sWorkerConfigTemplate.Namespace, "Name", k0sWorkerConfigTemplate.Name)
	}

	return nil
}

package controlplane

import (
	"context"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	k0sv1beta1 "github.com/k0sproject/k0smotron/api/controlplane/v1beta1"
	kmapi "github.com/k0sproject/k0smotron/api/k0smotron.io/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// EnsureK0smotronControlPlane ensures that a hosted K0smotron control plane exists in the management cluster.
func EnsureK0smotronControlPlane(ctx context.Context, c client.Client, colony *infrav1.Colony, scheme *runtime.Scheme) error {
	log := log.FromContext(ctx)

	k0smotronControlPlane := &k0sv1beta1.K0smotronControlPlane{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "controlplane.cluster.x-k8s.io/v1beta1",
			Kind:       "K0smotronControlPlane",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Spec.ClusterName + "-cp",
			Namespace: colony.Namespace,
		},
		Spec: kmapi.ClusterSpec{
			Version: colony.Spec.K8sVersion + "-k0s.0",
			Persistence: kmapi.PersistenceSpec{
				Type: "emptyDir",
			},
			Service: kmapi.ServiceSpec{
				Type: "NodePort",
			},
			Etcd: kmapi.EtcdSpec{
				AutoDeletePVCs: false,
				Image:          "quay.io/k0sproject/etcd:v3.5.13",
				Persistence: kmapi.EtcdPersistenceSpec{
					Size: resource.MustParse("1Gi"),
				},
			},
		},
	}

	existing := &k0sv1beta1.K0smotronControlPlane{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: k0smotronControlPlane.Namespace, Name: k0smotronControlPlane.Name}, existing); err != nil {
		if errors.IsNotFound(err) {
			if err := c.Create(ctx, k0smotronControlPlane); err != nil {
				log.Error(err, "Failed to create K0smotronControlPlane")
				return err
			}
			log.Info("Created K0smotronControlPlane", "Namespace", k0smotronControlPlane.Namespace, "Name", k0smotronControlPlane.Name)
		} else {
			return err
		}
	} else {
		log.Info("K0smotronControlPlane already exists", "Namespace", k0smotronControlPlane.Namespace, "Name", k0smotronControlPlane.Name)
	}

	return nil
}

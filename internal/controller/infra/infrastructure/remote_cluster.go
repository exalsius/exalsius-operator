package infrastructure

import (
	"context"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	k0sv1beta1 "github.com/k0sproject/k0smotron/api/infrastructure/v1beta1"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

func EnsureRemoteClusterResources(ctx context.Context, c client.Client, colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster, scheme *runtime.Scheme) error {
	log := log.FromContext(ctx)

	if err := ensureRemoteCluster(ctx, c, colony, colonyCluster); err != nil {
		log.Error(err, "Failed to ensure RemoteCluster")
		return err
	}

	return nil
}

func ensureRemoteCluster(ctx context.Context, c client.Client, colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster) error {
	log := log.FromContext(ctx)

	remoteCluster := &k0sv1beta1.RemoteCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Name + "-" + colonyCluster.ClusterName,
			Namespace: colony.Namespace,
		},
		Spec: k0sv1beta1.RemoteClusterSpec{},
	}

	existing := &k0sv1beta1.RemoteCluster{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: remoteCluster.Namespace, Name: remoteCluster.Name}, existing); err != nil {
		if errors.IsNotFound(err) {
			if err := c.Create(ctx, remoteCluster); err != nil {
				log.Error(err, "failed to create RemoteCluster", "Namespace", remoteCluster.Namespace, "Name", remoteCluster.Name)
				return err
			}
			log.Info("Created RemoteCluster", "Namespace", remoteCluster.Namespace, "Name", remoteCluster.Name)
		} else {
			return err
		}
	} else {
		log.Info("RemoteCluster already exists", "Namespace", remoteCluster.Namespace, "Name", remoteCluster.Name)
	}

	return nil
}

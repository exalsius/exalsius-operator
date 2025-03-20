package docker

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	capiresources "github.com/exalsius/exalsius-operator/internal/controller/infra/capi"
	capdv1beta1 "sigs.k8s.io/cluster-api/test/infrastructure/docker/api/v1beta1"
)

func EnsureDockerResources(ctx context.Context, c client.Client, colony *infrav1.Colony, scheme *runtime.Scheme) error {
	log := log.FromContext(ctx)

	if colony.Spec.Docker == nil {
		log.Info("No Docker configuration provided in Colony; skipping Docker resources creation")
		return nil
	}

	if err := capiresources.EnsureMachineDeployment(ctx, c, colony); err != nil {
		log.Error(err, "Failed to ensure MachineDeployment")
		return err
	}

	if err := ensureDockerMachineTemplate(ctx, c, colony); err != nil {
		log.Error(err, "Failed to ensure DockerMachineTemplate")
		return err
	}

	if err := ensureDockerCluster(ctx, c, colony); err != nil {
		log.Error(err, "Failed to ensure DockerCluster")
		return err
	}

	return nil
}

// ensureDockerMachineTemplate ensures that the DockerMachineTemplate CR is created.
func ensureDockerMachineTemplate(ctx context.Context, c client.Client, colony *infrav1.Colony) error {
	log := log.FromContext(ctx)

	dockerMT := &capdv1beta1.DockerMachineTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "infrastructure.cluster.x-k8s.io/v1beta1",
			Kind:       "DockerMachineTemplate",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Spec.ClusterName + "-mt",
			Namespace: colony.Namespace,
		},
		Spec: capdv1beta1.DockerMachineTemplateSpec{
			Template: capdv1beta1.DockerMachineTemplateResource{
				Spec: capdv1beta1.DockerMachineSpec{},
			},
		},
	}

	existing := &capdv1beta1.DockerMachineTemplate{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: dockerMT.Namespace, Name: dockerMT.Name}, existing); err != nil {
		if errors.IsNotFound(err) {
			if err := c.Create(ctx, dockerMT); err != nil {
				log.Error(err, "failed to create DockerMachineTemplate", "Namespace", dockerMT.Namespace, "Name", dockerMT.Name)
				return err
			}
			log.Info("Created DockerMachineTemplate", "Namespace", dockerMT.Namespace, "Name", dockerMT.Name)
		} else {
			return err
		}
	} else {
		log.Info("DockerMachineTemplate already exists", "Namespace", dockerMT.Namespace, "Name", dockerMT.Name)
	}

	return nil
}

func ensureDockerCluster(ctx context.Context, c client.Client, colony *infrav1.Colony) error {
	log := log.FromContext(ctx)

	dockerCluster := &capdv1beta1.DockerCluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "infrastructure.cluster.x-k8s.io/v1beta1",
			Kind:       "DockerCluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Spec.ClusterName,
			Namespace: colony.Namespace,
			Annotations: map[string]string{
				"cluster.x-k8s.io/managed-by": "k0smotron",
			},
		},
		Spec: capdv1beta1.DockerClusterSpec{},
	}

	existing := &capdv1beta1.DockerCluster{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: dockerCluster.Namespace, Name: dockerCluster.Name}, existing); err != nil {
		if errors.IsNotFound(err) {
			if err := c.Create(ctx, dockerCluster); err != nil {
				log.Error(err, "failed to create DockerCluster", "Namespace", dockerCluster.Namespace, "Name", dockerCluster.Name)
				return err
			}
			log.Info("Created DockerCluster", "Namespace", dockerCluster.Namespace, "Name", dockerCluster.Name)
		} else {
			return err
		}
	} else {
		log.Info("DockerCluster already exists", "Namespace", dockerCluster.Namespace, "Name", dockerCluster.Name)
	}

	return nil
}

package controlplane

import (
	"context"
	"fmt"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	k0sv1beta1 "github.com/k0sproject/k0smotron/api/controlplane/v1beta1"
	kmapi "github.com/k0sproject/k0smotron/api/k0smotron.io/v1beta1"
	"golang.org/x/exp/rand"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// EnsureK0smotronControlPlane ensures that a hosted K0smotron control plane exists in the management cluster.
func EnsureK0smotronControlPlane(ctx context.Context, c client.Client, colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster, scheme *runtime.Scheme) error {
	log := log.FromContext(ctx)

	apiPort, konnectivityPort, err := getAvailableNodePorts(ctx, c)
	if err != nil {
		return fmt.Errorf("failed to get available NodePort: %w", err)
	}

	var externalAddress string
	if colony.Spec.ExternalAddress != nil {
		externalAddress = *colony.Spec.ExternalAddress
	} else {
		externalAddress, err = getExternalAddress(ctx, c)
		if err != nil {
			return fmt.Errorf("failed to get external address: %w", err)
		}
	}

	k0smotronControlPlane := &k0sv1beta1.K0smotronControlPlane{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "controlplane.cluster.x-k8s.io/v1beta1",
			Kind:       "K0smotronControlPlane",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Name + "-" + colonyCluster.ClusterName + "-cp",
			Namespace: colony.Namespace,
		},
		Spec: kmapi.ClusterSpec{
			Version:         colony.Spec.K8sVersion + "-k0s.0",
			ExternalAddress: externalAddress,
			Persistence: kmapi.PersistenceSpec{
				Type: "emptyDir",
			},
			Service: kmapi.ServiceSpec{
				Type:             "NodePort",
				APIPort:          apiPort,
				KonnectivityPort: konnectivityPort,
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

// getAvailableNodePort returns an available NodePort from the default range (30000-32767)
func getAvailableNodePorts(ctx context.Context, c client.Client) (int, int, error) {
	// Get all services across all namespaces
	serviceList := &v1.ServiceList{}
	if err := c.List(ctx, serviceList); err != nil {
		return 0, 0, fmt.Errorf("failed to list services: %w", err)
	}

	// Create a map of used ports
	usedPorts := make(map[int]bool)
	for _, svc := range serviceList.Items {
		if svc.Spec.Type == v1.ServiceTypeNodePort || svc.Spec.Type == v1.ServiceTypeLoadBalancer {
			// Check NodePort in service ports
			for _, port := range svc.Spec.Ports {
				if port.NodePort != 0 {
					usedPorts[int(port.NodePort)] = true
				}
			}
		}
	}

	// get all available ports to be able to choose a random port from there
	availablePorts := make([]int, 0)
	for port := 30000; port <= 32767; port++ {
		if !usedPorts[port] {
			availablePorts = append(availablePorts, port)
		}
	}

	if len(availablePorts) < 2 {
		return 0, 0, fmt.Errorf("not enough available ports found in range 30000-32767")
	}

	// choose two random ports from the available ports
	// the problem here is that if we create a colony with multiple clusters,
	// those clusters cannot have the same ports. But when creating the colony, the respective
	// control planes and services are not created yet. Therefore, here we choose two random ports
	// and hope for the best.
	// TODO: find a better solution
	apiPort := availablePorts[rand.Intn(len(availablePorts))]
	konnectivityPort := availablePorts[rand.Intn(len(availablePorts))]

	return apiPort, konnectivityPort, nil
}

// getExternalAddress returns the external IP/hostname of the current cluster
func getExternalAddress(ctx context.Context, c client.Client) (string, error) {
	// Get all nodes
	nodeList := &v1.NodeList{}
	if err := c.List(ctx, nodeList); err != nil {
		return "", fmt.Errorf("failed to list nodes: %w", err)
	}

	// Look through node addresses to find ExternalIP or Hostname
	for _, node := range nodeList.Items {
		for _, addr := range node.Status.Addresses {
			// Prefer ExternalIP if available
			if addr.Type == v1.NodeExternalIP {
				return addr.Address, nil
			}
			// Fallback to Hostname if no ExternalIP found
			if addr.Type == v1.NodeHostName {
				return addr.Address, nil
			}
		}
	}

	return "", fmt.Errorf("no external address found for any node")
}

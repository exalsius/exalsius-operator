package aws

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	capiresources "github.com/exalsius/exalsius-operator/internal/controller/infra/capi"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capav1beta2 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
)

// EnsureAWSResources ensures that the AWS resources exist.
// It calls separate functions to create AWSMachineTemplate and AWSCluster.
func EnsureAWSResources(ctx context.Context, c client.Client, colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster, scheme *runtime.Scheme) error {
	log := log.FromContext(ctx)

	// Only proceed if the Colony spec has AWS configuration.
	if colonyCluster.AWS == nil {
		log.Info("No AWS configuration provided in ColonyCluster; skipping AWS resources creation")
		return nil
	}

	if err := ensureAWSMachineTemplate(ctx, c, colony, colonyCluster); err != nil {
		log.Error(err, "Failed to ensure AWSMachineTemplate")
		return err
	}

	if err := capiresources.EnsureMachineDeployment(ctx, c, colony, colonyCluster); err != nil {
		log.Error(err, "Failed to ensure MachineDeployment")
		return err
	}

	if err := ensureAWSCluster(ctx, c, colony, colonyCluster); err != nil {
		log.Error(err, "Failed to ensure AWSCluster")
		return err
	}

	return nil
}

// ensureAWSMachineTemplate creates the AWSMachineTemplate CR.
func ensureAWSMachineTemplate(ctx context.Context, c client.Client, colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster) error {
	log := log.FromContext(ctx)

	awsMT := &capav1beta2.AWSMachineTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
			Kind:       "AWSMachineTemplate",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Name + "-" + colonyCluster.ClusterName + "-mt",
			Namespace: colony.Namespace,
		},
		Spec: capav1beta2.AWSMachineTemplateSpec{
			Template: capav1beta2.AWSMachineTemplateResource{
				Spec: capav1beta2.AWSMachineSpec{
					AMI: capav1beta2.AMIReference{
						ID: &colonyCluster.AWS.AMI,
					},
					InstanceType:       colonyCluster.AWS.InstanceType,
					PublicIP:           ptr.To(true),
					IAMInstanceProfile: colonyCluster.AWS.IAMInstanceProfile, // e.g. "nodes.cluster-api-provider-aws.sigs.k8s.io"
					CloudInit: capav1beta2.CloudInit{
						InsecureSkipSecretsManager: true,
					},
					SSHKeyName: &colonyCluster.AWS.SSHKeyName, // e.g. "exalsius"
				},
			},
		},
	}

	// Check if the AWSMachineTemplate already exists.
	existing := &capav1beta2.AWSMachineTemplate{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: awsMT.Namespace, Name: awsMT.Name}, existing); err != nil {
		if errors.IsNotFound(err) {
			if err := c.Create(ctx, awsMT); err != nil {
				log.Error(err, "failed to create AWSMachineTemplate", "Namespace", awsMT.Namespace, "Name", awsMT.Name)
				return err
			}
			log.Info("Created AWSMachineTemplate", "Namespace", awsMT.Namespace, "Name", awsMT.Name)
		} else {
			return err
		}
	} else {
		log.Info("AWSMachineTemplate already exists", "Namespace", awsMT.Namespace, "Name", awsMT.Name)
	}

	return nil
}

// ensureAWSCluster creates the AWSCluster CR.
func ensureAWSCluster(ctx context.Context, c client.Client, colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster) error {
	log := log.FromContext(ctx)

	awsCluster := &capav1beta2.AWSCluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
			Kind:       "AWSCluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Name + "-" + colonyCluster.ClusterName,
			Namespace: colony.Namespace,
			Annotations: map[string]string{
				"cluster.x-k8s.io/managed-by": "exalsius-operator",
			},
		},
		Spec: capav1beta2.AWSClusterSpec{
			Region:     colonyCluster.AWS.Region,
			SSHKeyName: &colonyCluster.AWS.SSHKeyName,
		},
	}

	// Check if the AWSCluster already exists.
	existing := &capav1beta2.AWSCluster{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: awsCluster.Namespace, Name: awsCluster.Name}, existing); err != nil {
		if errors.IsNotFound(err) {
			if err := c.Create(ctx, awsCluster); err != nil {
				log.Error(err, "failed to create AWSCluster", "Namespace", awsCluster.Namespace, "Name", awsCluster.Name)
				return err
			}
			log.Info("Created AWSCluster", "Namespace", awsCluster.Namespace, "Name", awsCluster.Name)
		} else {
			return err
		}
	} else {
		log.Info("AWSCluster already exists", "Namespace", awsCluster.Namespace, "Name", awsCluster.Name)
	}

	return nil
}

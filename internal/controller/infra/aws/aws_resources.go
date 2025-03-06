package aws

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capav1beta2 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
)

// EnsureAWSResources ensures that the AWS resources exist.
// It calls separate functions to create AWSMachineTemplate and AWSCluster.
func EnsureAWSResources(ctx context.Context, c client.Client, colony *infrav1.Colony, scheme *runtime.Scheme) error {
	log := log.FromContext(ctx)

	// Only proceed if the Colony spec has AWS configuration.
	if colony.Spec.AWS == nil {
		log.Info("No AWS configuration provided in Colony; skipping AWS resources creation")
		return nil
	}

	if err := ensureAWSMachineTemplate(ctx, c, colony, scheme); err != nil {
		log.Error(err, "Failed to ensure AWSMachineTemplate")
		return err
	}

	if err := ensureAWSCluster(ctx, c, colony, scheme); err != nil {
		log.Error(err, "Failed to ensure AWSCluster")
		return err
	}

	return nil
}

// ensureAWSMachineTemplate creates the AWSMachineTemplate CR.
func ensureAWSMachineTemplate(ctx context.Context, c client.Client, colony *infrav1.Colony, scheme *runtime.Scheme) error {
	log := log.FromContext(ctx)

	awsMT := &capav1beta2.AWSMachineTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
			Kind:       "AWSMachineTemplate",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Spec.ClusterName + "-mt",
			Namespace: colony.Namespace,
		},
		Spec: capav1beta2.AWSMachineTemplateSpec{
			Template: capav1beta2.AWSMachineTemplateResource{
				Spec: capav1beta2.AWSMachineSpec{
					UncompressedUserData: ptr.To(false),
					AMI: capav1beta2.AMIReference{
						ID: &colony.Spec.AWS.AMI,
					},
					InstanceType:       colony.Spec.AWS.InstanceType,
					PublicIP:           ptr.To(true),
					IAMInstanceProfile: colony.Spec.AWS.IAMInstanceProfile, // e.g. "nodes.cluster-api-provider-aws.sigs.k8s.io"
					CloudInit: capav1beta2.CloudInit{
						InsecureSkipSecretsManager: true,
					},
					SSHKeyName: &colony.Spec.AWS.SSHKeyName, // e.g. "exalsius"
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
func ensureAWSCluster(ctx context.Context, c client.Client, colony *infrav1.Colony, scheme *runtime.Scheme) error {
	log := log.FromContext(ctx)

	protocol := capav1beta2.ELBProtocolTCP

	awsCluster := &capav1beta2.AWSCluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
			Kind:       "AWSCluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Spec.ClusterName,
			Namespace: colony.Namespace,
		},
		Spec: capav1beta2.AWSClusterSpec{
			Region:     colony.Spec.AWS.Region,      // e.g. "eu-central-1"
			SSHKeyName: &colony.Spec.AWS.SSHKeyName, // e.g. "exalsius"
			ControlPlaneLoadBalancer: &capav1beta2.AWSLoadBalancerSpec{
				HealthCheckProtocol: &protocol,
			},
			NetworkSpec: capav1beta2.NetworkSpec{
				AdditionalControlPlaneIngressRules: []capav1beta2.IngressRule{
					{
						Description: "k0s controller join API",
						Protocol:    "tcp",
						FromPort:    9443,
						ToPort:      9443,
					},
				},
			},
		},
	}

	// Set Colony as the owner of the AWSCluster.
	//if err := controllerutil.SetControllerReference(colony, awsCluster, scheme); err != nil {
	//	return err
	//}

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

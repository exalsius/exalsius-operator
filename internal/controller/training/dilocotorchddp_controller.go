/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package training

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	trainingv1 "github.com/exalsius/exalsius-operator/api/training/v1"
	vol "volcano.sh/apis/pkg/apis/batch/v1alpha1"
)

// DilocoTorchDDPReconciler reconciles a DilocoTorchDDP object
type DilocoTorchDDPReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// getClientForTargetCluster gets a client for the specified cluster
func (r *DilocoTorchDDPReconciler) getClientForTargetCluster(ctx context.Context, colonyName, colonyNamespace, clusterName string) (client.Client, error) {
	log := log.FromContext(ctx)

	var colony infrav1.Colony
	if err := r.Get(ctx, client.ObjectKey{Name: colonyName, Namespace: colonyNamespace}, &colony); err != nil {
		if errors.IsNotFound(err) {
			log.Error(err, "Colony not found")
			return nil, err
		}
		log.Error(err, "Failed to get Colony")
		return nil, err
	}

	if len(colony.Status.ClusterRefs) == 0 {
		return nil, fmt.Errorf("no clusters found in colony %q", colonyName)
	}

	aggregatedKubeconfigSecretName := colonyName + "-kubeconfigs"
	var aggregatedSecret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Name: aggregatedKubeconfigSecretName, Namespace: colonyNamespace}, &aggregatedSecret); err != nil {
		return nil, fmt.Errorf("failed to get aggregated kubeconfig secret %q: %w", aggregatedKubeconfigSecretName, err)
	}

	refBytes, ok := aggregatedSecret.Data[clusterName]
	if !ok {
		return nil, fmt.Errorf("no kubeconfig secret found for cluster %q", clusterName)
	}

	var objRef corev1.ObjectReference
	if err := json.Unmarshal(refBytes, &objRef); err != nil {
		return nil, fmt.Errorf("failed to unmarshal object reference for cluster %q: %w", clusterName, err)
	}

	var kubeconfigSecret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Name: objRef.Name, Namespace: objRef.Namespace}, &kubeconfigSecret); err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig secret %q for cluster %q: %w", objRef.Name, clusterName, err)
	}

	kubeconfigBytes, ok := kubeconfigSecret.Data["value"]
	if !ok {
		return nil, fmt.Errorf("kubeconfig secret %q does not contain key 'value'", objRef.Name)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config for cluster %q: %w", clusterName, err)
	}

	clusterClient, err := client.New(restConfig, client.Options{Scheme: r.Scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create client for cluster %q: %w", clusterName, err)
	}

	return clusterClient, nil
}

// +kubebuilder:rbac:groups=training.exalsius.ai,resources=dilocotorchddps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=training.exalsius.ai,resources=dilocotorchddps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=training.exalsius.ai,resources=dilocotorchddps/finalizers,verbs=update

func (r *DilocoTorchDDPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var training trainingv1.DilocoTorchDDP
	if err := r.Get(ctx, req.NamespacedName, &training); err != nil {
		if errors.IsNotFound(err) {
			log.Info("DilocoTorchDDP resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get DilocoTorchDDP")
		return ctrl.Result{}, err
	}

	// Determine which colony to use for the training
	var targetClient client.Client
	if training.Spec.TargetColony != nil {
		var err error

		// check if a colony with the given name exists by checking if a Colony CR exists
		colony := &infrav1.Colony{}
		if err := r.Get(ctx, client.ObjectKey{Name: *training.Spec.TargetColony, Namespace: training.Namespace}, colony); err != nil {
			if errors.IsNotFound(err) {
				log.Error(err, "No colony found with name %s", "colony", *training.Spec.TargetColony)
				return ctrl.Result{}, err
			}
			log.Error(err, "Failed to get Colony")
			return ctrl.Result{}, err
		}

		// TODO: Currently a colony consists of a single cluster, but in the future we will support multiple clusters
		if len(colony.Status.ClusterRefs) == 0 {
			log.Error(fmt.Errorf("no clusters found in colony %q", colony.Name), "No colony found with name %s", "colony", *training.Spec.TargetColony)
			return ctrl.Result{}, fmt.Errorf("no clusters found in colony %q", colony.Name)
		}

		clusterName := colony.Status.ClusterRefs[0].Name

		targetClient, err = r.getClientForTargetCluster(ctx, colony.Name, colony.Namespace, clusterName)
		if err != nil {
			log.Error(err, "Failed to get cluster client")
			return ctrl.Result{}, err
		}
	} else {
		targetClient = r.Client
	}

	// Create the Volcano Job for the training.
	if err := r.ensureDilocoTrainingVolcanoJob(ctx, &training, targetClient); err != nil {
		log.Error(err, "Failed to ensure diloco training Volcano Job")
		return ctrl.Result{}, err
	}

	// Update the CR status based on the Volcano Job status.
	if err := r.updateCRStatusFromVolcanoJob(ctx, &training, targetClient); err != nil {
		log.Error(err, "Failed to update CR status from Volcano Job")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	log.Info("DilocoTorchDDP reconciled successfully")

	return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
}

// ensureDilocoTrainingVolcanoJob creates a Volcano Job for distributed PyTorch training.
func (r *DilocoTorchDDPReconciler) ensureDilocoTrainingVolcanoJob(ctx context.Context, training *trainingv1.DilocoTorchDDP, targetClient client.Client) error {
	jobName := fmt.Sprintf("diloco-job-%s", training.Name)
	namespace := training.Namespace
	log := log.FromContext(ctx)

	var existingJob vol.Job
	if err := targetClient.Get(ctx, client.ObjectKey{Name: jobName, Namespace: namespace}, &existingJob); err == nil {
		log.Info("Volcano Job already exists", "Job.Name", jobName)
		return nil
	} else if !errors.IsNotFound(err) {
		log.Error(err, "Failed to get Volcano Job")
		return err
	}

	// Define replica counts for master and worker tasks.
	masterReplicas := int32(1)
	workerReplicas := training.Spec.Parallelism - 1

	// Use the image and WANDB API key from the training resource.
	image := training.Spec.Image
	wanDBKey := training.Spec.WandBAPIKey

	nprocPerNode := training.Spec.NProcPerNode
	if nprocPerNode == 0 {
		nprocPerNode = 1
	}

	scriptArgs := append([]string{training.Spec.ScriptPath}, training.Spec.Args...)
	joinedArgs := strings.Join(scriptArgs, " ")
	cmd := fmt.Sprintf(`torchrun --nproc_per_node=%d %s`, nprocPerNode, joinedArgs)

	runtimeClassName := "nvidia"

	// Create the Volcano Job object.
	volJob := &vol.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch.volcano.sh/v1alpha1",
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
			Labels:    map[string]string{"job-name": jobName},
		},
		Spec: vol.JobSpec{
			MinAvailable:  training.Spec.Parallelism,
			SchedulerName: "volcano",
			Plugins: map[string][]string{
				"pytorch": {"--master=master", "--worker=worker", "--port=23456"},
			},
			Tasks: []vol.TaskSpec{
				{
					Name:     "master",
					Replicas: masterReplicas,
					Policies: []vol.LifecyclePolicy{
						{
							Event:  "TaskCompleted",
							Action: "CompleteJob",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"job-name": jobName, "role": "master"},
							Annotations: map[string]string{
								"scheduling.k8s.io/group-name": jobName,
							},
						},
						Spec: corev1.PodSpec{
							RestartPolicy:    corev1.RestartPolicyOnFailure,
							RuntimeClassName: &runtimeClassName,
							Containers: []corev1.Container{
								{
									Name:            "master",
									Image:           image,
									ImagePullPolicy: corev1.PullAlways,
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											"nvidia.com/gpu": *resource.NewQuantity(int64(training.Spec.NProcPerNode), resource.DecimalSI),
										},
									},
									Env: []corev1.EnvVar{
										{
											Name:  "WANDB_API_KEY",
											Value: wanDBKey,
										},
									},
									Command: []string{"/bin/sh", "-c"},
									Args: []string{
										// Note: The PyTorch plugin in Volcano will inject necessary env variables.
										cmd,
									},
								},
							},
						},
					},
				},
				{
					Name:     "worker",
					Replicas: workerReplicas,
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"job-name": jobName, "role": "worker"},
							Annotations: map[string]string{
								"scheduling.k8s.io/group-name": jobName,
							},
						},
						Spec: corev1.PodSpec{
							RestartPolicy:    corev1.RestartPolicyOnFailure,
							RuntimeClassName: &runtimeClassName,
							Containers: []corev1.Container{
								{
									Name:            "worker",
									Image:           image,
									ImagePullPolicy: corev1.PullAlways,
									WorkingDir:      "/app",
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											"nvidia.com/gpu": *resource.NewQuantity(int64(training.Spec.NProcPerNode), resource.DecimalSI),
										},
									},
									Env: []corev1.EnvVar{
										{
											Name:  "WANDB_API_KEY",
											Value: wanDBKey,
										},
									},
									Command: []string{"/bin/sh", "-c"},
									Args: []string{
										cmd,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	if err := controllerutil.SetControllerReference(training, volJob, r.Scheme); err != nil {
		return err
	}

	if err := targetClient.Create(ctx, volJob); err != nil {
		log.Error(err, "Failed to create Volcano Job")
		return err
	}

	log.Info("Created new Volcano Job", "Job.Name", jobName)

	return nil
}

func (r *DilocoTorchDDPReconciler) updateCRStatusFromVolcanoJob(ctx context.Context, training *trainingv1.DilocoTorchDDP, targetClient client.Client) error {
	jobName := fmt.Sprintf("diloco-job-%s", training.Name)
	namespace := training.Namespace
	log := log.FromContext(ctx)

	var volJob vol.Job
	if err := targetClient.Get(ctx, client.ObjectKey{Name: jobName, Namespace: namespace}, &volJob); err != nil {
		return err
	}

	// Update the CR status directly with the Volcano Job's phase
	if training.Status.Phase != trainingv1.JobPhase(volJob.Status.State.Phase) {
		log.Info("Updating CR status", "Phase", volJob.Status.State.Phase)
		training.Status.Phase = trainingv1.JobPhase(volJob.Status.State.Phase)
		if err := r.Status().Update(ctx, training); err != nil {
			return err
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DilocoTorchDDPReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&trainingv1.DilocoTorchDDP{}).
		Named("dilocotorchddp").
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Complete(r)
}

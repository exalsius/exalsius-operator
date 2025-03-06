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
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	trainingv1 "github.com/exalsius/exalsius-operator/api/training/v1"
	vol "volcano.sh/apis/pkg/apis/batch/v1alpha1"
)

// DilocoTorchDDPReconciler reconciles a DilocoTorchDDP object
type DilocoTorchDDPReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=training.exalsius.ai,resources=dilocotorchddps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=training.exalsius.ai,resources=dilocotorchddps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=training.exalsius.ai,resources=dilocotorchddps/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DilocoTorchDDP object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.0/pkg/reconcile
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

	// Create the Volcano Job for the training.
	if err := r.ensureDilocoTrainingVolcanoJob(ctx, &training); err != nil {
		log.Error(err, "Failed to ensure diloco training Volcano Job")
		return ctrl.Result{}, err
	}

	// Update the CR status based on the Volcano Job status.
	if err := r.updateCRStatusFromVolcanoJob(ctx, &training); err != nil {
		log.Error(err, "Failed to update CR status from Volcano Job")
		return ctrl.Result{}, err
	}

	log.Info("DilocoTorchDDP reconciled successfully")

	return ctrl.Result{}, nil
}

// ensureDilocoTrainingVolcanoJob creates a Volcano Job for distributed PyTorch training.
func (r *DilocoTorchDDPReconciler) ensureDilocoTrainingVolcanoJob(ctx context.Context, training *trainingv1.DilocoTorchDDP) error {
	jobName := fmt.Sprintf("diloco-job-%s", training.Name)
	namespace := training.Namespace
	log := log.FromContext(ctx)

	var existingJob vol.Job
	if err := r.Get(ctx, client.ObjectKey{Name: jobName, Namespace: namespace}, &existingJob); err == nil {
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

	if err := r.Create(ctx, volJob); err != nil {
		log.Error(err, "Failed to create Volcano Job")
		return err
	}

	log.Info("Created new Volcano Job", "Job.Name", jobName)

	return nil
}

func (r *DilocoTorchDDPReconciler) updateCRStatusFromVolcanoJob(ctx context.Context, training *trainingv1.DilocoTorchDDP) error {
	jobName := fmt.Sprintf("diloco-job-%s", training.Name)
	namespace := training.Namespace
	log := log.FromContext(ctx)

	var volJob vol.Job
	if err := r.Get(ctx, client.ObjectKey{Name: jobName, Namespace: namespace}, &volJob); err != nil {
		return err
	}

	// Determine a phase based on the Volcano Job's state
	var phase string
	switch volJob.Status.State.Phase {
	case vol.Completed:
		phase = "Succeeded"
	case vol.Failed, vol.Terminated:
		phase = "Failed"
	case vol.Running:
		phase = "Running"
	case vol.Pending:
		phase = "Pending"
	default:
		phase = string(volJob.Status.State.Phase)
	}

	// Update the CR status if the phase has changed.
	if training.Status.Status != phase {
		log.Info("Updating CR status", "Phase", phase)
		training.Status.Status = phase
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

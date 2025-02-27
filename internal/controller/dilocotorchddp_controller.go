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

package controller

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	trainingv1 "github.com/exalsius/exalsius-operator/api/v1"
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

	// ensure the headless service exists
	if err := r.ensureHeadlessService(ctx, &training); err != nil {
		log.Error(err, "Failed to ensure headless service")
		return ctrl.Result{}, err
	}

	// ensure the diloco training job exists
	if err := r.ensureDilocoTrainingJob(ctx, &training); err != nil {
		log.Error(err, "Failed to ensure diloco training job")
		return ctrl.Result{}, err
	}

	log.Info("DilocoTorchDDP reconciled successfully")

	return ctrl.Result{}, nil
}

func (r *DilocoTorchDDPReconciler) ensureHeadlessService(ctx context.Context, training *trainingv1.DilocoTorchDDP) error {
	log := log.FromContext(ctx)
	serviceName := fmt.Sprintf("%s-worker", training.Name)
	namespace := training.Namespace

	// Check if the service already exists
	var existingService corev1.Service
	err := r.Get(ctx, client.ObjectKey{Name: serviceName, Namespace: namespace}, &existingService)
	if err == nil {
		log.Info("Headless service already exists", "Service.Name", serviceName)
		return nil
	} else if !errors.IsNotFound(err) {
		log.Error(err, "Failed to get headless service")
		return err
	}

	// Create the headless service
	jobName := fmt.Sprintf("diloco-job-%s", training.Name)
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
			Labels:    map[string]string{"job-name": jobName},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None", // Headless service
			Selector:  map[string]string{"job-name": jobName},
			Ports: []corev1.ServicePort{
				{
					Protocol: corev1.ProtocolTCP,
					Port:     29500,
					TargetPort: intstr.IntOrString{
						IntVal: 29500,
					},
				},
			},
		},
	}

	// Set owner reference so that the Service is deleted when the CR is deleted.
	if err := controllerutil.SetControllerReference(training, service, r.Scheme); err != nil {
		return err
	}

	if err := r.Create(ctx, service); err != nil {
		log.Error(err, "Failed to create headless service")
		return err
	}

	log.Info("Created headless service", "Service.Name", serviceName)
	return nil
}

// ensurePytorchJob ensures the training job is running
func (r *DilocoTorchDDPReconciler) ensureDilocoTrainingJob(ctx context.Context, training *trainingv1.DilocoTorchDDP) error {
	jobName := fmt.Sprintf("diloco-job-%s", training.Name)
	namespace := training.Namespace
	log := log.FromContext(ctx)

	// Check if the Job already exists
	var existingJob batchv1.Job
	err := r.Get(ctx, client.ObjectKey{Name: jobName, Namespace: namespace}, &existingJob)
	if err == nil {
		log.Info("Diloco Job already exists", "Job.Name", jobName)
		return nil
	} else if !errors.IsNotFound(err) {
		log.Error(err, "Failed to get Job")
		return err
	}

	// Default values
	nprocPerNode := training.Spec.NProcPerNode
	if nprocPerNode == 0 {
		nprocPerNode = 1
	}

	replicas := training.Spec.Parallelism

	// currenty hardcoded to nvidia
	runtimeClassName := "nvidia"

	// Construct script arguments
	scriptArgs := append([]string{training.Spec.ScriptPath}, training.Spec.Args...)
	joinedArgs := strings.Join(scriptArgs, " ")

	// The StatefulSet pods will have stable names: stsName-0, stsName-1, etc.
	// The headless service (named "<training.Name>-worker") allows DNS resolution:
	// e.g. stsName-0.<training.Name>-worker.default.svc.cluster.local resolves to the master pod.
	// In the startup script, if the pod's hostname ends with "-0", it acts as the RDZV master.
	startupScript := fmt.Sprintf(`
if [ "$JOB_COMPLETION_INDEX" = "0" ]; then 
    RDZV_ENDPOINT=$(hostname -i); 
else 
    RDZV_ENDPOINT=$(getent hosts %s-worker.default.svc.cluster.local | awk '{print $1}' | head -n 1); 
fi; 
echo "Using RDZV endpoint: $RDZV_ENDPOINT"; 
torchrun --nnodes=%d --nproc_per_node=%d --rdzv_id=42 --rdzv_backend=c10d --rdzv_endpoint=$RDZV_ENDPOINT:29500 %s
`, training.Name, training.Spec.Parallelism, nprocPerNode, joinedArgs)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
			Labels:    map[string]string{"job-name": jobName},
		},
		Spec: batchv1.JobSpec{
			Completions:    &replicas,
			Parallelism:    &replicas,
			CompletionMode: func() *batchv1.CompletionMode { mode := batchv1.IndexedCompletion; return &mode }(),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"job-name": jobName},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:    corev1.RestartPolicyNever,
					RuntimeClassName: &runtimeClassName,
					Containers: []corev1.Container{
						{
							Name:    "diloco-torch",
							Image:   training.Spec.Image,
							Command: []string{"/bin/sh", "-c", startupScript},
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 29500,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									"nvidia.com/gpu": *resource.NewQuantity(int64(nprocPerNode), resource.DecimalSI),
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "WANDB_API_KEY",
									Value: training.Spec.WandBAPIKey,
								},
							},
						},
					},
				},
			},
		},
	}

	// Set owner reference so that the Service is deleted when the CR is deleted.
	if err := controllerutil.SetControllerReference(training, job, r.Scheme); err != nil {
		return err
	}

	if err := r.Create(ctx, job); err != nil {
		log.Error(err, "Failed to create Job")
		return err
	}

	log.Info("Created new Diloco PyTorch Job", "Job.Name", jobName)
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

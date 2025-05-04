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

package infra

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k0rdentv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
	clusterdeployment "github.com/exalsius/exalsius-operator/internal/controller/infra/clusterdeployment"
)

const (
	colonyFinalizer = "colony.infra.exalsius.ai/finalizer"
)

// ColonyReconciler reconciles a Colony object
type ColonyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=infra.exalsius.ai,resources=colonies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infra.exalsius.ai,resources=colonies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infra.exalsius.ai,resources=colonies/finalizers,verbs=update
func (r *ColonyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	colony := &infrav1.Colony{}
	if err := r.Get(ctx, req.NamespacedName, colony); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if colony.GetDeletionTimestamp() != nil {
		log.Info("Colony marked for deletion. Shutting down cluster resources and removing finalizer.")
		colony.Status.Phase = "Deleting"
		if err := r.Status().Update(ctx, colony); err != nil {
			log.Error(err, "Failed to update Colony status to deleting")
			return ctrl.Result{}, err
		}
		for _, colonyCluster := range colony.Spec.ColonyClusters {
			if err := r.cleanupAssociatedResources(ctx, colony, &colonyCluster); err != nil {
				log.Error(err, "Failed to cleanup associated cluster resources for ColonyCluster", "ColonyCluster.Name", colonyCluster.ClusterName)
				return ctrl.Result{}, err
			}
		}
		if err := r.Get(ctx, req.NamespacedName, colony); err != nil {
			log.Error(err, "Failed to refetch Colony before removing finalizer")
			return ctrl.Result{}, err
		}
		controllerutil.RemoveFinalizer(colony, colonyFinalizer)
		if err := r.Update(ctx, colony); err != nil {
			log.Error(err, "Failed to remove finalizer from Colony")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(colony, colonyFinalizer) {
		log.Info("Adding finalizer to Colony", "Colony.Namespace", colony.Namespace, "Colony.Name", colony.Name)
		controllerutil.AddFinalizer(colony, colonyFinalizer)
		if err := r.Update(ctx, colony); err != nil {
			log.Error(err, "Failed to add finalizer to Colony", "Colony.Namespace", colony.Namespace, "Colony.Name", colony.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	for _, colonyCluster := range colony.Spec.ColonyClusters {
		if colonyCluster.ClusterDeploymentSpec != nil {
			if err := clusterdeployment.EnsureClusterDeployment(ctx, r.Client, colony, &colonyCluster, r.Scheme); err != nil {
				log.Error(err, "Failed to ensure cluster deployment")
				return ctrl.Result{}, err
			}
		}

	}

	if err := r.updateColonyStatusFromClusters(ctx, colony); err != nil {
		log.Error(err, "Failed to update Colony status from clusters")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	// if not all clusters are ready, requeue sooner
	if colony.Status.ReadyClusters != colony.Status.TotalClusters {
		log.Info("Not all clusters are ready, requeueing",
			"ready", colony.Status.ReadyClusters,
			"total", colony.Status.TotalClusters,
		)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	if result, err := r.ensureAggregatedKubeconfigSecretExists(ctx, colony); err != nil {
		log.Error(err, "Failed to ensure aggregated kubeconfig secret")
		return result, err
	} else if result.Requeue || result.RequeueAfter > 0 {
		return result, nil
	}

	log.Info("Colony reconciled successfully")

	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func (r *ColonyReconciler) ensureAggregatedKubeconfigSecretExists(ctx context.Context, colony *infrav1.Colony) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	references := make(map[string][]byte)

	// iterate over all clusters in the colony
	for _, clusterDeploymentRef := range colony.Status.ClusterDeploymentRefs {
		kubeconfigSecretName := fmt.Sprintf("%s-kubeconfig", clusterDeploymentRef.Name)

		var kubeconfigSecret corev1.Secret
		if err := r.Get(ctx, client.ObjectKey{Namespace: clusterDeploymentRef.Namespace, Name: kubeconfigSecretName}, &kubeconfigSecret); err != nil {
			if errors.IsNotFound(err) {
				log.Info("Kubeconfig secret not found for cluster, will retry", "cluster", fmt.Sprintf("%s/%s", clusterDeploymentRef.Namespace, clusterDeploymentRef.Name))
				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			} else {
				log.Error(err, "Failed to get Kubeconfig secret for cluster", "cluster", fmt.Sprintf("%s/%s", clusterDeploymentRef.Namespace, clusterDeploymentRef.Name))
				return ctrl.Result{}, err
			}
		}

		// Create an ObjectReference for the kubeconfig secret.
		objRef := corev1.ObjectReference{
			APIVersion: kubeconfigSecret.APIVersion,
			Kind:       kubeconfigSecret.Kind,
			Namespace:  kubeconfigSecret.Namespace,
			Name:       kubeconfigSecret.Name,
		}

		// JSON-encode the ObjectReference.
		refBytes, err := json.Marshal(objRef)
		if err != nil {
			log.Error(err, "Failed to marshal object reference", "cluster", clusterDeploymentRef.Name)
			return ctrl.Result{}, err
		}
		references[clusterDeploymentRef.Name] = refBytes

	}

	aggregatedSecretName := colony.Name + "-kubeconfigs"
	aggregatedSecretNamespace := colony.Namespace

	var aggregatedKubeconfigSecret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: aggregatedSecretNamespace, Name: aggregatedSecretName}, &aggregatedKubeconfigSecret); err != nil {
		if errors.IsNotFound(err) {
			// The secret doesn't exist, so create it.
			log.Info("Aggregated kubeconfig secret not found. Creating...")
			aggregatedKubeconfigSecret = corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      aggregatedSecretName,
					Namespace: aggregatedSecretNamespace,
				},
				Data: references,
			}
			if err := r.Create(ctx, &aggregatedKubeconfigSecret); err != nil {
				log.Error(err, "Failed to create aggregated kubeconfig secret")
				return ctrl.Result{}, err
			}
			log.Info("Aggregated kubeconfig secret created",
				"namespace", aggregatedSecretNamespace,
				"name", aggregatedSecretName,
			)
			return ctrl.Result{}, nil
		} else {
			log.Error(err, "Failed to get aggregated kubeconfig secret")
			return ctrl.Result{}, err
		}
	} else {
		// The secret exists, update its Data field.
		aggregatedKubeconfigSecret.Data = references
		if err := r.Update(ctx, &aggregatedKubeconfigSecret); err != nil {
			log.Error(err, "Failed to update aggregated kubeconfig secret")
			return ctrl.Result{}, err
		}
		log.Info("Aggregated kubeconfig secret updated",
			"namespace", aggregatedKubeconfigSecret.Namespace,
			"name", aggregatedKubeconfigSecret.Name)
	}

	// set owner reference to the colony
	if err := ctrl.SetControllerReference(colony, &aggregatedKubeconfigSecret, r.Scheme); err != nil {
		log.Error(err, "Failed to set owner reference to the aggregated kubeconfig secret")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ColonyReconciler) cleanupAssociatedResources(ctx context.Context, colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster) error {
	log := log.FromContext(ctx)
	// Delete the ClusterDeployment object which will trigger the deletion of all associated resources
	clusterDeployment := &k0rdentv1alpha1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Name + "-" + colonyCluster.ClusterName,
			Namespace: colony.Namespace,
		},
	}
	if err := r.Client.Delete(ctx, clusterDeployment); err != nil && !errors.IsNotFound(err) {
		log.Error(err, "Failed to delete ClusterDeployment", "ClusterDeployment.Namespace", clusterDeployment.Namespace, "ClusterDeployment.Name", clusterDeployment.Name)
		return err
	}

	// wait for the cluster to be deleted
	if err := r.waitForClusterDeletion(ctx, clusterDeployment, 10*time.Minute, 10*time.Second); err != nil {
		log.Error(err, "Failed to wait for Cluster deletion")
		return err
	}
	return nil
}

// waitForDeletion polls until the given Cluster is no longer found.
func (r *ColonyReconciler) waitForClusterDeletion(ctx context.Context, clusterDeployment *k0rdentv1alpha1.ClusterDeployment, timeout, interval time.Duration) error {
	log := log.FromContext(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	timeoutCh := time.After(timeout)
	for {
		select {
		case <-timeoutCh:
			return fmt.Errorf("timeout waiting for cluster %s deletion", clusterDeployment.Name)
		case <-ticker.C:
			temp := &k0rdentv1alpha1.ClusterDeployment{}
			err := r.Client.Get(ctx, client.ObjectKeyFromObject(clusterDeployment), temp)
			if errors.IsNotFound(err) {
				log.Info("ClusterDeployment deleted", "ClusterDeployment.Namespace", clusterDeployment.Namespace, "ClusterDeployment.Name", clusterDeployment.Name)
				return nil
			}
			log.Info("Waiting for cluster deletion", "ClusterDeployment.Namespace", clusterDeployment.Namespace, "ClusterDeployment.Name", clusterDeployment.Name)
		}
	}
}

// updateColonyStatusFromClusters checks all clusters referenced in the Colony status
// and updates the Colony status with a ClusterReady condition that is true only if
// all referenced Clusters have a ready condition.
func (r *ColonyReconciler) updateColonyStatusFromClusters(ctx context.Context, colony *infrav1.Colony) error {
	log := log.FromContext(ctx)
	allReady := true
	var notReadyClusters []string

	orig := colony.DeepCopy()

	for _, ref := range colony.Status.ClusterDeploymentRefs {
		clusterDeployment := &k0rdentv1alpha1.ClusterDeployment{}
		key := client.ObjectKey{
			Namespace: ref.Namespace,
			Name:      ref.Name,
		}
		if err := r.Client.Get(ctx, key, clusterDeployment); err != nil {
			if errors.IsNotFound(err) {
				log.Info("ClusterDeployment not found", "clusterDeployment", fmt.Sprintf("%s/%s", ref.Namespace, ref.Name))
				notReadyClusters = append(notReadyClusters, fmt.Sprintf("%s/%s (not found)", ref.Namespace, ref.Name))
				allReady = false
				continue
			}
			return err
		}

		clusterDeploymentConditions := clusterDeployment.GetConditions()
		for _, condition := range *clusterDeploymentConditions {
			if condition.Type == k0rdentv1alpha1.ReadyCondition && condition.Status == metav1.ConditionFalse {
				allReady = false
				notReadyClusters = append(notReadyClusters, fmt.Sprintf("%s/%s", ref.Namespace, ref.Name))
			}
		}
	}

	colony.Status.TotalClusters = int32(len(colony.Status.ClusterDeploymentRefs))
	colony.Status.ReadyClusters = int32(len(colony.Status.ClusterDeploymentRefs) - len(notReadyClusters))

	var condition metav1.Condition
	if allReady {
		colony.Status.Phase = "Ready"
		condition = metav1.Condition{
			Type:    "ClustersReady",
			Status:  metav1.ConditionTrue,
			Reason:  "AllClustersReady",
			Message: fmt.Sprintf("All %d clusters of the colony are ready", len(colony.Status.ClusterDeploymentRefs)),
		}
	} else {
		colony.Status.Phase = "Provisioning"
		condition = metav1.Condition{
			Type:    "ClustersReady",
			Status:  metav1.ConditionFalse,
			Reason:  "NotAllClustersReady",
			Message: fmt.Sprintf("%d out of %d clusters are ready. Not ready clusters: %v", colony.Status.ReadyClusters, colony.Status.TotalClusters, notReadyClusters),
		}
	}

	meta.SetStatusCondition(&colony.Status.Conditions, condition)

	if err := r.Client.Status().Patch(ctx, colony, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("failed to update Colony status: %w", err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ColonyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.Colony{}).
		Named("infra-colony").
		Complete(r)
}

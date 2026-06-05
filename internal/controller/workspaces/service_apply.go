package workspaces

import (
	"context"
	"fmt"
	"time"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// defaultStateManagementProvider is the well-known k0rdent StateManagementProvider
	// name used by all ServiceSets. K0rdent creates this as part of its Management setup.
	defaultStateManagementProvider = "ksm-projectsveltos"

	// defaultDeployTimeout is the default Helm wait timeout for workspace deployments.
	defaultDeployTimeout = "15m"
)

// serviceEntryName returns the service entry name for a workspace deployment.
// This name is used both in the ServiceSet's services[] and for status tracking.
// Includes the cluster name to ensure uniqueness.
func serviceEntryName(wsd *workspacesv1.WorkspaceDeployment) string {
	return fmt.Sprintf("wsd-%s-%s", wsd.Spec.ClusterDeploymentRef.Name, wsd.Name)
}

// serviceSetName returns the name of the ServiceSet for a workspace deployment.
// Includes the cluster name to make the relationship clear and avoid collisions.
func serviceSetName(wsd *workspacesv1.WorkspaceDeployment) string {
	return fmt.Sprintf("wsd-%s-%s", wsd.Spec.ClusterDeploymentRef.Name, wsd.Name)
}

// ensureWorkspaceServiceSet creates or updates a ServiceSet for the workspace.
// Each workspace gets its own ServiceSet, which k0rdent reconciles into a
// Sveltos Profile on the regional cluster. This avoids writing to the
// ClusterDeployment's serviceSpec and eliminates race conditions with the
// colony controller.
func ensureWorkspaceServiceSet(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	wsd *workspacesv1.WorkspaceDeployment,
	wsc *workspacesv1.WorkspaceClass,
	mergedValues string,
) error {
	log := log.FromContext(ctx)

	cdRef := wsd.Spec.ClusterDeploymentRef
	ssName := serviceSetName(wsd)
	entryName := serviceEntryName(wsd)

	// Build the Helm options
	deployTimeout := defaultDeployTimeout
	if wsc.Spec.DeployTimeout != nil {
		deployTimeout = wsc.Spec.DeployTimeout.Duration.String()
	}
	dur := parseDuration(deployTimeout)
	helmOptions := &k0rdentv1beta1.ServiceHelmOptions{
		Wait:    ptrBool(true),
		Timeout: &metav1.Duration{Duration: dur},
	}

	// Build the service entry. The Helm release installs into the dedicated
	// workspace namespace (pre-created with the mesh-discovery label by the
	// reconciler) — never a shared namespace, so same-name chart Services on
	// different child clusters can never merge in the mesh (ADR-0001).
	svc := k0rdentv1beta1.ServiceWithValues{
		Name:        entryName,
		Namespace:   workspaceNamespaceName(wsd),
		Template:    wsc.Spec.ServiceTemplate.Name,
		Values:      mergedValues,
		HelmOptions: helmOptions,
	}

	// Check if ServiceSet already exists
	existing := &k0rdentv1beta1.ServiceSet{}
	err := c.Get(ctx, client.ObjectKey{Name: ssName, Namespace: cdRef.Namespace}, existing)
	if err == nil {
		// ServiceSet exists — update services if needed
		existing.Spec.Services = []k0rdentv1beta1.ServiceWithValues{svc}
		if err := c.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed to update ServiceSet: %w", err)
		}
		log.Info("Updated workspace ServiceSet", "serviceSet", ssName)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get ServiceSet: %w", err)
	}

	// Create new ServiceSet
	ss := &k0rdentv1beta1.ServiceSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ssName,
			Namespace: cdRef.Namespace,
			Labels: map[string]string{
				// Required by the StateManagementProvider's selector to match this ServiceSet
				"ksm.k0rdent.mirantis.com/adapter": "kcm-controller-manager",
			},
		},
		Spec: k0rdentv1beta1.ServiceSetSpec{
			Cluster: cdRef.Name,
			Provider: k0rdentv1beta1.StateManagementProviderConfig{
				Name: defaultStateManagementProvider,
			},
			Services: []k0rdentv1beta1.ServiceWithValues{svc},
		},
	}

	// Set owner reference to the WorkspaceDeployment for garbage collection
	if err := controllerutil.SetOwnerReference(wsd, ss, scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on ServiceSet: %w", err)
	}

	if err := c.Create(ctx, ss); err != nil {
		return fmt.Errorf("failed to create ServiceSet: %w", err)
	}

	log.Info("Created workspace ServiceSet",
		"serviceSet", ssName,
		"cluster", cdRef.Name,
		"service", entryName)

	return nil
}

// ensureWorkspaceServiceSetDeleted requests deletion of the workspace's
// ServiceSet and reports whether it is fully gone. Idempotent — callers
// requeue until gone=true before proceeding to namespace cleanup, so the
// Helm uninstall never races namespace termination on the child cluster.
func ensureWorkspaceServiceSetDeleted(
	ctx context.Context,
	c client.Client,
	wsd *workspacesv1.WorkspaceDeployment,
) (bool, error) {
	log := log.FromContext(ctx)

	ssName := serviceSetName(wsd)
	cdRef := wsd.Spec.ClusterDeploymentRef

	ss := &k0rdentv1beta1.ServiceSet{}
	err := c.Get(ctx, client.ObjectKey{Name: ssName, Namespace: cdRef.Namespace}, ss)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to get ServiceSet: %w", err)
	}

	if ss.DeletionTimestamp.IsZero() {
		if err := c.Delete(ctx, ss); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("failed to delete ServiceSet: %w", err)
		}
		log.Info("Deleted workspace ServiceSet", "serviceSet", ssName)
	}

	// Re-check: without finalizers the ServiceSet vanishes immediately; with
	// k0rdent finalizers it lingers while the release uninstalls, and the
	// caller requeues until it is gone.
	err = c.Get(ctx, client.ObjectKey{Name: ssName, Namespace: cdRef.Namespace}, ss)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to re-check ServiceSet: %w", err)
	}
	return false, nil
}

func ptrBool(b bool) *bool {
	return &b
}

func parseDuration(s string) time.Duration {
	d, _ := time.ParseDuration(s)
	if d == 0 {
		d = 15 * time.Minute
	}
	return d
}

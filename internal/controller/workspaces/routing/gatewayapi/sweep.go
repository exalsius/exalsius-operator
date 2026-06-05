/*
Copyright 2025 Exalsius contributors.

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

package gatewayapi

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"github.com/exalsius/exalsius-operator/internal/controller/infra/common"
	"github.com/exalsius/exalsius-operator/internal/controller/workspaces/routing"
)

// SweepOrphans implements routing.OrphanSweeper: it visits every regional
// cluster and removes workspace mirror namespaces (and their TCPRoutes,
// which are the port-allocation table) whose workspace no longer exists.
//
// Unreachable regional clusters are logged and skipped — the sweep is a
// periodic backstop; the next run retries.
func (p *Provider) SweepOrphans(ctx context.Context, req routing.SweepRequest) error {
	logger := log.FromContext(ctx)

	regionals, err := listRegionalClusterDeployments(ctx, req.ManagementClient)
	if err != nil {
		return err
	}

	for _, regionalCD := range regionals {
		regionalClient, err := common.GetRegionalClusterClient(
			ctx, req.ManagementClient, regionalCD.Namespace, regionalCD.Name, req.Scheme)
		if err != nil {
			logger.Info("Skipping unreachable regional cluster during orphan sweep",
				"regionalClusterDeployment", regionalCD.Name, "error", err.Error())
			continue
		}

		namespaces := &corev1.NamespaceList{}
		if err := regionalClient.List(ctx, namespaces, client.HasLabels{routing.LabelWorkspace}); err != nil {
			logger.Info("Failed to list workspace namespaces during orphan sweep",
				"regionalClusterDeployment", regionalCD.Name, "error", err.Error())
			continue
		}

		for i := range namespaces.Items {
			ns := &namespaces.Items[i]
			if !ns.DeletionTimestamp.IsZero() {
				continue
			}
			workspaceName := ns.Labels[routing.LabelWorkspace]
			if workspaceName == "" || req.IsActiveWorkspace(workspaceName) {
				continue
			}

			logger.Info("Sweeping orphaned workspace routes",
				"namespace", ns.Name, "workspace", workspaceName,
				"regionalClusterDeployment", regionalCD.Name)

			// TCPRoutes first — they are the port-allocation table, and
			// namespace termination can lag.
			if err := regionalClient.DeleteAllOf(ctx, &gatewayv1alpha2.TCPRoute{},
				client.InNamespace(ns.Name),
			); err != nil && !apierrors.IsNotFound(err) && !apimeta.IsNoMatchError(err) {
				logger.Info("Failed to delete orphaned TCPRoutes", "namespace", ns.Name, "error", err.Error())
				continue
			}
			if err := regionalClient.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
				logger.Info("Failed to delete orphaned namespace", "namespace", ns.Name, "error", err.Error())
			}
		}
	}
	return nil
}

// listRegionalClusterDeployments finds all regional ClusterDeployments
// across namespaces via the KOF role label, as PartialObjectMetadata to
// keep the provider decoupled from the k0rdent types.
func listRegionalClusterDeployments(ctx context.Context, c client.Client) ([]metav1.PartialObjectMetadata, error) {
	cds := &metav1.PartialObjectMetadataList{}
	cds.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "k0rdent.mirantis.com", Version: "v1beta1", Kind: "ClusterDeploymentList",
	})
	if err := c.List(ctx, cds, client.MatchingLabels{common.LabelKOFClusterRole: "regional"}); err != nil {
		return nil, fmt.Errorf("failed to list regional ClusterDeployments: %w", err)
	}
	return cds.Items, nil
}

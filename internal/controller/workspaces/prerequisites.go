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

package workspaces

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
)

const (
	// Labels applied to prerequisite ServiceSets so the WSD controller's
	// ServiceSet watch mapper can fan out events to all WSDs that depend on
	// the prerequisite.
	labelPrerequisiteRole        = "workspaces.exalsius.ai/role"
	labelPrerequisiteRoleValue   = "prerequisite"
	labelPrerequisiteCluster     = "workspaces.exalsius.ai/cluster-deployment"
	labelPrerequisiteTemplate    = "workspaces.exalsius.ai/template"
	prerequisiteServiceSetPrefix = "wsprereq-"
)

// labelValueRE constrains a Kubernetes label value: up to 63 chars,
// alphanumeric or [-_.] in the middle, alphanumeric at the boundaries.
var labelValueRE = regexp.MustCompile(`^[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$`)

// prerequisiteServiceSetName returns the deterministic name of the shared
// ServiceSet that installs the given prerequisite template on the cluster.
// The name is shared across all WorkspaceDeployments needing the same
// prerequisite, so multiple WSDs converge on a single SS.
func prerequisiteServiceSetName(cdName, templateName string) string {
	return boundedName(fmt.Sprintf("%s%s-%s", prerequisiteServiceSetPrefix, cdName, templateName))
}

// maxServiceSetNameLen is the longest a ServiceSet name may be. K0rdent names
// the Sveltos Profile after the ServiceSet, and Sveltos stamps that name as a
// label *value* on the ClusterSummary it creates. Kubernetes caps label values
// at 63 characters, so a longer name makes Sveltos silently fail to create the
// ClusterSummary — the chart never deploys and the ServiceSet hangs in
// Provisioning. CD name + template/WSD name can exceed this (e.g. a 23-char CD
// plus a versioned template name), so every ServiceSet name is bounded here.
const maxServiceSetNameLen = 63

// boundedName returns name unchanged when it already fits maxServiceSetNameLen
// (so existing short names — and their already-created resources — are
// untouched); otherwise it truncates and appends a short deterministic hash of
// the full name. The result stays unique, stable across reconciles, and a valid
// DNS-1123 / label value.
func boundedName(name string) string {
	if len(name) <= maxServiceSetNameLen {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	suffix := "-" + hex.EncodeToString(sum[:4]) // "-" + 8 hex chars
	prefix := strings.TrimRight(name[:maxServiceSetNameLen-len(suffix)], "-.")
	return prefix + suffix
}

// isPrerequisiteServiceSet returns true if the ServiceSet was created by the
// prerequisite auto-installer (identified by labels rather than name parsing,
// since template names can contain hyphens).
func isPrerequisiteServiceSet(ss *k0rdentv1beta1.ServiceSet) bool {
	return ss.Labels[labelPrerequisiteRole] == labelPrerequisiteRoleValue
}

// sanitizeLabelValue ensures the value is acceptable as a Kubernetes label
// value. If it doesn't match the spec, returns a hash-derived fallback.
func sanitizeLabelValue(v string) string {
	if labelValueRE.MatchString(v) {
		return v
	}
	sum := sha256.Sum256([]byte(v))
	return "hash-" + hex.EncodeToString(sum[:8])
}

// ensurePrerequisiteServiceSet creates the prerequisite ServiceSet if missing.
// It is idempotent — concurrent reconciles of different WSDs sharing the same
// prerequisite converge on a single SS via the deterministic name.
func ensurePrerequisiteServiceSet(
	ctx context.Context,
	c client.Client,
	wsd *workspacesv1.WorkspaceDeployment,
	wsc *workspacesv1.WorkspaceClass,
	prereq workspacesv1.PrerequisiteSpec,
) error {
	cdRef := wsd.Spec.ClusterDeploymentRef
	templateName := prereq.ServiceTemplate.Name
	ssName := prerequisiteServiceSetName(cdRef.Name, templateName)

	// Already exists → nothing to do (we never mutate prereq SS once created;
	// values are baked in at creation time).
	existing := &k0rdentv1beta1.ServiceSet{}
	err := c.Get(ctx, client.ObjectKey{Name: ssName, Namespace: cdRef.Namespace}, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get prerequisite ServiceSet %s: %w", ssName, err)
	}

	// Build helm options — prereqs use prerequisiteDeployTimeout if set,
	// else fall back to deployTimeout, else the package default.
	timeout := defaultDeployTimeout
	if wsc.Spec.DeployTimeout != nil {
		timeout = wsc.Spec.DeployTimeout.Duration.String()
	}
	if wsc.Spec.PrerequisiteDeployTimeout != nil {
		timeout = wsc.Spec.PrerequisiteDeployTimeout.Duration.String()
	}
	helmOptions := &k0rdentv1beta1.ServiceHelmOptions{
		Wait:    ptrBool(true),
		Timeout: &metav1.Duration{Duration: parseDuration(timeout)},
	}

	values := ""
	if prereq.Values != nil && len(prereq.Values.Raw) > 0 {
		// PrerequisiteSpec.Values is freeform JSON; pass-through to k0rdent
		// which expects an inline YAML/JSON string.
		var v map[string]any
		if err := json.Unmarshal(prereq.Values.Raw, &v); err != nil {
			return fmt.Errorf("failed to unmarshal prerequisite values for %s: %w", templateName, err)
		}
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("failed to marshal prerequisite values for %s: %w", templateName, err)
		}
		values = string(data)
	}

	svc := k0rdentv1beta1.ServiceWithValues{
		Name:        sanitizeServiceEntryName(templateName),
		Namespace:   "default",
		Template:    templateName,
		Values:      values,
		HelmOptions: helmOptions,
	}

	ss := &k0rdentv1beta1.ServiceSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ssName,
			Namespace: cdRef.Namespace,
			Labels: map[string]string{
				// k0rdent state-management adapter label (same as workspace SS).
				"ksm.k0rdent.mirantis.com/adapter": "kcm-controller-manager",
				// Identifies this SS as a prerequisite install for the watch mapper.
				labelPrerequisiteRole:     labelPrerequisiteRoleValue,
				labelPrerequisiteCluster:  sanitizeLabelValue(cdRef.Name),
				labelPrerequisiteTemplate: sanitizeLabelValue(templateName),
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

	if err := c.Create(ctx, ss); err != nil {
		// Concurrent create from another WSD reconcile — treat as success.
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("failed to create prerequisite ServiceSet %s: %w", ssName, err)
	}
	return nil
}

// sanitizeServiceEntryName produces a service entry name suitable for the
// ServiceSet.spec.services[].name field. K0rdent / Sveltos require it to be a
// valid DNS-1123 label.
func sanitizeServiceEntryName(templateName string) string {
	// In practice template names are already DNS-compliant; this is a guard.
	s := strings.ToLower(templateName)
	if labelValueRE.MatchString(s) {
		return s
	}
	sum := sha256.Sum256([]byte(templateName))
	return "prereq-" + hex.EncodeToString(sum[:8])
}

// evaluatePrerequisites walks the WorkspaceClass prerequisites and returns
// the per-prerequisite status plus an overall verdict that drives the WSD
// state machine.
//
// Detection priority per prerequisite:
//  1. Our own prereq SS (wsprereq-<cd>-<template>): read its state directly.
//  2. The ClusterDeployment's spec/status services[] (colony-managed).
//  3. Otherwise the prerequisite is missing — we install it via a wsprereq SS.
func evaluatePrerequisites(
	ctx context.Context,
	c client.Client,
	wsd *workspacesv1.WorkspaceDeployment,
	wsc *workspacesv1.WorkspaceClass,
) (overall PrerequisitesVerdict, statuses []workspacesv1.PrerequisiteStatus, err error) {
	cdRef := wsd.Spec.ClusterDeploymentRef

	// Snapshot colony-managed services on the target CD up-front so we don't
	// fetch the CD once per prereq.
	cd := &k0rdentv1beta1.ClusterDeployment{}
	if err := c.Get(ctx, client.ObjectKey{Name: cdRef.Name, Namespace: cdRef.Namespace}, cd); err != nil {
		if apierrors.IsNotFound(err) {
			return PrerequisitesVerdictFailed, nil,
				fmt.Errorf("ClusterDeployment %s/%s not found", cdRef.Namespace, cdRef.Name)
		}
		return PrerequisitesVerdictFailed, nil, err
	}
	colonyDeployed := map[string]bool{}
	colonyKnown := map[string]bool{}
	for _, svc := range cd.Status.Services {
		colonyKnown[svc.Template] = true
		if svc.State == k0rdentv1beta1.ServiceStateDeployed {
			colonyDeployed[svc.Template] = true
		}
	}
	for _, svc := range cd.Spec.ServiceSpec.Services {
		colonyKnown[svc.Template] = true
	}

	overall = PrerequisitesVerdictSatisfied
	for _, prereq := range wsc.Spec.Prerequisites {
		// Reject version-constraint usage explicitly — Q8 fail-loudly.
		if prereq.ServiceTemplate.VersionConstraint != "" {
			return PrerequisitesVerdictInvalid,
				[]workspacesv1.PrerequisiteStatus{{
					Name:    prereq.ServiceTemplate.Name,
					Phase:   workspacesv1.PrerequisitePhaseFailed,
					Message: "versionConstraint is not yet implemented; specify the exact ServiceTemplate name in prereq.serviceTemplate.name",
				}},
				nil
		}

		st := workspacesv1.PrerequisiteStatus{Name: prereq.ServiceTemplate.Name}

		// (1) Our own prereq SS takes priority.
		ss := &k0rdentv1beta1.ServiceSet{}
		ssName := prerequisiteServiceSetName(cdRef.Name, prereq.ServiceTemplate.Name)
		err := c.Get(ctx, client.ObjectKey{Name: ssName, Namespace: cdRef.Namespace}, ss)
		switch {
		case err == nil:
			st.Source = workspacesv1.PrerequisiteSourceWorkspace
			ssState, ssMsg := readPrerequisiteSSState(ss, prereq.ServiceTemplate.Name)
			switch ssState {
			case k0rdentv1beta1.ServiceStateDeployed:
				st.Phase = workspacesv1.PrerequisitePhaseSatisfied
			case k0rdentv1beta1.ServiceStateFailed:
				st.Phase = workspacesv1.PrerequisitePhaseFailed
				st.Message = ssMsg
				overall = PrerequisitesVerdictFailed
			default:
				st.Phase = workspacesv1.PrerequisitePhaseInstalling
				if overall != PrerequisitesVerdictFailed {
					overall = PrerequisitesVerdictInstalling
				}
			}
		case apierrors.IsNotFound(err):
			// (2) Colony-managed satisfaction — only when status confirms.
			if colonyDeployed[prereq.ServiceTemplate.Name] {
				st.Source = workspacesv1.PrerequisiteSourceColony
				st.Phase = workspacesv1.PrerequisitePhaseSatisfied
			} else if colonyKnown[prereq.ServiceTemplate.Name] {
				// Colony is mid-install; wait rather than racing it.
				st.Source = workspacesv1.PrerequisiteSourceColony
				st.Phase = workspacesv1.PrerequisitePhaseInstalling
				if overall != PrerequisitesVerdictFailed {
					overall = PrerequisitesVerdictInstalling
				}
			} else {
				// (3) Nobody is providing it — we install via a wsprereq SS.
				st.Source = workspacesv1.PrerequisiteSourceWorkspace
				st.Phase = workspacesv1.PrerequisitePhasePending
				if overall != PrerequisitesVerdictFailed {
					overall = PrerequisitesVerdictMissing
				}
			}
		default:
			return PrerequisitesVerdictFailed, nil,
				fmt.Errorf("failed to get prerequisite ServiceSet %s: %w", ssName, err)
		}

		statuses = append(statuses, st)
	}
	return overall, statuses, nil
}

// readPrerequisiteSSState extracts the state of a single template entry from
// the ServiceSet status.
func readPrerequisiteSSState(ss *k0rdentv1beta1.ServiceSet, templateName string) (state, message string) {
	for _, svc := range ss.Status.Services {
		if svc.Template == templateName {
			return svc.State, svc.FailureMessage
		}
	}
	return "", ""
}

// PrerequisitesVerdict is the overall conclusion of evaluatePrerequisites.
type PrerequisitesVerdict int

const (
	// All prerequisites satisfied — proceed to Deploying.
	PrerequisitesVerdictSatisfied PrerequisitesVerdict = iota
	// At least one prereq is in flight (installing); wait.
	PrerequisitesVerdictInstalling
	// At least one prereq has no provider yet — we need to ensure SSes.
	PrerequisitesVerdictMissing
	// At least one prereq failed; fail-fast the WSD.
	PrerequisitesVerdictFailed
	// At least one prereq is configured invalidly (e.g. versionConstraint).
	PrerequisitesVerdictInvalid
)

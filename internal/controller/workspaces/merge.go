package workspaces

import (
	"encoding/json"
	"fmt"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// mergeResources merges user-provided resource overrides with class defaults.
//
// Numeric fields (replicas, cpu, memory, storage, gpuCount) and the GPUVendor
// constraint inherit from class defaults when not overridden. GPUType is
// special: it never inherits from class — admins cannot pin a specific GPU
// model on a class (that's a per-deployment cluster-dependent choice).
// Nil GPUType means "any model" for feasibility matching.
func mergeResources(classDefaults workspacesv1.WorkspaceResourceSpec, userOverrides *workspacesv1.WorkspaceResourceSpec) workspacesv1.WorkspaceResourceSpec {
	result := *classDefaults.DeepCopy()
	// Defensive: even if a class somehow has gpuType set (CEL bypass), wipe it.
	result.PerReplica.GPUType = nil

	if userOverrides == nil {
		return result
	}

	if userOverrides.Replicas != nil {
		result.Replicas = userOverrides.Replicas
	}
	if userOverrides.PerReplica.CPU != nil {
		result.PerReplica.CPU = userOverrides.PerReplica.CPU
	}
	if userOverrides.PerReplica.Memory != nil {
		result.PerReplica.Memory = userOverrides.PerReplica.Memory
	}
	if userOverrides.PerReplica.Storage != nil {
		result.PerReplica.Storage = userOverrides.PerReplica.Storage
	}
	if userOverrides.PerReplica.GPUCount != nil {
		result.PerReplica.GPUCount = userOverrides.PerReplica.GPUCount
	}
	if userOverrides.PerReplica.GPUVendor != nil {
		result.PerReplica.GPUVendor = userOverrides.PerReplica.GPUVendor
	}
	if userOverrides.PerReplica.GPUType != nil {
		result.PerReplica.GPUType = userOverrides.PerReplica.GPUType
	}

	return result
}

// mergeValues merges class default Helm values with user-provided values.
// User values take precedence over class defaults (shallow merge at the top level).
// Returns the merged JSON string for the service entry's values field.
func mergeValues(classDefaults *apiextensionsv1.JSON, userValues *apiextensionsv1.JSON) (string, error) {
	merged := make(map[string]any)

	// Start with class defaults
	if classDefaults != nil && len(classDefaults.Raw) > 0 {
		if err := json.Unmarshal(classDefaults.Raw, &merged); err != nil {
			return "", fmt.Errorf("failed to unmarshal class default values: %w", err)
		}
	}

	// Overlay user values (user wins)
	if userValues != nil && len(userValues.Raw) > 0 {
		var userMap map[string]any
		if err := json.Unmarshal(userValues.Raw, &userMap); err != nil {
			return "", fmt.Errorf("failed to unmarshal user values: %w", err)
		}
		for k, v := range userMap {
			merged[k] = v
		}
	}

	if len(merged) == 0 {
		return "", nil
	}

	data, err := json.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("failed to marshal merged values: %w", err)
	}
	return string(data), nil
}

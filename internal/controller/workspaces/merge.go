package workspaces

import (
	"encoding/json"
	"fmt"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// mergeResources merges user-provided resource overrides with class defaults.
// User overrides win; nil fields fall back to class defaults.
func mergeResources(classDefaults workspacesv1.WorkspaceResourceSpec, userOverrides *workspacesv1.WorkspaceResourceSpec) workspacesv1.WorkspaceResourceSpec {
	if userOverrides == nil {
		return *classDefaults.DeepCopy()
	}

	result := *classDefaults.DeepCopy()

	if userOverrides.NodeCount != nil {
		result.NodeCount = userOverrides.NodeCount
	}
	if userOverrides.PerNode.CPU != nil {
		result.PerNode.CPU = userOverrides.PerNode.CPU
	}
	if userOverrides.PerNode.Memory != nil {
		result.PerNode.Memory = userOverrides.PerNode.Memory
	}
	if userOverrides.PerNode.Storage != nil {
		result.PerNode.Storage = userOverrides.PerNode.Storage
	}
	if userOverrides.PerNode.GPUCount != nil {
		result.PerNode.GPUCount = userOverrides.PerNode.GPUCount
	}
	if userOverrides.PerNode.GPUVendor != nil {
		result.PerNode.GPUVendor = userOverrides.PerNode.GPUVendor
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

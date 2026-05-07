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
	"fmt"
	"strings"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
)

// exalsiusValuesKey is the well-known top-level Helm value key under which
// the operator injects resolved per-replica resources for charts that opt
// into reading them. Underscore prefix marks it as system-managed; chart
// authors should treat it as read-only input.
const exalsiusValuesKey = "_exalsius"

// parsePath turns a JSONPath-lite string into the list of segment names
// to walk. Dots separate normal segments; bracket-quoted segments preserve
// keys with dots, slashes, or other special characters. Single or double
// quotes around the bracketed key are accepted.
//
// Examples:
//
//	"a.b.c"                                   → ["a", "b", "c"]
//	`jupyterhub.singleuser.cpu.guarantee`      → ["jupyterhub","singleuser","cpu","guarantee"]
//	`a["b.c"].d`                               → ["a", "b.c", "d"]
//	`a['nvidia.com/gpu']`                      → ["a", "nvidia.com/gpu"]
//
// Returns an error for empty paths, unclosed brackets, dangling dots, or
// brackets without a quoted key.
func parsePath(s string) ([]string, error) {
	if s == "" {
		return nil, fmt.Errorf("path is empty")
	}

	var (
		out []string
		buf strings.Builder
		i   = 0
		n   = len(s)
		// emittedSinceDot is true when at least one segment (bare or bracket)
		// has been produced since the last unconsumed '.'. Used to detect
		// "a..b" (empty between dots) versus a legitimate post-bracket '.'.
		emittedSinceDot = false
	)

	flushBare := func() error {
		if buf.Len() == 0 {
			return nil
		}
		out = append(out, buf.String())
		buf.Reset()
		emittedSinceDot = true
		return nil
	}

	for i < n {
		c := s[i]
		switch c {
		case '.':
			if err := flushBare(); err != nil {
				return nil, err
			}
			if !emittedSinceDot {
				return nil, fmt.Errorf("empty segment in path %q", s)
			}
			emittedSinceDot = false
			i++
			if i == n {
				return nil, fmt.Errorf("path %q ends with a trailing dot", s)
			}
		case '[':
			if err := flushBare(); err != nil {
				return nil, err
			}
			i++
			if i >= n {
				return nil, fmt.Errorf("path %q has unclosed bracket", s)
			}
			quote := s[i]
			if quote != '"' && quote != '\'' {
				return nil, fmt.Errorf("bracket segment in path %q must use single or double quotes", s)
			}
			i++
			start := i
			for i < n && s[i] != quote {
				i++
			}
			if i == n {
				return nil, fmt.Errorf("path %q has unterminated bracket-quoted segment", s)
			}
			key := s[start:i]
			if key == "" {
				return nil, fmt.Errorf("path %q has empty bracket-quoted segment", s)
			}
			out = append(out, key)
			emittedSinceDot = true
			i++ // closing quote
			if i >= n || s[i] != ']' {
				return nil, fmt.Errorf("path %q is missing closing bracket", s)
			}
			i++ // closing bracket
			// After a bracket segment, the next character (if any) must be
			// '.' or '[' — bare-segment continuation isn't supported.
			if i < n && s[i] != '.' && s[i] != '[' {
				return nil, fmt.Errorf("path %q has unexpected char %q after bracket segment", s, s[i])
			}
		default:
			buf.WriteByte(c)
			i++
		}
	}

	if err := flushBare(); err != nil {
		return nil, err
	}
	if !emittedSinceDot {
		return nil, fmt.Errorf("path %q ends with a trailing dot", s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("path %q yielded no segments", s)
	}
	return out, nil
}

// setNestedValue walks `m` along `path`, creating intermediate map[string]any
// nodes as needed, and writes `value` at the leaf. Returns the previous value
// at the leaf (nil if absent) and a boolean indicating whether a non-nil
// previous value was overwritten.
//
// If an intermediate node along the path holds a non-map value, that value is
// replaced with a fresh map (and counted as overwritten so callers can warn).
func setNestedValue(m map[string]any, path []string, value any) (previous any, overwritten bool) {
	if len(path) == 0 {
		return nil, false
	}
	cur := m
	for i, seg := range path {
		if i == len(path)-1 {
			prev, hadPrev := cur[seg]
			cur[seg] = value
			return prev, hadPrev && prev != nil
		}
		next, ok := cur[seg].(map[string]any)
		if !ok {
			// Either absent or a non-map — replace with fresh map.
			if existing, present := cur[seg]; present && existing != nil {
				overwritten = true
			}
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
	return nil, overwritten
}

// resourceInjectionWarning describes a single overwrite event so the caller
// can surface it on the WSD status.
type resourceInjectionWarning struct {
	Path     string
	Field    string
	Previous any
}

// String renders the warning into a single human-readable line.
func (w resourceInjectionWarning) String() string {
	return fmt.Sprintf("operator overwrote spec.values.%s (%s) — use spec.resources.perReplica.%s instead",
		w.Path, w.Field, w.Field)
}

// validateInjectionPaths parses every path in m. Returns an error referencing
// the first malformed path; nil when all paths are valid.
func validateInjectionPaths(m *workspacesv1.ResourceInjectionMap) error {
	if m == nil {
		return nil
	}
	for field, paths := range injectionFieldPaths(m) {
		for _, p := range paths {
			if _, err := parsePath(string(p)); err != nil {
				return fmt.Errorf("resourceInjection.%s path %q: %w", field, p, err)
			}
		}
	}
	return nil
}

// injectionFieldPaths returns a map of resource field name → list of injection
// paths for that field. Centralises the field-name → list correspondence so
// validation, injection, and tests agree.
func injectionFieldPaths(m *workspacesv1.ResourceInjectionMap) map[string][]workspacesv1.InjectionPath {
	if m == nil {
		return nil
	}
	return map[string][]workspacesv1.InjectionPath{
		"replicas":  m.Replicas,
		"cpu":       m.CPU,
		"memory":    m.Memory,
		"storage":   m.Storage,
		"gpuCount":  m.GPUCount,
		"gpuVendor": m.GPUVendor,
		"gpuType":   m.GPUType,
	}
}

// injectResources splices resolved per-replica resources into the merged
// Helm values map, in two ways:
//
//  1. Always at the well-known `_exalsius.resources` path, regardless of
//     class configuration. Charts written for exalsius can consume this path
//     directly.
//  2. At every additional path declared in class.spec.resourceInjection.
//     This is the umbrella-chart escape hatch: admins map structured
//     resources to chart-specific subchart paths.
//
// Nil resource fields are skipped silently. User-supplied paths in `values`
// that get overwritten are returned in `warnings` so the caller can surface
// a ResourcesInjected condition with reason=UserPathsOverwritten.
//
// Conflict resolution: operator injection always wins. The structured
// spec.resources channel is authoritative; users wanting different behavior
// must change spec.resources rather than spec.values.
func injectResources(
	values map[string]any,
	resolved workspacesv1.WorkspaceResourceSpec,
	im *workspacesv1.ResourceInjectionMap,
) (warnings []resourceInjectionWarning) {
	if values == nil {
		return nil
	}

	// Build a structured representation of what gets injected. nil fields are
	// dropped; non-nil fields land both at _exalsius.resources and at any
	// user-declared injection paths.
	type fieldEntry struct {
		name  string
		value any
	}
	var entries []fieldEntry

	if resolved.Replicas != nil {
		entries = append(entries, fieldEntry{"replicas", int64(*resolved.Replicas)})
	}
	if resolved.PerReplica.CPU != nil {
		entries = append(entries, fieldEntry{"cpu", resolved.PerReplica.CPU.String()})
	}
	if resolved.PerReplica.Memory != nil {
		entries = append(entries, fieldEntry{"memory", resolved.PerReplica.Memory.String()})
	}
	if resolved.PerReplica.Storage != nil {
		entries = append(entries, fieldEntry{"storage", resolved.PerReplica.Storage.String()})
	}
	if resolved.PerReplica.GPUCount != nil {
		entries = append(entries, fieldEntry{"gpuCount", int64(*resolved.PerReplica.GPUCount)})
	}
	if resolved.PerReplica.GPUVendor != nil {
		entries = append(entries, fieldEntry{"gpuVendor", string(*resolved.PerReplica.GPUVendor)})
	}
	if resolved.PerReplica.GPUType != nil {
		entries = append(entries, fieldEntry{"gpuType", *resolved.PerReplica.GPUType})
	}

	// (1) Standard `_exalsius.resources.<field>` path — always set when the
	// resolved value is non-nil. perReplica fields land under
	// `_exalsius.resources.perReplica.<field>`; replicas under
	// `_exalsius.resources.replicas`.
	exalsius, ok := values[exalsiusValuesKey].(map[string]any)
	if !ok {
		exalsius = map[string]any{}
		values[exalsiusValuesKey] = exalsius
	}
	resourcesNode, ok := exalsius["resources"].(map[string]any)
	if !ok {
		resourcesNode = map[string]any{}
		exalsius["resources"] = resourcesNode
	}
	perReplica, ok := resourcesNode["perReplica"].(map[string]any)
	if !ok {
		perReplica = map[string]any{}
		resourcesNode["perReplica"] = perReplica
	}
	for _, e := range entries {
		if e.name == "replicas" {
			resourcesNode["replicas"] = e.value
		} else {
			perReplica[e.name] = e.value
		}
	}

	// (2) Additional paths from class.resourceInjection — for umbrella charts.
	if im == nil {
		return warnings
	}
	for _, e := range entries {
		var paths []workspacesv1.InjectionPath
		switch e.name {
		case "replicas":
			paths = im.Replicas
		case "cpu":
			paths = im.CPU
		case "memory":
			paths = im.Memory
		case "storage":
			paths = im.Storage
		case "gpuCount":
			paths = im.GPUCount
		case "gpuVendor":
			paths = im.GPUVendor
		case "gpuType":
			paths = im.GPUType
		}
		for _, p := range paths {
			segs, err := parsePath(string(p))
			if err != nil {
				// Validity reconciler should already have caught this; if we
				// reach here, the WorkspaceClass slipped through. Skip and
				// surface a warning so the operator log + WSD status reflect
				// it instead of a panic.
				warnings = append(warnings, resourceInjectionWarning{
					Path:     string(p),
					Field:    e.name,
					Previous: fmt.Sprintf("PARSE ERROR: %v", err),
				})
				continue
			}
			prev, overwrote := setNestedValue(values, segs, e.value)
			if overwrote {
				warnings = append(warnings, resourceInjectionWarning{
					Path:     string(p),
					Field:    e.name,
					Previous: prev,
				})
			}
		}
	}
	return warnings
}

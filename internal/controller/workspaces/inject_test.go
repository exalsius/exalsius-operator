package workspaces

import (
	"reflect"
	"testing"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
)

func TestParsePath_Simple(t *testing.T) {
	cases := map[string][]string{
		"a":                                   {"a"},
		"a.b":                                 {"a", "b"},
		"a.b.c":                               {"a", "b", "c"},
		"jupyterhub.singleuser.cpu.guarantee": {"jupyterhub", "singleuser", "cpu", "guarantee"},
	}
	for input, want := range cases {
		got, err := parsePath(input)
		if err != nil {
			t.Errorf("parsePath(%q) returned unexpected error: %v", input, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("parsePath(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestParsePath_BracketSegments(t *testing.T) {
	cases := map[string][]string{
		`a["b.c"]`:                               {"a", "b.c"},
		`a['b.c']`:                               {"a", "b.c"},
		`a["nvidia.com/gpu"]`:                    {"a", "nvidia.com/gpu"},
		`a.b["c.d"].e`:                           {"a", "b", "c.d", "e"},
		`extraResource.limits["nvidia.com/gpu"]`: {"extraResource", "limits", "nvidia.com/gpu"},
	}
	for input, want := range cases {
		got, err := parsePath(input)
		if err != nil {
			t.Errorf("parsePath(%q) returned unexpected error: %v", input, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("parsePath(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestParsePath_Errors(t *testing.T) {
	bad := []string{
		"",        // empty
		".",       // bare dot
		"a..b",    // empty segment
		"a.",      // trailing dot
		"a[",      // unclosed bracket
		"a[]",     // empty bracket
		"a[b]",    // unquoted bracket
		`a["b]`,   // unterminated quoted bracket
		`a["b"`,   // missing closing bracket
		`a["b"]c`, // bare segment after bracket
	}
	for _, input := range bad {
		if _, err := parsePath(input); err == nil {
			t.Errorf("parsePath(%q) should have errored", input)
		}
	}
}

func TestSetNestedValue_CreatesIntermediateMaps(t *testing.T) {
	m := map[string]any{}
	prev, ow := setNestedValue(m, []string{"a", "b", "c"}, "v")
	if prev != nil || ow {
		t.Errorf("expected no overwrite for fresh map, got prev=%v ow=%v", prev, ow)
	}
	got := m["a"].(map[string]any)["b"].(map[string]any)["c"]
	if got != "v" {
		t.Errorf("expected v at a.b.c, got %v", got)
	}
}

func TestSetNestedValue_OverwritesScalar(t *testing.T) {
	m := map[string]any{"a": map[string]any{"b": "old"}}
	prev, ow := setNestedValue(m, []string{"a", "b"}, "new")
	if !ow || prev != "old" {
		t.Errorf("expected overwrite of 'old', got prev=%v ow=%v", prev, ow)
	}
	if m["a"].(map[string]any)["b"] != "new" {
		t.Errorf("expected new value, got %v", m["a"].(map[string]any)["b"])
	}
}

func TestInjectResources_StandardPathAlwaysSet(t *testing.T) {
	m := map[string]any{}
	resolved := workspacesv1.WorkspaceResourceSpec{
		Replicas: int32Ptr(2),
		PerReplica: workspacesv1.ResourceRequirements{
			CPU:    mustQty("4"),
			Memory: mustQty("8Gi"),
		},
	}
	warns := injectResources(m, resolved, nil)
	if len(warns) != 0 {
		t.Errorf("expected no warnings, got %d", len(warns))
	}

	exalsius := m["_exalsius"].(map[string]any)
	res := exalsius["resources"].(map[string]any)
	if res["replicas"].(int64) != 2 {
		t.Errorf("replicas=%v, want 2", res["replicas"])
	}
	per := res["perReplica"].(map[string]any)
	if per["cpu"] != "4" || per["memory"] != "8Gi" {
		t.Errorf("perReplica=%v, want cpu=4 memory=8Gi", per)
	}
}

func TestInjectResources_NilFieldsSkipped(t *testing.T) {
	m := map[string]any{}
	resolved := workspacesv1.WorkspaceResourceSpec{
		Replicas: int32Ptr(1),
		PerReplica: workspacesv1.ResourceRequirements{
			CPU: mustQty("2"),
			// Memory, Storage, GPUCount, GPUVendor, GPUType all nil
		},
	}
	injectResources(m, resolved, nil)
	per := m["_exalsius"].(map[string]any)["resources"].(map[string]any)["perReplica"].(map[string]any)
	if _, ok := per["memory"]; ok {
		t.Errorf("expected memory NOT set when nil, got %v", per["memory"])
	}
	if _, ok := per["gpuCount"]; ok {
		t.Errorf("expected gpuCount NOT set when nil")
	}
	if per["cpu"] != "2" {
		t.Errorf("cpu=%v, want 2", per["cpu"])
	}
}

func TestInjectResources_MapPathsForUmbrellaChart(t *testing.T) {
	m := map[string]any{}
	resolved := workspacesv1.WorkspaceResourceSpec{
		Replicas: int32Ptr(3),
		PerReplica: workspacesv1.ResourceRequirements{
			CPU:    mustQty("8"),
			Memory: mustQty("32Gi"),
		},
	}
	im := &workspacesv1.ResourceInjectionMap{
		CPU:      []workspacesv1.InjectionPath{"jupyterhub.singleuser.cpu.guarantee"},
		Memory:   []workspacesv1.InjectionPath{"jupyterhub.singleuser.memory.guarantee"},
		Replicas: []workspacesv1.InjectionPath{"jupyterhub.replicas"},
	}
	warns := injectResources(m, resolved, im)
	if len(warns) != 0 {
		t.Errorf("expected no warnings, got %v", warns)
	}

	jh := m["jupyterhub"].(map[string]any)
	if jh["replicas"].(int64) != 3 {
		t.Errorf("jupyterhub.replicas=%v, want 3", jh["replicas"])
	}
	su := jh["singleuser"].(map[string]any)
	if su["cpu"].(map[string]any)["guarantee"] != "8" {
		t.Errorf("singleuser.cpu.guarantee mismatch: %v", su["cpu"])
	}
	if su["memory"].(map[string]any)["guarantee"] != "32Gi" {
		t.Errorf("singleuser.memory.guarantee mismatch: %v", su["memory"])
	}
}

func TestInjectResources_SpecialKeyInBracket(t *testing.T) {
	m := map[string]any{}
	resolved := workspacesv1.WorkspaceResourceSpec{
		PerReplica: workspacesv1.ResourceRequirements{
			GPUCount: int32Ptr(2),
		},
	}
	im := &workspacesv1.ResourceInjectionMap{
		GPUCount: []workspacesv1.InjectionPath{`jupyterhub.singleuser.extraResource.limits["nvidia.com/gpu"]`},
	}
	warns := injectResources(m, resolved, im)
	if len(warns) != 0 {
		t.Errorf("expected no warnings, got %v", warns)
	}
	limits := m["jupyterhub"].(map[string]any)["singleuser"].(map[string]any)["extraResource"].(map[string]any)["limits"].(map[string]any)
	if limits["nvidia.com/gpu"].(int64) != 2 {
		t.Errorf("limits[nvidia.com/gpu]=%v, want 2", limits["nvidia.com/gpu"])
	}
}

func TestInjectResources_OverwriteEmitsWarning(t *testing.T) {
	// User pre-populated spec.values with an override at a path that the
	// class also injects into. Operator must overwrite (operator wins) and
	// emit a warning so the user sees their value was ignored.
	m := map[string]any{
		"jupyterhub": map[string]any{
			"singleuser": map[string]any{
				"cpu": map[string]any{
					"guarantee": "16", // user set 16, but spec.resources says 4
				},
			},
		},
	}
	resolved := workspacesv1.WorkspaceResourceSpec{
		PerReplica: workspacesv1.ResourceRequirements{
			CPU: mustQty("4"),
		},
	}
	im := &workspacesv1.ResourceInjectionMap{
		CPU: []workspacesv1.InjectionPath{"jupyterhub.singleuser.cpu.guarantee"},
	}
	warns := injectResources(m, resolved, im)
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d (%v)", len(warns), warns)
	}
	if warns[0].Field != "cpu" {
		t.Errorf("warning.Field=%q, want cpu", warns[0].Field)
	}
	if warns[0].Previous != "16" {
		t.Errorf("warning.Previous=%v, want 16", warns[0].Previous)
	}
	got := m["jupyterhub"].(map[string]any)["singleuser"].(map[string]any)["cpu"].(map[string]any)["guarantee"]
	if got != "4" {
		t.Errorf("operator should win — expected cpu=4, got %v", got)
	}
}

func TestValidateInjectionPaths_AcceptsValid(t *testing.T) {
	im := &workspacesv1.ResourceInjectionMap{
		CPU:    []workspacesv1.InjectionPath{"a.b.c"},
		Memory: []workspacesv1.InjectionPath{`a["b.c"].d`},
	}
	if err := validateInjectionPaths(im); err != nil {
		t.Errorf("expected valid, got %v", err)
	}
}

func TestValidateInjectionPaths_RejectsMalformed(t *testing.T) {
	im := &workspacesv1.ResourceInjectionMap{
		CPU: []workspacesv1.InjectionPath{"a..b"},
	}
	if err := validateInjectionPaths(im); err == nil {
		t.Errorf("expected error for malformed path")
	}
}

func TestInjectNodeSelector_WritesUnderExalsiusScheduling(t *testing.T) {
	values := map[string]any{}
	injectNodeSelector(values, map[string]string{"exalsius.ai/gpu-model": "H100"})

	ex, ok := values[exalsiusValuesKey].(map[string]any)
	if !ok {
		t.Fatalf("expected %q map, got %T", exalsiusValuesKey, values[exalsiusValuesKey])
	}
	sched, ok := ex["scheduling"].(map[string]any)
	if !ok {
		t.Fatalf("expected scheduling map, got %T", ex["scheduling"])
	}
	sel, ok := sched["nodeSelector"].(map[string]any)
	if !ok {
		t.Fatalf("expected nodeSelector map, got %T", sched["nodeSelector"])
	}
	if sel["exalsius.ai/gpu-model"] != "H100" {
		t.Errorf("unexpected nodeSelector: %v", sel)
	}
}

func TestInjectNodeSelector_NilOrEmptyIsNoOp(t *testing.T) {
	// nil values map: must not panic.
	injectNodeSelector(nil, map[string]string{"a": "b"})

	values := map[string]any{}
	injectNodeSelector(values, nil)
	if _, ok := values[exalsiusValuesKey]; ok {
		t.Errorf("empty selector should not create %q key: %v", exalsiusValuesKey, values)
	}
}

func TestInjectGPUResourceName(t *testing.T) {
	values := map[string]any{}
	injectGPUResourceName(values, "amd.com/gpu")

	ex := values[exalsiusValuesKey].(map[string]any)
	res := ex["resources"].(map[string]any)
	per := res["perReplica"].(map[string]any)
	if per["gpuResourceName"] != "amd.com/gpu" {
		t.Errorf("expected gpuResourceName=amd.com/gpu, got %v", per["gpuResourceName"])
	}

	// Empty resource name is a no-op.
	empty := map[string]any{}
	injectGPUResourceName(empty, "")
	if _, ok := empty[exalsiusValuesKey]; ok {
		t.Errorf("empty resource name should not write %q: %v", exalsiusValuesKey, empty)
	}
}

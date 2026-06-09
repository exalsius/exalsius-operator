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

package routing

import (
	"reflect"
	"testing"
)

func TestMeshNamespaceLabels(t *testing.T) {
	cases := []struct {
		mode MeshMode
		want map[string]string
	}{
		{MeshModeAmbient, map[string]string{"istio.io/dataplane-mode": "ambient"}},
		{MeshModeSidecar, map[string]string{"istio-injection": "enabled"}},
		{MeshModeNone, nil},
		{MeshMode("bogus"), nil},
		{MeshMode(""), nil},
	}
	for _, c := range cases {
		got := MeshNamespaceLabels(c.mode)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("MeshNamespaceLabels(%q) = %v, want %v", c.mode, got, c.want)
		}
	}
}

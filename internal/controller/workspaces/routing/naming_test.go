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
	wp := WaypointConfig{Name: "istio-waypoint", Namespace: "istio-system"}
	cases := []struct {
		name string
		mode MeshMode
		wp   WaypointConfig
		want map[string]string
	}{
		{
			name: "ambient with waypoint",
			mode: MeshModeAmbient,
			wp:   wp,
			want: map[string]string{
				"istio.io/dataplane-mode":         "ambient",
				"istio.io/use-waypoint":           "istio-waypoint",
				"istio.io/use-waypoint-namespace": "istio-system",
				"istio.io/ingress-use-waypoint":   "true",
			},
		},
		{
			name: "ambient without waypoint",
			mode: MeshModeAmbient,
			wp:   WaypointConfig{},
			want: map[string]string{"istio.io/dataplane-mode": "ambient"},
		},
		{name: "sidecar", mode: MeshModeSidecar, wp: wp, want: map[string]string{"istio-injection": "enabled"}},
		{name: "none", mode: MeshModeNone, wp: wp, want: nil},
		{name: "unknown", mode: MeshMode("bogus"), wp: wp, want: nil},
	}
	for _, c := range cases {
		got := MeshNamespaceLabels(c.mode, c.wp)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: MeshNamespaceLabels(%q) = %v, want %v", c.name, c.mode, got, c.want)
		}
	}
}

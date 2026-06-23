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

func TestWaypointNameForClusterDeployment(t *testing.T) {
	t.Run("short name gets the plain suffix", func(t *testing.T) {
		if got := WaypointNameForClusterDeployment("default-child-adopted-1"); got != "default-child-adopted-1-waypoint" {
			t.Errorf("got %q, want %q", got, "default-child-adopted-1-waypoint")
		}
	})

	t.Run("over-long name is truncated, hashed, and stays a valid label", func(t *testing.T) {
		long := ""
		for len(long) < 80 {
			long += "abcdefghij"
		}
		got := WaypointNameForClusterDeployment(long)
		if len(got) > maxDNS1123Label {
			t.Errorf("name %q is %d chars, exceeds %d", got, len(got), maxDNS1123Label)
		}
		if got[len(got)-len(waypointNameSuffix):] != waypointNameSuffix {
			t.Errorf("name %q does not end with %q", got, waypointNameSuffix)
		}
		// Deterministic: same input → same output.
		if WaypointNameForClusterDeployment(long) != got {
			t.Errorf("not deterministic")
		}
	})

	t.Run("distinct long names hash differently", func(t *testing.T) {
		base := ""
		for len(base) < 70 {
			base += "x"
		}
		if WaypointNameForClusterDeployment(base+"-a") == WaypointNameForClusterDeployment(base+"-b") {
			t.Errorf("distinct CD names collided")
		}
	})
}

func TestMeshConfigNamespaceLabels(t *testing.T) {
	t.Run("ambient + waypoint enabled derives the per-child waypoint", func(t *testing.T) {
		c := MeshConfig{Mode: MeshModeAmbient, WaypointEnabled: true, WaypointNamespace: "istio-system"}
		got := c.NamespaceLabels("cd-1")
		want := map[string]string{
			"istio.io/dataplane-mode":         "ambient",
			"istio.io/use-waypoint":           "cd-1-waypoint",
			"istio.io/use-waypoint-namespace": "istio-system",
			"istio.io/ingress-use-waypoint":   "true",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("waypoint disabled omits the waypoint labels", func(t *testing.T) {
		c := MeshConfig{Mode: MeshModeAmbient, WaypointEnabled: false, WaypointNamespace: "istio-system"}
		got := c.NamespaceLabels("cd-1")
		want := map[string]string{"istio.io/dataplane-mode": "ambient"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

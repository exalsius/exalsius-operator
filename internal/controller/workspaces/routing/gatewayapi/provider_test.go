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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newFakeClient(objs ...client.Object) client.Client {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
}

func TestMarkServiceGlobal(t *testing.T) {
	ctx := context.Background()

	t.Run("labels an existing unlabeled service", func(t *testing.T) {
		c := newFakeClient(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ws-x"},
		})
		if err := markServiceGlobal(ctx, c, "ws-x", "svc"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := &corev1.Service{}
		if err := c.Get(ctx, client.ObjectKey{Name: "svc", Namespace: "ws-x"}, got); err != nil {
			t.Fatal(err)
		}
		if got.Labels[labelIstioGlobal] != labelIstioGlobalValue {
			t.Fatalf("expected %s=true, got labels %v", labelIstioGlobal, got.Labels)
		}
	})

	t.Run("tolerates a missing service (chart not reconciled yet)", func(t *testing.T) {
		c := newFakeClient()
		if err := markServiceGlobal(ctx, c, "ws-x", "absent"); err != nil {
			t.Fatalf("expected nil for missing service, got %v", err)
		}
	})

	t.Run("is a no-op when already labeled", func(t *testing.T) {
		c := newFakeClient(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: "svc", Namespace: "ws-x",
				Labels:          map[string]string{labelIstioGlobal: labelIstioGlobalValue},
				ResourceVersion: "1",
			},
		})
		if err := markServiceGlobal(ctx, c, "ws-x", "svc"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := &corev1.Service{}
		_ = c.Get(ctx, client.ObjectKey{Name: "svc", Namespace: "ws-x"}, got)
		if got.ResourceVersion != "1" {
			t.Fatalf("expected no update (rv unchanged), got rv=%s", got.ResourceVersion)
		}
	})
}

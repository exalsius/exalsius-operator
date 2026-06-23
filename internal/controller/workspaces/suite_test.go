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
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	"github.com/exalsius/exalsius-operator/internal/controller/workspaces/routing"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
	// testRouteProvider is the fake RouteProvider wired into the suite's
	// reconciler — lifecycle specs assert against it without any Gateway
	// API fixtures.
	testRouteProvider = newFakeRouteProvider()
)

func TestWorkspaces(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Workspaces Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	Expect(infrav1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(k0rdentv1beta1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(workspacesv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(gatewayv1.Install(scheme.Scheme)).To(Succeed())
	Expect(gatewayv1alpha2.Install(scheme.Scheme)).To(Succeed())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "..", "test", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	if dir := getFirstFoundEnvTestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Start a controller manager with the WorkspaceDeployment reconciler
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // disable metrics for tests
		},
	})
	Expect(err).NotTo(HaveOccurred())

	err = (&WorkspaceDeploymentReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		RouteProvider: testRouteProvider,
		Recorder:      mgr.GetEventRecorder("workspace-deployment"),
		APIReader:     mgr.GetAPIReader(),
		Mesh: routing.MeshConfig{
			Mode:              routing.MeshModeAmbient,
			WaypointEnabled:   true,
			WaypointNamespace: "istio-system",
		},
	}).SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&WorkspaceClassReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})

func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}

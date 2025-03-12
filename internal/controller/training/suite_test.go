/*
Copyright 2025.

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

package training

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	trainingv1 "github.com/exalsius/exalsius-operator/api/training/v1"
	volcanoalpha1 "volcano.sh/apis/pkg/apis/batch/v1alpha1"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
)

const externalCRDBaseDir = "../../../test/crds"

// externalCRDSources is a map of external CRDs to be added to the test environment
var externalCRDSources = map[string]string{
	"batch.volcano.sh_jobs.yaml": "https://raw.githubusercontent.com/volcano-sh/volcano/refs/heads/release-1.11/config/crd/volcano/bases/batch.volcano.sh_jobs.yaml",
}

func ensureExternalCRDs() error {
	if err := os.MkdirAll(externalCRDBaseDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create external CRD base directory: %w", err)
	}

	for name, url := range externalCRDSources {
		crdPath := filepath.Join(externalCRDBaseDir, name)

		if _, err := os.Stat(crdPath); err == nil {
			fmt.Printf("CRD '%s' already exists, skipping download.\n", name)
			continue
		}

		fmt.Printf("Downloading CRD: %s from %s\n", name, url)

		resp, err := http.Get(url)
		if err != nil {
			return fmt.Errorf("failed to download CRD %s: %w", name, err)
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				fmt.Printf("Failed to close response body: %v\n", err)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("failed to download CRD %s: HTTP %d", name, resp.StatusCode)
		}

		out, err := os.Create(crdPath)
		if err != nil {
			return fmt.Errorf("failed to create file for CRD %s: %w", name, err)
		}
		defer func() {
			if err := out.Close(); err != nil {
				fmt.Printf("Failed to close output: %v\n", err)
			}
		}()

		_, err = io.Copy(out, resp.Body)
		if err != nil {
			return fmt.Errorf("failed to write CRD %s to file: %w", name, err)
		}

		fmt.Printf("Successfully downloaded CRD: %s\n", name)
	}

	return nil
}

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	Expect(ensureExternalCRDs()).To(Succeed())

	ctx, cancel = context.WithCancel(context.TODO())

	var err error
	err = trainingv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	By("adding volcano api scheme")
	err = volcanoalpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	By("adding infra api scheme")
	err = infrav1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths: []string{externalCRDBaseDir},
		},
	}

	// Retrieve the first found binary directory to allow running tests from IDEs
	if getFirstFoundEnvTestBinaryDir() != "" {
		testEnv.BinaryAssetsDirectory = getFirstFoundEnvTestBinaryDir()
	}

	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// getFirstFoundEnvTestBinaryDir locates the first binary in the specified path.
// ENVTEST-based tests depend on specific binaries, usually located in paths set by
// controller-runtime. When running tests directly (e.g., via an IDE) without using
// Makefile targets, the 'BinaryAssetsDirectory' must be explicitly configured.
//
// This function streamlines the process by finding the required binaries, similar to
// setting the 'KUBEBUILDER_ASSETS' environment variable. To ensure the binaries are
// properly set up, run 'make setup-envtest' beforehand.
func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		logf.Log.Error(err, "Failed to read directory", "path", basePath)
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}

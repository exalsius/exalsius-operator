package workspaces

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

var _ = Describe("mergeResources", func() {
	It("should return class defaults when no user overrides", func() {
		defaults := workspacesv1.WorkspaceResourceSpec{
			PerNode: workspacesv1.ResourceRequirements{
				CPU:      resourceQuantityPtr("2"),
				Memory:   resourceQuantityPtr("8Gi"),
				GPUCount: int32Ptr(0),
			},
		}

		result := mergeResources(defaults, nil)
		Expect(result.PerNode.CPU.String()).To(Equal("2"))
		Expect(result.PerNode.Memory.String()).To(Equal("8Gi"))
		Expect(*result.PerNode.GPUCount).To(Equal(int32(0)))
	})

	It("should override only specified fields", func() {
		defaults := workspacesv1.WorkspaceResourceSpec{
			PerNode: workspacesv1.ResourceRequirements{
				CPU:      resourceQuantityPtr("2"),
				Memory:   resourceQuantityPtr("8Gi"),
				Storage:  resourceQuantityPtr("20Gi"),
				GPUCount: int32Ptr(0),
			},
		}

		overrides := &workspacesv1.WorkspaceResourceSpec{
			PerNode: workspacesv1.ResourceRequirements{
				CPU:      resourceQuantityPtr("4"),
				GPUCount: int32Ptr(2),
			},
		}

		result := mergeResources(defaults, overrides)
		// Overridden fields
		Expect(result.PerNode.CPU.String()).To(Equal("4"))
		Expect(*result.PerNode.GPUCount).To(Equal(int32(2)))
		// Fallback to defaults
		Expect(result.PerNode.Memory.String()).To(Equal("8Gi"))
		Expect(result.PerNode.Storage.String()).To(Equal("20Gi"))
	})

	It("should override nodeCount for MultiNode", func() {
		defaults := workspacesv1.WorkspaceResourceSpec{
			NodeCount: int32Ptr(3),
			PerNode: workspacesv1.ResourceRequirements{
				CPU: resourceQuantityPtr("8"),
			},
		}

		overrides := &workspacesv1.WorkspaceResourceSpec{
			NodeCount: int32Ptr(5),
		}

		result := mergeResources(defaults, overrides)
		Expect(*result.NodeCount).To(Equal(int32(5)))
		Expect(result.PerNode.CPU.String()).To(Equal("8"))
	})

	It("should override GPU vendor", func() {
		nvidia := workspacesv1.GPUVendorNVIDIA
		amd := workspacesv1.GPUVendorAMD
		defaults := workspacesv1.WorkspaceResourceSpec{
			PerNode: workspacesv1.ResourceRequirements{
				GPUVendor: &nvidia,
			},
		}

		overrides := &workspacesv1.WorkspaceResourceSpec{
			PerNode: workspacesv1.ResourceRequirements{
				GPUVendor: &amd,
			},
		}

		result := mergeResources(defaults, overrides)
		Expect(*result.PerNode.GPUVendor).To(Equal(workspacesv1.GPUVendorAMD))
	})
})

var _ = Describe("mergeValues", func() {
	It("should return empty string when both are nil", func() {
		result, err := mergeValues(nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeEmpty())
	})

	It("should return class defaults when user values are nil", func() {
		classDefaults := &apiextensionsv1.JSON{Raw: []byte(`{"key1":"value1","key2":"value2"}`)}
		result, err := mergeValues(classDefaults, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(ContainSubstring(`"key1":"value1"`))
		Expect(result).To(ContainSubstring(`"key2":"value2"`))
	})

	It("should return user values when class defaults are nil", func() {
		userValues := &apiextensionsv1.JSON{Raw: []byte(`{"password":"secret"}`)}
		result, err := mergeValues(nil, userValues)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(ContainSubstring(`"password":"secret"`))
	})

	It("should merge with user values taking precedence", func() {
		classDefaults := &apiextensionsv1.JSON{Raw: []byte(`{"key1":"default","key2":"keep"}`)}
		userValues := &apiextensionsv1.JSON{Raw: []byte(`{"key1":"override","key3":"new"}`)}
		result, err := mergeValues(classDefaults, userValues)
		Expect(err).NotTo(HaveOccurred())

		var merged map[string]any
		Expect(json.Unmarshal([]byte(result), &merged)).To(Succeed())
		Expect(merged["key1"]).To(Equal("override"))
		Expect(merged["key2"]).To(Equal("keep"))
		Expect(merged["key3"]).To(Equal("new"))
	})
})

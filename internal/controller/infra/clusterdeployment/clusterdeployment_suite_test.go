package clusterdeployment_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestClusterdeployment(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Clusterdeployment Suite")
}

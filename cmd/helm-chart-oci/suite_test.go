package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHelmChartOCICLI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "helm-chart-oci CLI")
}

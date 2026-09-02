package helmchartoci_test

import (
	"os"
	"path/filepath"

	"github.com/konflux-ci/tools/internal/helmchartoci"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chart", func() {
	It("reads chart name from Chart.yaml", func() {
		dir := GinkgoT().TempDir()
		writeChartFile(dir, `apiVersion: v2
name: product-chart
version: 0.1.0
`)

		got, err := helmchartoci.ChartNameFromFile(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("product-chart"))
	})

	Describe("ChartNameFromFile errors", func() {
		It("returns error when Chart.yaml is missing", func() {
			_, err := helmchartoci.ChartNameFromFile(GinkgoT().TempDir())
			Expect(err).To(HaveOccurred())
		})

		It("returns error for invalid yaml", func() {
			dir := GinkgoT().TempDir()
			writeChartFile(dir, "{")
			_, err := helmchartoci.ChartNameFromFile(dir)
			Expect(err).To(HaveOccurred())
		})

		It("returns error when name field is missing", func() {
			dir := GinkgoT().TempDir()
			writeChartFile(dir, "version: 0.1.0\n")
			_, err := helmchartoci.ChartNameFromFile(dir)
			Expect(err).To(HaveOccurred())
		})
	})

	DescribeTable("ParseOverwriteChartName",
		func(value string, want bool) {
			got, err := helmchartoci.ParseOverwriteChartName(value)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("empty defaults true", "", true),
		Entry("true", "true", true),
		Entry("TRUE", "TRUE", true),
		Entry("1", "1", true),
		Entry("yes", "yes", true),
		Entry("false", "false", false),
		Entry("0", "0", false),
		Entry("no", "no", false),
	)

	It("rejects invalid overwrite values", func() {
		_, err := helmchartoci.ParseOverwriteChartName("maybe")
		Expect(err).To(HaveOccurred())
	})

	It("overwrites chart name from image when enabled", func() {
		dir := GinkgoT().TempDir()
		writeChartFile(dir, `apiVersion: v2
name: old-name
version: 0.1.0
`)

		got, err := helmchartoci.ResolveChartName(dir, "quay.io/org/my-chart:tag", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("my-chart"))

		data, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("name: my-chart"))
	})

	It("preserves chart name from Chart.yaml when disabled", func() {
		dir := GinkgoT().TempDir()
		original := `apiVersion: v2
name: product-chart
version: 0.1.0
`
		writeChartFile(dir, original)

		got, err := helmchartoci.ResolveChartName(dir, "quay.io/org/product-v1-component:tag", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("product-chart"))

		data, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(original))
	})

	It("defaults overwrite to enabled", func() {
		Expect(helmchartoci.OverwriteChartNameEnabled(nil)).To(BeTrue())

		trueVal := true
		Expect(helmchartoci.OverwriteChartNameEnabled(&trueVal)).To(BeTrue())

		falseVal := false
		Expect(helmchartoci.OverwriteChartNameEnabled(&falseVal)).To(BeFalse())
	})

	It("rewrites only the name field in Chart.yaml", func() {
		chartDir := GinkgoT().TempDir()
		original := `apiVersion: v2
name: old
description: test chart
dependencies:
  - name: sub
    version: 1.0.0
`
		Expect(os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(original), 0o644)).To(Succeed())
		Expect(helmchartoci.SetChartName(chartDir, "new")).To(Succeed())

		data, err := os.ReadFile(filepath.Join(chartDir, "Chart.yaml"))
		Expect(err).NotTo(HaveOccurred())
		content := string(data)
		Expect(content).To(ContainSubstring("apiVersion: v2"))
		Expect(content).To(ContainSubstring("name: new"))
		Expect(content).To(ContainSubstring("description: test chart"))
		Expect(content).To(ContainSubstring("dependencies:"))
	})

	Describe("SetChartName errors", func() {
		It("returns error when Chart.yaml is missing", func() {
			Expect(helmchartoci.SetChartName(GinkgoT().TempDir(), "new")).NotTo(Succeed())
		})

		It("returns error for invalid yaml", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("{"), 0o644)).To(Succeed())
			Expect(helmchartoci.SetChartName(dir, "new")).NotTo(Succeed())
		})

		It("returns error when name field is missing", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("version: 0.0.1\n"), 0o644)).To(Succeed())
			Expect(helmchartoci.SetChartName(dir, "new")).NotTo(Succeed())
		})
	})
})

package helmchartoci_test

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/konflux-ci/tools/internal/helmchartoci"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/loader"
	v2chart "helm.sh/helm/v4/pkg/chart/v2"
)

var _ = Describe("Annotations", func() {
	DescribeTable("ParseAnnotations",
		func(entries []string, want map[string]string, wantErr bool) {
			got, err := helmchartoci.ParseAnnotations(entries)
			if wantErr {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("empty", nil, map[string]string{}, false),
		Entry("single", []string{"release-channel=rc"}, map[string]string{"release-channel": "rc"}, false),
		Entry("dotted key", []string{"git.commit=abc123"}, map[string]string{"git.commit": "abc123"}, false),
		Entry("value with equals", []string{"key=value=more"}, map[string]string{"key": "value=more"}, false),
		Entry("skips blank entries", []string{"", " a=b ", "c=d"}, map[string]string{"a": "b", "c": "d"}, false),
		Entry("invalid without equals", []string{"invalid"}, nil, true),
		Entry("invalid empty key", []string{"=value"}, nil, true),
		Entry("empty value", []string{"optional="}, map[string]string{"optional": ""}, false),
		Entry("spaces in value", []string{"description=helm chart for product"}, map[string]string{"description": "helm chart for product"}, false),
		Entry("OCI URL with query", []string{"org.opencontainers.image.source=https://github.com/konflux-ci/tools.git?ref=main"}, map[string]string{"org.opencontainers.image.source": "https://github.com/konflux-ci/tools.git?ref=main"}, false),
		Entry("timestamp", []string{"org.opencontainers.image.created=2024-02-02T00:00:00Z"}, map[string]string{"org.opencontainers.image.created": "2024-02-02T00:00:00Z"}, false),
		Entry("version with plus", []string{"org.opencontainers.image.version=1.2.3+abcdef1"}, map[string]string{"org.opencontainers.image.version": "1.2.3+abcdef1"}, false),
	)

	It("reloads varied annotation formats from Chart.yaml", func() {
		long := "summary=" + strings.Repeat("x", 400)
		entries := []string{
			"release-channel=release-candidate",
			"git.commit=abcdef1234567890abcdef1234567890abcdef12",
			"org.opencontainers.image.source=https://github.com/konflux-ci/tools.git?ref=main",
			"org.opencontainers.image.created=2024-02-02T00:00:00Z",
			"org.opencontainers.image.version=1.2.3+abcdef1",
			"build-args=GOFLAGS=-mod=vendor",
			"description=helm chart for product",
			long,
			"optional=",
		}
		parsed, err := helmchartoci.ParseAnnotations(entries)
		Expect(err).NotTo(HaveOccurred())

		dir := GinkgoT().TempDir()
		writeChartFile(dir, `apiVersion: v2
name: product-chart
version: 0.1.0
`)
		Expect(helmchartoci.ApplyChartAnnotations(dir, parsed)).To(Succeed())

		data, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
		Expect(err).NotTo(HaveOccurred())
		var doc struct {
			Annotations map[string]string `yaml:"annotations"`
		}
		Expect(yaml.Unmarshal(data, &doc)).To(Succeed())
		Expect(doc.Annotations).To(Equal(parsed))
	})

	It("merges annotations into Chart.yaml", func() {
		dir := GinkgoT().TempDir()
		writeChartFile(dir, `apiVersion: v2
name: product-chart
version: 0.1.0
annotations:
  static: kept
`)

		Expect(helmchartoci.ApplyChartAnnotations(dir, map[string]string{
			"release-channel": "release-candidate",
			"git.commit":      "abcdef1234567890",
			"static":          "overwritten",
		})).To(Succeed())

		data, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
		Expect(err).NotTo(HaveOccurred())
		content := string(data)
		Expect(content).To(ContainSubstring("release-channel: release-candidate"))
		Expect(content).To(ContainSubstring("git.commit: abcdef1234567890"))
		Expect(content).To(ContainSubstring("static: overwritten"))
	})

	It("emits new annotations in deterministic key order", func() {
		dir := GinkgoT().TempDir()
		writeChartFile(dir, `apiVersion: v2
name: product-chart
version: 0.1.0
`)

		Expect(helmchartoci.ApplyChartAnnotations(dir, map[string]string{
			"zebra":  "z",
			"alpha":  "a",
			"middle": "m",
		})).To(Succeed())

		data, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
		Expect(err).NotTo(HaveOccurred())
		content := string(data)
		Expect(strings.Index(content, "alpha:")).To(BeNumerically("<", strings.Index(content, "middle:")))
		Expect(strings.Index(content, "middle:")).To(BeNumerically("<", strings.Index(content, "zebra:")))
	})

	It("creates annotations when Chart.yaml has none", func() {
		dir := GinkgoT().TempDir()
		writeChartFile(dir, `apiVersion: v2
name: product-chart
version: 0.1.0
`)

		Expect(helmchartoci.ApplyChartAnnotations(dir, map[string]string{
			"build-id": "2.4.0-rc.1-abcdef1",
		})).To(Succeed())

		data, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("build-id: 2.4.0-rc.1-abcdef1"))
	})

	It("is a no-op for empty annotations", func() {
		dir := GinkgoT().TempDir()
		original := `apiVersion: v2
name: product-chart
version: 0.1.0
`
		writeChartFile(dir, original)

		Expect(helmchartoci.ApplyChartAnnotations(dir, map[string]string{})).To(Succeed())

		data, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(original))
	})

	It("preserves YAML-like annotation values when packaging", func() {
		dir := GinkgoT().TempDir()
		writeChartFile(dir, `apiVersion: v2
name: product-chart
version: 0.1.0
`)

		Expect(helmchartoci.ApplyChartAnnotations(dir, map[string]string{
			"literal-null": "null",
			"literal-true": "true",
			"literal-int":  "123",
		})).To(Succeed())

		data, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
		Expect(err).NotTo(HaveOccurred())
		content := string(data)
		Expect(content).To(ContainSubstring(`literal-null: "null"`))
		Expect(content).To(ContainSubstring(`literal-true: "true"`))
		Expect(content).To(ContainSubstring(`literal-int: "123"`))

		pkg := action.NewPackage()
		archive, err := pkg.Run(dir, nil)
		Expect(err).NotTo(HaveOccurred())

		ch, err := loader.Load(archive)
		Expect(err).NotTo(HaveOccurred())
		chart, ok := ch.(*v2chart.Chart)
		Expect(ok).To(BeTrue(), "expected v2 chart")
		Expect(chart.Metadata.Annotations).To(Equal(map[string]string{
			"literal-null": "null",
			"literal-true": "true",
			"literal-int":  "123",
		}))
		Expect(os.Remove(archive)).To(Succeed())
	})

	It("packages chart metadata with merged annotations", func() {
		dir := GinkgoT().TempDir()
		writeChartFile(dir, `apiVersion: v2
name: product-chart
version: 0.1.0
`)

		Expect(helmchartoci.ApplyChartAnnotations(dir, map[string]string{
			"release-channel": "release-candidate",
			"git.commit":      "abcdef1234567890",
		})).To(Succeed())

		pkg := action.NewPackage()
		pkg.Version = "1.0.0"
		pkg.AppVersion = "1.0.0"
		archive, err := pkg.Run(dir, nil)
		Expect(err).NotTo(HaveOccurred())

		ch, err := loader.Load(archive)
		Expect(err).NotTo(HaveOccurred())
		var metadata *v2chart.Metadata
		switch chart := ch.(type) {
		case v2chart.Chart:
			metadata = chart.Metadata
		case *v2chart.Chart:
			metadata = chart.Metadata
		default:
			Fail("expected v2 chart")
		}
		Expect(metadata.Annotations).To(Equal(map[string]string{
			"release-channel": "release-candidate",
			"git.commit":      "abcdef1234567890",
		}))
		Expect(os.Remove(archive)).To(Succeed())
	})

	It("coerces a non-mapping annotations field into a mapping", func() {
		dir := GinkgoT().TempDir()
		writeChartFile(dir, `apiVersion: v2
name: product-chart
version: 0.1.0
annotations: legacy-scalar
`)

		Expect(helmchartoci.ApplyChartAnnotations(dir, map[string]string{
			"release-channel": "rc",
		})).To(Succeed())

		data, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("release-channel: rc"))
	})

	Describe("ApplyChartAnnotations errors", func() {
		It("returns error when Chart.yaml is missing", func() {
			Expect(helmchartoci.ApplyChartAnnotations(GinkgoT().TempDir(), map[string]string{
				"a": "b",
			})).NotTo(Succeed())
		})

		It("returns error for invalid yaml", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("{"), 0o644)).To(Succeed())
			Expect(helmchartoci.ApplyChartAnnotations(dir, map[string]string{
				"a": "b",
			})).NotTo(Succeed())
		})
	})
})

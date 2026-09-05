package main

import (
	"context"
	"errors"

	"github.com/konflux-ci/tools/internal/helmchartoci"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CLI", func() {
	It("parses explicit flags and positional values files", func() {
		cfg, err := parseCLI(func(string) string { return "" }, []string{
			"--image", "quay.io/org/chart:tag",
			"--chart-version", "1.0.0",
			"--source-code-dir", "src",
			"--chart-context", "charts/app",
			"values.yaml", "values-prod.yaml",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.image).To(Equal("quay.io/org/chart:tag"))
		Expect(cfg.chartVersion).To(Equal("1.0.0"))
		Expect(cfg.valuesFiles).To(Equal([]string{"values.yaml", "values-prod.yaml"}))
	})

	It("applies environment defaults", func() {
		env := func(key string) string {
			switch key {
			case "IMAGE":
				return "quay.io/org/chart:tag"
			case "CHART_VERSION":
				return "2.0.0"
			case "SOURCE_CODE_DIR":
				return "source"
			case "CHART_CONTEXT":
				return "dist/chart"
			case "TAG_PREFIX":
				return "v"
			case "IMAGE_MAPPINGS":
				return "[]"
			default:
				return ""
			}
		}

		cfg, err := parseCLI(env, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.sourceCodeDir).To(Equal("source"))
		Expect(cfg.tagPrefix).To(Equal("v"))
		Expect(cfg.valuesFiles).To(Equal([]string{"values.yaml"}))
	})

	DescribeTable("parseCLI errors",
		func(args []string) {
			_, err := parseCLI(func(string) string { return "" }, args)
			Expect(err).To(HaveOccurred())
		},
		Entry("missing image", []string{"--commit-sha", "abc"}),
		Entry("missing commit", []string{"--image", "quay.io/org/chart:tag"}),
		Entry("unknown flag", []string{"--image", "x", "--chart-version", "1.0.0", "--unknown"}),
	)

	It("resolves envOr from environment or fallback", func() {
		env := func(key string) string {
			if key == "SET" {
				return "value"
			}
			return ""
		}
		Expect(envOr(env, "SET", "fallback")).To(Equal("value"))
		Expect(envOr(env, "MISSING", "fallback")).To(Equal("fallback"))
	})

	It("parses overwrite chart name from env and flag", func() {
		env := func(key string) string {
			switch key {
			case "IMAGE":
				return "quay.io/org/chart:tag"
			case "CHART_VERSION":
				return "1.0.0"
			case "OVERWRITE_CHART_NAME":
				return "false"
			default:
				return ""
			}
		}

		cfg, err := parseCLI(env, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.overwriteChartName).To(BeFalse())

		cfg, err = parseCLI(func(string) string { return "" }, []string{
			"--image", "quay.io/org/chart:tag",
			"--chart-version", "1.0.0",
			"--overwrite-chart-name=false",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.overwriteChartName).To(BeFalse())
	})

	It("defaults overwrite chart name to true", func() {
		cfg, err := parseCLI(func(string) string { return "" }, []string{
			"--image", "quay.io/org/chart:tag",
			"--chart-version", "1.0.0",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.overwriteChartName).To(BeTrue())
	})

	It("allows flag to override invalid OVERWRITE_CHART_NAME env", func() {
		env := func(key string) string {
			switch key {
			case "OVERWRITE_CHART_NAME":
				return "maybe"
			default:
				return ""
			}
		}

		cfg, err := parseCLI(env, []string{
			"--image", "quay.io/org/chart:tag",
			"--chart-version", "1.0.0",
			"--overwrite-chart-name=false",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.overwriteChartName).To(BeFalse())
	})

	It("rejects invalid OVERWRITE_CHART_NAME env when flag is unset", func() {
		env := func(key string) string {
			switch key {
			case "IMAGE":
				return "quay.io/org/chart:tag"
			case "CHART_VERSION":
				return "1.0.0"
			case "OVERWRITE_CHART_NAME":
				return "maybe"
			default:
				return ""
			}
		}

		_, err := parseCLI(env, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("OVERWRITE_CHART_NAME"))
	})

	It("passes overwrite chart name to Run", func() {
		var got helmchartoci.RunOptions
		overwrite := false
		Expect(execute(context.Background(), cliConfig{
			image:              "quay.io/org/chart:tag",
			chartVersion:       "1.0.0",
			sourceCodeDir:      "source",
			chartContext:       "chart",
			overwriteChartName: false,
		}, func(_ context.Context, opts helmchartoci.RunOptions) error {
			got = opts
			return nil
		})).To(Succeed())
		Expect(got.OverwriteChartName).NotTo(BeNil())
		Expect(*got.OverwriteChartName).To(Equal(overwrite))
	})

	It("builds run options from CLI config", func() {
		var got helmchartoci.RunOptions
		Expect(execute(context.Background(), cliConfig{
			image:         "quay.io/org/chart:tag",
			chartVersion:  "1.0.0",
			sourceCodeDir: "source",
			chartContext:  "chart",
			valuesFiles:   []string{"values.yaml"},
		}, func(_ context.Context, opts helmchartoci.RunOptions) error {
			got = opts
			return nil
		})).To(Succeed())
		Expect(got.Image).To(Equal("quay.io/org/chart:tag"))
		Expect(got.Git).To(BeNil())
	})

	It("uses git when chart version is unset", func() {
		var got helmchartoci.RunOptions
		Expect(execute(context.Background(), cliConfig{
			image:         "quay.io/org/chart:tag",
			commitSHA:     "abc123",
			sourceCodeDir: "source",
			chartContext:  "chart",
		}, func(_ context.Context, opts helmchartoci.RunOptions) error {
			got = opts
			return nil
		})).To(Succeed())
		Expect(got.Git).NotTo(BeNil())
	})

	It("exits successfully for --help", func() {
		code := runMain([]string{"--help"}, func(string) string { return "" }, nil)
		Expect(code).To(BeZero())
	})

	It("maps runMain exit codes", func() {
		code := runMain([]string{
			"--image", "quay.io/org/chart:tag",
			"--chart-version", "1.0.0",
		}, func(string) string { return "" }, func(context.Context, helmchartoci.RunOptions) error {
			return nil
		})
		Expect(code).To(BeZero())

		code = runMain([]string{"--image", "quay.io/org/chart:tag"}, func(string) string { return "" }, nil)
		Expect(code).To(Equal(1))

		code = runMain([]string{
			"--image", "quay.io/org/chart:tag",
			"--chart-version", "1.0.0",
		}, func(string) string { return "" }, func(context.Context, helmchartoci.RunOptions) error {
			return errors.New("boom")
		})
		Expect(code).To(Equal(1))
	})
})

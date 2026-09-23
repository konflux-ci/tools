package main

import (
	"context"
	"errors"
	"strings"

	"github.com/konflux-ci/tools/internal/helmchartoci"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CLI", func() {
	It("parses explicit flags including values files", func() {
		cfg, err := parseCLI(func(string) string { return "" }, []string{
			"--image", "quay.io/org/chart:tag",
			"--chart-version", "1.0.0",
			"--source-code-dir", "src",
			"--chart-context", "charts/app",
			"--annotation", "release-channel=rc",
			"--annotation", "git.commit=abc123",
			"--values", "values.yaml", "values-prod.yaml",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.image).To(Equal("quay.io/org/chart:tag"))
		Expect(cfg.chartVersion).To(Equal("1.0.0"))
		Expect(cfg.annotations).To(Equal([]string{"release-channel=rc", "git.commit=abc123"}))
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
		Entry("positional values file", []string{"--image", "x", "--chart-version", "1.0.0", "values.yaml"}),
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

	It("parses push chart to image repository from env and flag", func() {
		env := func(key string) string {
			switch key {
			case "IMAGE":
				return "quay.io/org/chart:tag"
			case "CHART_VERSION":
				return "1.0.0"
			case "PUSH_CHART_TO_IMAGE_REPOSITORY":
				return "true"
			default:
				return ""
			}
		}

		cfg, err := parseCLI(env, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.pushChartToImageRepository).To(BeTrue())

		cfg, err = parseCLI(func(string) string { return "" }, []string{
			"--image", "quay.io/org/chart:tag",
			"--chart-version", "1.0.0",
			"--push-chart-to-image-repository=true",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.pushChartToImageRepository).To(BeTrue())
	})

	It("defaults push chart to image repository to false", func() {
		cfg, err := parseCLI(func(string) string { return "" }, []string{
			"--image", "quay.io/org/chart:tag",
			"--chart-version", "1.0.0",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.pushChartToImageRepository).To(BeFalse())
	})

	It("rejects invalid PUSH_CHART_TO_IMAGE_REPOSITORY env when flag is unset", func() {
		env := func(key string) string {
			switch key {
			case "IMAGE":
				return "quay.io/org/chart:tag"
			case "CHART_VERSION":
				return "1.0.0"
			case "PUSH_CHART_TO_IMAGE_REPOSITORY":
				return "maybe"
			default:
				return ""
			}
		}

		_, err := parseCLI(env, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("PUSH_CHART_TO_IMAGE_REPOSITORY"))
	})

	DescribeTable("expandArrayFlags",
		func(args, want []string) {
			Expect(expandArrayFlags(args, "annotation", "values")).To(Equal(want))
		},
		Entry("nil", nil, []string{}),
		Entry("passthrough", []string{"--image", "quay.io/org/chart:tag"}, []string{"--image", "quay.io/org/chart:tag"}),
		Entry("groups annotations until the next flag", []string{"--annotation", "a=1", "b=2", "--image", "x"}, []string{"--annotation", "a=1", "--annotation", "b=2", "--image", "x"}),
		Entry("groups values until the next flag", []string{"--values", "a.yaml", "b.yaml", "--image", "x"}, []string{"--values", "a.yaml", "--values", "b.yaml", "--image", "x"}),
		Entry("groups both array flags", []string{"--annotation", "a=1", "b=2", "--values", "v.yaml", "w.yaml"}, []string{"--annotation", "a=1", "--annotation", "b=2", "--values", "v.yaml", "--values", "w.yaml"}),
		Entry("drops a bare annotation flag", []string{"--annotation", "--values", "values.yaml"}, []string{"--values", "values.yaml"}),
		Entry("drops a bare values flag", []string{"--values", "--annotation", "a=1"}, []string{"--annotation", "a=1"}),
		Entry("drops a trailing bare flag", []string{"--chart-version", "1.0.0", "--annotation"}, []string{"--chart-version", "1.0.0"}),
		Entry("keeps an equals form and following values", []string{"--annotation=a=1", "b=2"}, []string{"--annotation", "a=1", "--annotation", "b=2"}),
		Entry("keeps an equals form for values", []string{"--values=v.yaml", "w.yaml"}, []string{"--values", "v.yaml", "--values", "w.yaml"}),
		Entry("stops before a dash-prefixed value", []string{"--annotation", "a=1", "-b=2"}, []string{"--annotation", "a=1", "-b=2"}),
		Entry("does not rewrite arguments after --", []string{"--", "--annotation", "a=1", "b=2"}, []string{"--", "--annotation", "a=1", "b=2"}),
		Entry("leaves already repeated flags unchanged", []string{"--annotation", "a=1", "--annotation", "b=2"}, []string{"--annotation", "a=1", "--annotation", "b=2"}),
	)

	It("accepts grouped annotation and values flags", func() {
		cfg, err := parseCLI(func(string) string { return "" }, []string{
			"--image", "quay.io/org/chart:tag",
			"--chart-version", "1.0.0",
			"--annotation", "release-channel=rc", "git.commit=abc123",
			"--values", "values.yaml", "values-prod.yaml",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.annotations).To(Equal([]string{"release-channel=rc", "git.commit=abc123"}))
		Expect(cfg.valuesFiles).To(Equal([]string{"values.yaml", "values-prod.yaml"}))
	})

	It("ignores bare array flags from empty Tekton arrays", func() {
		cfg, err := parseCLI(func(string) string { return "" }, []string{
			"--image", "quay.io/org/chart:tag",
			"--chart-version", "1.0.0",
			"--annotation",
			"--values",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.annotations).To(BeEmpty())
		Expect(cfg.valuesFiles).To(Equal([]string{"values.yaml"}))
	})

	It("rejects leftover positional arguments", func() {
		_, err := parseCLI(func(string) string { return "" }, []string{
			"--image", "quay.io/org/chart:tag",
			"--chart-version", "1.0.0",
			"values.yaml",
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`unexpected argument "values.yaml"`))
		Expect(err.Error()).To(ContainSubstring("--values"))
	})

	It("parses an equals-form annotation flag", func() {
		cfg, err := parseCLI(func(string) string { return "" }, []string{
			"--image", "quay.io/org/chart:tag",
			"--chart-version", "1.0.0",
			"--annotation=release-channel=rc",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.annotations).To(Equal([]string{"release-channel=rc"}))
		Expect(cfg.valuesFiles).To(Equal([]string{"values.yaml"}))
	})

	It("parses grouped annotations in different formats", func() {
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
		args := append([]string{
			"--image", "quay.io/org/chart:tag",
			"--chart-version", "1.0.0",
			"--annotation",
		}, entries...)
		args = append(args, "--values", "values.yaml", "my=values.yaml")

		cfg, err := parseCLI(func(string) string { return "" }, args)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.annotations).To(Equal(entries))
		Expect(cfg.valuesFiles).To(Equal([]string{"values.yaml", "my=values.yaml"}))

		parsed, err := helmchartoci.ParseAnnotations(cfg.annotations)
		Expect(err).NotTo(HaveOccurred())
		Expect(parsed["org.opencontainers.image.source"]).To(Equal("https://github.com/konflux-ci/tools.git?ref=main"))
		Expect(parsed["description"]).To(Equal("helm chart for product"))
		Expect(parsed["summary"]).To(HaveLen(400))
		Expect(parsed["optional"]).To(BeEmpty())
	})

	It("parses the same annotation formats from ANNOTATIONS", func() {
		env := func(key string) string {
			switch key {
			case "IMAGE":
				return "quay.io/org/chart:tag"
			case "CHART_VERSION":
				return "1.0.0"
			case "ANNOTATIONS":
				return strings.Join([]string{
					"release-channel=release-candidate",
					"org.opencontainers.image.source=https://github.com/konflux-ci/tools.git?ref=main",
					"description=helm chart for product",
					"build-args=GOFLAGS=-mod=vendor",
					"optional=",
				}, "\n")
			default:
				return ""
			}
		}

		cfg, err := parseCLI(env, nil)
		Expect(err).NotTo(HaveOccurred())
		parsed, err := helmchartoci.ParseAnnotations(cfg.annotations)
		Expect(err).NotTo(HaveOccurred())
		Expect(parsed).To(Equal(map[string]string{
			"release-channel":                 "release-candidate",
			"org.opencontainers.image.source": "https://github.com/konflux-ci/tools.git?ref=main",
			"description":                     "helm chart for product",
			"build-args":                      "GOFLAGS=-mod=vendor",
			"optional":                        "",
		}))
	})

	It("prefers explicit annotation flags over duplicate env entries", func() {
		env := func(key string) string {
			switch key {
			case "IMAGE":
				return "quay.io/org/chart:tag"
			case "CHART_VERSION":
				return "1.0.0"
			case "ANNOTATIONS":
				return "release-channel=from-env\ngit.commit=from-env"
			default:
				return ""
			}
		}

		cfg, err := parseCLI(env, []string{
			"--annotation", "release-channel=from-flag",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.annotations).To(Equal([]string{
			"release-channel=from-env",
			"git.commit=from-env",
			"release-channel=from-flag",
		}))

		parsed, err := helmchartoci.ParseAnnotations(cfg.annotations)
		Expect(err).NotTo(HaveOccurred())
		Expect(parsed).To(Equal(map[string]string{
			"release-channel": "from-flag",
			"git.commit":      "from-env",
		}))
	})

	It("parses annotations from env", func() {
		env := func(key string) string {
			switch key {
			case "IMAGE":
				return "quay.io/org/chart:tag"
			case "CHART_VERSION":
				return "1.0.0"
			case "ANNOTATIONS":
				return "release-channel=rc\ngit.commit=abc123\n"
			default:
				return ""
			}
		}

		cfg, err := parseCLI(env, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.annotations).To(Equal([]string{"release-channel=rc", "git.commit=abc123"}))
	})

	It("passes annotations to Run", func() {
		var got helmchartoci.RunOptions
		Expect(execute(context.Background(), cliConfig{
			image:         "quay.io/org/chart:tag",
			chartVersion:  "1.0.0",
			sourceCodeDir: "source",
			chartContext:  "chart",
			annotations:   []string{"release-channel=rc"},
		}, func(_ context.Context, opts helmchartoci.RunOptions) error {
			got = opts
			return nil
		})).To(Succeed())
		Expect(got.Annotations).To(Equal([]string{"release-channel=rc"}))
	})

	It("passes push chart to image repository to Run", func() {
		var got helmchartoci.RunOptions
		Expect(execute(context.Background(), cliConfig{
			image:                      "quay.io/org/chart:tag",
			chartVersion:               "1.0.0",
			sourceCodeDir:              "source",
			chartContext:               "chart",
			pushChartToImageRepository: true,
		}, func(_ context.Context, opts helmchartoci.RunOptions) error {
			got = opts
			return nil
		})).To(Succeed())
		Expect(got.PushChartToImageRepository).To(BeTrue())
	})

	It("passes overwrite chart name to Run", func() {
		var got helmchartoci.RunOptions
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
		Expect(*got.OverwriteChartName).To(BeFalse())
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

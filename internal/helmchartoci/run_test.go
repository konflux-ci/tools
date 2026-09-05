package helmchartoci_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/konflux-ci/tools/internal/helmchartoci"
	"github.com/konflux-ci/tools/internal/helmchartoci/push"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Run", func() {
	It("packages and pushes a chart with default overwrite", func() {
		root := GinkgoT().TempDir()
		chartDir := filepath.Join(root, "chart")
		Expect(os.MkdirAll(chartDir, 0o755)).To(Succeed())
		writeChart(chartDir, "old-name")

		urlResult := filepath.Join(GinkgoT().TempDir(), "IMAGE_URL")
		digestResult := filepath.Join(GinkgoT().TempDir(), "IMAGE_DIGEST")

		pusher := &fakePusher{
			result: push.Result{
				ImageURL:    "quay.io/org/my-chart:1.0.0_test",
				ImageDigest: "sha256:deadbeef",
			},
		}

		Expect(helmchartoci.Run(context.Background(), helmchartoci.RunOptions{
			Image:             "quay.io/org/my-chart:tag",
			ChartVersion:      "1.0.0+test",
			AppVersion:        "app",
			SourceCodeDir:     root,
			ChartContext:      "chart",
			ImageURLResult:    urlResult,
			ImageDigestResult: digestResult,
			Pusher:            pusher,
		})).To(Succeed())

		Expect(pusher.opts.ChartName).To(Equal("my-chart"))
		Expect(pusher.opts.ChartVersion).To(Equal("1.0.0+test"))
		Expect(pusher.opts.AppVersion).To(Equal("app"))
		Expect(pusher.opts.Image).To(Equal("quay.io/org/my-chart:tag"))

		gotURL, err := os.ReadFile(urlResult)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(gotURL)).To(Equal("quay.io/org/my-chart:1.0.0_test"))

		gotDigest, err := os.ReadFile(digestResult)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(gotDigest)).To(Equal("sha256:deadbeef"))

		updated, err := os.ReadFile(filepath.Join(chartDir, "Chart.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(updated)).To(ContainSubstring("name: my-chart"))
	})

	It("preserves chart name when overwrite is disabled", func() {
		root := GinkgoT().TempDir()
		chartDir := filepath.Join(root, "chart")
		Expect(os.MkdirAll(chartDir, 0o755)).To(Succeed())
		writeChart(chartDir, "product-chart")

		pusher := &fakePusher{
			result: push.Result{
				ImageURL:    "quay.io/org/product-chart:1.0.0_test",
				ImageDigest: "sha256:deadbeef",
			},
		}

		overwrite := false
		Expect(helmchartoci.Run(context.Background(), helmchartoci.RunOptions{
			Image:              "quay.io/org/product-v1-component:tag",
			ChartVersion:       "1.0.0+test",
			SourceCodeDir:      root,
			ChartContext:       "chart",
			OverwriteChartName: &overwrite,
			Pusher:             pusher,
		})).To(Succeed())

		Expect(pusher.opts.ChartName).To(Equal("product-chart"))

		updated, err := os.ReadFile(filepath.Join(chartDir, "Chart.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(updated)).To(ContainSubstring("name: product-chart"))
		Expect(string(updated)).NotTo(ContainSubstring("product-v1-component"))
	})

	It("defaults overwrite chart name to enabled", func() {
		root := GinkgoT().TempDir()
		chartDir := filepath.Join(root, "chart")
		Expect(os.MkdirAll(chartDir, 0o755)).To(Succeed())
		writeChart(chartDir, "source-name")

		pusher := &fakePusher{}
		Expect(helmchartoci.Run(context.Background(), helmchartoci.RunOptions{
			Image:         "quay.io/org/delivery-chart:tag",
			ChartVersion:  "1.0.0",
			SourceCodeDir: root,
			ChartContext:  "chart",
			Pusher:        pusher,
		})).To(Succeed())
		Expect(pusher.opts.ChartName).To(Equal("delivery-chart"))
	})

	Describe("errors", func() {
		var (
			root     string
			chartDir string
			sentinel = errors.New("push failed")
		)

		BeforeEach(func() {
			root = GinkgoT().TempDir()
			chartDir = filepath.Join(root, "chart")
			Expect(os.MkdirAll(chartDir, 0o755)).To(Succeed())
			writeChart(chartDir, "chart")
		})

		It("returns error for invalid image", func() {
			err := helmchartoci.Run(context.Background(), helmchartoci.RunOptions{
				SourceCodeDir: root,
				ChartContext:  "chart",
			})
			Expect(err).To(HaveOccurred())
		})

		It("returns error for invalid mappings", func() {
			err := helmchartoci.Run(context.Background(), helmchartoci.RunOptions{
				Image:         "quay.io/org/chart:tag",
				ChartVersion:  "1.0.0",
				SourceCodeDir: root,
				ChartContext:  "chart",
				ImageMappings: "{",
			})
			Expect(err).To(HaveOccurred())
		})

		It("returns error from pusher", func() {
			err := helmchartoci.Run(context.Background(), helmchartoci.RunOptions{
				Image:         "quay.io/org/chart:tag",
				ChartVersion:  "1.0.0",
				SourceCodeDir: root,
				ChartContext:  "chart",
				Pusher:        &fakePusher{err: sentinel},
			})
			Expect(err).To(MatchError(sentinel))
		})

		It("returns error when chart name is missing", func() {
			root := GinkgoT().TempDir()
			chartDir := filepath.Join(root, "chart")
			Expect(os.MkdirAll(chartDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte("version: 0.0.1\n"), 0o644)).To(Succeed())

			err := helmchartoci.Run(context.Background(), helmchartoci.RunOptions{
				Image:         "quay.io/org/chart:tag",
				ChartVersion:  "1.0.0",
				SourceCodeDir: root,
				ChartContext:  "chart",
				Pusher:        &fakePusher{},
			})
			Expect(err).To(HaveOccurred())
		})

		It("returns error when result files cannot be written", func() {
			err := helmchartoci.Run(context.Background(), helmchartoci.RunOptions{
				Image:             "quay.io/org/chart:tag",
				ChartVersion:      "1.0.0",
				SourceCodeDir:     root,
				ChartContext:      "chart",
				ImageURLResult:    filepath.Join(root, "missing", "IMAGE_URL"),
				ImageDigestResult: filepath.Join(GinkgoT().TempDir(), "IMAGE_DIGEST"),
				Pusher:            &fakePusher{result: push.Result{ImageURL: "url", ImageDigest: "digest"}},
			})
			Expect(err).To(HaveOccurred())

			digestResult := filepath.Join(GinkgoT().TempDir(), "IMAGE_DIGEST")
			err = helmchartoci.Run(context.Background(), helmchartoci.RunOptions{
				Image:             "quay.io/org/chart:tag",
				ChartVersion:      "1.0.0",
				SourceCodeDir:     root,
				ChartContext:      "chart",
				ImageURLResult:    digestResult,
				ImageDigestResult: filepath.Join(root, "missing", "IMAGE_DIGEST"),
				Pusher:            &fakePusher{result: push.Result{ImageURL: "url", ImageDigest: "digest"}},
			})
			Expect(err).To(HaveOccurred())
		})

		It("returns error without registry auth when using default pusher", func() {
			err := helmchartoci.Run(context.Background(), helmchartoci.RunOptions{
				Image:         "quay.io/org/chart:tag",
				ChartVersion:  "1.0.0",
				SourceCodeDir: root,
				ChartContext:  "chart",
			})
			Expect(err).To(HaveOccurred())
		})
	})
})

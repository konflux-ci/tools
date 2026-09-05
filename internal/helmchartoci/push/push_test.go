package push

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OCI references", func() {
	DescribeTable("ociPushRef",
		func(imageRepo, chartName, chartVersion, want string) {
			Expect(ociPushRef(imageRepo, chartName, chartVersion)).To(Equal(want))
		},
		Entry(
			"nested tenant image path",
			"quay.io/redhat-user-workloads/konflux-vanguard-tenant/tekton-tools/helm-chart-oci-e2e",
			"helm-chart-oci-e2e",
			"0.1.0+test",
			"oci://quay.io/redhat-user-workloads/konflux-vanguard-tenant/tekton-tools/helm-chart-oci-e2e:0.1.0+test",
		),
		Entry("simple quay repo", "quay.io/org/my-chart", "my-chart", "1.2.3", "oci://quay.io/org/my-chart:1.2.3"),
		Entry("localhost with port", "localhost:5000/team/my-chart", "my-chart", "0.0.1", "oci://localhost:5000/team/my-chart:0.0.1"),
	)

	It("sanitizes chart version for OCI tags", func() {
		Expect(ociChartTag("1.2.3+abc")).To(Equal("1.2.3_abc"))
	})

	DescribeTable("parentRepo",
		func(imageRepo, want string) {
			Expect(parentRepo(imageRepo)).To(Equal(want))
		},
		Entry("quay repo", "quay.io/org/my-chart", "quay.io/org"),
		Entry("localhost with port", "localhost:5000/team/my-chart", "localhost:5000/team"),
		Entry("bare name", "my-chart", "my-chart"),
	)

	DescribeTable("pushedChartRef",
		func(imageRepo, chartName, ociTag, want string) {
			Expect(pushedChartRef(imageRepo, chartName, ociTag)).To(Equal(want))
		},
		Entry("matching names", "quay.io/org/my-chart", "my-chart", "1.0.0_build", "quay.io/org/my-chart:1.0.0_build"),
		Entry("decoupled chart and image basename", "quay.io/org/product-v1-component", "product-chart", "2.0.0", "quay.io/org/product-chart:2.0.0"),
	)
})

var _ = Describe("Client.PackageAndPush", func() {
	It("pushes decoupled chart names to the chart repository path", func() {
		opts := Options{
			ChartDir:     "/chart",
			ChartName:    "product-chart",
			ChartVersion: "1.0.0",
			AppVersion:   "test",
			ImageRepo:    "quay.io/org/product-v1-component",
			Image:        "quay.io/org/product-chart:on-pr-abc",
		}

		client := &Client{
			BuildDependencies: func(string) error { return nil },
			PackageChart:      func(Options) (string, error) { return "/tmp/product-chart.tgz", nil },
			PushChart: func(archive, dest, authFile string) error {
				Expect(dest).To(Equal("oci://quay.io/org/product-chart:1.0.0"))
				return nil
			},
			CopyImage: func(_ context.Context, src, dst string) error {
				Expect(src).To(Equal("quay.io/org/product-chart:1.0.0"))
				Expect(dst).To(Equal(opts.Image))
				return nil
			},
			ChartDigest: func(_ context.Context, ref string) (string, error) {
				Expect(ref).To(Equal("quay.io/org/product-chart:1.0.0"))
				return "sha256:abc", nil
			},
			ScopedAuth: func(string) (string, error) { return "/tmp/auth.json", nil },
		}

		result, err := client.PackageAndPush(context.Background(), opts)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.ImageURL).To(Equal("quay.io/org/product-chart:1.0.0"))
	})

	It("packages, pushes, tags, and records digest", func() {
		opts := Options{
			ChartDir:     "/chart",
			ChartName:    "my-chart",
			ChartVersion: "1.0.0+build",
			AppVersion:   "test",
			ImageRepo:    "quay.io/org/my-chart",
			Image:        "quay.io/org/my-chart:on-pr-abc",
		}

		client := &Client{
			BuildDependencies: func(string) error { return nil },
			PackageChart: func(Options) (string, error) {
				return "/tmp/my-chart.tgz", nil
			},
			PushChart: func(archive, dest, authFile string) error {
				Expect(archive).To(Equal("/tmp/my-chart.tgz"))
				Expect(dest).To(Equal("oci://quay.io/org/my-chart:1.0.0+build"))
				Expect(authFile).To(Equal("/tmp/auth.json"))
				return nil
			},
			CopyImage: func(_ context.Context, src, dst string) error {
				Expect(src).To(Equal("quay.io/org/my-chart:1.0.0_build"))
				Expect(dst).To(Equal(opts.Image))
				return nil
			},
			ChartDigest: func(_ context.Context, ref string) (string, error) {
				Expect(ref).To(Equal("quay.io/org/my-chart:1.0.0_build"))
				return "sha256:abc", nil
			},
			ScopedAuth: func(string) (string, error) { return "/tmp/auth.json", nil },
		}

		result, err := client.PackageAndPush(context.Background(), opts)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.ImageURL).To(Equal("quay.io/org/my-chart:1.0.0_build"))
		Expect(result.ImageDigest).To(Equal("sha256:abc"))
	})

	It("warns and continues when digest lookup fails", func() {
		client := &Client{
			BuildDependencies: func(string) error { return nil },
			PackageChart:      func(Options) (string, error) { return "/tmp/x.tgz", nil },
			PushChart:         func(string, string, string) error { return nil },
			CopyImage:         func(context.Context, string, string) error { return nil },
			ChartDigest:       func(context.Context, string) (string, error) { return "", errors.New("no digest") },
			ScopedAuth:        func(string) (string, error) { return "/tmp/auth.json", nil },
		}

		var result Result
		stderr := captureStderr(func() {
			var err error
			result, err = client.PackageAndPush(context.Background(), Options{
				ChartName:    "chart",
				ChartVersion: "1.0.0",
				ImageRepo:    "quay.io/org/chart",
				Image:        "quay.io/org/chart:tag",
			})
			Expect(err).NotTo(HaveOccurred())
		})
		Expect(result.ImageDigest).To(BeEmpty())
		Expect(stderr).To(ContainSubstring("Could not retrieve manifest digest from pushed image"))
		Expect(stderr).To(ContainSubstring("This does not affect the main functionality"))
	})

	DescribeTable("returns errors from pipeline stages",
		func(client *Client) {
			opts := Options{ChartDir: "/chart", ImageRepo: "quay.io/org/chart", Image: "quay.io/org/chart:tag"}
			_, err := client.PackageAndPush(context.Background(), opts)
			Expect(err).To(HaveOccurred())
		},
		Entry("build dependencies", &Client{
			BuildDependencies: func(string) error { return errors.New("boom") },
		}),
		Entry("package chart", &Client{
			BuildDependencies: func(string) error { return nil },
			PackageChart:      func(Options) (string, error) { return "", errors.New("boom") },
		}),
		Entry("scoped auth", &Client{
			BuildDependencies: func(string) error { return nil },
			PackageChart:      func(Options) (string, error) { return "/tmp/x.tgz", nil },
			ScopedAuth:        func(string) (string, error) { return "", errors.New("boom") },
		}),
		Entry("push chart", &Client{
			BuildDependencies: func(string) error { return nil },
			PackageChart:      func(Options) (string, error) { return "/tmp/x.tgz", nil },
			ScopedAuth:        func(string) (string, error) { return "/tmp/auth.json", nil },
			PushChart:         func(string, string, string) error { return errors.New("boom") },
		}),
		Entry("copy image", &Client{
			BuildDependencies: func(string) error { return nil },
			PackageChart:      func(Options) (string, error) { return "/tmp/x.tgz", nil },
			ScopedAuth:        func(string) (string, error) { return "/tmp/auth.json", nil },
			PushChart:         func(string, string, string) error { return nil },
			CopyImage:         func(context.Context, string, string) error { return errors.New("boom") },
		}),
	)
})

var _ = Describe("Chart packaging", func() {
	It("packages a chart archive", func() {
		chartDir := writeTestChart()

		archive, err := packageChart(Options{
			ChartDir:     chartDir,
			ChartVersion: "2.0.0+test",
			AppVersion:   "app",
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = os.Remove(archive) })
		Expect(archive).To(BeAnExistingFile())
	})

	It("returns error for missing chart directory", func() {
		_, err := packageChart(Options{
			ChartDir:     filepath.Join(GinkgoT().TempDir(), "missing"),
			ChartVersion: "1.0.0",
		})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Dependencies", func() {
	It("skips build when Chart.lock is absent", func() {
		Expect(buildDependencies(GinkgoT().TempDir())).To(Succeed())
	})

	It("skips build when Chart.lock has no dependencies", func() {
		chartDir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(chartDir, "Chart.lock"), []byte("dependencies: []\n"), 0o644)).To(Succeed())
		Expect(buildDependencies(chartDir)).To(Succeed())
	})

	It("returns error for invalid Chart.lock", func() {
		chartDir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(chartDir, "Chart.lock"), []byte("{"), 0o644)).To(Succeed())
		Expect(buildDependencies(chartDir)).To(HaveOccurred())
	})

	It("returns error when Chart.lock is a directory", func() {
		chartDir := GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(chartDir, "Chart.lock"), 0o755)).To(Succeed())
		Expect(buildDependencies(chartDir)).To(HaveOccurred())
	})
})

var _ = Describe("Registry auth", func() {
	It("scopes docker config to the target repository", func() {
		GinkgoT().Setenv("HOME", GinkgoT().TempDir())
		configDir := filepath.Join(os.Getenv("HOME"), ".docker")
		Expect(os.MkdirAll(configDir, 0o755)).To(Succeed())

		auth := map[string]any{"auth": "cXVheTpwYXNz"}
		config := map[string]any{
			"auths": map[string]any{
				"quay.io": auth,
			},
		}
		data, err := json.Marshal(config)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(configDir, "config.json"), data, 0o600)).To(Succeed())

		path, err := scopedRegistryAuth("quay.io/org/my-chart")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = os.Remove(path) })

		scopedData, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		var scoped struct {
			Auths map[string]json.RawMessage `json:"auths"`
		}
		Expect(json.Unmarshal(scopedData, &scoped)).To(Succeed())
		Expect(scoped.Auths).To(HaveKey("quay.io/org/my-chart"))
	})

	It("returns error when docker config is missing", func() {
		GinkgoT().Setenv("HOME", GinkgoT().TempDir())
		_, err := scopedRegistryAuth("quay.io/org/chart")
		Expect(err).To(HaveOccurred())
	})

	It("returns error for invalid docker config JSON", func() {
		home := GinkgoT().TempDir()
		GinkgoT().Setenv("HOME", home)
		configDir := filepath.Join(home, ".docker")
		Expect(os.MkdirAll(configDir, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{"), 0o600)).To(Succeed())
		_, err := scopedRegistryAuth("quay.io/org/chart")
		Expect(err).To(HaveOccurred())
	})

	It("returns error when repository auth key is missing", func() {
		home := GinkgoT().TempDir()
		GinkgoT().Setenv("HOME", home)
		configDir := filepath.Join(home, ".docker")
		Expect(os.MkdirAll(configDir, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"auths":{}}`), 0o600)).To(Succeed())
		_, err := scopedRegistryAuth("quay.io/org/chart")
		Expect(err).To(HaveOccurred())
	})

	It("looks up auth by full repository key", func() {
		auth := json.RawMessage(`{"auth":"abc"}`)
		auths := map[string]json.RawMessage{
			"quay.io/redhat-user-workloads/tenant/app": auth,
		}

		got, err := lookupDockerAuth(auths, "quay.io/redhat-user-workloads/tenant/app")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal(string(auth)))
	})

	It("returns error when auth is missing", func() {
		_, err := lookupDockerAuth(map[string]json.RawMessage{}, "quay.io/org/chart")
		Expect(err).To(HaveOccurred())
	})

	It("falls back to registry host keys", func() {
		keys := dockerAuthKeys("quay.io/org/my-chart")
		Expect(keys[0]).To(Equal("quay.io/org/my-chart"))
		Expect(keys).To(ContainElement("quay.io"))
	})
})

var _ = Describe("Push integration", func() {
	It("wires default client dependencies", func() {
		client := NewClient()
		Expect(client.BuildDependencies).NotTo(BeNil())
		Expect(client.PackageChart).NotTo(BeNil())
		Expect(client.PushChart).NotTo(BeNil())
		Expect(client.CopyImage).NotTo(BeNil())
		Expect(client.ChartDigest).NotTo(BeNil())
		Expect(client.ScopedAuth).NotTo(BeNil())
	})

	It("returns error without registry auth", func() {
		chartDir := writeTestChart()
		_, err := PackageAndPush(context.Background(), Options{
			ChartDir:     chartDir,
			ChartName:    "test-chart",
			ChartVersion: "1.0.0",
			AppVersion:   "app",
			ImageRepo:    "quay.io/org/test-chart",
			Image:        "quay.io/org/test-chart:tag",
		})
		Expect(err).To(HaveOccurred())
	})

	It("returns error when archive is missing", func() {
		Expect(pushChart(filepath.Join(GinkgoT().TempDir(), "missing.tgz"), "oci://x", "/tmp/auth")).NotTo(Succeed())
	})

	It("returns error when credentials file is missing", func() {
		archive := filepath.Join(GinkgoT().TempDir(), "chart.tgz")
		Expect(os.WriteFile(archive, []byte("not-a-chart"), 0o644)).To(Succeed())
		Expect(pushChart(archive, "oci://quay.io/org/chart:1.0.0", filepath.Join(GinkgoT().TempDir(), "missing.json"))).NotTo(Succeed())
	})
})

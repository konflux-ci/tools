package push

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/registry"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const tlsRegistryDomain = "registry.example.com"

func writeAnnotatedChart(chartDir string) {
	chartYAML := `apiVersion: v2
name: annotated-chart
description: annotation integration test
version: 0.1.0
annotations:
  release-channel: release-candidate
  build-id: 2.4.0-rc.1-abcdef1
  git.commit: abcdef1234567890
`
	Expect(os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(chartYAML), 0o644)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte("replicaCount: 1\n"), 0o644)).To(Succeed())
}

func configureRegistryAuth(home, registryHost string) {
	configDir := filepath.Join(home, ".docker")
	Expect(os.MkdirAll(configDir, 0o755)).To(Succeed())
	Expect(os.WriteFile(
		filepath.Join(configDir, "config.json"),
		[]byte(`{"auths":{"`+registryHost+`":{}}}`),
		0o600,
	)).To(Succeed())
}

var _ = Describe("Chart annotations OCI integration", func() {
	It("publishes Chart.yaml annotations on the OCI manifest via PackageAndPush", func() {
		srv, err := registry.TLS(tlsRegistryDomain)
		Expect(err).NotTo(HaveOccurred())
		defer srv.Close()

		home := GinkgoT().TempDir()
		GinkgoT().Setenv("HOME", home)
		configureRegistryAuth(home, tlsRegistryDomain)

		chartDir := GinkgoT().TempDir()
		writeAnnotatedChart(chartDir)

		imageRepo := tlsRegistryDomain + "/org/annotated-chart"
		client := NewClient(WithRegistryHTTPClient(srv.Client()))

		result, err := client.PackageAndPush(context.Background(), Options{
			ChartDir:     chartDir,
			ChartName:    "annotated-chart",
			ChartVersion: "1.0.0+e2e",
			AppVersion:   "v1.0.0-e2e",
			ImageRepo:    imageRepo,
			Image:        imageRepo + ":on-pr-abc",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.ImageURL).To(Equal(imageRepo + ":1.0.0_e2e"))
		Expect(result.ImageDigest).NotTo(BeEmpty())

		raw, err := crane.Manifest(
			imageRepo+":1.0.0_e2e",
			crane.WithTransport(srv.Client().Transport),
		)
		Expect(err).NotTo(HaveOccurred())

		var manifest struct {
			Annotations map[string]string `json:"annotations"`
		}
		Expect(json.Unmarshal(raw, &manifest)).To(Succeed())
		Expect(manifest.Annotations).To(HaveKeyWithValue("release-channel", "release-candidate"))
		Expect(manifest.Annotations).To(HaveKeyWithValue("build-id", "2.4.0-rc.1-abcdef1"))
		Expect(manifest.Annotations).To(HaveKeyWithValue("git.commit", "abcdef1234567890"))
		Expect(manifest.Annotations).To(HaveKeyWithValue("org.opencontainers.image.version", "1.0.0+e2e"))
	})
})

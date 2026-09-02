package push

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/registry"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/getter"
	helmregistry "helm.sh/helm/v4/pkg/registry"
)

const (
	testDepChartName    = "subchart"
	testDepChartVersion = "1.0.0"
)

func testHelmSettings() *cli.EnvSettings {
	settings := cli.New()
	cacheDir := GinkgoT().TempDir()
	settings.RepositoryCache = filepath.Join(cacheDir, "repository")
	settings.ContentCache = filepath.Join(cacheDir, "content")
	Expect(os.MkdirAll(settings.RepositoryCache, 0o755)).To(Succeed())
	Expect(os.MkdirAll(settings.ContentCache, 0o755)).To(Succeed())
	return settings
}

func packageTestChart(name, version string) []byte {
	chartDir := GinkgoT().TempDir()
	writeSubchart(chartDir, name, version)
	pkg := action.NewPackage()
	archive, err := pkg.Run(chartDir, nil)
	Expect(err).NotTo(HaveOccurred())
	data, err := os.ReadFile(archive)
	Expect(err).NotTo(HaveOccurred())
	return data
}

func chartArchiveName(name, version string) string {
	return fmt.Sprintf("%s-%s.tgz", name, version)
}

type httpChartRepo struct {
	URL      string
	Settings *cli.EnvSettings
	Cleanup  func()
}

func startHTTPChartRepo(archive []byte, name, version string, useTLS bool) httpChartRepo {
	settings := testHelmSettings()
	var baseURL string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/index.yaml"):
			_, err := fmt.Fprintf(w, `apiVersion: v1
entries:
  %s:
  - name: %s
    version: %s
    urls:
    - %s/charts/%s
`, name, name, version, baseURL, chartArchiveName(name, version))
			Expect(err).NotTo(HaveOccurred())
		case strings.HasSuffix(r.URL.Path, "/"+chartArchiveName(name, version)):
			_, err := w.Write(archive)
			Expect(err).NotTo(HaveOccurred())
		default:
			http.NotFound(w, r)
		}
	})

	var srv *httptest.Server
	if useTLS {
		srv = httptest.NewTLSServer(handler)
	} else {
		srv = httptest.NewServer(handler)
	}
	baseURL = srv.URL

	return httpChartRepo{
		URL:      srv.URL,
		Settings: settings,
		Cleanup:  srv.Close,
	}
}

type ociChartRegistry struct {
	RepositoryURL string
	Settings      *cli.EnvSettings
	Registry      *helmregistry.Client
	Cleanup       func()
}

func startOCIChartRegistry(archive []byte, name, version string) ociChartRegistry {
	settings := testHelmSettings()
	srv := httptest.NewServer(registry.New())
	client, err := helmregistry.NewClient(helmregistry.ClientOptPlainHTTP())
	Expect(err).NotTo(HaveOccurred())

	host := srv.Listener.Addr().String()
	dest := fmt.Sprintf("oci://%s/charts/%s:%s", host, name, version)
	_, err = client.Push(archive, dest)
	Expect(err).NotTo(HaveOccurred())

	return ociChartRegistry{
		RepositoryURL: fmt.Sprintf("oci://%s/charts", host),
		Settings:      settings,
		Registry:      client,
		Cleanup:       srv.Close,
	}
}

func dependencyConfigForHTTPRepo(repo httpChartRepo, useTLS bool) dependencyManagerConfig {
	var getters getter.Providers
	if useTLS {
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		getters = getter.All(repo.Settings, getter.WithTransport(transport))
	} else {
		getters = getter.All(repo.Settings)
	}
	return dependencyManagerConfig{
		settings: repo.Settings,
		getters:  getters,
		out:      io.Discard,
	}
}

func dependencyConfigForOCIRegistry(reg ociChartRegistry) dependencyManagerConfig {
	return dependencyManagerConfig{
		settings:       reg.Settings,
		getters:        getter.All(reg.Settings),
		registryClient: reg.Registry,
		out:            io.Discard,
	}
}

// Package push builds chart-format v2 archives (Chart.yaml apiVersion: v2) and
// publishes them to OCI using the Helm v4 SDK.
package push

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/downloader"
	"helm.sh/helm/v4/pkg/getter"
	"helm.sh/helm/v4/pkg/registry"
)

// Options configures chart packaging and OCI publication.
type Options struct {
	ChartDir     string
	ChartName    string
	ChartVersion string
	AppVersion   string
	ImageRepo    string
	Image        string
	// PushChartToImageRepository pushes under IMAGE's repository instead of the
	// tenant-level chart-name path used when Chart.yaml name differs from IMAGE.
	PushChartToImageRepository bool
}

// Result contains Tekton-compatible task results.
type Result struct {
	ImageURL    string
	ImageDigest string
}

type chartDependency struct {
	Name       string `yaml:"name"`
	Repository string `yaml:"repository"`
}

type chartYAML struct {
	Dependencies []chartDependency `yaml:"dependencies"`
}

func readChartYAML(chartDir string) (chartYAML, error) {
	path := filepath.Join(chartDir, "Chart.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return chartYAML{}, fmt.Errorf("read Chart.yaml: %w", err)
	}
	var meta chartYAML
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return chartYAML{}, fmt.Errorf("parse Chart.yaml: %w", err)
	}
	return meta, nil
}

// Client orchestrates chart packaging and OCI publication. Dependencies are
// injectable for testing.
type Client struct {
	BuildDependencies  func(chartDir string) error
	PackageChart       func(opts Options) (string, error)
	PushChart          func(archive, dest, authFile string, strict bool) error
	CopyImage          func(ctx context.Context, src, dst string) error
	ChartDigest        func(ctx context.Context, ref string) (string, error)
	ScopedAuth         func(imageRepo string) (string, error)
	registryHTTPClient *http.Client
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithRegistryHTTPClient overrides the HTTP client used for registry push,
// copy, and digest operations. Used by tests with in-memory TLS registries.
func WithRegistryHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.registryHTTPClient = httpClient
	}
}

// NewClient returns a Client wired to the default Helm and registry backends.
func NewClient(opts ...ClientOption) *Client {
	client := &Client{
		BuildDependencies: buildDependencies,
		PackageChart:      packageChart,
		ScopedAuth:        scopedRegistryAuth,
	}
	for _, opt := range opts {
		opt(client)
	}
	client.bindRegistryHooks()
	return client
}

func (c *Client) bindRegistryHooks() {
	httpClient := c.registryHTTPClient
	c.PushChart = func(archive, dest, authFile string, strict bool) error {
		return pushChart(archive, dest, authFile, strict, httpClient)
	}
	c.CopyImage = func(ctx context.Context, src, dst string) error {
		return copyImage(ctx, src, dst, httpClient)
	}
	c.ChartDigest = func(ctx context.Context, ref string) (string, error) {
		return chartDigest(ctx, ref, httpClient)
	}
}

// PackageAndPush is a convenience wrapper that creates a default Client and
// delegates to Client.PackageAndPush.
func PackageAndPush(ctx context.Context, opts Options) (Result, error) {
	return NewClient().PackageAndPush(ctx, opts)
}

// PackageAndPush builds chart dependencies, packages the chart, pushes to OCI, and tags IMAGE.
func (c *Client) PackageAndPush(ctx context.Context, opts Options) (Result, error) {
	if err := c.BuildDependencies(opts.ChartDir); err != nil {
		return Result{}, err
	}

	archive, err := c.PackageChart(opts)
	if err != nil {
		return Result{}, err
	}

	authFile, err := c.ScopedAuth(opts.ImageRepo)
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(authFile)

	ociTag := ociChartTag(opts.ChartVersion)
	dest := ociPushRef(opts.ImageRepo, opts.ChartName, opts.ChartVersion, opts.PushChartToImageRepository)
	strict := chartPushStrictMode(opts.ImageRepo, opts.ChartName, opts.PushChartToImageRepository)
	if err := c.PushChart(archive, dest, authFile, strict); err != nil {
		return Result{}, err
	}

	pushed := pushedChartRef(opts.ImageRepo, opts.ChartName, ociTag, opts.PushChartToImageRepository)
	if err := c.CopyImage(ctx, pushed, opts.Image); err != nil {
		return Result{}, fmt.Errorf("tag chart with %s: %w", opts.Image, err)
	}

	digest, err := c.ChartDigest(ctx, pushed)
	if err != nil {
		// Best-effort provenance: 0.3 logs and continues with an empty digest when
		// skopeo inspect fails; the chart push itself already succeeded.
		fmt.Fprintf(os.Stderr, "Could not retrieve manifest digest from pushed image: %v\n", err)
		fmt.Fprintln(os.Stderr, "This does not affect the main functionality")
		digest = ""
	}

	// IMAGE_URL is the semver-tagged chart ref (REPO:docker_tag), not opts.Image.
	// opts.Image receives an additional tag via CopyImage; see 0.3 semver_url assignment.
	return Result{
		ImageURL:    pushed,
		ImageDigest: digest,
	}, nil
}

// dependencyManagerConfig configures Helm's dependency downloader. Zero values use
// production defaults (stdout, default getters, and a new registry client).
type dependencyManagerConfig struct {
	settings       *cli.EnvSettings
	getters        getter.Providers
	registryClient *registry.Client
	out            io.Writer
}

func (c dependencyManagerConfig) resolved() (dependencyManagerConfig, error) {
	if c.settings == nil {
		c.settings = cli.New()
	}
	if c.getters == nil {
		c.getters = getter.All(c.settings)
	}
	if c.registryClient == nil {
		client, err := registry.NewClient()
		if err != nil {
			return dependencyManagerConfig{}, fmt.Errorf("create registry client: %w", err)
		}
		c.registryClient = client
	}
	if c.out == nil {
		c.out = os.Stdout
	}
	return c, nil
}

func buildDependencies(chartDir string) error {
	return buildDependenciesWith(chartDir, dependencyManagerConfig{})
}

func buildDependenciesWith(chartDir string, cfg dependencyManagerConfig) error {
	meta, err := readChartYAML(chartDir)
	if err != nil || len(meta.Dependencies) == 0 {
		return nil
	}

	cfg, err = cfg.resolved()
	if err != nil {
		return err
	}

	if err := registerHTTPChartRepositoriesFromChart(meta, cfg.settings, cfg.getters); err != nil {
		return err
	}

	man := &downloader.Manager{
		ChartPath:        chartDir,
		Out:              cfg.out,
		Getters:          cfg.getters,
		RegistryClient:   cfg.registryClient,
		RepositoryConfig: cfg.settings.RepositoryConfig,
		RepositoryCache:  cfg.settings.RepositoryCache,
		ContentCache:     cfg.settings.ContentCache,
	}
	if err := man.Build(); err != nil {
		return fmt.Errorf("build chart dependencies: %w", err)
	}
	return nil
}

func chartHasDependencies(chartDir string) bool {
	meta, err := readChartYAML(chartDir)
	if err != nil {
		return false
	}
	return len(meta.Dependencies) > 0
}

func packageChart(opts Options) (string, error) {
	pkg := action.NewPackage()
	pkg.Version = opts.ChartVersion
	pkg.AppVersion = opts.AppVersion
	archive, err := pkg.Run(opts.ChartDir, nil)
	if err != nil {
		return "", fmt.Errorf("package chart: %w", err)
	}
	return archive, nil
}

func copyImage(ctx context.Context, src, dst string, httpClient *http.Client) error {
	opts := []crane.Option{crane.WithContext(ctx)}
	if httpClient != nil {
		opts = append(opts, crane.WithTransport(httpClient.Transport))
	}
	return crane.Copy(src, dst, opts...)
}

func chartDigest(ctx context.Context, ref string, httpClient *http.Client) (string, error) {
	opts := []crane.Option{crane.WithContext(ctx)}
	if httpClient != nil {
		opts = append(opts, crane.WithTransport(httpClient.Transport))
	}
	return crane.Digest(ref, opts...)
}

func pushChart(archive, dest, authFile string, strict bool, httpClient *http.Client) error {
	data, err := os.ReadFile(archive)
	if err != nil {
		return fmt.Errorf("read chart archive: %w", err)
	}

	clientOpts := []registry.ClientOption{
		registry.ClientOptEnableCache(true),
		registry.ClientOptWriter(os.Stdout),
		registry.ClientOptCredentialsFile(authFile),
	}
	if httpClient != nil {
		clientOpts = append(clientOpts, registry.ClientOptHTTPClient(httpClient))
	}
	client, err := registry.NewClient(clientOpts...)
	if err != nil {
		return fmt.Errorf("create registry client: %w", err)
	}
	pushOpts := []registry.PushOption{}
	if !strict {
		pushOpts = append(pushOpts, registry.PushOptStrictMode(false))
	}
	_, err = client.Push(data, dest, pushOpts...)
	if err != nil {
		return fmt.Errorf("push chart to %s: %w", dest, err)
	}
	return nil
}

func parentRepo(imageRepo string) string {
	if idx := strings.LastIndex(imageRepo, "/"); idx >= 0 {
		return imageRepo[:idx]
	}
	return imageRepo
}

func repoBasename(imageRepo string) string {
	if idx := strings.LastIndex(imageRepo, "/"); idx >= 0 {
		return imageRepo[idx+1:]
	}
	return imageRepo
}

// chartOCIRepository returns the registry path (without tag) for the chart push.
// When pushToImageRepo is false, charts with a different Chart.yaml name are
// published under parent(imageRepo)/chartName so multiple components can share
// one delivery chart path. When true, charts are published under imageRepo so
// each Konflux ImageRepository receives artifacts in its credentialed repo
// while Chart.yaml metadata stays unchanged.
func chartOCIRepository(imageRepo, chartName string, pushToImageRepo bool) string {
	if !pushToImageRepo {
		return parentRepo(imageRepo) + "/" + chartName
	}
	return imageRepo
}

// chartPushStrictMode reports whether Helm's push strict check can stay enabled.
// Strict mode requires the OCI ref to end with /chartName:version. When pushing
// flat to imageRepo while Chart.yaml name differs from the repo basename, strict
// mode must be disabled.
func chartPushStrictMode(imageRepo, chartName string, pushToImageRepo bool) bool {
	if !pushToImageRepo {
		return true
	}
	return repoBasename(imageRepo) == chartName
}

// ociPushRef builds the OCI reference Helm expects in strict mode.
func ociPushRef(imageRepo, chartName, chartVersion string, pushToImageRepo bool) string {
	return fmt.Sprintf("oci://%s:%s", chartOCIRepository(imageRepo, chartName, pushToImageRepo), chartVersion)
}

// pushedChartRef returns the registry reference of the chart artifact after push.
func pushedChartRef(imageRepo, chartName, ociTag string, pushToImageRepo bool) string {
	return fmt.Sprintf("%s:%s", chartOCIRepository(imageRepo, chartName, pushToImageRepo), ociTag)
}

// ociChartTag replaces '+' with '_' for OCI registry tags (Helm convention).
func ociChartTag(chartVersion string) string {
	return strings.ReplaceAll(chartVersion, "+", "_")
}

func scopedRegistryAuth(imageRepo string) (string, error) {
	configPath := filepath.Join(os.Getenv("HOME"), ".docker", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("read docker config: %w", err)
	}

	var cfg struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse docker config: %w", err)
	}

	auth, err := lookupDockerAuth(cfg.Auths, imageRepo)
	if err != nil {
		return "", err
	}

	scoped, err := json.Marshal(map[string]any{
		"auths": map[string]json.RawMessage{
			imageRepo: auth,
		},
	})
	if err != nil {
		return "", err
	}

	file, err := os.CreateTemp("", "helm-chart-oci-auth-*.json")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.Write(scoped); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func lookupDockerAuth(auths map[string]json.RawMessage, imageRepo string) (json.RawMessage, error) {
	for _, key := range dockerAuthKeys(imageRepo) {
		if auth, ok := auths[key]; ok {
			return auth, nil
		}
	}
	return nil, fmt.Errorf("no auth for registry %s", imageRepo)
}

func dockerAuthKeys(imageRepo string) []string {
	keys := []string{imageRepo}
	ref, err := name.ParseReference(imageRepo + ":unused")
	if err != nil {
		return keys
	}
	host := ref.Context().RegistryStr()
	keys = append(keys, host, "https://"+host, "https://"+host+"/v2/")
	return keys
}

// Package push builds chart-format v2 archives (Chart.yaml apiVersion: v2) and
// publishes them to OCI using the Helm v4 SDK.
package push

import (
	"context"
	"encoding/json"
	"fmt"
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
}

// Result contains Tekton-compatible task results.
type Result struct {
	ImageURL    string
	ImageDigest string
}

type chartLock struct {
	Dependencies []struct {
		Name string `yaml:"name"`
	} `yaml:"dependencies"`
}

// Client orchestrates chart packaging and OCI publication. Dependencies are
// injectable for testing.
type Client struct {
	BuildDependencies func(chartDir string) error
	PackageChart      func(opts Options) (string, error)
	PushChart         func(archive, dest, authFile string) error
	CopyImage         func(ctx context.Context, src, dst string) error
	ChartDigest       func(ctx context.Context, ref string) (string, error)
	ScopedAuth        func(imageRepo string) (string, error)
}

// NewClient returns a Client wired to the default Helm and registry backends.
func NewClient() *Client {
	return &Client{
		BuildDependencies: buildDependencies,
		PackageChart:      packageChart,
		PushChart:         pushChart,
		CopyImage: func(ctx context.Context, src, dst string) error {
			return crane.Copy(src, dst, crane.WithContext(ctx))
		},
		ChartDigest: func(ctx context.Context, ref string) (string, error) {
			return crane.Digest(ref, crane.WithContext(ctx))
		},
		ScopedAuth: scopedRegistryAuth,
	}
}

// PackageAndPush builds chart dependencies, packages the chart, pushes to OCI, and tags IMAGE.
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
	dest := ociPushRef(opts.ImageRepo, opts.ChartName, opts.ChartVersion)
	if err := c.PushChart(archive, dest, authFile); err != nil {
		return Result{}, err
	}

	pushed := pushedChartRef(opts.ImageRepo, opts.ChartName, ociTag)
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

func buildDependencies(chartDir string) error {
	lockPath := filepath.Join(chartDir, "Chart.lock")
	if _, err := os.Stat(lockPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat Chart.lock: %w", err)
	}

	data, err := os.ReadFile(lockPath)
	if err != nil {
		return fmt.Errorf("read Chart.lock: %w", err)
	}
	var lock chartLock
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return fmt.Errorf("parse Chart.lock: %w", err)
	}
	if len(lock.Dependencies) == 0 {
		return nil
	}

	settings := cli.New()
	registryClient, err := registry.NewClient()
	if err != nil {
		return fmt.Errorf("create registry client: %w", err)
	}

	man := &downloader.Manager{
		ChartPath:        chartDir,
		Out:              os.Stdout,
		Getters:          getter.All(settings),
		RegistryClient:   registryClient,
		RepositoryConfig: settings.RepositoryConfig,
		RepositoryCache:  settings.RepositoryCache,
		ContentCache:     settings.ContentCache,
	}
	if err := man.Build(); err != nil {
		return fmt.Errorf("build chart dependencies: %w", err)
	}
	return nil
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

func pushChart(archive, dest, authFile string) error {
	data, err := os.ReadFile(archive)
	if err != nil {
		return fmt.Errorf("read chart archive: %w", err)
	}

	client, err := registry.NewClient(
		registry.ClientOptEnableCache(true),
		registry.ClientOptWriter(os.Stdout),
		registry.ClientOptCredentialsFile(authFile),
	)
	if err != nil {
		return fmt.Errorf("create registry client: %w", err)
	}
	_, err = client.Push(data, dest)
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

// ociPushRef builds the OCI reference Helm expects in strict mode, matching
// helm push: parent(imageRepo)/chartName:chartVersion.
func ociPushRef(imageRepo, chartName, chartVersion string) string {
	return fmt.Sprintf("oci://%s/%s:%s", parentRepo(imageRepo), chartName, chartVersion)
}

// pushedChartRef returns the registry reference of the chart artifact after push.
// The chart name may differ from the IMAGE repo basename when Chart.yaml name is preserved.
func pushedChartRef(imageRepo, chartName, ociTag string) string {
	return fmt.Sprintf("%s/%s:%s", parentRepo(imageRepo), chartName, ociTag)
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

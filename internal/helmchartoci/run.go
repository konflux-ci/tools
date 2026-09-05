package helmchartoci

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/konflux-ci/tools/internal/helmchartoci/push"
)

// ChartPusher packages and publishes a Helm chart to OCI.
type ChartPusher interface {
	PackageAndPush(ctx context.Context, opts push.Options) (push.Result, error)
}

type defaultChartPusher struct{}

func (defaultChartPusher) PackageAndPush(ctx context.Context, opts push.Options) (push.Result, error) {
	return push.PackageAndPush(ctx, opts)
}

// RunOptions mirrors build-helm-chart-oci-ta 0.3 parameters.
type RunOptions struct {
	Image              string
	CommitSHA          string
	SourceCodeDir      string
	ChartContext       string
	TagPrefix          string
	VersionSuffix      string
	ChartVersion       string
	AppVersion         string
	ImageMappings      string
	ValuesFiles        []string
	ImageURLResult     string
	ImageDigestResult  string
	OverwriteChartName *bool
	Git                Git
	Pusher             ChartPusher
}

// Run packages and pushes a Helm chart to OCI. See package helmchartoci documentation
// for parity expectations with build-helm-chart-oci-ta 0.3.
func Run(ctx context.Context, opts RunOptions) error {
	chartDir, err := filepath.Abs(filepath.Join(opts.SourceCodeDir, opts.ChartContext))
	if err != nil {
		return err
	}

	chartName, err := ResolveChartName(chartDir, opts.Image, OverwriteChartNameEnabled(opts.OverwriteChartName))
	if err != nil {
		return err
	}

	mappings, err := ParseImageMappings(opts.ImageMappings)
	if err != nil {
		return err
	}
	if err := ApplyImageMappings(chartDir, mappings, opts.ValuesFiles); err != nil {
		return err
	}

	chartVersion, err := ResolveChartVersion(
		opts.ChartVersion,
		opts.TagPrefix,
		opts.VersionSuffix,
		opts.CommitSHA,
		opts.Git,
	)
	if err != nil {
		return err
	}

	appVersion := ResolveAppVersion(opts.AppVersion, opts.CommitSHA)
	imageRepo := imageRepo(opts.Image)

	pusher := opts.Pusher
	if pusher == nil {
		pusher = defaultChartPusher{}
	}

	result, err := pusher.PackageAndPush(ctx, push.Options{
		ChartDir:     chartDir,
		ChartName:    chartName,
		ChartVersion: chartVersion,
		AppVersion:   appVersion,
		ImageRepo:    imageRepo,
		Image:        opts.Image,
	})
	if err != nil {
		return err
	}

	if opts.ImageURLResult != "" {
		if err := os.WriteFile(opts.ImageURLResult, []byte(result.ImageURL), 0o644); err != nil {
			return fmt.Errorf("write image url result: %w", err)
		}
	}
	if opts.ImageDigestResult != "" {
		if err := os.WriteFile(opts.ImageDigestResult, []byte(result.ImageDigest), 0o644); err != nil {
			return fmt.Errorf("write image digest result: %w", err)
		}
	}

	fmt.Printf("Pushed chart %s version %s\n", chartName, chartVersion)
	return nil
}

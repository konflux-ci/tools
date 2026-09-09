package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/konflux-ci/tools/internal/helmchartoci"
)

type cliConfig struct {
	image              string
	commitSHA          string
	sourceCodeDir      string
	chartContext       string
	tagPrefix          string
	versionSuffix      string
	chartVersion       string
	appVersion         string
	imageMappings      string
	imageURLResult     string
	imageDigestResult  string
	overwriteChartName bool
	valuesFiles        []string
}

func parseCLI(env func(string) string, args []string) (cliConfig, error) {
	fs := flag.NewFlagSet("helm-chart-oci", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	image := fs.String("image", env("IMAGE"), "Full image reference with tag")
	commitSHA := fs.String("commit-sha", env("COMMIT_SHA"), "Git commit SHA")
	sourceCodeDir := fs.String("source-code-dir", envOr(env, "SOURCE_CODE_DIR", "source"), "Source code directory")
	chartContext := fs.String("chart-context", envOr(env, "CHART_CONTEXT", "dist/chart/"), "Chart path relative to source code dir")
	tagPrefix := fs.String("tag-prefix", envOr(env, "TAG_PREFIX", "helm-"), "Git tag prefix for version resolution")
	versionSuffix := fs.String("version-suffix", env("VERSION_SUFFIX"), "Suffix appended to computed chart version")
	chartVersion := fs.String("chart-version", env("CHART_VERSION"), "Explicit chart version (skips git resolution)")
	appVersion := fs.String("app-version", env("APP_VERSION"), "Explicit appVersion override")
	imageMappings := fs.String("image-mappings", envOr(env, "IMAGE_MAPPINGS", "[]"), "JSON array of image mappings")
	imageURLResult := fs.String("image-url-result", "", "Path to write IMAGE_URL result")
	imageDigestResult := fs.String("image-digest-result", "", "Path to write IMAGE_DIGEST result")
	overwriteChartNameFlag := fs.Bool("overwrite-chart-name", true,
		"Rewrite Chart.yaml name from IMAGE repo basename (0.3 behavior)")

	if err := fs.Parse(args); err != nil {
		return cliConfig{}, err
	}

	overwriteChartName := *overwriteChartNameFlag
	overwriteChartNameSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "overwrite-chart-name" {
			overwriteChartNameSet = true
		}
	})
	if !overwriteChartNameSet {
		if envVal := env("OVERWRITE_CHART_NAME"); envVal != "" {
			parsed, err := helmchartoci.ParseOverwriteChartName(envVal)
			if err != nil {
				return cliConfig{}, err
			}
			overwriteChartName = parsed
		}
	}

	valuesFiles := fs.Args()
	if len(valuesFiles) == 0 {
		valuesFiles = []string{"values.yaml"}
	}

	if *image == "" {
		return cliConfig{}, fmt.Errorf("--image is required")
	}
	if *commitSHA == "" && *chartVersion == "" {
		return cliConfig{}, fmt.Errorf("--commit-sha is required when chart version is not set")
	}

	return cliConfig{
		image:              *image,
		commitSHA:          *commitSHA,
		sourceCodeDir:      *sourceCodeDir,
		chartContext:       *chartContext,
		tagPrefix:          *tagPrefix,
		versionSuffix:      *versionSuffix,
		chartVersion:       *chartVersion,
		appVersion:         *appVersion,
		imageMappings:      *imageMappings,
		imageURLResult:     *imageURLResult,
		imageDigestResult:  *imageDigestResult,
		overwriteChartName: overwriteChartName,
		valuesFiles:        valuesFiles,
	}, nil
}

func execute(ctx context.Context, cfg cliConfig, runFn func(context.Context, helmchartoci.RunOptions) error) error {
	chartDir, err := filepath.Abs(filepath.Join(cfg.sourceCodeDir, cfg.chartContext))
	if err != nil {
		return err
	}

	var git helmchartoci.Git
	if cfg.chartVersion == "" {
		git = &helmchartoci.ExecGit{Dir: chartDir}
	}

	return runFn(ctx, helmchartoci.RunOptions{
		Image:              cfg.image,
		CommitSHA:          cfg.commitSHA,
		SourceCodeDir:      cfg.sourceCodeDir,
		ChartContext:       cfg.chartContext,
		TagPrefix:          cfg.tagPrefix,
		VersionSuffix:      cfg.versionSuffix,
		ChartVersion:       cfg.chartVersion,
		AppVersion:         cfg.appVersion,
		ImageMappings:      cfg.imageMappings,
		ValuesFiles:        cfg.valuesFiles,
		ImageURLResult:     cfg.imageURLResult,
		ImageDigestResult:  cfg.imageDigestResult,
		OverwriteChartName: &cfg.overwriteChartName,
		Git:                git,
	})
}

func envOr(env func(string) string, key, fallback string) string {
	if value := env(key); value != "" {
		return value
	}
	return fallback
}

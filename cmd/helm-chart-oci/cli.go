package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/konflux-ci/tools/internal/helmchartoci"
)

type cliConfig struct {
	image                      string
	commitSHA                  string
	sourceCodeDir              string
	chartContext               string
	tagPrefix                  string
	versionSuffix              string
	chartVersion               string
	appVersion                 string
	imageMappings              string
	imageURLResult             string
	imageDigestResult          string
	overwriteChartName         bool
	pushChartToImageRepository bool
	annotations                []string
	valuesFiles                []string
}

func parseCLI(env func(string) string, args []string) (cliConfig, error) {
	args = expandArrayFlags(args, "annotation", "values")
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
	pushChartToImageRepositoryFlag := fs.Bool("push-chart-to-image-repository", false,
		"Publish under the IMAGE repository (oci://<IMAGE>:<version>) instead of oci://<parent(IMAGE)>/<chart-name>:<version>; use with OVERWRITE_CHART_NAME=false")
	annotations := annotationsFromEnv(env("ANNOTATIONS"))
	fs.Func("annotation", "OCI manifest annotation as key=value (repeatable; several values may follow one flag)", func(value string) error {
		annotations = append(annotations, value)
		return nil
	})
	var valuesFiles []string
	fs.Func("values", "Values file for image substitution (repeatable; several values may follow one flag)", func(value string) error {
		valuesFiles = append(valuesFiles, value)
		return nil
	})

	if err := fs.Parse(args); err != nil {
		return cliConfig{}, err
	}
	if leftover := fs.Args(); len(leftover) > 0 {
		return cliConfig{}, fmt.Errorf("unexpected argument %q; use --values for values files", leftover[0])
	}
	if len(valuesFiles) == 0 {
		valuesFiles = []string{"values.yaml"}
	}

	overwriteChartName, err := boolFlagWithEnv(
		fs,
		"overwrite-chart-name",
		*overwriteChartNameFlag,
		env,
		"OVERWRITE_CHART_NAME",
		helmchartoci.ParseOverwriteChartName,
	)
	if err != nil {
		return cliConfig{}, err
	}

	pushChartToImageRepository, err := boolFlagWithEnv(
		fs,
		"push-chart-to-image-repository",
		*pushChartToImageRepositoryFlag,
		env,
		"PUSH_CHART_TO_IMAGE_REPOSITORY",
		helmchartoci.ParsePushChartToImageRepository,
	)
	if err != nil {
		return cliConfig{}, err
	}

	if *image == "" {
		return cliConfig{}, fmt.Errorf("--image is required")
	}
	if *commitSHA == "" && *chartVersion == "" {
		return cliConfig{}, fmt.Errorf("--commit-sha is required when chart version is not set")
	}

	return cliConfig{
		image:                      *image,
		commitSHA:                  *commitSHA,
		sourceCodeDir:              *sourceCodeDir,
		chartContext:               *chartContext,
		tagPrefix:                  *tagPrefix,
		versionSuffix:              *versionSuffix,
		chartVersion:               *chartVersion,
		appVersion:                 *appVersion,
		imageMappings:              *imageMappings,
		imageURLResult:             *imageURLResult,
		imageDigestResult:          *imageDigestResult,
		overwriteChartName:         overwriteChartName,
		pushChartToImageRepository: pushChartToImageRepository,
		annotations:                annotations,
		valuesFiles:                valuesFiles,
	}, nil
}

// expandArrayFlags rewrites Tekton-style array flags into repeated flags, matching
// konflux-build-cli. `--annotation a b --values v1 v2` becomes
// `--annotation a --annotation b --values v1 --values v2`. A bare array flag with
// no following values is dropped so an empty Tekton array does not consume the
// next argument. Already-repeated flags are unchanged.
func expandArrayFlags(args []string, flagNames ...string) []string {
	multi := make(map[string]bool, len(flagNames))
	for _, name := range flagNames {
		multi["--"+name] = true
	}

	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			out = append(out, args[i:]...)
			break
		}

		flagName, inline, hasInline := splitArrayFlag(arg, multi)
		if flagName == "" {
			out = append(out, arg)
			continue
		}
		if hasInline {
			out = append(out, flagName, inline)
		}
		j := i + 1
		for j < len(args) && args[j] != "--" && !strings.HasPrefix(args[j], "-") {
			out = append(out, flagName, args[j])
			j++
		}
		i = j - 1
	}
	return out
}

func splitArrayFlag(arg string, multi map[string]bool) (flagName, inline string, hasInline bool) {
	if multi[arg] {
		return arg, "", false
	}
	if !strings.HasPrefix(arg, "--") {
		return "", "", false
	}
	name, value, ok := strings.Cut(arg, "=")
	if !ok || !multi[name] {
		return "", "", false
	}
	return name, value, true
}

func annotationsFromEnv(value string) []string {
	if value == "" {
		return nil
	}
	var entries []string
	for line := range strings.SplitSeq(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			entries = append(entries, line)
		}
	}
	return entries
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
		Image:                      cfg.image,
		CommitSHA:                  cfg.commitSHA,
		SourceCodeDir:              cfg.sourceCodeDir,
		ChartContext:               cfg.chartContext,
		TagPrefix:                  cfg.tagPrefix,
		VersionSuffix:              cfg.versionSuffix,
		ChartVersion:               cfg.chartVersion,
		AppVersion:                 cfg.appVersion,
		ImageMappings:              cfg.imageMappings,
		Annotations:                cfg.annotations,
		ValuesFiles:                cfg.valuesFiles,
		ImageURLResult:             cfg.imageURLResult,
		ImageDigestResult:          cfg.imageDigestResult,
		OverwriteChartName:         &cfg.overwriteChartName,
		PushChartToImageRepository: cfg.pushChartToImageRepository,
		Git:                        git,
	})
}

func envOr(env func(string) string, key, fallback string) string {
	if value := env(key); value != "" {
		return value
	}
	return fallback
}

func boolFlagWithEnv(
	fs *flag.FlagSet,
	flagName string,
	flagValue bool,
	env func(string) string,
	envKey string,
	parse func(string) (bool, error),
) (bool, error) {
	setOnCLI := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == flagName {
			setOnCLI = true
		}
	})
	if setOnCLI {
		return flagValue, nil
	}
	if envVal := env(envKey); envVal != "" {
		return parse(envVal)
	}
	return flagValue, nil
}

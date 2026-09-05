package helmchartoci

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var commitSHAPattern = regexp.MustCompile(`^[0-9a-fA-F]+$`)

// Git describes git commands used for chart version resolution.
type Git interface {
	FetchUnshallow(commitSHA string) error
	Describe(tagPrefix string) (string, error)
	ShortSHA() (string, error)
	CommitCount() (int, error)
}

// ExecGit runs git in chartDir. Runner is optional and used to inject git
// command execution in tests.
type ExecGit struct {
	Dir    string
	Runner func(args ...string) (string, error)
}

func validateCommitSHA(commitSHA string) error {
	if commitSHA == "" {
		return fmt.Errorf("commit sha is required")
	}
	if !commitSHAPattern.MatchString(commitSHA) {
		return fmt.Errorf("invalid commit sha %q", commitSHA)
	}
	return nil
}

func (g *ExecGit) run(args ...string) (string, error) {
	if g.Runner != nil {
		return g.Runner(args...)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = g.Dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *ExecGit) FetchUnshallow(commitSHA string) error {
	if err := validateCommitSHA(commitSHA); err != nil {
		return err
	}

	shallow, err := g.isShallow()
	if err != nil {
		return err
	}
	if shallow {
		_, err = g.run("fetch", "--unshallow", "--tags", "origin", commitSHA)
		return err
	}
	_, err = g.run("fetch", "--tags", "origin", commitSHA)
	return err
}

func (g *ExecGit) isShallow() (bool, error) {
	out, err := g.run("rev-parse", "--is-shallow-repository")
	if err != nil {
		return false, err
	}
	return out == "true", nil
}

func (g *ExecGit) Describe(tagPrefix string) (string, error) {
	return g.run("describe", "--tags", "--match="+tagPrefix+"*")
}

func (g *ExecGit) ShortSHA() (string, error) {
	return g.run("rev-parse", "--short", "HEAD")
}

func (g *ExecGit) CommitCount() (int, error) {
	out, err := g.run("rev-list", "HEAD", "--count")
	if err != nil {
		return 0, err
	}
	count, err := strconv.Atoi(out)
	if err != nil {
		return 0, fmt.Errorf("parse commit count %q: %w", out, err)
	}
	return count, nil
}

// ResolveChartVersion returns an explicit version or derives one from git metadata.
//
// When CHART_VERSION is unset, the version is derived from git tags matching
// TAG_PREFIX (default helm-). The flow mirrors build-helm-chart-oci-ta 0.3:
//
//  1. git describe --tags --match=TAG_PREFIX*
//  2. Convert describe output to semver via ChartVersionFromDescribe
//  3. If step 2 yields an empty version, use FallbackChartVersion
//     (0.1.<commit-count>+<short-sha>)
//
// If git describe fails for any reason (no matching tag, shallow history, git
// error), the failure is treated as empty describe output and the fallback path
// runs. This matches 0.3's pipeline without pipefail: a failed describe still
// leaves chart_version empty and triggers the `if [ -z "${chart_version}" ]`
// branch. Fetch, rev-parse, and rev-list errors still fail the build.
func ResolveChartVersion(
	chartVersion, tagPrefix, versionSuffix, commitSHA string,
	git Git,
) (string, error) {
	if chartVersion != "" {
		return chartVersion, nil
	}
	if git == nil {
		return "", fmt.Errorf("git client is required when chart version is not set")
	}
	if err := git.FetchUnshallow(commitSHA); err != nil {
		return "", err
	}

	describe, err := git.Describe(tagPrefix)
	if err != nil {
		// Treat describe failure like empty output; see ResolveChartVersion.
		describe = ""
	}

	shortSHA, err := git.ShortSHA()
	if err != nil {
		return "", err
	}

	version := ChartVersionFromDescribe(tagPrefix, describe, shortSHA)
	if version == "" {
		count, err := git.CommitCount()
		if err != nil {
			return "", err
		}
		version = FallbackChartVersion(count, shortSHA)
	}

	return version + versionSuffix, nil
}

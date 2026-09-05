package helmchartoci

import (
	"fmt"
	"regexp"
	"strings"
)

var twoComponentVersion = regexp.MustCompile(`^[^.]+\.[^.]+$`)

// ChartVersionFromDescribe converts `git describe` output into a semver-compatible
// chart version, mirroring build-helm-chart-oci-ta 0.3 sed rules. Prerelease tag
// edge cases inherit the same limitations as the bash implementation.
func ChartVersionFromDescribe(tagPrefix, describe, shortSHA string) string {
	chartVersion := describe
	if tagPrefix != "" && strings.HasPrefix(chartVersion, tagPrefix) {
		chartVersion = strings.TrimPrefix(chartVersion, tagPrefix)
	}

	if idx := strings.Index(chartVersion, "-"); idx >= 0 {
		chartVersion = chartVersion[:idx] + "." + chartVersion[idx+1:]
	}
	if idx := strings.Index(chartVersion, "-"); idx >= 0 {
		chartVersion = chartVersion[:idx] + "+" + chartVersion[idx+1:]
	}

	if twoComponentVersion.MatchString(chartVersion) {
		chartVersion = fmt.Sprintf("%s.0+%s", chartVersion, shortSHA)
	}

	return chartVersion
}

// FallbackChartVersion is used when git describe produces no usable version
// (no matching tag or describe failure). See ResolveChartVersion.
func FallbackChartVersion(commitCount int, shortSHA string) string {
	return fmt.Sprintf("0.1.%d+%s", commitCount, shortSHA)
}

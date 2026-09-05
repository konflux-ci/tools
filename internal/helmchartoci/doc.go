// Package helmchartoci implements the build-helm-chart-oci-ta workflow in Go.
//
// Behavior intentionally tracks build-helm-chart-oci-ta 0.3 in konflux-ci/build-definitions
// unless a function comment notes otherwise. When changing versioning, image mappings, or
// Tekton task results, compare against that task script.
//
// Chart version resolution (when CHART_VERSION is unset) uses git describe with
// TAG_PREFIX; describe failures fall back to 0.1.<commit-count>+<short-sha>.
// See ResolveChartVersion for the full flow.
package helmchartoci

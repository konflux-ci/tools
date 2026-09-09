// Package helmchartoci implements the build-helm-chart-oci-ta Tekton task workflow
// in Go. The helm-chart-oci CLI calls Run.
//
// # Parity
//
// Behavior intentionally tracks build-helm-chart-oci-ta 0.3 in
// konflux-ci/build-definitions unless a function comment notes otherwise.
// When changing versioning, image mappings, dependency builds, or Tekton task
// results, compare against that task script.
//
// # Pipeline
//
// Run executes these steps in order:
//
//  1. Resolve chart name (Chart.yaml and/or IMAGE; see Chart metadata).
//  2. Apply IMAGE_MAPPINGS to templates/ and values files.
//  3. Resolve chart version (parameters and/or git; see Versioning).
//  4. Resolve appVersion (APP_VERSION or COMMIT_SHA).
//  5. Package the chart (helm package --version / --app-version) and push to
//     OCI, then tag the IMAGE reference.
//  6. Write IMAGE_URL and IMAGE_DIGEST task results when paths are set.
//
// Chart dependencies are built when Chart.yaml declares dependencies; see the
// push package.
//
// # Versioning
//
// The packaged chart version (helm package --version) is chosen as follows:
//
//  1. CHART_VERSION set → use as-is. Git is not consulted and VERSION_SUFFIX is
//     not appended.
//  2. CHART_VERSION unset → derive from git tags matching TAG_PREFIX (default
//     helm-): fetch tags, run git describe --tags --match=TAG_PREFIX*, convert
//     the result with ChartVersionFromDescribe, fall back to
//     0.1.<commit-count>+<short-sha> when describe fails or matches nothing,
//     then append VERSION_SUFFIX when set. See ResolveChartVersion.
//
// Examples with TAG_PREFIX=helm-:
//
//   - Tag helm-1.2 on current commit → 1.2.0+<short-sha>
//   - Three commits after helm-1.2 → 1.2.3+<short-sha>
//   - No matching tag → 0.1.<commit-count>+<short-sha>
//
// OCI chart tags replace '+' with '_' (see push.ociChartTag).
//
// Chart.yaml version is not read. The resolved version is passed to helm package
// and overrides whatever is in Chart.yaml.
//
// See ResolveChartVersion and ChartVersionFromDescribe for implementation detail.
//
// # App version
//
// appVersion (helm package --app-version) is APP_VERSION when set, otherwise
// COMMIT_SHA. Chart.yaml appVersion is not read.
//
// See ResolveAppVersion.
//
// # Chart metadata
//
//   - name: from Chart.yaml when OVERWRITE_CHART_NAME is false (0.4 default true
//     preserves 0.3: rewrite name from IMAGE repository basename). See
//     ResolveChartName.
//   - version: not taken from Chart.yaml; see Versioning.
//   - appVersion: not taken from Chart.yaml; see App version.
//   - dependencies: built from Chart.yaml when .dependencies is present.
//
// # Image mappings
//
// IMAGE_MAPPINGS is a JSON array of {source, target} image references. Sources
// are matched in templates/*.yaml, templates/*.yml, and listed values files.
// Missing values files are skipped. See ApplyImageMappings.
package helmchartoci

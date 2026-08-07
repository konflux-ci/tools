#!/usr/bin/env bash
# Run inside the konflux tools container (mirrors Tekton helm-chart-oci-e2e task layout).
#
# Runs helm_chart_oci twice: (1) explicit CHART_VERSION, (2) version from git (helm-* tag).
#
# Registry TLS: when HELM_CHART_OCI_REGISTRY_CERT_DIR contains ca.crt (PEM), helm_chart_oci
# and helm pull verify the registry using that CA (TLS verification on).
# Alternatively: HELM_CHART_OCI_TLS_VERIFY=false or HELM_CHART_OCI_PLAIN_HTTP=1 for insecure paths.
set -euo pipefail

: "${E2E_CHART_REPO_NO_TAG:?set to registry chart repo without tag (e.g. host:5000/helm-oci/tools)}"
: "${GITHUB_RUN_ID:?}"
: "${COMMIT_SHA:?}"

WORKSPACE="${WORKSPACE:-/work}"
E2E_EXPLICIT_VERSION="${E2E_EXPLICIT_VERSION:-9.8.7}"

git config --global --add safe.directory "${WORKSPACE}" 2>/dev/null || true

mkdir -p "${HOME}/.docker"
auth="$(printf '%s' 'x:x' | base64 -w0)"
jq -n --arg repo "${E2E_CHART_REPO_NO_TAG}" --arg auth "${auth}" \
  '{auths: {($repo): {auth: $auth}}}' >"${HOME}/.docker/config.json"

write_minimal_chart() {
  local root=$1
  mkdir -p "${root}/source/chart/templates"
  cat >"${root}/source/chart/Chart.yaml" <<'EOF'
apiVersion: v2
name: placeholder
version: 0.1.0
EOF
  cat >"${root}/source/chart/values.yaml" <<'EOF'
image: quay.io/example/app:latest
EOF
  cat >"${root}/source/chart/templates/configmap.yaml" <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: e2e
data:
  key: value
EOF
}

helm_pull_extra=()
ph="${HELM_CHART_OCI_PLAIN_HTTP:-}"
tv="${HELM_CHART_OCI_TLS_VERIFY:-true}"
cert_dir="${HELM_CHART_OCI_REGISTRY_CERT_DIR:-}"
tls_relaxed=0
if [[ "${tv,,}" =~ ^(0|false|no|off)$ ]]; then
  tls_relaxed=1
fi
if [[ "${ph,,}" =~ ^(1|true|yes)$ ]]; then
  helm_pull_extra+=(--plain-http)
elif [[ -n "${cert_dir}" && -f "${cert_dir}/ca.crt" && "${tls_relaxed}" -eq 0 ]]; then
  helm_pull_extra+=(--ca-file "${cert_dir}/ca.crt")
elif [[ "${tls_relaxed}" -eq 1 ]]; then
  helm_pull_extra+=(--insecure-skip-tls-verify)
fi

run_phase() {
  local phase=$1 image=$2
  local explicit_version=${3:-}
  local work="${WORKSPACE}/e2e-${phase}"
  local url_result="/tmp/pushed-image-url-${phase}"
  local digest_result="/tmp/pushed-image-digest-${phase}"

  rm -rf "${work}"
  mkdir -p "${work}"
  write_minimal_chart "${work}"

  (
    cd "${work}"
    export IMAGE="${image}"
    if [[ -n "${explicit_version}" ]]; then
      export CHART_VERSION="${explicit_version}"
    else
      unset CHART_VERSION
    fi
    helm_chart_oci \
      --workdir "${work}" \
      --result-image-url "${url_result}" \
      --result-image-digest "${digest_result}" \
      values.yaml
  )

  local repo="${image%:*}"
  local chart_name="${repo##*/}"
  local actual_url
  actual_url="$(cat "${url_result}")"
  local docker_tag="${actual_url##*:}"
  local repo_from_url="${actual_url%:"${docker_tag}"}"
  if [[ "${repo_from_url}" != "${repo}" ]]; then
    echo "[${phase}] Unexpected repository in pushed URL"
    echo "Expected repo: ${repo}"
    echo "Actual URL:    ${actual_url}"
    exit 1
  fi

  local chart_pull_version
  if [[ -n "${explicit_version}" ]]; then
    local expected_docker_tag="${explicit_version//+/_}"
    if [[ "${docker_tag}" != "${expected_docker_tag}" ]]; then
      echo "[${phase}] Unexpected chart version tag in pushed URL (OCI uses _ for + in semver)"
      echo "Expected tag: ${expected_docker_tag}"
      echo "Actual URL:   ${actual_url}"
      exit 1
    fi
    chart_pull_version="${explicit_version}"
  else
    chart_pull_version="${docker_tag//_/+}"
  fi

  if [[ ! -s "${digest_result}" ]]; then
    echo "[${phase}] Expected a non-empty pushed image digest"
    exit 1
  fi

  local pull_dest="${work}/pulled"
  mkdir -p "${pull_dest}"

  helm pull "oci://${repo%/*}/${chart_name}" \
    --version "${chart_pull_version}" \
    --destination "${pull_dest}" \
    "${helm_pull_extra[@]}"

  local pulled_chart="${pull_dest}/${chart_name}-${chart_pull_version}.tgz"
  if [[ ! -f "${pulled_chart}" ]]; then
    echo "[${phase}] Expected pulled chart archive: ${pulled_chart}"
    exit 1
  fi

  tar -tzf "${pulled_chart}" >/dev/null
  echo "[${phase}] helm_chart_oci + helm pull OK"
}

IMAGE_EXPLICIT="${E2E_CHART_REPO_NO_TAG}:gha-${GITHUB_RUN_ID}-explicit"
IMAGE_GIT="${E2E_CHART_REPO_NO_TAG}:gha-${GITHUB_RUN_ID}-git"

run_phase explicit "${IMAGE_EXPLICIT}" "${E2E_EXPLICIT_VERSION}"
run_phase git "${IMAGE_GIT}" ""

echo "Helm chart OCI tools-image e2e passed (explicit CHART_VERSION + git describe)."

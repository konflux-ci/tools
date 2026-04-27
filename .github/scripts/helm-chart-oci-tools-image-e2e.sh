#!/usr/bin/env bash
# Run inside the konflux tools container (mirrors Tekton helm-chart-oci-e2e task layout).
#
# Registry TLS: when HELM_CHART_OCI_REGISTRY_CERT_DIR contains ca.crt (PEM), helm_chart_oci
# and helm pull verify the registry using that CA (TLS verification on).
# Alternatively: HELM_CHART_OCI_TLS_VERIFY=false or HELM_CHART_OCI_PLAIN_HTTP=1 for insecure paths.
set -euo pipefail

: "${IMAGE:?set IMAGE to the full OCI reference including tag}"
: "${CHART_VERSION:=1.2.3}"

WORKROOT="${WORKROOT:-/work}"
cd "${WORKROOT}"

mkdir -p "${HOME}/.docker"
repo_no_tag="${IMAGE%:*}"
auth="$(printf '%s' 'x:x' | base64 -w0)"
jq -n --arg repo "${repo_no_tag}" --arg auth "${auth}" \
  '{auths: {($repo): {auth: $auth}}}' >"${HOME}/.docker/config.json"

mkdir -p source/chart/templates

cat >source/chart/Chart.yaml <<'EOF'
apiVersion: v2
name: placeholder
version: 0.1.0
EOF

cat >source/chart/values.yaml <<'EOF'
image: quay.io/example/app:latest
EOF

cat >source/chart/templates/configmap.yaml <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: e2e
data:
  key: value
EOF

helm_chart_oci \
  --workdir "${WORKROOT}" \
  --result-image-url /tmp/pushed-image-url \
  --result-image-digest /tmp/pushed-image-digest \
  values.yaml

repo="${IMAGE%:*}"
chart_name="${repo##*/}"
expected_url="${repo}:${CHART_VERSION}"
actual_url="$(cat /tmp/pushed-image-url)"

if [[ "${actual_url}" != "${expected_url}" ]]; then
  echo "Unexpected pushed image URL"
  echo "Expected: ${expected_url}"
  echo "Actual:   ${actual_url}"
  exit 1
fi

if [[ ! -s /tmp/pushed-image-digest ]]; then
  echo "Expected a non-empty pushed image digest"
  exit 1
fi

pull_dest="${WORKROOT}/pulled"
mkdir -p "${pull_dest}"

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

helm pull "oci://${repo%/*}/${chart_name}" \
  --version "${CHART_VERSION}" \
  --destination "${pull_dest}" \
  "${helm_pull_extra[@]}"

pulled_chart="${pull_dest}/${chart_name}-${CHART_VERSION}.tgz"
if [[ ! -f "${pulled_chart}" ]]; then
  echo "Expected pulled chart archive: ${pulled_chart}"
  exit 1
fi

tar -tzf "${pulled_chart}" >/dev/null
echo "Helm chart OCI tools-image e2e passed."

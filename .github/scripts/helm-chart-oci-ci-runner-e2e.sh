#!/usr/bin/env bash
# GitHub Actions CI runner: start Zot (TLS), build the tools image, run the in-image e2e script.
# Expects: GITHUB_WORKSPACE, GITHUB_RUN_ID, RUNNER_TEMP, ZOT_IMAGE, GITHUB_SHA.
# The in-container script runs helm_chart_oci twice: explicit CHART_VERSION and git describe.
set -euo pipefail

: "${GITHUB_WORKSPACE:?}"
: "${GITHUB_RUN_ID:?}"
: "${RUNNER_TEMP:?}"
: "${ZOT_IMAGE:?}"
: "${GITHUB_SHA:?}"

cd "${GITHUB_WORKSPACE}"

# Tag HEAD so helm_chart_oci can resolve the chart version via git describe (TAG_PREFIX=helm-).
git config user.email "helm-chart-oci-e2e@localhost"
git config user.name "helm-chart-oci-e2e"
git tag -f "helm-1.2.3" HEAD

NET="helm-oci-e2e-${GITHUB_RUN_ID}"
REG_CONTAINER="helm-reg-${GITHUB_RUN_ID}"
ZOT_DIR="${RUNNER_TEMP}/zot-e2e-${GITHUB_RUN_ID}"
mkdir -p "${ZOT_DIR}/data" "${ZOT_DIR}/registry-ca" "${ZOT_DIR}/registry-trust"

# Local CA + server cert so clients verify TLS with HELM_CHART_OCI_REGISTRY_CERT_DIR (no insecure skip).
openssl genrsa -out "${ZOT_DIR}/registry-ca/ca.key" 2048
openssl req -new -x509 -key "${ZOT_DIR}/registry-ca/ca.key" -sha256 -days 1 \
  -out "${ZOT_DIR}/registry-ca/ca.crt" \
  -subj "/CN=helm-chart-oci-e2e-registry-ca"

openssl genrsa -out "${ZOT_DIR}/domain.key" 2048
openssl req -new -key "${ZOT_DIR}/domain.key" -out "${ZOT_DIR}/domain.csr" \
  -subj "/CN=${REG_CONTAINER}" \
  -addext "subjectAltName=DNS:${REG_CONTAINER}"
openssl x509 -req -in "${ZOT_DIR}/domain.csr" \
  -CA "${ZOT_DIR}/registry-ca/ca.crt" -CAkey "${ZOT_DIR}/registry-ca/ca.key" \
  -CAcreateserial -out "${ZOT_DIR}/domain.crt" -days 1 -copy_extensions copyall
rm -f "${ZOT_DIR}/domain.csr"

# Skopeo --*-cert-dir must not see private keys (e.g. ca.key): it treats *key as
# client TLS material and errors "missing client certificate ca.cert for key ca.key".
cp "${ZOT_DIR}/registry-ca/ca.crt" "${ZOT_DIR}/registry-trust/ca.crt"

jq -n \
  --arg cert /srv/zot/domain.crt \
  --arg key /srv/zot/domain.key \
  '{
    distSpecVersion: "1.1.1",
    storage: {rootDirectory: "/srv/zot/data"},
    http: {
      address: "0.0.0.0",
      port: "5000",
      tls: {cert: $cert, key: $key},
      accessControl: {
        repositories: {
          "**": {
            anonymousPolicy: ["read", "create", "update"]
          }
        }
      }
    },
    log: {level: "error"}
  }' >"${ZOT_DIR}/config.json"

sudo chown -R 1001:1001 "${ZOT_DIR}"

docker network create "$NET"
docker run -d --name "$REG_CONTAINER" --network "$NET" \
  -v "${ZOT_DIR}:/srv/zot:z" \
  "${ZOT_IMAGE}"
sleep 5
if [ "$(docker inspect -f '{{.State.Running}}' "${REG_CONTAINER}")" != "true" ]; then
  echo "Zot did not stay running; logs:"
  docker logs "${REG_CONTAINER}" 2>&1 || true
  exit 1
fi

docker build -t konflux-tools-e2e:ci .

REPO_NO_TAG="${REG_CONTAINER}:5000/helm-oci/tools"

docker run --rm \
  --user "$(id -u):$(id -g)" \
  --network "$NET" \
  -e HELM_CHART_OCI_REGISTRY_CERT_DIR=/tmp/zot-registry-ca \
  -e HOME=/tmp/e2e-home \
  -e "E2E_CHART_REPO_NO_TAG=${REPO_NO_TAG}" \
  -e "GITHUB_RUN_ID=${GITHUB_RUN_ID}" \
  -e "COMMIT_SHA=${GITHUB_SHA}" \
  -e SOURCE_CODE_DIR=source \
  -e CHART_CONTEXT=chart \
  -e VERSION_SUFFIX= \
  -e TAG_PREFIX=helm- \
  -e IMAGE_MAPPINGS='[]' \
  -e APP_VERSION=e2e \
  -v "${ZOT_DIR}/registry-trust:/tmp/zot-registry-ca:ro" \
  -v "${GITHUB_WORKSPACE}:/work" \
  -w /work \
  konflux-tools-e2e:ci \
  bash /work/.github/scripts/helm-chart-oci-tools-image-e2e.sh

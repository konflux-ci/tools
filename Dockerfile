FROM registry.access.redhat.com/ubi10/go-toolset:1790644755@sha256:290ba654458e9a269b1509d10e6ebbd3c2b2456570e73e73201adb3ee54fb244 AS go-builder

WORKDIR /workspace
COPY go.mod go.sum ./
COPY cmd/ cmd/
COPY internal/ internal/
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o /tmp/helm-chart-oci ./cmd/helm-chart-oci

FROM registry.access.redhat.com/ubi9/python-312:1791202666@sha256:a9f1c5dd1cd239c987058b0743c53af0378df48a12b6004bf4496847051062bf

LABEL \
    name="konflux-ci/tools" \
    description="Tools for Red Hat AppStudio" \
    io.k8s.description="Tools for Red Hat AppStudio" \
    io.k8s.display-name="Tools for Red Hat AppStudio" \
    io.openshift.tags="appstudio" \
    summary="This image contains various tools that are used within Red Hat \
AppStudio. The included tools are written in Python and Go." \
    com.redhat.component="konflux-ci-tools-container" \
    version="1.0" \
    release="1" \
    vendor="Red Hat, Inc." \
    distribution-scope="public" \
    url="https://github.com/konflux-ci/tools"

# Keep PIN_PIPENV_VERSION in sync with .pipenv-version
ENV \
    ENABLE_PIPENV=true \
    PIN_PIPENV_VERSION=2023.11.15 \
    REQUESTS_CA_BUNDLE=/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem

USER 0
ADD . /tmp/src
ADD --chown=root:root --chmod=644 data/ca-trust/* /etc/pki/ca-trust/source/anchors
RUN /usr/bin/fix-permissions /tmp/src \
    && /usr/bin/update-ca-trust
RUN yum install -y skopeo
ARG TARGETARCH
COPY --from=go-builder /tmp/helm-chart-oci /opt/app-root/bin/helm-chart-oci

USER 1001

RUN \
    case "${TARGETARCH}" in \
        amd64) OCP_ARCH=amd64  ;; \
        arm64) OCP_ARCH=arm64 ;; \
        *)     echo "Unsupported arch: ${TARGETARCH}" && exit 1 ;; \
    esac \
    && curl -fsSL --retry 3 --retry-delay 5 --retry-all-errors "https://mirror.openshift.com/pub/openshift-v4/${OCP_ARCH}/clients/ocp/4.12.36/openshift-client-linux.tar.gz" \
       | tar -C /opt/app-root/bin/ -xvzf - oc \
    && /usr/libexec/s2i/assemble

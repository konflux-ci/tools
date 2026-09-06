FROM quay.io/konflux-ci/task-runner@sha256:c34c933c269e2401bb042fe69e2999cf288331b6586d4f4eca9c845270d9b1f9 AS task-runner-image

FROM registry.access.redhat.com/ubi9/python-312:1785964036@sha256:9e030f2458759faacb43682ef0c98babd78d1e15b3aeef7b2ccd5a6caf27abe4

LABEL \
    name="konflux-ci/tools" \
    description="Tools for Red Hat AppStudio" \
    io.k8s.description="Tools for Red Hat AppStudio" \
    io.k8s.display-name="Tools for Red Hat AppStudio" \
    io.openshift.tags="appstudio" \
    summary="This image contains various tools that are used within Red Hat \
AppStudio. The included tools are, for the most part, written in Python." \
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
    REQUESTS_CA_BUNDLE=/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem \
    RETRY_STOP_IF_STDERR_MATCHES=unauthorized

USER 0
ADD . /tmp/src
ADD --chown=root:root --chmod=644 data/ca-trust/* /etc/pki/ca-trust/source/anchors
RUN /usr/bin/fix-permissions /tmp/src \
    && /usr/bin/update-ca-trust
RUN yum install -y skopeo jq bc
ARG TARGETARCH
ARG HELM_VERSION=v3.21.4
RUN case "${TARGETARCH}" in \
        amd64) HELM_ARCH=amd64 ;; \
        arm64) HELM_ARCH=arm64 ;; \
        *)     echo "Unsupported arch: ${TARGETARCH}" && exit 1 ;; \
    esac \
    && curl -fsSL --retry 3 --retry-delay 5 --retry-all-errors -o /tmp/helm.tar.gz "https://get.helm.sh/helm-${HELM_VERSION}-linux-${HELM_ARCH}.tar.gz" \
    && curl -fsSL --retry 3 --retry-delay 5 --retry-all-errors -o /tmp/helm.tar.gz.sha256 "https://get.helm.sh/helm-${HELM_VERSION}-linux-${HELM_ARCH}.tar.gz.sha256" \
    && echo "$(awk '{print $1}' /tmp/helm.tar.gz.sha256)  /tmp/helm.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/helm.tar.gz --strip-components=1 -C /usr/local/bin "linux-${HELM_ARCH}/helm" \
    && rm /tmp/helm.tar.gz /tmp/helm.tar.gz.sha256 \
    && helm version
COPY --from=task-runner-image /usr/local/bin/retry /usr/bin/

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

# Reference: hermetic and multi-arch baseline (konflux-ci/tools)

## Konflux build (PR / main)

| Item | Location | Notes |
|------|----------|-------|
| Platforms | `.tekton/tools-pull-request.yaml`, `tools-push.yaml` | `linux/x86_64`, `linux/arm64` |
| Pipeline | `.tekton/build-pipeline.yaml` | Multi-platform `buildah` + OCI TA |
| Hermetic param | `build-pipeline.yaml` `hermetic` | Default `'false'` (network allowed) |
| Prefetch | `prefetch-input` | Cachi2/Hermeto JSON; empty skips prefetch |
| Registry proxy | `enable-package-registry-proxy` | Default `'true'` for prefetch |

Hermetic build = network isolation during image build plus prefetched inputs.
Today the tools image build **relies on network** in several Dockerfile steps;
the goal is to **not add** more of that without a plan.

## Known baseline debt (do not re-copy into new lines)

Documented for reviewers; fixing these is out of scope for most PRs unless the
PR targets them.

### Dockerfile (`Dockerfile`)

- `curl` install script for Helm (live GitHub raw URL).
- `wget` of `yq_linux_amd64` — **not** keyed to `TARGETARCH`; on `arm64`
  builds this installs the x86_64 binary, which fails at runtime or behaves
  incorrectly.
- `yum install` and OpenShift client `curl` use `TARGETARCH` / `OCP_ARCH` for
  `oc` — pattern to **follow** for new arch-specific binaries.

### Python packaging

- `Pipfile` / `pyproject.toml`: PyPI deps installed at image build via pipenv
  (s2i); not vendored in-repo.
- `pyproject.toml` entry points still expose deprecated CLIs:
  `odcs_compose_generator`, `odcs_ping`, `clean_spacerequests`.

### Deprecated directories (minimal maintenance only)

| Path | Status |
|------|--------|
| `generate_compose/` | ODCS compose; removal planned; uses `requests.get` |
| `clean_spacerequests/` | SpaceRequest cleanup; avoid feature expansion |

AGENTS.md: do not expand these; flag new features, dependencies, or tests that
grow scope.

### Multi-arch in product logic

- `verify_rpms/rpm_verifier.py` and tests model multiple architectures in image
  manifests — new code here should remain arch-agnostic unless intentionally
  platform-specific with tests for each supported arch.

## Quick grep (PR diff)

Run on changed files or the PR patch:

```bash
# Hermetic / network-at-build signals
git diff origin/main...HEAD -- Dockerfile Pipfile pyproject.toml .tekton/ \
  | grep -E 'curl |wget |pip install|yum install|dnf install|prefetch|hermetic'

# Single-arch binary / assumption signals
git diff origin/main...HEAD -- Dockerfile '**/*.py' \
  | grep -E 'amd64|x86_64|arm64|aarch64|TARGETARCH|platform\.machine|uname'
```

## When a PR improves debt

Approve and optionally note:

- Replaces hardcoded `yq_linux_amd64` with `${TARGETARCH}` mapping.
- Adds or updates `prefetch-input` / documents Cachi2 config for new deps.
- Removes unused network fetch or deprecated module usage.
- Adds tests for `arm64` behavior when introducing arch-sensitive code.

## AGENTS.md line budget

GitHub Actions enforces `AGENTS.md` ≤ 300 lines (see
`.github/workflows/test.yml`, step `Validate AGENTS.md line count`). Keep long
inventories in this file; link from AGENTS.md with one row in a Skills table.

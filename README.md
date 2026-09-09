Tools for Konflux-CI
===============================================
This repository includes various tools that are used within Konflux-CI
Pipelines. Tools are written in **Python** and **Go**.

| Language | Location | Tools |
|----------|----------|-------|
| Python | `verify_rpms/` | `rpm_verifier` — RPM signature verification |
| Go | `cmd/helm-chart-oci/`, `internal/helmchartoci/` | `helm-chart-oci` — package and push Helm charts to OCI |

## Installation

Prerequisites and container workflow: see [CONTRIBUTING.md](CONTRIBUTING.md).

**Python**

```bash
pipenv sync
```

**Go**

Go 1.26+ (see `go.mod`). Dependencies are fetched automatically by the Go toolchain.

```bash
go build -o helm-chart-oci ./cmd/helm-chart-oci
```

## Usage

**Python**

```bash
pipenv run rpm_verifier --help
```

**Go**

```bash
./helm-chart-oci --help
```

Full runs need `skopeo` (and related tools) on `PATH` for Python tools, or use the container workflow in [CONTRIBUTING.md](CONTRIBUTING.md). The `helm-chart-oci` binary uses the Helm v4 SDK and registry libraries directly; it only shells out to `git` when chart version is derived from repository metadata.

## Development

See [AGENTS.md](AGENTS.md) for commands and conventions.

**Python**

```bash
pipenv run pytest tests
```

**Go**

```bash
go test ./...
go build ./cmd/helm-chart-oci
```

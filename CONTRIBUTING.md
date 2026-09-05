# Developing this repo

## Python local development prerequisites
* Python 3.12 (see `.python-version`)
* pipenv (see `.pipenv-version`; must match `PIN_PIPENV_VERSION` in the Dockerfile)

## Go local development prerequisites
* Go 1.26+ (see `go.mod`)
* Dependencies are fetched automatically by the Go toolchain

## Working with the container image

### To build this repo into a container:
```
podman build -t appstudio-tools .
```

### To run the built container and gain an interactive shell:
```
podman run -it --rm appstudio-tools bash
```
Within the container, all the tools will be available in `$PATH`.

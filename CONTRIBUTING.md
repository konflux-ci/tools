# Developing this repo

## Python local development prerequisites
* Python 3.12 (see `.python-version`)
* pipenv (see `.pipenv-version`; must match `PIN_PIPENV_VERSION` in the Dockerfile)
* yum packages: krb5-devel, gcc, python3.12-devel

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

### Checking for ODCS connectivity:

You will need to obtain an OIDC service account that can use ODCS. The `odcs_ping` tool
can then be used to check the connectivity.
```
podman run -it --rm \
    -e CLIENT_ID=<client_id> \
    -e CLIENT_SECRET=<client-secret> \
    appstudio-tools odcs_ping
```

If everything goes well, the output will resemble the following (Some
parts omitted for brevity):
```
Connected ODCS version 0.7.0.post5+git.2d65205
Signing keys: [...]
Allowed users:
  ...
Allowed groups:
  ...

Attempting to generate a compose
Compose done:
  repo file at: http://.../odcs-1234567.repo
  owner: your-oidc-user-here
```

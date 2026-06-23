"""CLI entry point for Helm OCI package-and-push (Konflux Tekton)."""

from __future__ import annotations

import shlex
from pathlib import Path

import click

from helm_chart_oci.push import chart_dir_from_parts, package_and_push


@click.command()
@click.option(
    "--workdir",
    type=click.Path(path_type=Path, exists=True, file_okay=False, dir_okay=True),
    default=None,
    help="Workspace root (defaults to current working directory).",
)
@click.option(
    "--source-code-dir",
    type=str,
    envvar="SOURCE_CODE_DIR",
    default="source",
    show_default=True,
)
@click.option(
    "--chart-context",
    type=str,
    envvar="CHART_CONTEXT",
    default="dist/chart/",
    show_default=True,
)
@click.option("--image", type=str, envvar="IMAGE", required=True)
@click.option("--commit-sha", type=str, envvar="COMMIT_SHA", required=True)
@click.option("--version-suffix", type=str, envvar="VERSION_SUFFIX", default="")
@click.option(
    "--tag-prefix", type=str, envvar="TAG_PREFIX", default="helm-", show_default=True
)
@click.option("--image-mappings", type=str, envvar="IMAGE_MAPPINGS", default="[]")
@click.option("--chart-version", type=str, envvar="CHART_VERSION", default="")
@click.option("--app-version", type=str, envvar="APP_VERSION", default="")
@click.option(
    "--result-image-url",
    "result_image_url",
    type=click.Path(path_type=Path),
    required=True,
    help="Path to write IMAGE_URL Tekton result.",
)
@click.option(
    "--result-image-digest",
    "result_image_digest",
    type=click.Path(path_type=Path),
    required=True,
    help="Path to write IMAGE_DIGEST Tekton result.",
)
@click.argument("values_files", nargs=-1)
def main(  # pylint: disable=too-many-arguments,too-many-positional-arguments,too-many-locals
    workdir: Path | None,
    source_code_dir: str,
    chart_context: str,
    image: str,
    commit_sha: str,
    version_suffix: str,
    tag_prefix: str,
    image_mappings: str,
    chart_version: str,
    app_version: str,
    result_image_url: Path,
    result_image_digest: Path,
    values_files: tuple[str, ...],
) -> None:
    """Package a Helm chart and push it to an OCI registry."""
    normalized_values_files: list[str] = []
    for entry in values_files:
        # Tekton may pass array params as one space-joined argv token (bash "$*" style).
        normalized_values_files.extend(shlex.split(entry))

    root = workdir if workdir is not None else Path.cwd()
    chart_dir = chart_dir_from_parts(root, source_code_dir, chart_context)
    package_and_push(
        chart_dir=chart_dir,
        image=image,
        commit_sha=commit_sha,
        version_suffix=version_suffix,
        tag_prefix=tag_prefix,
        image_mappings_raw=image_mappings,
        chart_version_param=chart_version,
        app_version_param=app_version,
        values_files=normalized_values_files,
        result_image_url=result_image_url,
        result_image_digest=result_image_digest,
    )


if __name__ == "__main__":
    main()  # pylint: disable=no-value-for-parameter

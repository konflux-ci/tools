"""Orchestrate Helm package + OCI push (subprocesses to git, helm, skopeo; YAML in Python)."""

from __future__ import annotations

import os
import subprocess
from collections.abc import Callable, Mapping, Sequence
from pathlib import Path
from subprocess import CalledProcessError, CompletedProcess

from helm_chart_oci import pure


def _helm_plain_http_requested() -> bool:
    """When true, append ``--plain-http`` to ``helm push`` (unencrypted HTTP registry)."""
    return os.environ.get("HELM_CHART_OCI_PLAIN_HTTP", "").strip().lower() in (
        "1",
        "true",
        "yes",
    )


def _tls_verify_requested() -> bool:
    """
    Whether to verify TLS certificates for registry traffic (Helm + skopeo).

    Default is strict verification (unset or ``true``). Set
    ``HELM_CHART_OCI_TLS_VERIFY`` to ``0`` / ``false`` / ``no`` / ``off`` to skip
    verification (HTTPS with a self-signed or mismatched cert, or CI registries).
    """
    raw = os.environ.get("HELM_CHART_OCI_TLS_VERIFY")
    if raw is None or not raw.strip():
        return True
    return raw.strip().lower() not in ("0", "false", "no", "off")


def _skopeo_relax_tls() -> bool:
    """Relax skopeo TLS checks for plain-HTTP registries or when verify is disabled."""
    return _helm_plain_http_requested() or not _tls_verify_requested()


def _helm_push_transport_flags() -> list[str]:
    """Extra ``helm push`` flags for registry transport security."""
    if _helm_plain_http_requested():
        return ["--plain-http"]
    if not _tls_verify_requested():
        return ["--insecure-skip-tls-verify"]
    return []


def _registry_tls_cert_dir() -> Path | None:
    """
    Directory containing ``ca.crt`` (PEM) for registry TLS verification.

    When set via ``HELM_CHART_OCI_REGISTRY_CERT_DIR``, Helm uses ``--ca-file`` and
    skopeo uses ``--src-cert-dir`` / ``--dest-cert-dir`` / ``--cert-dir`` so
    connections stay verified (no ``--tls-verify=false``). Do not place private
    keys in this directory: skopeo treats ``*.key`` files as client TLS keys.
    """
    raw = os.environ.get("HELM_CHART_OCI_REGISTRY_CERT_DIR", "").strip()
    if not raw:
        return None
    d = Path(raw)
    if d.is_dir() and (d / "ca.crt").is_file():
        return d
    return None


def _run(  # pylint: disable=too-many-arguments
    argv: list[str],
    *,
    cwd: Path | None = None,
    env: Mapping[str, str] | None = None,
    capture_output: bool = False,
    check: bool = True,
    runner: Callable[..., CompletedProcess[str]] = subprocess.run,
) -> CompletedProcess[str]:
    return runner(
        argv,
        cwd=cwd,
        env=dict(env) if env is not None else None,
        text=True,
        check=check,
        capture_output=capture_output,
    )


def _process_image_mappings(  # pylint: disable=too-many-locals
    chart_dir: Path,
    mappings: list[dict[str, str]],
    values_files: Sequence[str],
) -> None:
    templates = chart_dir / "templates"
    for mapping in mappings:
        source_image = mapping["source"]
        target_image = mapping["target"]
        print(
            f"Replacing '{source_image}' with '{target_image}' in templates and values files..."
        )
        for template_file in pure.iter_template_yaml_files(templates):
            text = template_file.read_text(encoding="utf-8")
            updated = pure.substitute_template_image_line(
                text, source_image, target_image
            )
            if updated != text:
                template_file.write_text(updated, encoding="utf-8")

        source_repo, source_tag = pure.parse_image_ref(source_image)
        target_repo, target_tag = pure.parse_image_ref(target_image)

        for vf_name in values_files:
            vf = chart_dir / vf_name
            if not vf.is_file():
                print(f"Warning: Values file '{vf_name}' not found, skipping...")
                continue
            print(f"Processing {vf_name}...")
            pure.apply_values_file_image_mapping(
                vf,
                source_image,
                target_image,
                source_repo,
                source_tag,
                target_repo,
                target_tag,
            )


def _helm_dependency_build(
    chart_dir: Path, runner: Callable[..., CompletedProcess[str]]
) -> None:
    chart_yaml = chart_dir / "Chart.yaml"
    if not chart_yaml.is_file() or not pure.chart_yaml_has_buildable_dependencies(
        chart_yaml
    ):
        return
    print("Building Helm dependencies from Chart.lock...")
    # OCI-only charts: skip "helm repo add" for oci:// URLs and skip "helm repo update"
    # when no HTTP repo was added (build-definitions PR #3439).
    non_oci_repo_added = False
    for repo_url in sorted(set(pure.chart_yaml_dependency_repository_urls(chart_yaml))):
        if not repo_url:
            continue
        if pure.is_oci_dependency_repository(repo_url):
            print(f"Skipping helm repo add for OCI dependency: {repo_url}")
            continue
        non_oci_repo_added = True
        repo_name = pure.helm_repo_local_name_from_url(repo_url)
        print(f"Adding repository: {repo_name} ({repo_url})")
        _run(
            ["helm", "repo", "add", repo_name, repo_url],
            cwd=chart_dir,
            runner=runner,
            check=False,
        )
    if non_oci_repo_added:
        _run(["helm", "repo", "update"], cwd=chart_dir, runner=runner)
    _run(["helm", "dependency", "build", "."], cwd=chart_dir, runner=runner)


def _resolve_chart_version_git(
    chart_dir: Path,
    commit_sha: str,
    tag_prefix: str,
    version_suffix: str,
    runner: Callable[..., CompletedProcess[str]],
) -> str:
    _run(
        ["git", "fetch", "--unshallow", "--tags", "origin", commit_sha],
        cwd=chart_dir,
        runner=runner,
    )
    describe_proc = _run(
        ["git", "describe", "--tags", f"--match={tag_prefix}*"],
        cwd=chart_dir,
        capture_output=True,
        check=False,
        runner=runner,
    )
    describe_line = (
        "" if describe_proc.returncode != 0 else (describe_proc.stdout or "").strip()
    )
    chart_version = pure.semver_from_git_describe_line(describe_line, tag_prefix)
    if chart_version is None:
        chart_version = ""

    short_sha_proc = _run(
        ["git", "rev-parse", "--short", "HEAD"],
        cwd=chart_dir,
        capture_output=True,
        runner=runner,
    )
    short_sha = short_sha_proc.stdout.strip()

    if pure.is_xy_only_version(chart_version):
        chart_version = pure.bump_xy_version_with_build_metadata(
            chart_version, short_sha
        )

    if not chart_version:
        count_proc = _run(
            ["git", "rev-list", "HEAD", "--count"],
            cwd=chart_dir,
            capture_output=True,
            runner=runner,
        )
        count = count_proc.stdout.strip()
        chart_version = f"0.1.{count}+{short_sha}"

    return chart_version + version_suffix


def package_and_push(  # pylint: disable=too-many-locals,too-many-statements,too-many-arguments,too-many-branches
    *,
    chart_dir: Path,
    image: str,
    commit_sha: str,
    version_suffix: str,
    tag_prefix: str,
    image_mappings_raw: str,
    chart_version_param: str,
    app_version_param: str,
    values_files: Sequence[str],
    result_image_url: Path,
    result_image_digest: Path,
    runner: Callable[..., CompletedProcess[str]] = subprocess.run,
) -> None:
    """
    Full workflow previously implemented in build-helm-chart-oci-ta bash.
    """
    chart_dir = chart_dir.resolve()
    repo = pure.image_repository_strip_tag(image)
    chart_name = pure.chart_name_from_repository(repo)
    pure.set_chart_name_in_chart_yaml(chart_dir / "Chart.yaml", chart_name)

    mappings = pure.sort_image_mappings_longest_source_first(
        pure.load_image_mappings_json(image_mappings_raw)
    )
    if mappings:
        print("Processing image mappings...")
        _process_image_mappings(chart_dir, mappings, values_files)
        print("Image substitution completed.")

    if chart_version_param.strip():
        chart_version = chart_version_param.strip()
        print(f"Using provided chart version: {chart_version}")
    else:
        chart_version = _resolve_chart_version_git(
            chart_dir, commit_sha, tag_prefix, version_suffix, runner
        )

    _helm_dependency_build(chart_dir, runner)

    if app_version_param.strip():
        app_version = app_version_param.strip()
        print(f"Using provided appVersion: {app_version}")
    else:
        app_version = commit_sha

    tgz = f"{chart_name}-{chart_version}.tgz"
    _run(
        [
            "helm",
            "package",
            ".",
            "--version",
            chart_version,
            "--app-version",
            app_version,
        ],
        cwd=chart_dir,
        runner=runner,
    )

    docker_config = Path.home() / ".docker" / "config.json"
    scoped = chart_dir / "scoped_authfile.json"
    pure.write_scoped_docker_auth(repo, docker_config, scoped)

    dest = f"oci://{repo.rsplit('/', 1)[0]}"
    print("Pushing image to registry")
    print(f"Pushing file: {tgz}")
    print(f"Destination: {dest}")
    push_argv: list[str] = [
        "retry",
        "helm",
        "push",
        tgz,
        dest,
        "--registry-config",
        str(scoped),
    ]
    push_argv.extend(_helm_push_transport_flags())
    cert_dir = _registry_tls_cert_dir()
    if (
        cert_dir is not None
        and not _helm_plain_http_requested()
        and _tls_verify_requested()
    ):
        push_argv.extend(["--ca-file", str(cert_dir / "ca.crt")])
    push_proc = _run(
        push_argv,
        cwd=chart_dir,
        capture_output=True,
        check=False,
        runner=runner,
    )
    output = (push_proc.stdout or "") + (push_proc.stderr or "")
    if push_proc.returncode != 0:
        print("Failed to push image to registry")
        print(f"Output: {output}")
        raise CalledProcessError(push_proc.returncode, push_proc.args, output=output)
    print("Push command completed successfully")
    print(f"Push output: {output}")

    docker_tag = pure.oci_tag_from_chart_version(chart_version)
    pushed = f"{repo}:{docker_tag}"
    print(f"Constructed pushed URL: {pushed}")

    print(f"Tagging chart with additional tag: {image}")
    skopeo_copy_argv: list[str] = ["skopeo", "copy"]
    if _skopeo_relax_tls():
        skopeo_copy_argv.extend(["--src-tls-verify=false", "--dest-tls-verify=false"])
    elif cert_dir is not None:
        cdir = str(cert_dir)
        skopeo_copy_argv.extend(["--src-cert-dir", cdir, "--dest-cert-dir", cdir])
    skopeo_copy_argv.extend([f"docker://{pushed}", f"docker://{image}"])
    skopeo_proc = _run(
        skopeo_copy_argv,
        cwd=chart_dir,
        capture_output=True,
        check=False,
        runner=runner,
    )
    skopeo_out = (skopeo_proc.stdout or "") + (skopeo_proc.stderr or "")
    if skopeo_proc.returncode != 0:
        print(f"Failed to tag chart with {image}")
        print(f"Source: docker://{pushed}")
        print(f"Destination: docker://{image}")
        print(f"Skopeo output: {skopeo_out}")
        raise CalledProcessError(
            skopeo_proc.returncode, skopeo_proc.args, output=skopeo_out
        )

    digest = ""
    skopeo_inspect_argv: list[str] = ["skopeo", "inspect"]
    if _skopeo_relax_tls():
        skopeo_inspect_argv.append("--tls-verify=false")
    elif cert_dir is not None:
        skopeo_inspect_argv.extend(["--cert-dir", str(cert_dir)])
    skopeo_inspect_argv.extend(["--raw", f"docker://{pushed}"])
    inspect_proc = subprocess.run(
        skopeo_inspect_argv,
        cwd=chart_dir,
        capture_output=True,
        check=False,
    )
    raw_manifest = inspect_proc.stdout
    if inspect_proc.returncode == 0 and raw_manifest:
        digest = pure.manifest_digest_from_skopeo_raw(raw_manifest)
        print(f"Successfully retrieved manifest digest: {digest}")
    else:
        print("Could not retrieve manifest digest from pushed image")
        print("This does not affect the main functionality")

    semver_url = f"{repo}:{docker_tag}"
    result_image_url.write_text(semver_url, encoding="utf-8")
    result_image_digest.write_text(digest, encoding="utf-8")


def chart_dir_from_parts(
    workdir: Path, source_code_dir: str, chart_context: str
) -> Path:
    """Absolute path to the chart directory under the Tekton workspace."""
    return (workdir / source_code_dir / chart_context).resolve()

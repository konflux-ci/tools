"""Pure helpers for Helm OCI package-and-push (testable without subprocess)."""

from __future__ import annotations

import hashlib
import json
import re
from pathlib import Path
from typing import Any

import yaml  # type: ignore[import-untyped]


def image_repository_strip_tag(image: str) -> str:
    """
    Match bash REPO="${IMAGE%:*}" — strip the last ':' segment (tag or digest tail).
    """
    if ":" not in image:
        return image
    return image.rsplit(":", 1)[0]


def chart_name_from_repository(repo: str) -> str:
    """Last path segment of a repository string (bash ${REPO##*/})."""
    return repo.rsplit("/", 1)[-1]


def parse_image_ref(image: str) -> tuple[str, str]:
    """Split repository and tag; default tag 'latest' when absent."""
    if ":" in image:
        repo, tag = image.rsplit(":", 1)
        return repo, tag
    return image, "latest"


def sort_image_mappings_longest_source_first(
    mappings: list[dict[str, str]],
) -> list[dict[str, str]]:
    """Avoid partial replacements by processing longest source image strings first."""
    return sorted(mappings, key=lambda m: len(m.get("source", "")), reverse=True)


def load_image_mappings_json(raw: str) -> list[dict[str, str]]:
    """Parse IMAGE_MAPPINGS JSON; treat '[]' or empty as no mappings."""
    raw = raw.strip()
    if not raw or raw == "[]":
        return []
    data: Any = json.loads(raw)
    if not isinstance(data, list):
        raise ValueError("IMAGE_MAPPINGS must be a JSON array")
    out: list[dict[str, str]] = []
    for item in data:
        if not isinstance(item, dict):
            raise ValueError("each IMAGE_MAPPINGS entry must be an object")
        src = item.get("source")
        tgt = item.get("target")
        if not isinstance(src, str) or not isinstance(tgt, str):
            raise ValueError("each mapping needs string 'source' and 'target'")
        out.append({"source": src, "target": tgt})
    return out


def strip_git_describe_prefix(describe: str, tag_prefix: str) -> str:
    """Remove tag_prefix from the start of a git-describe line when present."""
    line = describe.strip()
    if line.startswith(tag_prefix):
        return line[len(tag_prefix) :]
    return line


def first_hyphen_to_dot(value: str) -> str:
    """GNU sed '0,/-/s//./' — first '-' -> '.'."""
    idx = value.find("-")
    if idx < 0:
        return value
    return value[:idx] + "." + value[idx + 1 :]


def first_hyphen_to_plus(value: str) -> str:
    """GNU sed '0,/-/s//+/' — first '-' -> '+'."""
    idx = value.find("-")
    if idx < 0:
        return value
    return value[:idx] + "+" + value[idx + 1 :]


def semver_from_git_describe_line(describe_line: str, tag_prefix: str) -> str | None:
    """
    Convert `git describe --tags --match=TAG_PREFIX*` line to chart semver fragment,
    mirroring the legacy shell sed pipeline (before X.Y-only bump and fallbacks).
    """
    if not describe_line.strip():
        return None
    stripped = strip_git_describe_prefix(describe_line, tag_prefix)
    return first_hyphen_to_plus(first_hyphen_to_dot(stripped))


def is_xy_only_version(version: str) -> bool:
    """True for '1.2' style (exactly one dot, no third numeric segment yet)."""
    return bool(re.match(r"^[^.]+\.[^.]+$", version))


def bump_xy_version_with_build_metadata(version_xy: str, short_sha: str) -> str:
    """1.2 -> 1.2.0+sha when describe sits exactly on the tag (bash branch)."""
    return f"{version_xy}.0+{short_sha}"


def oci_tag_from_chart_version(chart_version: str) -> str:
    """Helm OCI replaces '+' in semver tags with '_' (bash docker_tag)."""
    return chart_version.replace("+", "_")


def helm_repo_local_name_from_url(repo_url: str) -> str:
    """
    Legacy repo naming: host part of URL with scheme stripped, '/' removed before
    first slash... mirrors: sed strip https/http, sed 's|/.*$||', dots -> underscores.
    """
    u = repo_url.strip()
    for prefix in ("https://", "http://"):
        if u.startswith(prefix):
            u = u[len(prefix) :]
            break
    host = u.split("/", 1)[0]
    return host.replace(".", "_")


def substitute_template_image_line(
    content: str, source_image: str, target_image: str
) -> str:
    """
    Match legacy sed: s|image: *[\"']?SOURCE[\"']?|image: \"TARGET\"|g
    """
    pattern = re.compile(
        r'image:\s*["\']?' + re.escape(source_image) + r'["\']?',
        flags=re.MULTILINE,
    )
    return pattern.sub(f'image: "{target_image}"', content)


def scoped_registry_auth_object(
    registry: str, docker_config: dict[str, Any]
) -> dict[str, Any]:
    """
    Equivalent to:
      jq --arg registry "$REPO" '.auths[$registry]' ~/.docker/config.json |
      jq -n --arg registry "$REPO" '{auths:{($registry):inputs}}'
    """
    auths = docker_config.get("auths")
    entry: Any = None
    if isinstance(auths, dict):
        entry = auths.get(registry)
    return {"auths": {registry: entry}}


def write_scoped_docker_auth(
    registry: str, docker_config_path: Path, dest: Path
) -> None:
    """Write a Docker config.json containing only auths for the given registry."""
    raw = json.loads(docker_config_path.read_text(encoding="utf-8"))
    scoped = scoped_registry_auth_object(registry, raw)
    dest.write_text(json.dumps(scoped), encoding="utf-8")


def sha256_hex_from_bytes(data: bytes) -> str:
    """Return lowercase hex SHA-256 of the given bytes."""
    return hashlib.sha256(data).hexdigest()


def iter_template_yaml_files(templates_dir: Path) -> list[Path]:
    """List .yaml/.yml files under a chart templates/ directory (recursive)."""
    if not templates_dir.is_dir():
        return []
    paths: list[Path] = []
    for p in sorted(templates_dir.rglob("*")):
        if p.is_file() and p.suffix.lower() in (".yaml", ".yml"):
            paths.append(p)
    return paths


def manifest_digest_from_skopeo_raw(raw_manifest: bytes) -> str:
    """Digest string matching `skopeo inspect --raw | sha256sum` (sha256:...)."""
    return f"sha256:{sha256_hex_from_bytes(raw_manifest)}"


def _yaml_dump(document: Any) -> str:
    """Serialize for Helm values/Chart files (stable, wide lines)."""
    return yaml.safe_dump(
        document,
        default_flow_style=False,
        sort_keys=False,
        allow_unicode=True,
        width=4096,
    )


def load_yaml_document(path: Path) -> Any:
    """Load a YAML file; returns None for an empty file."""
    raw = path.read_text(encoding="utf-8")
    if not raw.strip():
        return None
    return yaml.safe_load(raw)


def set_chart_name_in_chart_yaml(chart_yaml: Path, name: str) -> None:
    """Set ``name`` in Chart.yaml (replaces prior ``yq -i '.name = strenv(chart_name)'``)."""
    doc = load_yaml_document(chart_yaml)
    if not isinstance(doc, dict):
        raise ValueError(f"Chart.yaml must parse to a mapping: {chart_yaml}")
    doc["name"] = name
    chart_yaml.write_text(_yaml_dump(doc), encoding="utf-8")


def chart_yaml_has_buildable_dependencies(chart_yaml: Path) -> bool:
    """
    Whether Chart.yaml declares non-empty dependencies (legacy ``yq -e '.dependencies'``).

    Empty lists are treated as false so ``helm dependency build`` is skipped.
    """
    doc = load_yaml_document(chart_yaml)
    if not isinstance(doc, dict):
        return False
    deps = doc.get("dependencies")
    if deps is None:
        return False
    if isinstance(deps, list):
        return len(deps) > 0
    return bool(deps)


def dependency_repository_scheme(repository: str) -> str:
    """
    Return the URL scheme for a Chart dependency ``repository`` field, lowercased.

    Examples: ``https://charts.example`` → ``https``, ``oci://reg/ns/chart`` → ``oci``.
    Returns an empty string when there is no ``://`` (legacy or relative forms).
    """
    s = repository.strip()
    if "://" not in s:
        return ""
    return s.split("://", 1)[0].lower()


def is_oci_dependency_repository(repository: str) -> bool:
    """
    True if this dependency is resolved via OCI, not a classic Helm HTTP repo.

    ``helm repo add`` cannot register ``oci://`` references (see build-definitions#3439).
    """
    return dependency_repository_scheme(repository) == "oci"


def chart_yaml_dependency_repository_urls(chart_yaml: Path) -> list[str]:
    """Repository URLs from ``dependencies`` (replaces ``yq -r '.dependencies[].repository'``)."""
    doc = load_yaml_document(chart_yaml)
    if not isinstance(doc, dict):
        return []
    deps = doc.get("dependencies")
    if not isinstance(deps, list):
        return []
    urls: list[str] = []
    for dep in deps:
        if isinstance(dep, dict):
            repo = dep.get("repository")
            if isinstance(repo, str) and repo.strip():
                urls.append(repo.strip())
    return urls


def _mutate_values_scalar_images(
    node: Any, source_image: str, target_image: str
) -> None:
    """Recurse and replace string ``image`` fields equal to ``source_image``."""
    if isinstance(node, dict):
        img = node.get("image")
        if isinstance(img, str) and img == source_image:
            node["image"] = target_image
        for child in node.values():
            _mutate_values_scalar_images(child, source_image, target_image)
    elif isinstance(node, list):
        for item in node:
            _mutate_values_scalar_images(item, source_image, target_image)


def _mutate_values_repo_tag_images(
    node: Any,
    source_repo: str,
    source_tag: str,
    target_repo: str,
    target_tag: str,
) -> None:
    """
    Recurse and replace ``image: {repository, tag?}`` matching source repo/tag
    (missing tag matches ``latest``, same as legacy yq ``// "latest"``).
    """
    if isinstance(node, dict):
        img = node.get("image")
        if isinstance(img, dict):
            repo = img.get("repository")
            raw_tag = img.get("tag")
            tag = "latest" if raw_tag is None else str(raw_tag)
            if isinstance(repo, str) and repo == source_repo and tag == source_tag:
                img["repository"] = target_repo
                img["tag"] = target_tag
        for child in node.values():
            _mutate_values_repo_tag_images(
                child, source_repo, source_tag, target_repo, target_tag
            )
    elif isinstance(node, list):
        for item in node:
            _mutate_values_repo_tag_images(
                item, source_repo, source_tag, target_repo, target_tag
            )


def apply_values_file_image_mapping(  # pylint: disable=too-many-arguments,too-many-positional-arguments
    values_file: Path,
    source_image: str,
    target_image: str,
    source_repo: str,
    source_tag: str,
    target_repo: str,
    target_tag: str,
) -> None:
    """
    Apply IMAGE_MAPPINGS-style edits to a values YAML file using two tree passes
    (scalar ``image`` string, then ``image.repository`` / ``image.tag``), matching
    the previous ``yq -i 'with(.. select...)`` behavior without subprocesses.
    """
    data = load_yaml_document(values_file)
    if data is None:
        return
    _mutate_values_scalar_images(data, source_image, target_image)
    _mutate_values_repo_tag_images(
        data, source_repo, source_tag, target_repo, target_tag
    )
    values_file.write_text(_yaml_dump(data), encoding="utf-8")

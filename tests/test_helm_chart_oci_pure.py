"""Unit tests for helm_chart_oci.pure."""

from __future__ import annotations

import json
from pathlib import Path

import pytest
import yaml  # type: ignore[import-untyped]

from helm_chart_oci import pure

# Parametrized and small unit tests: docstrings add little signal for pylint.
# pylint: disable=missing-function-docstring


@pytest.mark.parametrize(
    ("image", "expected"),
    [
        ("quay.io/org/chart:1.2.3", "quay.io/org/chart"),
        ("host:5000/repo/img:tag", "host:5000/repo/img"),
        ("nexus.local/ns/app", "nexus.local/ns/app"),
    ],
)
def test_image_repository_strip_tag(image: str, expected: str) -> None:
    assert pure.image_repository_strip_tag(image) == expected


def test_chart_name_from_repository() -> None:
    assert pure.chart_name_from_repository("quay.io/ns/foo") == "foo"


@pytest.mark.parametrize(
    ("ref", "repo", "tag"),
    [
        ("img:1", "img", "1"),
        ("reg.io/a/b:latest", "reg.io/a/b", "latest"),
        ("plain", "plain", "latest"),
    ],
)
def test_parse_image_ref(ref: str, repo: str, tag: str) -> None:
    assert pure.parse_image_ref(ref) == (repo, tag)


def test_sort_image_mappings_longest_source_first() -> None:
    mappings = [
        {"source": "a", "target": "t1"},
        {"source": "ab", "target": "t2"},
    ]
    sorted_m = pure.sort_image_mappings_longest_source_first(mappings)
    assert [m["source"] for m in sorted_m] == ["ab", "a"]


def test_load_image_mappings_json_empty() -> None:
    assert not pure.load_image_mappings_json("")
    assert not pure.load_image_mappings_json("[]")
    assert not pure.load_image_mappings_json("  []  ")


def test_load_image_mappings_json_valid() -> None:
    raw = '[{"source": "a", "target": "b"}]'
    assert pure.load_image_mappings_json(raw) == [{"source": "a", "target": "b"}]


def test_load_image_mappings_json_invalid_type() -> None:
    with pytest.raises(ValueError, match="JSON array"):
        pure.load_image_mappings_json('{"a":1}')


def test_load_image_mappings_json_invalid_entry() -> None:
    with pytest.raises(ValueError, match="string"):
        pure.load_image_mappings_json('[{"source": 1, "target": "b"}]')


@pytest.mark.parametrize(
    ("describe", "prefix", "expected"),
    [
        ("helm-1.2-3-gabcdef", "helm-", "1.2.3+gabcdef"),
        ("helm-1.2-0-gabc", "helm-", "1.2.0+gabc"),
        ("", "helm-", None),
        ("   ", "helm-", None),
    ],
)
def test_semver_from_git_describe_line(
    describe: str, prefix: str, expected: str | None
) -> None:
    assert pure.semver_from_git_describe_line(describe, prefix) == expected


@pytest.mark.parametrize(
    ("version", "expected"),
    [
        ("1.2", True),
        ("1.2.3", False),
        ("10.20", True),
    ],
)
def test_is_xy_only_version(version: str, expected: bool) -> None:
    assert pure.is_xy_only_version(version) is expected


def test_bump_xy_version_with_build_metadata() -> None:
    assert pure.bump_xy_version_with_build_metadata("1.2", "abc") == "1.2.0+abc"


def test_oci_tag_from_chart_version() -> None:
    assert pure.oci_tag_from_chart_version("1.0.0+git.1") == "1.0.0_git.1"


@pytest.mark.parametrize(
    ("url", "name"),
    [
        ("https://charts.example.com/stable", "charts_example_com"),
        ("http://helm.io/foo/bar", "helm_io"),
    ],
)
def test_helm_repo_local_name_from_url(url: str, name: str) -> None:
    assert pure.helm_repo_local_name_from_url(url) == name


def test_substitute_template_image_line() -> None:
    src = "localhost/old"
    tgt = "quay.io/new:1"
    body = 'image: localhost/old\nother: x\nimage: "localhost/old"'
    got = pure.substitute_template_image_line(body, src, tgt)
    assert got.count(f'image: "{tgt}"') == 2


def test_scoped_registry_auth_object() -> None:
    cfg = {"auths": {"quay.io/ns": {"username": "u"}}}
    scoped = pure.scoped_registry_auth_object("quay.io/ns", cfg)
    assert scoped == {"auths": {"quay.io/ns": {"username": "u"}}}


def test_write_scoped_docker_auth(tmp_path: Path) -> None:
    cfg = tmp_path / "config.json"
    cfg.write_text(
        json.dumps({"auths": {"reg.io/foo": {"auth": "e30="}}}), encoding="utf-8"
    )
    dest = tmp_path / "scoped.json"
    pure.write_scoped_docker_auth("reg.io/foo", cfg, dest)
    data = json.loads(dest.read_text(encoding="utf-8"))
    assert "reg.io/foo" in data["auths"]


def test_manifest_digest_from_skopeo_raw() -> None:
    raw = (
        b'{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}'
    )
    d = pure.manifest_digest_from_skopeo_raw(raw)
    assert d.startswith("sha256:")
    assert len(d) == 71


def test_iter_template_yaml_files(tmp_path: Path) -> None:
    tpl = tmp_path / "templates"
    (tpl / "sub").mkdir(parents=True)
    (tpl / "a.yaml").write_text("a: 1", encoding="utf-8")
    (tpl / "sub" / "b.yml").write_text("b: 1", encoding="utf-8")
    (tpl / "skip.txt").write_text("", encoding="utf-8")
    paths = {p.relative_to(tpl) for p in pure.iter_template_yaml_files(tpl)}
    assert paths == {Path("a.yaml"), Path("sub/b.yml")}


def test_set_chart_name_in_chart_yaml(tmp_path: Path) -> None:
    ch = tmp_path / "Chart.yaml"
    ch.write_text("apiVersion: v2\nname: old\nversion: 1\n", encoding="utf-8")
    pure.set_chart_name_in_chart_yaml(ch, "newname")
    assert "name: newname" in ch.read_text(encoding="utf-8")


def test_chart_yaml_has_buildable_dependencies(tmp_path: Path) -> None:
    ch = tmp_path / "Chart.yaml"
    ch.write_text("apiVersion: v2\nname: x\n", encoding="utf-8")
    assert pure.chart_yaml_has_buildable_dependencies(ch) is False
    ch.write_text(
        "apiVersion: v2\nname: x\ndependencies: []\n",
        encoding="utf-8",
    )
    assert pure.chart_yaml_has_buildable_dependencies(ch) is False
    ch.write_text(
        "apiVersion: v2\nname: x\ndependencies:\n  - name: sub\n"
        "    repository: https://ex.com\n    version: 1\n",
        encoding="utf-8",
    )
    assert pure.chart_yaml_has_buildable_dependencies(ch) is True


@pytest.mark.parametrize(
    ("repo", "expected_scheme", "is_oci"),
    [
        ("oci://registry.example/ns/charts", "oci", True),
        ("OCI://registry.example/ns/charts", "oci", True),
        ("https://charts.example/stable", "https", False),
        ("http://helm.io/charts", "http", False),
        ("relative/path", "", False),
    ],
)
def test_dependency_repository_scheme_and_oci(
    repo: str, expected_scheme: str, is_oci: bool
) -> None:
    """``dependency.repository`` scheme detection (OCI vs HTTP) for Helm deps."""
    assert pure.dependency_repository_scheme(repo) == expected_scheme
    assert pure.is_oci_dependency_repository(repo) is is_oci


def test_chart_yaml_dependency_repository_urls(tmp_path: Path) -> None:
    ch = tmp_path / "Chart.yaml"
    ch.write_text(
        "dependencies:\n"
        "  - {repository: https://a.com, version: '1'}\n"
        "  - {repository: https://b.com, version: '2'}\n",
        encoding="utf-8",
    )
    urls = pure.chart_yaml_dependency_repository_urls(ch)
    assert urls == ["https://a.com", "https://b.com"]


def test_apply_values_file_image_mapping_scalar_and_nested(tmp_path: Path) -> None:
    vf = tmp_path / "values.yaml"
    vf.write_text(
        "outer:\n  image: localhost/a\npods:\n"
        "  - name: one\n    image: localhost/a\n",
        encoding="utf-8",
    )
    pure.apply_values_file_image_mapping(
        vf,
        "localhost/a",
        "quay.io/b:9",
        "localhost/a",
        "latest",
        "quay.io/b",
        "9",
    )
    text = vf.read_text(encoding="utf-8")
    assert text.count("quay.io/b:9") == 2


def test_apply_values_file_image_mapping_repo_tag_and_null_tag(tmp_path: Path) -> None:
    vf = tmp_path / "values.yaml"
    vf.write_text(
        "svc:\n  image:\n    repository: reg.io/img\n"
        "other:\n  image:\n    repository: reg.io/img\n    tag: null\n",
        encoding="utf-8",
    )
    pure.apply_values_file_image_mapping(
        vf,
        "unused",
        "unused",
        "reg.io/img",
        "latest",
        "reg.io/new",
        "v2",
    )
    data = yaml.safe_load(vf.read_text(encoding="utf-8"))
    assert data["svc"]["image"]["repository"] == "reg.io/new"
    assert data["svc"]["image"]["tag"] == "v2"
    assert data["other"]["image"]["repository"] == "reg.io/new"
    assert data["other"]["image"]["tag"] == "v2"

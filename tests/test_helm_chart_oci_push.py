"""Tests for helm_chart_oci.push orchestration."""

from __future__ import annotations

import json
from pathlib import Path
from subprocess import CompletedProcess
from typing import Any

import pytest

from helm_chart_oci.push import (
    _helm_dependency_build,
    _resolve_chart_version_git,
    chart_dir_from_parts,
    package_and_push,
)

# Nested ``fake_run`` signatures mirror ``subprocess.run``; keep checks uncluttered.
# pylint: disable=missing-function-docstring,too-many-arguments,too-many-positional-arguments,unused-argument


def test_chart_dir_from_parts(tmp_path: Path) -> None:
    d = chart_dir_from_parts(tmp_path, "source", "dist/chart")
    assert d == (tmp_path / "source" / "dist" / "chart").resolve()


def test_resolve_chart_version_git(tmp_path: Path) -> None:
    calls: list[list[str]] = []

    def fake_run(
        argv: list[str],
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
        text: bool = True,
        check: bool = True,
        capture_output: bool = False,
        **_kwargs: Any,
    ) -> CompletedProcess[str]:
        calls.append(argv)
        if argv[:3] == ["git", "fetch", "--unshallow"]:
            return CompletedProcess(argv, 0, "", "")
        if argv[:3] == ["git", "describe", "--tags"]:
            return CompletedProcess(argv, 0, "helm-1.2-3-gabcdef\n", "")
        if argv[:3] == ["git", "rev-parse", "--short"]:
            return CompletedProcess(argv, 0, "abc1234\n", "")
        if argv[:4] == ["git", "rev-list", "HEAD", "--count"]:
            return CompletedProcess(argv, 0, "99\n", "")
        raise AssertionError(f"unexpected argv: {argv}")

    ver = _resolve_chart_version_git(
        tmp_path, "deadbeef", "helm-", "-beta", runner=fake_run
    )
    assert ver == "1.2.3+gabcdef-beta"
    assert any(c[:3] == ["git", "fetch", "--unshallow"] for c in calls)


def test_resolve_chart_version_git_no_tag_fallback(tmp_path: Path) -> None:
    def fake_run(
        argv: list[str],
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
        text: bool = True,
        check: bool = True,
        capture_output: bool = False,
        **_kwargs: Any,
    ) -> CompletedProcess[str]:
        if argv[:3] == ["git", "fetch", "--unshallow"]:
            return CompletedProcess(argv, 0, "", "")
        if argv[:3] == ["git", "describe", "--tags"]:
            return CompletedProcess(argv, 1, "", "no tag")
        if argv[:3] == ["git", "rev-parse", "--short"]:
            return CompletedProcess(argv, 0, "feed\n", "")
        if argv[:4] == ["git", "rev-list", "HEAD", "--count"]:
            return CompletedProcess(argv, 0, "42\n", "")
        raise AssertionError(argv)

    ver = _resolve_chart_version_git(tmp_path, "sha", "helm-", "", runner=fake_run)
    assert ver == "0.1.42+feed"


def test_resolve_chart_version_git_xy_on_tag(tmp_path: Path) -> None:
    def fake_run(
        argv: list[str],
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
        text: bool = True,
        check: bool = True,
        capture_output: bool = False,
        **_kwargs: Any,
    ) -> CompletedProcess[str]:
        if argv[:3] == ["git", "fetch", "--unshallow"]:
            return CompletedProcess(argv, 0, "", "")
        if argv[:3] == ["git", "describe", "--tags"]:
            return CompletedProcess(argv, 0, "helm-1.2\n", "")
        if argv[:3] == ["git", "rev-parse", "--short"]:
            return CompletedProcess(argv, 0, "aaaabbbb\n", "")
        if argv[:4] == ["git", "rev-list", "HEAD", "--count"]:
            return CompletedProcess(argv, 0, "1\n", "")
        raise AssertionError(argv)

    ver = _resolve_chart_version_git(tmp_path, "sha", "helm-", "", runner=fake_run)
    assert ver == "1.2.0+aaaabbbb"


def test_package_and_push_writes_results(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(Path, "home", lambda: tmp_path)
    docker = tmp_path / ".docker"
    docker.mkdir()
    (docker / "config.json").write_text(
        json.dumps({"auths": {"quay.io/ns/foo": {"auth": "e30="}}}),
        encoding="utf-8",
    )

    chart = tmp_path / "chart"
    chart.mkdir()
    (chart / "Chart.yaml").write_text(
        "apiVersion: v2\nname: foo\nversion: 0.1.0\n",
        encoding="utf-8",
    )

    result_url = tmp_path / "IMAGE_URL"
    result_digest = tmp_path / "IMAGE_DIGEST"
    skopeo_copy_argv: list[str] = []

    def fake_run(
        argv: list[str],
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
        text: bool = True,
        check: bool = True,
        capture_output: bool = False,
        **_kwargs: Any,
    ) -> CompletedProcess[str]:
        if argv[:2] == ["helm", "package"]:
            assert cwd == chart
            (chart / "foo-2.0.1.tgz").write_bytes(b"PK\x03\x04")
            return CompletedProcess(argv, 0, "", "")
        if argv[:2] == ["retry", "helm"]:
            assert "--plain-http" not in argv
            assert "--insecure-skip-tls-verify" not in argv
            return CompletedProcess(argv, 0, "pushed", "")
        if argv[:2] == ["skopeo", "copy"]:
            skopeo_copy_argv.extend(argv)
            return CompletedProcess(argv, 0, "", "")
        raise AssertionError(f"unexpected: {argv}")

    def fake_subprocess_run(
        argv: list[str],
        **_kwargs: Any,
    ) -> CompletedProcess[bytes | str]:
        if (
            len(argv) >= 3
            and argv[0] == "skopeo"
            and argv[1] == "inspect"
            and "--raw" in argv
        ):
            assert "--tls-verify=false" not in argv
            return CompletedProcess(argv, 0, b'{"schemaVersion":2}', b"")
        raise AssertionError(f"unexpected subprocess.run: {argv}")

    monkeypatch.setattr("helm_chart_oci.push.subprocess.run", fake_subprocess_run)

    package_and_push(
        chart_dir=chart,
        image="quay.io/ns/foo:custom-tag",
        commit_sha="deadbeef",
        version_suffix="",
        tag_prefix="helm-",
        image_mappings_raw="[]",
        chart_version_param="2.0.1",
        app_version_param="appv1",
        values_files=(),
        result_image_url=result_url,
        result_image_digest=result_digest,
        runner=fake_run,
    )

    assert result_url.read_text(encoding="utf-8") == "quay.io/ns/foo:2.0.1"
    digest = result_digest.read_text(encoding="utf-8")
    assert digest.startswith("sha256:")
    assert "--src-tls-verify=false" not in skopeo_copy_argv


def test_package_and_push_registry_cert_dir_adds_ca_for_helm_and_skopeo(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    cert_dir = tmp_path / "regcerts"
    cert_dir.mkdir()
    (cert_dir / "ca.crt").write_text(
        "-----BEGIN CERTIFICATE-----\nMIIB\n", encoding="utf-8"
    )
    monkeypatch.setenv("HELM_CHART_OCI_REGISTRY_CERT_DIR", str(cert_dir))
    monkeypatch.setattr(Path, "home", lambda: tmp_path)
    docker = tmp_path / ".docker"
    docker.mkdir()
    (docker / "config.json").write_text(
        json.dumps({"auths": {"quay.io/ns/foo": {"auth": "e30="}}}),
        encoding="utf-8",
    )

    chart = tmp_path / "chart"
    chart.mkdir()
    (chart / "Chart.yaml").write_text(
        "apiVersion: v2\nname: foo\nversion: 0.1.0\n",
        encoding="utf-8",
    )

    result_url = tmp_path / "IMAGE_URL"
    result_digest = tmp_path / "IMAGE_DIGEST"
    helm_push_argv: list[str] = []
    skopeo_copy_argv: list[str] = []

    def fake_run(
        argv: list[str],
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
        text: bool = True,
        check: bool = True,
        capture_output: bool = False,
        **_kwargs: Any,
    ) -> CompletedProcess[str]:
        if argv[:2] == ["helm", "package"]:
            (chart / "foo-2.0.1.tgz").write_bytes(b"PK\x03\x04")
            return CompletedProcess(argv, 0, "", "")
        if argv[:2] == ["retry", "helm"]:
            helm_push_argv.extend(argv)
            return CompletedProcess(argv, 0, "pushed", "")
        if argv[:2] == ["skopeo", "copy"]:
            skopeo_copy_argv.extend(argv)
            return CompletedProcess(argv, 0, "", "")
        raise AssertionError(f"unexpected: {argv}")

    def fake_subprocess_run(
        argv: list[str],
        **_kwargs: Any,
    ) -> CompletedProcess[bytes | str]:
        if argv[0] == "skopeo" and argv[1] == "inspect" and "--raw" in argv:
            assert "--tls-verify=false" not in argv
            assert "--cert-dir" in argv
            assert str(cert_dir) in argv
            return CompletedProcess(argv, 0, b'{"schemaVersion":2}', b"")
        raise AssertionError(f"unexpected subprocess.run: {argv}")

    monkeypatch.setattr("helm_chart_oci.push.subprocess.run", fake_subprocess_run)

    package_and_push(
        chart_dir=chart,
        image="quay.io/ns/foo:custom-tag",
        commit_sha="deadbeef",
        version_suffix="",
        tag_prefix="helm-",
        image_mappings_raw="[]",
        chart_version_param="2.0.1",
        app_version_param="appv1",
        values_files=(),
        result_image_url=result_url,
        result_image_digest=result_digest,
        runner=fake_run,
    )

    assert "--ca-file" in helm_push_argv
    assert str(cert_dir / "ca.crt") in helm_push_argv
    assert "--insecure-skip-tls-verify" not in helm_push_argv
    assert "--src-cert-dir" in skopeo_copy_argv
    assert "--dest-cert-dir" in skopeo_copy_argv
    assert str(cert_dir) in skopeo_copy_argv
    assert "--src-tls-verify=false" not in skopeo_copy_argv


def test_package_and_push_helm_push_plain_http_env(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("HELM_CHART_OCI_PLAIN_HTTP", "1")
    monkeypatch.setattr(Path, "home", lambda: tmp_path)
    docker = tmp_path / ".docker"
    docker.mkdir()
    (docker / "config.json").write_text(
        json.dumps({"auths": {"quay.io/ns/foo": {"auth": "e30="}}}),
        encoding="utf-8",
    )

    chart = tmp_path / "chart"
    chart.mkdir()
    (chart / "Chart.yaml").write_text(
        "apiVersion: v2\nname: foo\nversion: 0.1.0\n",
        encoding="utf-8",
    )

    result_url = tmp_path / "IMAGE_URL"
    result_digest = tmp_path / "IMAGE_DIGEST"
    helm_push_argv: list[str] = []

    def fake_run(
        argv: list[str],
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
        text: bool = True,
        check: bool = True,
        capture_output: bool = False,
        **_kwargs: Any,
    ) -> CompletedProcess[str]:
        if argv[:2] == ["helm", "package"]:
            (chart / "foo-2.0.1.tgz").write_bytes(b"PK\x03\x04")
            return CompletedProcess(argv, 0, "", "")
        if argv[:2] == ["retry", "helm"]:
            helm_push_argv.extend(argv)
            return CompletedProcess(argv, 0, "pushed", "")
        if argv[:2] == ["skopeo", "copy"]:
            assert "--src-tls-verify=false" in argv
            return CompletedProcess(argv, 0, "", "")
        raise AssertionError(f"unexpected: {argv}")

    monkeypatch.setattr(
        "helm_chart_oci.push.subprocess.run",
        lambda argv, **kw: CompletedProcess(argv, 0, b"{}", b""),
    )

    package_and_push(
        chart_dir=chart,
        image="quay.io/ns/foo:custom-tag",
        commit_sha="deadbeef",
        version_suffix="",
        tag_prefix="helm-",
        image_mappings_raw="[]",
        chart_version_param="2.0.1",
        app_version_param="appv1",
        values_files=(),
        result_image_url=result_url,
        result_image_digest=result_digest,
        runner=fake_run,
    )

    assert "--plain-http" in helm_push_argv


def test_package_and_push_tls_verify_false_uses_insecure_helm_and_skopeo(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("HELM_CHART_OCI_TLS_VERIFY", "false")
    monkeypatch.setattr(Path, "home", lambda: tmp_path)
    docker = tmp_path / ".docker"
    docker.mkdir()
    (docker / "config.json").write_text(
        json.dumps({"auths": {"quay.io/ns/foo": {"auth": "e30="}}}),
        encoding="utf-8",
    )

    chart = tmp_path / "chart"
    chart.mkdir()
    (chart / "Chart.yaml").write_text(
        "apiVersion: v2\nname: foo\nversion: 0.1.0\n",
        encoding="utf-8",
    )

    result_url = tmp_path / "IMAGE_URL"
    result_digest = tmp_path / "IMAGE_DIGEST"
    helm_push_argv: list[str] = []
    skopeo_copy_argv: list[str] = []

    def fake_run(
        argv: list[str],
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
        text: bool = True,
        check: bool = True,
        capture_output: bool = False,
        **_kwargs: Any,
    ) -> CompletedProcess[str]:
        if argv[:2] == ["helm", "package"]:
            (chart / "foo-2.0.1.tgz").write_bytes(b"PK\x03\x04")
            return CompletedProcess(argv, 0, "", "")
        if argv[:2] == ["retry", "helm"]:
            helm_push_argv.extend(argv)
            return CompletedProcess(argv, 0, "pushed", "")
        if argv[:2] == ["skopeo", "copy"]:
            skopeo_copy_argv.extend(argv)
            return CompletedProcess(argv, 0, "", "")
        raise AssertionError(f"unexpected: {argv}")

    def fake_subprocess_run(
        argv: list[str],
        **_kwargs: Any,
    ) -> CompletedProcess[bytes | str]:
        if argv[0] == "skopeo" and argv[1] == "inspect" and "--raw" in argv:
            assert "--tls-verify=false" in argv
            return CompletedProcess(argv, 0, b'{"schemaVersion":2}', b"")
        raise AssertionError(f"unexpected subprocess.run: {argv}")

    monkeypatch.setattr("helm_chart_oci.push.subprocess.run", fake_subprocess_run)

    package_and_push(
        chart_dir=chart,
        image="quay.io/ns/foo:custom-tag",
        commit_sha="deadbeef",
        version_suffix="",
        tag_prefix="helm-",
        image_mappings_raw="[]",
        chart_version_param="2.0.1",
        app_version_param="appv1",
        values_files=(),
        result_image_url=result_url,
        result_image_digest=result_digest,
        runner=fake_run,
    )

    assert "--insecure-skip-tls-verify" in helm_push_argv
    assert "--plain-http" not in helm_push_argv
    assert "--src-tls-verify=false" in skopeo_copy_argv


def test_package_and_push_mapping_updates_template(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(Path, "home", lambda: tmp_path)
    docker = tmp_path / ".docker"
    docker.mkdir()
    (docker / "config.json").write_text(
        json.dumps({"auths": {"quay.io/ns/foo": {"auth": "e30="}}}),
        encoding="utf-8",
    )

    chart = tmp_path / "chart"
    tpl = chart / "templates"
    tpl.mkdir(parents=True)
    (chart / "Chart.yaml").write_text(
        "apiVersion: v2\nname: foo\nversion: 0.1.0\n",
        encoding="utf-8",
    )
    (tpl / "d.yaml").write_text("image: localhost/old\n", encoding="utf-8")

    result_url = tmp_path / "IMAGE_URL"
    result_digest = tmp_path / "IMAGE_DIGEST"

    def fake_run(
        argv: list[str],
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
        text: bool = True,
        check: bool = True,
        capture_output: bool = False,
        **_kwargs: Any,
    ) -> CompletedProcess[str]:
        if argv[:2] == ["helm", "package"]:
            (chart / "foo-2.0.0.tgz").write_bytes(b"x")
            return CompletedProcess(argv, 0, "", "")
        if argv[:2] == ["retry", "helm"]:
            return CompletedProcess(argv, 0, "", "")
        if argv[:2] == ["skopeo", "copy"]:
            return CompletedProcess(argv, 0, "", "")
        raise AssertionError(argv)

    monkeypatch.setattr(
        "helm_chart_oci.push.subprocess.run",
        lambda argv, **kw: CompletedProcess(argv, 0, b"{}", b""),
    )

    raw = json.dumps([{"source": "localhost/old", "target": "quay.io/ns/foo:1"}])
    package_and_push(
        chart_dir=chart,
        image="quay.io/ns/foo:tag",
        commit_sha="cafe",
        version_suffix="",
        tag_prefix="helm-",
        image_mappings_raw=raw,
        chart_version_param="2.0.0",
        app_version_param="",
        values_files=("values.yaml",),
        result_image_url=result_url,
        result_image_digest=result_digest,
        runner=fake_run,
    )

    assert 'image: "quay.io/ns/foo:1"' in (tpl / "d.yaml").read_text(encoding="utf-8")


def test_helm_dependency_build_oci_only_no_repo_add_or_update(
    tmp_path: Path,
) -> None:
    """PR #3439: OCI deps must not run helm repo add/update (only dependency build)."""
    calls: list[list[str]] = []

    def fake_run(
        argv: list[str],
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
        text: bool = True,
        check: bool = True,
        capture_output: bool = False,
        **_kwargs: Any,
    ) -> CompletedProcess[str]:
        calls.append(list(argv))
        return CompletedProcess(argv, 0, "", "")

    chart = tmp_path / "chart"
    chart.mkdir()
    (chart / "Chart.yaml").write_text(
        "apiVersion: v2\nname: t\nversion: 1\ndependencies:\n"
        "  - name: sub\n"
        "    repository: oci://registry.example/ns/charts\n"
        "    version: 1.0.0\n",
        encoding="utf-8",
    )

    _helm_dependency_build(chart, fake_run)

    assert not any(c[:3] == ["helm", "repo", "add"] for c in calls)
    assert not any(c[:3] == ["helm", "repo", "update"] for c in calls)
    assert ["helm", "dependency", "build", "."] in calls


def test_helm_dependency_build_mixed_oci_and_https_runs_update(
    tmp_path: Path,
) -> None:
    """PR #3439: non-OCI repos still get add + update before dependency build."""
    calls: list[list[str]] = []

    def fake_run(
        argv: list[str],
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
        text: bool = True,
        check: bool = True,
        capture_output: bool = False,
        **_kwargs: Any,
    ) -> CompletedProcess[str]:
        calls.append(list(argv))
        return CompletedProcess(argv, 0, "", "")

    chart = tmp_path / "chart"
    chart.mkdir()
    (chart / "Chart.yaml").write_text(
        "apiVersion: v2\nname: t\nversion: 1\ndependencies:\n"
        "  - name: oci_sub\n"
        "    repository: oci://registry.example/ns/charts\n"
        "    version: 1.0.0\n"
        "  - name: http_sub\n"
        "    repository: https://charts.example.com/stable\n"
        "    version: 1.0.0\n",
        encoding="utf-8",
    )

    _helm_dependency_build(chart, fake_run)

    assert any(c[:3] == ["helm", "repo", "add"] for c in calls)
    assert any(c[:3] == ["helm", "repo", "update"] for c in calls)
    assert any(c[:3] == ["helm", "dependency", "build"] for c in calls)

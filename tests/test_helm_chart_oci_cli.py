"""Tests for helm_chart_oci CLI."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import pytest
from click.testing import CliRunner

from helm_chart_oci import cli


def test_cli_splits_space_joined_values_files(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Tekton can join VALUES_FILES into one argument; CLI should split like bash ``for``."""
    captured: dict[str, Any] = {}

    def fake_package_and_push(*, values_files: Any, **_kwargs: Any) -> None:
        captured["values_files"] = list(values_files)

    monkeypatch.setattr(cli, "package_and_push", fake_package_and_push)

    chart = tmp_path / "source" / "dist" / "chart"
    chart.mkdir(parents=True)
    (chart / "Chart.yaml").write_text(
        "apiVersion: v2\nname: x\nversion: 0.1.0\n",
        encoding="utf-8",
    )

    runner = CliRunner()
    result = runner.invoke(
        cli.main,
        [
            "--workdir",
            str(tmp_path),
            "--image",
            "quay.io/ns/foo:tag",
            "--commit-sha",
            "deadbeef",
            "--result-image-url",
            str(tmp_path / "url"),
            "--result-image-digest",
            str(tmp_path / "dig"),
            "values.yaml values-prod.yaml",
        ],
        catch_exceptions=False,
    )

    assert result.exit_code == 0, result.output
    assert captured["values_files"] == ["values.yaml", "values-prod.yaml"]

"""Static checks"""

import re
import tomllib
from pathlib import Path
from subprocess import run
from typing import Final

ROOT: Final[Path] = Path(__file__).resolve().parent.parent
PKGS: Final[list[str]] = [
    "tests",
    "generate_compose",
    "verify_rpms",
    "helm_chart_oci",
    "clean_spacerequests",
]


def test_python_toolchain_versions_in_sync() -> None:
    """Python, Pipenv, and formatter settings share a single version source."""
    python_version = (ROOT / ".python-version").read_text(encoding="utf-8").strip()
    pipenv_version = (ROOT / ".pipenv-version").read_text(encoding="utf-8").strip()

    pipfile = (ROOT / "Pipfile").read_text(encoding="utf-8")
    pipfile_match = re.search(r'python_version\s*=\s*"(?P<version>[^"]+)"', pipfile)
    assert pipfile_match is not None, "Pipfile must declare python_version"
    assert pipfile_match.group("version") == python_version

    with (ROOT / "pyproject.toml").open("rb") as pyproject_file:
        pyproject = tomllib.load(pyproject_file)

    assert pyproject["project"]["requires-python"] == f">={python_version}"
    assert pyproject["tool"]["black"]["target-version"] == [
        f"py{python_version.replace('.', '')}"
    ]

    dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")
    dockerfile_match = re.search(r"PIN_PIPENV_VERSION=(?P<version>[0-9.]+)", dockerfile)
    assert dockerfile_match is not None, "Dockerfile must pin PIN_PIPENV_VERSION"
    assert dockerfile_match.group("version") == pipenv_version


def test_mypy() -> None:
    """Static Type check"""
    run(["mypy"] + PKGS, check=True)


def test_isort() -> None:
    """Imports formatting check"""
    run(["isort", "--check", "--profile", "black"] + PKGS, check=True)


def test_black() -> None:
    """Formatting check"""
    run(["black", "--check"] + PKGS, check=True)


def test_pylint() -> None:
    """Lint check"""
    run(["pylint"] + PKGS, check=True)

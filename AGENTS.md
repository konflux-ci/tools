# AGENTS.md

## Purpose
Repo guidance for AI/code agents working in `konflux-ci/tools`.

## Repo map
- `verify_rpms/`: RPM verification logic and CLI.
- `tests/`: pytest coverage for all tool modules.
- `.tekton/`: Pipeline-as-Code definitions used in Konflux.

## Environment and commands
- Python: use `pipenv` (see `Pipfile`, `Pipfile.lock`, and `.python-version`).
- Install deps: `pipenv sync`
- Run tests: `pipenv run pytest tests`
- Format/lint helper: `./format.sh`

## Working conventions
- Keep changes minimal and scoped to the requested tool.
- Prefer small pure functions over hidden side effects.
- Preserve current CLI behavior and argument names unless explicitly requested.
- When touching time logic, use timezone-aware UTC datetimes.
- Update or add tests for every behavior change.
- Keep new code aligned with long-term hermetic build compatibility.
- Keep implementations multi-arch friendly; avoid assumptions tied to one CPU architecture.
- Do not add behavior that increases hermetic or multi-arch technical debt.
- Do not edit `.tekton/` or workflow files unless the task requires CI/pipeline updates.
- Python version is pinned in `.python-version` and must stay in lockstep with `Pipfile` (`python_version`), `pyproject.toml` (`requires-python` and `[tool.black] target-version`), and the Dockerfile base image (`ubi9/python-3XX`). The `test_python_toolchain_versions_in_sync` test enforces the non-Dockerfile fields. Version bumps also require the corresponding Red Hat UBI base image to exist in the registry — this is the primary gate for Python upgrades.

## Validation expectations
- Always run targeted tests for changed modules.
- Run broader `pipenv run pytest tests` when changes cross modules.
- If changing dependency or packaging config, include a short rationale in PR notes.

## Single-file verification

After a small Python change, lint and type-check the **file you edited**.

1. Run `pipenv sync` if the environment is missing.
2. Run `pipenv run pylint <file>` and `pipenv run mypy <file>`.

Example (replace the path with the file you changed):

```bash
pipenv run pylint verify_rpms/rpm_verifier.py
pipenv run mypy verify_rpms/rpm_verifier.py
```

## Safety checks before finishing
- No secrets or credentials added.
- No unrelated refactors bundled with the fix.
- Documentation updated when introducing non-obvious behavior.

## Skills

| Skill | Use when |
|-------|----------|
| [review-hermetic-multiarch-debt](skills/review-hermetic-multiarch-debt/SKILL.md) | Reviewing PRs for Dockerfile, deps, `.tekton/`, or build/runtime debt |

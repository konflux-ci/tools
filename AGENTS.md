# AGENTS.md

## Purpose
Repo guidance for AI/code agents working in `konflux-ci/tools`.

## Repo map
- `generate_compose/`: deprecated ODCS compose code; only minimal maintenance while removal is planned.
- `verify_rpms/`: RPM verification logic and CLI.
- `clean_spacerequests/`: deprecated namespace/SpaceRequest cleanup tooling; avoid feature expansion.
- `tests/`: pytest coverage for all tool modules.
- `.tekton/`: Pipeline-as-Code definitions used in Konflux.

## Environment and commands
- Python: use `pipenv` (see `Pipfile` and `Pipfile.lock`).
- Install deps: `pipenv sync`
- Run tests: `pipenv run pytest tests`
- Run one test file: `pipenv run pytest tests/test_spacerequests_cleaner.py`
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

## Validation expectations
- Always run targeted tests for changed modules.
- Run broader `pipenv run pytest tests` when changes cross modules.
- If changing dependency or packaging config, include a short rationale in PR notes.

## Safety checks before finishing
- No secrets or credentials added.
- No unrelated refactors bundled with the fix.
- Documentation updated when introducing non-obvious behavior.

## Skills

| Skill | Use when |
|-------|----------|
| [review-hermetic-multiarch-debt](skills/review-hermetic-multiarch-debt/SKILL.md) | Reviewing PRs for Dockerfile, deps, `.tekton/`, or build/runtime debt |

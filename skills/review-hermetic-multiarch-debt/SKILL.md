---
name: review-hermetic-multiarch-debt
description: >-
  Use when reviewing konflux-ci/tools pull requests that touch Dockerfile,
  Pipfile, Pipfile.lock, pyproject.toml, .tekton/, container build scripts,
  Python dependencies, or runtime code shipped in the tools image; or when a PR
  adds network fetches, unpinned downloads, arch-specific binaries, or expands
  deprecated packages (generate_compose, clean_spacerequests).
---

# Review Hermetic and Multi-Arch Debt (tools)

**Goal:** Builds are **not** hermetic today and the tree has known multi-arch
gaps. Do **not** block PRs for failing to fix that baseline. **Do** flag changes
that **widen** hermetic or multi-arch technical debt.

These rules apply even when builds are not yet hermetic — existing debt is not
a justification for adding more network-at-build-time or single-arch assumptions.

## Triage

| Signal | Result | Action |
|--------|--------|--------|
| Docs/tests only; no build or image impact | Out of scope | **Stop** |
| Minimal bugfix in deprecated dir; no new deps/URLs/Dockerfile steps | Low risk | Flag only if debt signals below appear |
| `Dockerfile`, deps, `.tekton/`, `verify_rpms/` runtime | In scope | Full pass |
| New features/deps in `generate_compose/` or `clean_spacerequests/` | Debt | **Request changes** |
| PR reduces debt (TARGETARCH, prefetch, remove fetch) | Positive | Approve |

Konflux PR builds `linux/x86_64` + `linux/arm64`; hermetic defaults `false`.
Baseline debt and greps: [reference.md](reference.md).

## Hermetic debt — flag when the PR **adds**

- Dockerfile: new `curl`/`wget`/`pip install`/`yum`/`dnf` or pipe-to-bash
  without prefetch/Cachi2 path or maintainer exception in PR body.
- New runtime deps in `Pipfile`/`pyproject.toml` without prefetch note.
- **New** URL fetch at import/runtime in `verify_rpms/` (image-shipped code).
- Tekton changes that increase live-network reliance when prefetch exists.
- Git/private deps without a hermetic story.

**Skip:** unchanged legacy Dockerfile fetches; test mocks; narrow fixes in
deprecated dirs without new network behavior.

## Multi-arch debt — flag when the PR **adds**

- Dockerfile: arch-specific download without `TARGETARCH` (e.g. `*_amd64` only).
- Runtime assuming one CPU arch without `arm64` handling where relevant.
- Narrowing `.tekton` `build-platforms` below x86_64 + arm64 without justification.
- Copying patterns listed in [reference.md](reference.md) into **new** lines.

**Skip:** manifest test fixtures; PRs that fix existing single-arch lines.

## Comment template

```markdown
**Build constraints (tools):** Increases [hermetic / multi-arch] debt:
<concrete diff hunk>. Please avoid widening the gap. See
`skills/review-hermetic-multiarch-debt/SKILL.md`. Suggested: <optional hint>.
```

## Rationalizations (do not accept)

| Excuse | Reality |
|--------|---------|
| "Hermetic is off" | Default is off so builds work; new fetches still compound debt |
| "GHA has network" | Konflux image build is the contract |
| "Small curl" | Another prefetch gap |
| "Multi-arch follow-up later" | Block new single-arch binaries now |
| "Matches existing Dockerfile" | New lines copying debt still widen it |

## Red flags

New URL in `Dockerfile` `RUN`; new `Pipfile`/`pyproject.toml` dep;
`yq_linux_amd64` or amd64-only binary URL; expanded deprecated package scope.

## Related

[AGENTS.md](../../AGENTS.md) · [reference.md](reference.md)

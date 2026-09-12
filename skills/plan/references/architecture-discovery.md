# Architecture Discovery

## Goal
Build an evidence-backed model of how this repository already expects changes to be made.
Do not design from generic preference before this pass.

## Inspect in order

1. **Explicit authority** - AGENTS.md, CLAUDE.md, ADRs, architecture docs, contribution rules.
2. **Repository topology** - workspaces/packages/apps/libs/services, dependency boundaries.
3. **Repeated feature slices** - inspect at least two or three comparable modules when available.
4. **Data flow** - entrypoint → domain/service → persistence/external systems → response/event.
5. **Cross-cutting conventions** - validation, errors, auth, configuration, observability, testing.
6. **Change locality** - identify the smallest existing seam that can accept the requested behavior.

## Pattern confidence

Use recurrence and explicit docs rather than a hard numeric rule:
- repeated across multiple modules + docs/tests → established convention;
- repeated a few times → probable convention;
- one isolated implementation → local evidence only;
- contradictory patterns → identify generation/legacy boundaries before deciding.

## Architectural deviation gate

A new abstraction/pattern/dependency needs a concrete reason such as:
- existing boundary cannot represent the requirement;
- security/correctness defect;
- explicit migration/refactor objective;
- unacceptable coupling or operational constraint;
- compatibility requirement impossible under current pattern.

If deviating, keep scope minimal and document migration/compatibility impact. Never refactor unrelated
areas merely to make the repository match a preferred architecture.

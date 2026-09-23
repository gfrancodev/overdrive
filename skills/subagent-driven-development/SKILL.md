---
name: subagent-driven-development
description: Use when executing a spec whose implementation tasks are mostly independent and subagents are available
---
# Subagent-Driven Development

Use a fresh implementer context per independently testable task, followed by task review and final whole-change review.
The controller owns the spec, dependency graph, rulings, and integration state.

## Task brief

Each subagent gets only what it needs:
- task objective;
- relevant spec excerpts/decisions;
- produced interfaces it consumes;
- acceptance criteria;
- relevant repository locations/pattern evidence;
- applicable auto-run rulings;
- a small task-scoped Experience Engine slice when available;
- when the task is frontend-related: paths to `visual-spec.json` and `design-system.visual-spec.json`, plus
  the subset of binding `microDetails` for that task (not a prose summary).

The subagent inspects current code, follows established architecture, uses TDD for behavior changes, implements,
verifies, and returns a concise change report. Do not transmit an enormous implementation recipe or the complete historical experience store.

## Review loop

After each task, review for:
1. spec compliance;
2. architectural/design-system consistency;
3. binding Visual Spec `microDetails` fidelity by category when applicable (motion, navigation, image, icon);
4. code quality and tests.

Fix findings before downstream tasks rely on the output. Use a fresh reviewer when practical. At the end, run a
broad review across the branch and full verification.

## Coordination

Track completed tasks and rulings in a persistent ledger under `.overdrive/runs/<spec-name>/progress.md` so context
compaction cannot cause completed tasks to be repeated.

Do not use subagents when most tasks repeatedly edit the same files or require one tightly coupled evolving context;
`execute-plan` should choose normal execution instead.

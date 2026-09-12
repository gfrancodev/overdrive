---
name: execute-plan
description: Use when an approved Overdrive spec with ordered implementation tasks is ready to implement
---
# Execute Plan

Execute from the **spec**, not from a frozen line-by-line recipe. The spec's requirements and decisions are
binding; implementation details are discovered against the current code for each task.

## 1. Load and verify the spec

Read the full spec once. Confirm the ordered tasks, relevant decisions, dependencies, acceptance criteria,
and execution analysis. If project state changed, reconcile facts before implementation without silently
changing load-bearing requirements.

Use an isolated worktree unless the user explicitly chooses otherwise.

## 2. Recommend execution engine

Assess:
- number of tasks;
- independence between tasks;
- dependency edges;
- **shared files** and interface contention;
- integration risk;
- amount of architectural/design judgment;
- availability of subagents.

Recommend **subagent-driven** when multiple tasks have clear contracts and limited shared state/files.
Recommend **normal execution** when work is small, tightly coupled, repeatedly touches the same files, or
benefits from one continuously evolving implementation context.

Normal mode: state the recommendation briefly and ask the user to choose if the choice has not been made.
Auto-run mode: select the recommended engine automatically.

## 3. Task-scoped experience

When the Experience Engine is available, perform **task-scoped experience** recall before each ordered task.
Query using the task objective plus relevant module/domain terms. Provide the executor only applicable critical
rules, decisions, lessons, anti-patterns, and a small number of similar episodes.

Historical experience is advisory. Inspect the task's current files and closest established patterns before
acting. If live evidence contradicts a recalled memory, follow the live repository and mark the historical
memory as contradicted rather than forcing the old pattern.

Do not pass the entire experience store to a subagent. A focused experience slice belongs in the same compact
brief as task constraints and discovered context.

## 4A. Normal execution

For each task:
1. read the task plus relevant spec sections;
2. retrieve task-scoped experience when available;
3. inspect the current files and closest established patterns;
4. apply `test-driven-development` for behavior changes;
5. implement the smallest change consistent with the existing architecture;
6. run task-scoped verification;
7. record material rulings/deviations and evidence;
8. validate recalled experience that materially helped or conflicted;
9. mark the task complete and continue.

Do not stop between tasks for routine progress confirmation. A task may adapt file/function details discovered
in code as long as it preserves the spec.

## 4B. Subagent-driven execution

Use `subagent-driven-development`. Give each implementer a focused brief containing:
- task objective;
- only relevant spec decisions/constraints;
- dependencies/interfaces from completed tasks;
- acceptance criteria;
- discovered code context that prevents redundant exploration;
- only the task-scoped experience relevant to that task.

Do not give subagents a pre-written implementation transcript or the full historical store. They must inspect
the relevant code and follow established patterns.

## 5. Integration and completion

After all tasks:
- run broad tests/lint/typecheck/build appropriate to the project;
- run visual verification for Visual Specs at required viewports;
- request whole-change code review for meaningful changes;
- fix load-bearing findings;
- use `verification-before-completion` before claiming success;
- allow verification to perform evidence-backed Experience Engine capture;
- use `finishing-a-development-branch` for merge/PR/cleanup decisions.

## When to stop

Normal mode may ask when the spec is materially ambiguous and repository evidence cannot resolve it.
When auto-run is active, execution must not ask the user for approval, confirmation, execution-mode selection,
or continuation. Resolve uncertainty autonomously. If a destructive, irreversible, security-sensitive, production,
or externally consequential action was not authorized by the initial request, defer that action, continue all safe
work, and report the deferred boundary at completion.

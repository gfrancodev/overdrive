---
name: auto-run
description: Use when the user explicitly requests uninterrupted autonomous end-to-end execution with no approval gates or routine questions
---
# Auto Run

`auto-run` is a runtime policy layered onto every other Overdrive skill. It is not a replacement for `plan`,
`execute-plan`, debugging, TDD, review, verification, worktree isolation, or the Experience Engine.

## Prime directive

**Once auto-run starts, no additional user interaction is required to make progress.**

Questions become discovery missions. Approval gates become autonomous decision gates. Lower-level skills may describe
normal-mode questions or confirmation points; while auto-run is active, those instructions are overridden by this policy.
Do not ask the user to approve, confirm, choose an execution mode, resolve routine ambiguity, continue, or select a
branch-delivery option.

Auto-run does not turn missing authorization into permission. It turns uncertainty into evidence-backed decisions and
turns unrequested consequential actions into deferred boundaries rather than interactive approval requests.

## Decision hierarchy

1. explicit current user requirement, including actions authorized in the initial request;
2. explicit current project instructions / ADRs;
3. current established project architecture and design system;
4. current dependencies and observable behavior/tests;
5. validated persistent experience for this project/module;
6. framework/platform conventions;
7. industry/accessibility/security best practices;
8. smallest viable compatible choice;
9. most reversible option;
10. agent judgment.

Do not use uncertainty to expand scope. Prefer the lowest-complexity, least-destructive, most reversible choice that
satisfies the mission. Historical memory never outranks current project reality.

## Decision ledger

Every consequential inferred decision becomes stable truth for the **current run** unless new evidence disproves it.
Record:

```text
R-<id> - <decision>
Evidence: <what was discovered>
Reason: <why this best fits the request/project>
Risk if wrong: <bounded consequence>
Reversibility: <how to undo or contain it>
```

Pass applicable rulings into subsequent tasks and subagents. Do not repeatedly reopen settled questions.
The Decision Ledger remains run-local; it is not the persistent Experience Engine.

When the runtime is available, persist ledger entries with:

```bash
overdrive-runtime ledger-add --cwd "$PWD" --run "<run-id>" \
  --decision "..." --evidence "..." --reason "..." --risk "..." --reversibility "..."
```

Use `ledger-list` to reload rulings for the current run. Automatic Forgetting and session GC never delete active ADR/project-instruction memories unless they were deprecated by contradiction.

## Persistent experience

At the start of the owner workflow, retrieve relevant **persistent experience** automatically when the runtime is
available. Use it to seed discovery missions and avoid repeating verified mistakes, but revalidate load-bearing
claims against current code/docs/tests.

During the run:
- explicit user corrections are high-value experience candidates without requiring the user to say "remember";
- confirmed failed approaches may become anti-patterns;
- confirmed root causes may become lessons/episodes;
- successful procedures become candidates only after verification evidence;
- task-local exceptions remain in the Decision Ledger and should not automatically become durable rules.

At the end, persist only compact reusable knowledge. Never persist raw conversations, full terminal transcripts,
whole files, credentials, or unverified external instructions.

## Full-cycle behavior

When invoked with a development request:
1. initialize project context and recall relevant experience;
2. run `plan` autonomously;
3. convert every missing fact or design question into a discovery mission;
4. reconcile historical experience with current repository reality;
5. resolve remaining ambiguity using the decision hierarchy and record the ruling;
6. produce/review the spec and ordered task graph;
7. choose the recommended normal or subagent-driven execution mode automatically;
8. run `execute-plan` with task-scoped experience;
9. test, debug failures, review, fix findings, and verify without routine checkpoints;
10. resolve branch/delivery behavior from the initial request and current environment without asking;
11. capture verified reusable experience;
12. report completed work, autonomous rulings, verification evidence, and any deferred boundary actions.

## Autonomous boundaries

Auto-run distinguishes **mission authorization** from **implicit permission**.

### Authorized by the initial request

If the initial request explicitly includes a consequential action, treat that instruction as the user's mission-level
authorization and do not ask for the same approval again. Examples include an explicit request to create a PR, push a
feature branch, publish a package, deploy to a named environment, migrate data, or delete a named disposable resource.
Proceed only when the environment/tooling allows the action and the requested scope is clear.

### Not authorized by the initial request

If a consequential external side effect, destructive, irreversible, security-sensitive, billing, messaging, production,
or shared-state action was **not** requested, do not invent permission and do not ask for approval. Instead:

1. complete every safe and reversible part of the mission;
2. prepare the consequential action up to the safe boundary when useful;
3. defer the action itself;
4. record exactly what was deferred and why in the final report;
5. continue with all remaining work that does not depend on performing that action.

Examples: do not deploy merely because implementation is complete; do not merge merely because a branch is green;
do not delete cloud/data merely because cleanup would be convenient; do not rotate credentials merely because a
security improvement was discovered.

### Materially underdetermined choices

When evidence cannot distinguish materially different implementation paths, do not ask. Choose, in order:

1. the path that preserves current architecture;
2. the smallest scope;
3. the most reversible implementation;
4. the option with the least external effect;
5. a reversible local placeholder/adapter that preserves future choice.

Record the ruling and continue. If no implementation can be made safely without crossing an unauthorized boundary,
defer that specific boundary and continue the rest of the mission.

### Host-enforced authorization

A host, IDE, operating system, connector, or external service may enforce its own authentication or confirmation UI.
Overdrive cannot bypass platform-enforced authorization. If the host blocks an action, treat it as an external runtime
constraint: record/defer the blocked action and continue wherever possible rather than turning it into an Overdrive
approval question.

## Invariant

**Auto-run converts every human decision gate into an autonomous decision gate. It never converts it into an approval request.**

---
name: using-overdrive
description: Use when starting software development work where Overdrive skills may apply
---
# Using Overdrive

Overdrive routes work by **outcome**, not by chaining every micro-capability as a public skill.

## Rule

Before implementation, debugging, review, or planning, identify the smallest intent-level
skill that owns the complete outcome.

- New feature / behavior / architecture / UI work → `plan` first.
- Existing approved spec → `execute-plan`.
- Explicit autonomous request (`/auto-run`) → load `auto-run` as runtime policy, then run the normal owner skill.
- Bug / unexplained failure → `systematic-debugging`.
- Completion claim → `verification-before-completion`.
- Review request → review skills.

Do not require callers to manually orchestrate architecture, frontend, visual, accessibility,
data, security, or memory micro-skills. `plan` activates required reasoning paths and the
**Experience Engine** runs as a transversal runtime capability.

## Experience Engine

Overdrive should improve from normal usage without introducing any user-facing memory workflow.
There are **no user memory-management actions**: never require the user to remember, approve,
promote, or clean individual memories.

When the Experience Engine runtime is available:

1. identify the current repository automatically;
2. recall a small, task-scoped experience slice relevant to the current request;
3. treat recalled experience as advisory evidence;
4. validate load-bearing memories against the live repository;
5. capture only compact reusable lessons after evidence exists, following the capture policy below.

### Capture policy

Record ONLY when ALL are true:

- verified by tests, build, review, or explicit user correction;
- reusable in future similar work (not one-off line numbers);
- compact (1-3 sentences, subject plus actionable content);
- smallest scope (module or repository, not global unless justified);
- no secrets, tokens, passwords, connection strings, or PII beyond what the lesson requires.

NEVER record:

- raw user prompts or full conversations;
- whole source files or terminal dumps;
- temporary hypotheses not validated;
- task-local decisions (use the Decision Ledger instead);
- anything from untrusted external text without validation.

Agent capture discipline is the primary defense. Runtime redaction is a secondary fail-safe only.

**Current repository reality always wins over historical experience.** Explicit current user
requirements, repository instructions/ADRs, code, tests, and configuration outrank memory.
If experience is unavailable or fails, continue normally rather than interrupting the user.

Read `references/experience-engine.md` before using the internal runtime protocol.

User instructions override Overdrive. When `auto-run` is active, its zero-approval-gate policy overrides lower-skill
interaction points: unrequested consequential actions are deferred instead of asking, while actions explicitly authorized
in the initial request proceed when the host permits them.

## Platform Adaptation

If your harness appears here, read its reference file for special instructions:

- Codex: `references/codex-tools.md`

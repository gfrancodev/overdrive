---
name: plan
description: Use when a software change needs understanding, design, architecture decisions, visual reasoning, or an implementation spec before code changes
---
# Plan

`plan` is Overdrive's architecture-aware specification engine. Its output is one authoritative
spec with **ordered implementation tasks**. There is no second verbose implementation-plan stage.

## Core principle

**Discover before deciding. Follow the existing architecture before inventing another one.**

In an established repository, architectural consistency outranks theoretical purity.
Prefer extension over invention, reuse over abstraction, and local consistency over generic preference.

## 1. Understand intent and scope

Capture the requested outcome, constraints, success criteria, and non-goals. Do not expand scope
to resolve uncertainty. Use YAGNI aggressively.

Classify only to choose depth:
- trivial/bounded: short spec or in-chat plan may be enough;
- architectural/cross-cutting: persisted spec is required;
- mixed: include every relevant domain in one spec, not multiple competing plans.

## 2. Repository and Architecture Discovery

Before proposing structure, inspect the existing project. Read explicit authority first:
AGENTS/CLAUDE/project instructions, ADRs, architecture docs, README, contributing rules,
workspace/configuration, and tests.

Then discover the **existing architecture** and **existing pattern** evidence:
- module and package boundaries;
- dependency direction;
- controllers/services/repositories or equivalent conventions;
- persistence and transaction patterns;
- validation and error handling;
- events/jobs/messaging;
- auth/security and configuration;
- logging/metrics/tracing;
- testing structure;
- frontend components, tokens, state management, routing, and design system.

Treat repeated patterns as stronger evidence than isolated files. One-off legacy code is not
automatically an architectural convention.

Authority order:
1. explicit user requirement;
2. explicit project docs/ADRs;
3. project-wide established patterns;
4. local module patterns;
5. framework conventions;
6. industry best practices;
7. agent preference.

Only deviate when the current pattern demonstrably cannot satisfy the requirement safely or
maintainably. Record the deviation, reason, scope, compatibility/migration impact, and why a
smaller change is insufficient.

Read `references/architecture-discovery.md` when planning inside an existing repository.

## 2A. Experience Recall and reconciliation

When the Experience Engine runtime is available, perform **experience recall** using the current request
before deep discovery. Use recalled rules, decisions, lessons, anti-patterns, and similar episodes to prioritize
where to inspect; never use them as a substitute for inspection.

For every load-bearing recalled claim:
- confirm it against current repository instructions, ADRs, code, tests, or configuration;
- if current evidence agrees, it may guide the spec and can be validated as successful;
- if current evidence conflicts, **reconcile memory with reality**: current repository reality wins and the
  contradicted memory should be retired/deprecated through the Experience Engine;
- if it is merely unverified, label it historical evidence and do not promote it to an architecture decision.

Keep the recall slice small. Do not inject unrelated project history into the spec.

## 3. Activate only required reasoning domains

Do not make the caller chain many skills. Detect what this request actually needs.

Activate visual reasoning when **any** visual input is present (screenshot, image, video, URL, HTML,
Figma, or "make it look like this site"), a page/component build, a meaningful visual change, or explicit
fidelity requirement.

Run the **mandatory frontend classification gate** first (see `references/visual-reasoning.md`).
When classified as frontend-related, produce binding JSON artifacts:

- `visual-spec.json`: surface contract (`references/visual-spec.schema.json`)
- `design-system.visual-spec.json`: token/component contract (`references/design-system.visual-spec.json`)

Ask once whether to use `reference-exact` or `project-design-system` fidelity when a project design system
exists (normal mode only). Record paths, `fidelityMode`, and `microDetails` in the main spec. The JSON is
the implementation contract, not a prose summary.

Follow `references/micro-detail-taxonomy.md` for exhaustive categories: layout, typography, css, motion,
navigation, image, icon, and interaction. Classify icons (custom vs library) and document image acquisition
(download, consult, generate) before implementation.

Also reason about data, security, infrastructure, migration, API compatibility, and performance
when the request or discovered architecture makes them load-bearing. Keep these analyses inside
this plan/spec.

## 4. Resolve uncertainty

Normal mode: investigate first, then ask only consequential questions that cannot be resolved
from project evidence or safe inference.

When `auto-run` is active: questions become discovery missions. Follow the auto-run policy,
record evidence-backed rulings, choose the safest compatible reversible path, and continue without asking.
If a consequential action is outside the initial mission authorization, defer that boundary rather than requesting approval.

## 5. Produce the single authoritative Spec

The spec should contain only useful implementation context:
- intent, goals/non-goals;
- relevant existing-system findings;
- decisions and constraints;
- architecture / data flow / interfaces;
- compatibility and migration where relevant;
- Visual Spec JSON paths, `fidelityMode`, and binding `microDetails` when activated;
- error/failure handling;
- security/privacy where relevant;
- testing strategy;
- acceptance criteria;
- ordered implementation tasks;
- execution analysis and recommended mode.

Do not pre-script shell commands, exact code bodies, `git add`, or test-by-test TDD mechanics.
Those are execution concerns and may become stale before the task starts.

Each ordered task should include:
- objective;
- relevant spec decisions;
- dependencies on other tasks;
- acceptance criteria;
- files/components only when discovery makes them reliable and useful.

## 6. Recommend execution mode

Analyze task count, independence, dependencies, shared files, integration risk, and judgment required.

- Recommend **subagent-driven** when several tasks are independently implementable with clear contracts
  and low shared-file contention.
- Recommend **normal execution** for small work, tightly coupled tasks, high shared-file pressure, or
  work where understanding must evolve continuously in one context.

Normal mode: present the recommendation and let the user choose.
Auto-run: choose the recommendation automatically and proceed to `execute-plan`.

## 7. Experience handoff

Do not persist the whole spec as memory. Carry forward only the recalled memory IDs actually relied on and
compact durable discoveries that may deserve capture later. Follow capture discipline in
`using-overdrive/references/experience-engine.md` when promoting discoveries. Verification, not planning
confidence, is the primary positive-learning gate.

## Self-review

Before handoff, verify:
- every requirement maps to the spec/tasks;
- no contradiction with discovered architecture;
- no unsupported architectural novelty;
- unknown vs inferred visual decisions are explicit;
- frontend references produced `visual-spec.json` with exhaustive `microDetails` when classification requires it;
- `fidelityMode` and design-system choice are recorded;
- taxonomy categories covered (motion, navigation, image, icon) with binding `microDetails`;
- acceptance criteria are observable;
- task ordering follows dependency reality;
- the spec is sufficient without a second verbose implementation-plan document.

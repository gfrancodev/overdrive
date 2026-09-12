# Overdrive Architecture

## Core model

Overdrive separates **intent**, **experience**, **current reality**, **decisions**, and **execution**.

```text
Intent → Experience Recall → Live Discovery → Reconciliation → Decisions → Spec → Ordered Tasks → Execution → Verification → Experience Capture
```

The spec is authoritative for the current change. It owns requirements, architecture decisions, visual contracts
when relevant, constraints, acceptance criteria, and the ordered task list. Historical experience never replaces
the spec or current repository evidence.

## Authority model

Authority order:

1. explicit current user requirement;
2. current repository instructions / ADRs / architecture docs;
3. current project-wide repeated patterns;
4. current local module patterns;
5. validated applicable project experience;
6. framework conventions;
7. industry best practices;
8. agent preference.

A memory conflict is resolved in favor of current reality. The stale memory is validated as a contradiction so it
stops biasing later runs.

## Experience Engine

The Experience Engine is a transversal runtime capability, not a public skill.

```text
                   skills
                     │
        ┌────────────┼────────────┐
        │            │            │
       plan      execute-plan   debugging
        │            │            │
        └────────────┼────────────┘
                     ▼
              overdrive-runtime
                     │
       ┌─────────────┼─────────────┐
       │             │             │
 working memory  durable layers  ledger (run)
       │             │             │
       └─────────────┼─────────────┘
                     ▼
           SQLite WAL + FTS5
                     ▼
        TurboVec IdMapIndex (official)
                     ▼
          ~/.overdrive/experience-v2.db
          ~/.overdrive/experience-v2.tvim
```

The shipped 0.3 runtime is a prebuilt Go binary (no user CGO) with embedded SQLite, FTS5 lexical search, official TurboVec reranking via a bundled FFI library, lazy embeddings with hashed fail-open fallback, working memory, decision ledger, conflict records, evidence scoring, and automatic forgetting on session start. It has no daemon and no resident model unless the embedding pack was downloaded.

Memory kinds: fact, rule, decision, preference, procedure, lesson, anti-pattern, episode.
Memory scopes: global, organization, repository, module.

## Plan engine

`plan` behaves like a software architect working inside a real codebase.

It first retrieves only relevant historical experience, then discovers the current repository and reconciles any
load-bearing historical claims. Recalled experience prioritizes inspection; it does not excuse inspection.

The engine detects relevant reasoning domains instead of chaining many public skills: architecture,
frontend/visual, data, security, infrastructure, API, migration, and testing. Only necessary perspectives activate.

## Architectural consistency

Architecture discovery searches for structure, dependency direction, module boundaries, validation, persistence,
error handling, testing conventions, auth, configuration, observability, and design-system patterns.

A repeated current project pattern should be extended. A one-off legacy artifact and a historical memory are evidence,
not automatically conventions. Architectural deviation must be explicit, scoped, and justified.

## Visual pipeline

```text
visual reference
  ↓
visual extraction
  ↓
Observed / Inferred / Unknown
  ↓
design-system discovery
  ↓
reconciliation
  ↓
responsive + states + accessibility
  ↓
Visual Spec embedded in main Spec
```

Coordinates may be captured as evidence, but layout relationships, hierarchy, spacing, proportions, grids, and
component semantics are the primary representation.

## Execution selection

`execute-plan` scores the task graph using independence, dependency edges, shared files, integration risk,
architectural judgment, and subagent availability.

- tightly coupled or small work → normal execution;
- multiple independent tasks with clear contracts → subagent-driven;
- `/auto-run` chooses the recommendation automatically;
- normal mode recommends and asks the user to choose.

Each task receives only task-scoped experience. Subagents do not inherit the whole historical store.

## Auto-run policy

Auto-run is not a domain skill. It changes runtime behavior globally and guarantees **no additional user interaction**
is required for Overdrive to continue its workflow. Lower-skill approval/confirmation points are replaced by autonomous
decision gates.

Routine uncertainty becomes:

```text
Question → Experience Recall → Discovery Mission → Evidence → Ruling → Ledger → Continue
```

The Decision Ledger is run-local. Persistent Experience is cross-run. Run-local exceptions do not automatically
become durable rules.

### Autonomous boundaries

The initial request defines mission authorization. An explicitly requested consequential action (for example, opening
a PR or deploying to a named environment) does not require a second Overdrive approval. A consequential action that
was not requested is not implicitly authorized: Overdrive completes all safe/reversible work, **defers** that boundary,
and continues instead of asking the user.

For materially underdetermined choices, auto-run selects the architecture-preserving, smallest-scope, most reversible
option and records the ruling. Host/IDE/service authorization that is enforced outside Overdrive cannot be bypassed;
a host-blocked action is deferred and reported rather than converted into an Overdrive approval question.

## Learning lifecycle

```text
working evidence
      ↓
verified outcome
      ↓
episode / lesson candidate
      ↓
repeated evidence or explicit correction
      ↓
durable knowledge
      ↓
future recall
      ↓
validation against current reality
```

The engine stores distilled knowledge, not chat transcripts or whole files. Verification is the primary gate for
positive learning; contradictions reduce confidence or deprecate stale memory.

<p align="center">
  <img src="assets/logo.png" alt="Overdrive" width="160">
</p>

<h1 align="center">Overdrive</h1>

<p align="center">
  <strong>Architecture-aware autonomous software engineering that learns from every execution.</strong>
</p>

**First public release (1.0.0).** Overdrive is an independent software development methodology for coding agents that understands the system before changing it.

It combines architecture-aware discovery, a single authoritative spec, adaptive execution, autonomous decision-making, test-driven development, systematic debugging, isolated workspaces, code review, evidence-based verification, and a **local Experience Engine** (SQLite, FTS5, TurboVec, lazy MiniLM) that compounds operational knowledge across runs without a memory-management workflow.

> **Discover before deciding. Follow the architecture. Spec before execution. Verify before completion.**

Overdrive is not a replacement for other agent methodologies. [Superpowers](https://github.com/obra/superpowers) is an excellent, full-featured framework. Use it freely if it fits your workflow. Overdrive is **one maintainer's take**: the same family of ideas (skills, TDD, systematic debugging, worktrees, verification), reorganized for **day-to-day work on established codebases** where existing architecture, module boundaries, and project skills should win over agent preference.

## Table of Contents

- [How it works](#how-it-works)
- [Why Overdrive](#why-overdrive)
- [Installation](#installation)
  - [Cross-runtime skills](#cross-runtime-skills)
  - [Claude Code](#claude-code)
  - [Codex](#codex)
  - [Cursor](#cursor)
- [The Basic Workflow](#the-basic-workflow)
- [Plan](#plan)
  - [Architecture Discovery](#architecture-discovery)
  - [Frontend and Visual Planning](#frontend-and-visual-planning)
- [Execute Plan](#execute-plan)
- [Auto Run](#auto-run)
- [Experience Engine](#experience-engine)
- [What's Inside](#whats-inside)
- [Philosophy](#philosophy)
- [Development and Validation](#development-and-validation)
- [Contributing](#contributing)
- [Code of Conduct](#code-of-conduct)
- [Acknowledgements](#acknowledgements)
- [License](#license)

## How it works

Overdrive starts before the first line of code.

When your coding agent receives a development request, it does not immediately invent a solution and it does not immediately generate a giant implementation plan. It first identifies the project, recalls only relevant prior experience when available, and then discovers the current project reality.

It inspects repository instructions, architecture documentation, ADRs, module boundaries, naming conventions, dependency direction, persistence patterns, tests, existing abstractions, frontend components, design tokens, and repeated implementation patterns. Recalled experience helps prioritize discovery, but current repository evidence always wins when history and reality disagree.

The core rule is simple:

> **Existing architecture outranks agent preference.**

If the project already has an established way to solve the problem, Overdrive follows it. A theoretically cleaner architecture is not a reason to introduce a new pattern into an established codebase.

Once the system is understood, `plan` resolves the necessary decisions and creates a single authoritative specification containing the requirements, constraints, architecture decisions, relevant visual specification, acceptance criteria, and an ordered list of implementation tasks.

There is no second giant document that predicts every line of code before the agent has inspected the implementation context.

When the spec is ready, Overdrive analyzes the task graph and recommends the most appropriate execution strategy:

- **normal execution** for tightly coupled, sequential, or context-sensitive work;
- **subagent-driven development** when tasks have clear boundaries and can benefit from isolated contexts and independent review.

If `/auto-run` is active, Overdrive makes that choice itself and continues through implementation, testing, review, fixes, and final verification without routine user checkpoints.

```text
request
   ↓
identify project + recall relevant experience
   ↓
discover current project
   ↓
reconcile history × reality
   ↓
understand architecture
   ↓
resolve decisions
   ↓
PLAN
   ├─ functional spec
   ├─ architecture constraints
   ├─ visual spec when relevant
   ├─ acceptance criteria
   └─ ordered tasks
              ↓
       execution analysis
         ↙           ↘
      normal     subagent-driven
         ↘           ↙
           implementation
                ↓
               TDD
                ↓
              review
                ↓
           verification
                ↓
       compact experience capture
                ↓
               done
```

Because Overdrive's skills trigger from intent and context, the agent should not need to manually orchestrate a chain of tiny skills. High-level skills own complete outcomes and activate supporting reasoning only when it is actually required.

## Why Overdrive

Coding agents commonly fail in two opposite directions.

The first is **coding too early**: they see the request, recognize a familiar problem, and impose their preferred architecture before understanding the project.

The second is **planning too much**: they generate an implementation document so prescriptive that it becomes stale the moment real code reveals a better implementation path.

Overdrive is designed around a different model:

```text
Understand deeply.
Decide explicitly.
Specify once.
Execute adaptively.
Verify with evidence.
```

The spec defines **what must remain true**.

The codebase determines **how the implementation should fit**.

The executor is expected to inspect reality instead of blindly following a prewritten recipe.

## Installation

Overdrive ships with plugin metadata for Cursor, Claude Code, and Codex. When published, releases will be available at [https://github.com/gfrancodev/overdrive](https://github.com/gfrancodev/overdrive). Clone the repository, install the skills, and load the plugin from your checkout.

```bash
git clone https://github.com/gfrancodev/overdrive.git
cd overdrive
./scripts/install.sh
```

That is the primary install path for all supported runtimes.

### Cross-runtime skills

`./scripts/install.sh` copies Overdrive skills into the cross-runtime agents skills directory:

By default the installer places skills and the local Experience Engine runtime at:

```text
~/.agents/skills/
~/.overdrive/bin/overdrive-runtime
```

The runtime is a small on-demand binary (`CGO_ENABLED=0`). It does not run a daemon and does not require Docker, Python, a database server, GPU, or an external API. MiniLM embeddings download lazily into `~/.overdrive/` on first use, or fail open to hashed vectors.

To install into a custom directory:

```bash
OVERDRIVE_SKILLS_DIR=/path/to/skills ./scripts/install.sh
```

On Windows PowerShell:

```powershell
./scripts/install.ps1
```

The installer selects the matching prebuilt runtime automatically. Experience storage lives under `~/.overdrive/` and requires no user memory-management commands.

### Claude Code

Overdrive includes Claude plugin metadata under:

```text
.claude-plugin/
```

After cloning the repo and running `./scripts/install.sh`, load the repository as a local plugin in Claude Code. The plugin manifest, skills, and commands are all in the checkout; no separate marketplace install is required.

### Codex

Overdrive includes Codex plugin metadata under:

```text
.codex-plugin/
```

Clone the repo, run `./scripts/install.sh`, and point Codex at the checkout as a local plugin. Skills installed to `~/.agents/skills/` are also picked up by Codex-compatible runtimes that scan that directory.

### Cursor

Overdrive includes Cursor plugin metadata under:

```text
.cursor-plugin/
```

Clone the repo, run `./scripts/install.sh`, and install the plugin from the repository checkout in Cursor. The `.cursor-plugin/` manifest, skills, commands, and hooks ship with the repo.

## The Basic Workflow

1. **using-overdrive** - Routes development work, initializes relevant Experience Engine context when available, and applies global rules.

2. **plan** - Understands the request, inspects the project, discovers the existing architecture, activates specialized reasoning only when relevant, resolves decisions, and creates the authoritative spec with an ordered task list.

3. **using-git-worktrees** - Creates or verifies an isolated workspace before implementation when repository changes require it.

4. **execute-plan** - Reviews the spec and task graph, recommends normal or subagent-driven execution, and executes against the spec rather than a line-by-line implementation recipe.

5. **test-driven-development** - Applies RED-GREEN-REFACTOR during implementation where behavior is being created or changed.

6. **systematic-debugging** - Handles unexpected behavior through evidence and root-cause analysis instead of speculative fixes.

7. **requesting-code-review / receiving-code-review** - Reviews implementation quality and spec compliance, then handles findings rigorously.

8. **verification-before-completion** - Requires fresh evidence before any completion claim and gates positive Experience Engine learning.

9. **finishing-a-development-branch** - Handles the final branch state and delivery workflow after verification.

When `/auto-run` is active, routine questions and execution-choice checkpoints become autonomous discovery and decision steps.

## Plan

`plan` is the architectural brain of Overdrive.

It replaces the traditional `brainstorming → writing-plans` happy path with one coherent planning process.

The purpose of `plan` is not to predict the implementation. Its job is to understand enough of the system and the request that implementation can proceed without architectural drift.

A complete spec may include:

- problem and intent;
- goals and non-goals;
- existing system context;
- constraints;
- architecture decisions;
- interfaces and boundaries;
- data and persistence implications;
- security considerations;
- frontend and visual specification when relevant;
- states and error behavior;
- testing strategy;
- acceptance criteria;
- ordered implementation tasks;
- execution analysis.

The amount of ceremony scales with the problem.

A small bounded change should produce a small plan.

A system-level architectural change may require a deep spec.

> **Plan as deep as necessary, as small as possible.**

### Architecture Discovery

Before proposing architecture in an existing project, Overdrive discovers the architecture already present.

It looks for evidence in this order:

1. explicit user requirements;
2. repository instructions;
3. architecture documentation and ADRs;
4. repeated project-wide patterns;
5. local module conventions;
6. framework conventions;
7. industry best practices;
8. agent preference.

Agent preference is intentionally last.

Overdrive examines signals such as:

```text
module boundaries
dependency direction
controller/service/repository patterns
domain organization
validation strategy
error handling
persistence
events and messaging
dependency injection
configuration
authentication
testing conventions
observability
frontend composition
state management
API conventions
```

Repeated patterns are stronger evidence than isolated legacy code.

The default policy is:

> **Prefer extension over invention. Prefer reuse over abstraction. Prefer local consistency over theoretical elegance.**

A new architectural pattern requires a concrete reason: the established architecture cannot safely or maintainably satisfy the requested behavior, the user explicitly requested a migration, or a real constraint makes the existing pattern unsuitable.

Any necessary deviation should be recorded in the spec with its reason and scope.

### Frontend and Visual Planning

Frontend reasoning is not a separate workflow the user has to remember to invoke.

`plan` detects when the requested work has meaningful visual requirements and activates visual reasoning automatically.

Typical triggers include:

- screenshots;
- Figma references;
- UI reconstruction requests;
- meaningful page or component redesigns;
- responsive behavior;
- explicit visual-fidelity requirements.

For visual references, Overdrive converts the reference into structured information before implementation.

```text
screenshot / Figma / reference
           ↓
      visual analysis
           ↓
   structured visual model
           ↓
 observed / inferred / unknown
           ↓
 design-system reconciliation
           ↓
        Visual Spec
```

Observed facts are kept separate from inferred behavior and unknown information. This prevents assumptions from being treated as if they were visible in the reference.

Before inventing styles, Overdrive inspects the project's existing design system:

```text
existing components
design tokens
Tailwind theme
CSS variables
typography
spacing
radius
elevation
state patterns
Storybook
component libraries
```

Existing design-system decisions have priority over arbitrary pixel-level reproduction when both can preserve the intended visual result.

If the project lacks sufficient visual direction, Overdrive may use external design guidance such as TypeUI as a conditional design-intelligence provider. External guidance supplements the project; it does not replace an established design system.

The final Visual Spec can describe:

- layout hierarchy;
- containers and grids;
- spacing and proportions;
- typography;
- component mapping;
- responsive behavior;
- interaction states;
- loading, empty, and error states;
- accessibility requirements;
- platform-specific behavior;
- assets and content;
- observed/inferred/unknown decisions.

## Execute Plan

`execute-plan` turns the spec into verified code.

The spec is authoritative for requirements, constraints, and decisions.

The implementation details remain adaptive.

```text
SPEC
 ↓
current task
 ↓
inspect relevant code
 ↓
implement in existing patterns
 ↓
test
 ↓
verify
 ↓
next task
```

If implementation reveals that a file, abstraction, or interface already exists, the executor should reuse or extend it instead of following stale assumptions from planning.

The executor may adapt implementation detail freely as long as it does not violate the decisions and acceptance criteria recorded in the spec.

### Execution strategy

Before implementation, Overdrive analyzes the task graph.

**Normal execution is preferred when:**

- tasks are tightly coupled;
- the same files are repeatedly touched;
- understanding must evolve sequentially;
- the task is small;
- coordination overhead would exceed the benefit of isolation.

**Subagent-driven development is preferred when:**

- multiple tasks have clear boundaries;
- most work can be implemented independently;
- shared-file contention is low;
- interfaces are well specified;
- isolated context and independent review increase reliability.

In normal mode, Overdrive recommends a strategy and asks the user before execution.

With `/auto-run`, it automatically chooses the recommended strategy.

## Auto Run

`/auto-run` is an autonomy policy that can be combined with the rest of Overdrive.

Its central rule is:

> **Questions become discovery missions.**

When an agent encounters uncertainty, it should not immediately interrupt the user.

Instead:

```text
unknown
   ↓
can evidence answer it?
   ↓ yes
discover
   ↓
evaluate
   ↓
record ruling
   ↓
continue
```

When the answer cannot be directly discovered, Overdrive chooses the smallest, safest decision compatible with:

1. explicit user requirements;
2. project conventions;
3. project architecture;
4. existing dependencies;
5. documentation;
6. tests and observable behavior;
7. platform best practices;
8. YAGNI;
9. agent judgment.

Important decisions become rulings and remain authoritative for the rest of the development cycle.

Example:

```text
R-003 - Queue implementation

Decision:
Reuse BullMQ.

Evidence:
- BullMQ is already installed.
- Redis is already provisioned.
- Existing background jobs use the same infrastructure.

Consequence:
No new message broker is introduced.
```

`/auto-run` may autonomously:

- inspect the repository;
- research implementation context;
- resolve discoverable ambiguity;
- make architecture-compatible decisions;
- choose execution mode;
- implement;
- test;
- debug;
- review;
- fix findings;
- verify.

### Zero-approval-gate auto-run

Once `/auto-run` starts, **no additional user interaction** is required for the Overdrive workflow to keep progressing.
Questions become discovery missions and approval gates become autonomous decision gates.

The **initial request** defines mission authorization. If it explicitly asks Overdrive to open a PR, push a branch,
publish, deploy to a named environment, migrate data, or perform another consequential action, Overdrive does not ask
for that same approval again.

If a destructive, irreversible, production, security-sensitive, billing, messaging, shared-state, or other external
side effect was **not** requested, Overdrive does not infer permission and does not interrupt the run to ask. It:

1. completes every safe and reversible part of the task;
2. prepares the consequential action up to a safe boundary when useful;
3. **defers** the unauthorized action;
4. continues all independent work;
5. reports the deferred boundary at completion.

For materially ambiguous implementation decisions, Overdrive chooses the existing-architecture-compatible,
smallest-scope, most reversible option and records the ruling. Host/IDE/service authorization prompts enforced outside
Overdrive remain host constraints; Overdrive cannot bypass them and will defer a blocked action rather than create a
second approval workflow.

## Experience Engine

Overdrive 1.0 ships a **Local Runtime** that compounds operational experience without a user-facing memory workflow. You still plan, execute, debug, review, and auto-run exactly as before. Recall and capture happen as a side effect of normal development.

```text
normal development
      ↓
identify project + session-start (GC)
      ↓
relevant experience recall
      ↓
live repository validation
      ↓
execution + verification
      ↓
compact reusable learning
      ↓
next run starts better informed
```

The engine stores compact facts, rules, decisions, preferences, procedures, lessons, anti-patterns, and episodes at global, organization, repository, or module scope. History is advisory. Current code, ADRs, tests, and explicit requirements always win.

**Local Runtime stack (`sqlite-fts5-turbovec-v1`):**

| Piece | Role |
|---|---|
| SQLite + WAL + FTS5 | Durable store and lexical candidates (`experience-v2.db`) |
| [TurboVec](https://github.com/RyanCodrai/turbovec) `IdMapIndex` | Bundled FFI rerank with allowlist search (`experience-v2.tvim`) |
| MiniLM-L6 (lazy) | First `record`/`recall` may download ONNX + tokenizer to `~/.overdrive/models/` and an ONNX Runtime sidecar to `~/.overdrive/lib/` |
| Hashed fail-open | Network, model, tokenizer, or ORT failure falls back to 256-d hashed vectors; `record`/`recall` never fail because of the embedder |
| Working Memory | Session scratch keyed by repository; cleared on the next `session-start`; never promoted to durable knowledge |
| Decision Ledger | Run-local rulings in SQLite + `.overdrive/runs/<run>/ledger.md` |
| Automatic Forgetting | GC on `session-start`; protected sources (`adr`, user feedback, project instructions) are kept unless already deprecated |

Prebuilt binaries and TurboVec libraries are installed automatically. You do not need Go, Rust, cargo, pip, or CGO on the user machine. MiniLM and ORT are **not** in git and are **not** among the six bundled `runtime/lib/` artifacts. Tests/CI use `OVERDRIVE_EMBEDDER=stub` so nothing is downloaded.

`status` reports `backend`, `embedder` (`stub` \| `hashed` \| `minilm`), `embedder_dim`, and `turbovec_available`. When the embedding dimension changes, the runtime rebuilds `.tvim` from active memories.

Read [`docs/EXPERIENCE_ENGINE.md`](docs/EXPERIENCE_ENGINE.md) for the lifecycle, trust model, storage, environment variables, privacy rules, and internal protocol.

## What's Inside

### Core

- **using-overdrive** - Entry point, workflow router, and Experience Engine bootstrap policy.
- **plan** - Architecture-aware analysis and specification.
- **execute-plan** - Adaptive spec execution.
- **auto-run** - Autonomous discovery and decision policy.

### Runtime

- **Experience Engine** - Local persistent operational learning with project-scoped retrieval, session Working Memory, and fail-open embeddings.
- **overdrive-runtime** - Prebuilt Local Runtime (`CGO_ENABLED=0`): SQLite/FTS5, bundled TurboVec FFI, lazy MiniLM via ONNX Runtime sidecar, hashed fallback.

### Development

- **test-driven-development** - RED-GREEN-REFACTOR implementation discipline.
- **systematic-debugging** - Evidence-driven root-cause debugging.
- **using-git-worktrees** - Isolated development workspaces.
- **subagent-driven-development** - Fresh-context task execution with review.

### Quality

- **verification-before-completion** - Evidence before completion claims.
- **requesting-code-review** - Structured implementation review.
- **receiving-code-review** - Rigorous handling of review feedback.
- **finishing-a-development-branch** - Final branch and delivery workflow.

### Meta

- **writing-skills** - Process for creating and testing reusable skills.

### Planning references

`plan` owns supporting reasoning rather than forcing users to orchestrate many public micro-skills.

Current planning references include:

- architecture discovery;
- visual reasoning;
- Visual Spec schema;
- Experience Engine internal protocol.

These are capabilities used by planning when evidence says they are necessary.

## Philosophy

### Understand before changing

A coding agent should understand the system it is modifying before deciding how to modify it.

### Existing architecture beats theoretical purity

Consistency inside an established codebase is usually more valuable than importing the agent's favorite architecture.

### Spec is the source of truth

Requirements and decisions belong in one authoritative spec, not a chain of progressively reinterpreted documents.

### Decisions are stable; implementation is adaptive

The spec constrains behavior and architecture. The executor discovers implementation detail from the real code.

### Planning should not duplicate execution

TDD, exact commands, local implementation detail, and code-level discovery belong in execution unless they are required to define the contract.

### Skills own outcomes

A high-level skill should know which supporting capabilities it needs. Users should not need to manually orchestrate a long chain of micro-skills.

### Questions should first become discovery

When the answer is present in the repository, environment, documentation, or evidence, the agent should find it instead of asking the user.

### Visual references are inputs, not specifications

Screenshots and Figma references should be converted into structured visual requirements and reconciled with the project's design system before implementation.

### Evidence over assumptions

Architecture decisions, debugging conclusions, reviews, completion claims, and durable experience should be supported by observable evidence.

### Experience is advisory, reality is authoritative

Past executions should make future work faster, but remembered architecture never outranks the current repository. Contradictions retire stale experience instead of forcing old decisions onto new code.

### YAGNI

Uncertainty is not permission to expand scope.

## What Makes Overdrive Different

Overdrive is built around a few architectural principles that shape the entire development cycle.

### The project is the primary source of architectural truth

The agent does not begin by choosing its preferred architecture.

It discovers the architecture already present, identifies recurring patterns, and extends them unless there is a concrete reason not to.

```text
existing system
      ↓
architecture discovery
      ↓
established conventions
      ↓
requested change
      ↓
smallest compatible solution
```

### One authoritative spec

Overdrive avoids chains of documents that progressively reinterpret the same requirement.

The plan produces one spec that contains the decisions and contracts needed for implementation.

```text
request
   ↓
discovery
   ↓
decisions
   ↓
SPEC
   ↓
execution
```

### Planning defines contracts, not every keystroke

The spec explains what must be built, what constraints must be preserved, and in which order the work should happen.

Implementation details are resolved against the real code when each task is executed.

### Execution strategy is contextual

Normal execution and subagent-driven execution are tools, not dogma.

Overdrive recommends the mode that best matches task independence, shared-file contention, integration risk, and the need for progressive understanding.

### Autonomy is evidence-driven

`/auto-run` does not mean guessing without permission.

It means resolving routine uncertainty through discovery, evidence, and recorded decisions before interrupting the user.

### Visual work is specified before it is coded

When frontend work depends on screenshots, Figma, or other visual references, Overdrive turns those inputs into structured visual requirements, reconciles them with the existing design system, and only then implements the UI.

### Experience compounds quietly

Overdrive learns reusable project rules, lessons, anti-patterns, and confirmed debugging outcomes as a side effect of verified work. There is no `/remember` workflow and no memory dashboard required to get value from it.

### Rigor stays where it matters

Overdrive keeps strong engineering disciplines (testing, debugging, review, isolated development, and verification) without forcing unnecessary planning ceremony onto every task.

## Development and Validation

Run the structural and behavior-oriented local test suite:

```bash
python -m pytest -q
```

Build all portable Experience Engine runtime binaries (maintainer/CI only; requires Go, Rust, and Zig for cross-platform TurboVec FFI):

```bash
# Zig is used by cargo-zigbuild for non-native TurboVec targets.
export PATH="/path/to/zig:$PATH"
./scripts/build-runtime.sh
```

Validate the Overdrive package:

```bash
python scripts/validate.py
```

Validate Bash scripts:

```bash
bash -n scripts/*.sh
```

### Releases

GitHub Actions runs on every push to `main` and on pull requests (`.github/workflows/ci.yml`).
When the repository is published, tagging `v1.0.0` (or any `v*` tag) will trigger `.github/workflows/release.yml`, which rebuilds
all six runtime binaries and TurboVec libraries and attaches one zip per OS/arch pair to
[GitHub Releases](https://github.com/gfrancodev/overdrive/releases).
The repo also keeps prebuilt artifacts under `runtime/` for zero-toolchain installs; see
[CONTRIBUTING.md](CONTRIBUTING.md#releases) for the maintainer checklist.

The project is expected to evolve toward a dedicated behavioral eval harness that measures Overdrive against controlled baselines and alternative agent workflows using identical fixtures, models, tasks, and repeated runs.

The benchmark should measure not only success rate, but also:

```text
token usage
duration
user interruptions
architecture violations
unnecessary abstractions
review findings
test success
visual fidelity
design-system compliance
rework
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for skill conventions, runtime maintainer
workflow, validation commands, and pull request expectations.

Overdrive is intentionally opinionated about changes to its core methodology.
Treat skill edits as behavior changes, not documentation-only tweaks.

## Code of Conduct

This project follows the [Contributor Covenant](https://www.contributor-covenant.org/version/2/0/code_of_conduct.html).
Read [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for community standards and how to
report unacceptable behavior.

## Acknowledgements

Overdrive was **inspired by** [Superpowers](https://github.com/obra/superpowers) by [Jesse Vincent](https://blog.fsck.com) and [Prime Radiant](https://primeradiant.com). Superpowers remains a strong default for many teams and coding agents. The authors did outstanding work, and you should use it without hesitation when it matches how you want to build.

Overdrive exists because, for some contexts (especially brownfield systems with real architecture constraints), a **different emphasis** made more sense in daily practice: discover the system first, one authoritative spec, adaptive execution, and strict respect for patterns already in the repo. That is a personal workflow choice, not a claim that one approach is universally better.

Several skill structures and disciplines in Overdrive trace back to Superpowers (composable skills, TDD, systematic debugging, subagent-driven execution, git worktrees, verification-before-completion). Overdrive is an **independent project**: not affiliated with, endorsed by, or maintained by the Superpowers authors.

Superpowers is distributed under the **MIT License**. Copyright (c) 2025 Jesse Vincent. See the [Superpowers LICENSE](https://github.com/obra/superpowers/blob/main/LICENSE).

Overdrive complies with the MIT License by including Jesse Vincent’s copyright notice and the MIT permission notice in [`LICENSE`](LICENSE), and by retaining this attribution in copies and substantial portions of this project (including this README).

## License

MIT License. Copyright (c) 2026 Gustavo Franco.

This project also acknowledges material inspired by Superpowers (MIT License, Copyright (c) 2025 Jesse Vincent). See [`LICENSE`](LICENSE) for the complete license text, including both copyright notices and the MIT permission notice required for redistribution and derivative works.
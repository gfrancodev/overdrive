# Changelog

## 1.2.0 - 2026-09-23

### Shared memory (P2P)

- Opt-in **circles** with Ed25519 device identity, expiring invites, signed member lists, and revocation.
- **Allowed folders** per circle: sharing and peer recall only run inside explicitly authorized paths.
- Session-start **delta sync** over TCP (encrypted batch, signed items) with optional `share listen` for LAN/VPN peers.
- Separate TurboVec index (`experience-v2-peer.tvim`) and `peer_memories` bucket in recall.
- Two-stage peer recall: vector floor when TurboVec is available, plus required concrete problem-signature overlap.
- Second-brain distillation: layer-2 packets (`packet_content`, `problem_signature`), hot index, session consolidation.
- Precise memory loop: narrow recall, packet-first peer content, validate feedback adjusts hot retention.

### Hooks and session lifecycle

- `sessionStart` hook syncs peer deltas and prints `share status` when a circle applies to the current folder.
- New `sessionEnd` hook runs `session-end` consolidation for the closing session.
- New `overdrive-runtime share status` (`--format text` for hooks) and `session-end` commands.

### Install and packaging

- `install.sh` / `install.ps1` now install only skills plus the prebuilt runtime and TurboVec library for the current OS/CPU.
- Foreign platform binaries and stale TurboVec libraries are pruned from the destination on install.
- Go/Rust source builds are opt-in via `OVERDRIVE_INSTALL_FROM_SOURCE=1` (contributor fallback only).
- New `scripts/package-platform.sh` builds a slim per-platform install tree without Go source, examples, or foreign binaries.
- Shared install helpers in `scripts/install-common.sh`.

### Runtime quality

- Experience runtime refactored into testable `*_logic.go` modules (recall, sync, GC, ORT helpers) with injectable test hooks.
- Broad integration and coverage test suite; hybrid search benchmarked (~5 ms recall at 100 memories, TurboVec search ~40 µs at 1k vectors).
- LAN P2P smoke script: `runtime/experience/scripts/p2p-lan-test.sh`.
- ONNX Runtime session path split (`ort_ffi.go` / `ort_fake.go` build tags) for CI vs production.

## 1.1.0 - 2026-09-23

### Micro-detail taxonomy and assets

- New `micro-detail-taxonomy.md` checklist: layout, typography, css, motion, navigation, image, icon, interaction.
- Extended `visual-spec.schema.json`: `css`, `motion`, `navigation`, structured `typography`, hardened `assets` with `iconKind`, `customized`, `acquisition`.
- Extended `design-system.visual-spec.schema.json`: motion keyframes/transitions, navigation tokens, `icons` section.
- Step 3B asset/icon acquisition: download, consult, generate (images); custom vs library icon classification.
- Step 4A exhaustive extraction: CSS computed styles, micro-animations, animated navigation.
- `design-system.visual-spec.example.json` added; `visual-spec.example.json` expanded (15+ microDetails).
- Verification by category including motion replay and icon/image fidelity.
- Prose convention: no em dash (U+2014) in package markdown; structural test enforces it.

### Visual Spec fidelity

- Mandatory **frontend classification gate** for any visual input (image, video, URL, HTML, Figma, site reference).
- **Fidelity mode** choice: `reference-exact` (binding reference micro-details) vs `project-design-system`
  (explicit `reconciliation` mappings). Normal mode asks once when a project design system exists; auto-run
  resolves via Decision Ledger without questions.
- Hardened `visual-spec.schema.json` with `classification`, `fidelityMode`, layout tree, `elements`,
  `microDetails` checklist, and `reconciliation`.
- New `design-system.visual-spec.schema.json` for token/component anatomy contracts.
- New `visual-spec.example.json` demonstrating expected granularity.
- Reference acquisition guidance: `wget` mirror to `.overdrive/visual-sources/<slug>/` for URL references.
- `execute-plan`, subagent briefs, and `verification-before-completion` now treat Visual Spec JSON as binding
  implementation contract.

## 1.0.0 - 2026-09-12

First public release of Overdrive.

- Architecture-aware methodology for coding agents: discover before deciding, spec before execution, verify before completion.
- Full skill suite for planning, execution, TDD, debugging, code review, verification, git worktrees, and branch finishing.
- **Local Runtime** (Experience Engine): SQLite + WAL + FTS5, TurboVec SIMD rerank, lazy MiniLM embeddings, working memory, decision ledger, conflict resolution, evidence scoring, and automatic forgetting.
- Prebuilt runtime binaries and TurboVec FFI libraries for Linux, macOS, and Windows (x64 and ARM64).
- Plugin metadata for Cursor, Claude Code, and Codex with cross-runtime skill installation.
- CI and release workflows for validation and multi-platform artifact builds.

### Development history

#### 0.3.0 - 2026-09-12

- Replaced JSON store with **SQLite + WAL + FTS5** (`~/.overdrive/experience-v2.db`) and automatic migration from `experience-v1.json`.
- Integrated official **[TurboVec](https://github.com/RyanCodrai/turbovec)** `IdMapIndex` via a prebuilt Rust FFI library shipped per OS/arch (`runtime/lib/`).
- Added hybrid retrieval: FTS5 candidate allowlist → TurboVec SIMD rerank, with fail-open to FTS5-only when the index or embedder is unavailable.
- Added lazy-loaded embeddings (MiniLM-L6 pack download on first use; hashed 256-d fallback; `OVERDRIVE_EMBEDDER=stub` for CI).
- Added **Working Memory** (session-scoped scratch), **Decision Ledger** (`ledger-add` / `ledger-list` + SQLite + `.overdrive/runs/<run>/ledger.md`), **conflict resolution** records, **evidence scoring**, and **Automatic Forgetting** on `session-start`.
- Backend identifier is now `sqlite-fts5-turbovec-v1`. Installers copy runtime + TurboVec library; users never build Go/Rust/Cargo.
- Updated protocol docs, architecture diagrams, and skills to reflect the full Local Runtime stack.

#### 0.2.2 - 2026-09-12

- Added plugin logo (`assets/logo.png`) and wired it into Cursor, Claude, and Codex manifests.
- Added Codex marketplace branding color aligned with the logo palette.

#### 0.2.1 - 2026-09-12

- Made `/auto-run` a zero-approval-gate runtime policy: once started, Overdrive never asks the user for routine approvals, confirmations, execution-mode choices, continuation, or delivery choices.
- Replaced protected stop conditions with **Autonomous Boundaries**. Unrequested consequential actions are deferred while all safe/reversible work continues.
- Treats consequential actions explicitly included in the initial request as mission-level authorization, avoiding duplicate approval prompts.
- Added deterministic fallback rules for materially underdetermined choices: preserve architecture, minimize scope, maximize reversibility, minimize external effects.
- Propagated auto-run behavior into planning, execution, worktree isolation, branch finishing, commands, architecture docs, README, and agent instructions.
- Added host-authorization handling: platform-enforced prompts cannot be bypassed, so blocked actions are deferred rather than turned into Overdrive approval questions.

#### 0.2.0 - 2026-09-12

- Added the automatic local Experience Engine as a transversal runtime capability, not a user-facing skill.
- Added a zero-dependency Go runtime with Git-remote project identity, scoped memory, trust/evidence scoring, secret redaction, atomic persistence, cross-process locking, and hybrid lexical/hashed retrieval.
- Added automatic experience recall/reconciliation to planning, task-scoped recall to execution, similar-incident recall to debugging, and verification-gated capture.
- Preserved the rule that current repository reality always outranks historical experience.
- Added Linux, macOS, and Windows x64/ARM64 prebuilt runtime targets plus Bash/PowerShell installers.
- Added durable memory kinds/scopes, confidence validation, contradiction/deprecation, and deduplication.
- Added Experience Engine architecture and internal protocol documentation.
- Kept semantic infrastructure deliberately lightweight; TurboVec is reserved as a future ANN backend when benchmarks justify it.

#### 0.1.0 - 2026-09-08

- Initial release of Overdrive.
- Replaced brainstorming + writing-plans with architecture-aware `plan`.
- Added single-source-of-truth specs with ordered tasks.
- Integrated automatic visual reasoning and Visual Spec JSON schema.
- Added conditional TypeUI guidance integration.
- Added adaptive `execute-plan` engine selection.
- Added `/auto-run` discovery/ruling policy.
- Retained TDD, systematic debugging, verification, reviews, worktrees, and branch finishing disciplines.

# Changelog

## Unreleased

## 2.0.1 - 2026-10-16

### Share mesh dashboard

- **Whitelabel (optional):** `$OVERDRIVE_HOME/share/whitelabel.json` overrides title, icons, `themeColor`, and color tokens (`ink`, `sea`, `foam`, `cyan`, `live`, `mute`). With no file, the dashboard keeps the built-in **Overdrive** look. Legacy `share/ui.json` is still read when `whitelabel.json` is absent.
- **Brand assets:** `logo`, `favicon`, and `appleTouchIcon` accept `/logo.png`, `share/assets/*`, or `https://` URLs. Remote images download once at `share listen` startup and are served from `/assets/…` (cache map in `share/whitelabel-remote-cache.json`).
- **Tray icon:** `overdrive-tray` uses the whitelabel `logo` (or `favicon`) when set; otherwise the built-in mark.
- **Theming:** Token-driven accents apply across the orbit view, dock, chat/call chrome, and xterm palette; examples in `docs/share-whitelabel.example.json` and `docs/share-whitelabel-remote.example.json`.

### Fixes

- Mesh dashboard boot: shared helpers (`dashboard-shared.mjs`) avoid a `computerLabel` temporal-dead-zone when the ESM bundle loads.
- Custom icons served from `share/assets/`; embedded logo remains the default fallback.

## 2.0.0 - 2026-10-16

### Share mesh dashboard

- `share listen` serves an embedded dashboard (chat, mesh voice/video calls, live terminals, peer machines) on `OVERDRIVE_SHARE_UI` (default listen port + 1).
- WebRTC mesh calls with signaling over `/api/call/signal` and `/api/call/events`; STUN by default, optional TURN via `OVERDRIVE_TURN_URLS` / `OVERDRIVE_TURN_USERNAME` / `OVERDRIVE_TURN_CREDENTIAL` (local coturn: `docker-compose.turn.yml`).
- Circle chat with attachments, voice notes, YouTube/Vimeo embed previews, and encrypted transport over the circle key.
- Shared terminal sessions: create, attach, control grants, context bundles, workspace artifacts, and remote viewers.
- Core lifecycle API (`/api/core/ui`, mesh pause/resume) and tray helper under `runtime/tray/`.
- Harness adapter manifests under `hooks/adapters/` for Cursor, Claude, Codex, and other agents.

### Quality and CI

- Node contract test for two-peer offer/answer/ICE (`tests/mesh_call_signaling.test.mjs`); Playwright smoke with fake media between two `share listen` instances (job `browser-media`).
- Go coverage gate at **82%** on the runtime package excluding `terminal_host.go` (`scripts/go-coverage-gate.sh`); dashboard JavaScript lcov published to SonarCloud.
- SonarCloud scan, gremlins mutation gate on security-sensitive paths, and expanded pytest/Go coverage for `share_call`, chat, terminal APIs, and dashboard modules.
- Optional idle circle cleanup: `OVERDRIVE_CIRCLE_IDLE_DAYS` and `share circle prune` (`--dry-run`).

### Runtime

- MCP stdio server (`mcp` command) for recall, record, handoff, and terminal context tools.
- Local continuity: handoffs, standing decisions, transcript span index, and `before-prompt` / `index-tail` hooks.
- Experience Engine docs updated for TURN, mesh calls, and CI expectations.

## 1.4.0 - 2026-10-11

### Local continuity

- Decisions and handoffs stay local across harnesses: standing versus run scope, pin, circle packets without vectors, and harness adapters.

### SonarCloud

- Open issues cleaned up, and Go coverage is published to SonarCloud.

## 1.3.0 - 2026-09-30

### Second brain recall

- `recall` still ranks with FTS5 and TurboVec, then adds one graph hop on a shared path, symbol, concrete error code, or `supersedes` link.
- The same response includes `page_index`: a repository, folder, kind, and memory branch with a short index card and no lesson body.
- Generic uppercase words such as `WAL`, `HTTP`, and `FTS` do not become error nodes. `SQLITE_BUSY`, `E500`, and `db.go` do.
- A memory and its graph nodes commit in one transaction. Catalog import and backfill stay fail-open.
- ID sync and catalog dials use a short timeout so a dead peer does not hold recall or the circle page.
- `plan`, `execute-plan`, `auto-run`, `systematic-debugging`, and `AGENTS.md` tell the agent to read `page_index` and `graph_neighbors` before widening recall, and to treat `peer_memories` as colleague evidence. `share: off` and `folder-blocked` stay local-only.

### Shared circle peers

- Each member now stores dial endpoint, remote address, hostname, and last seen time.
- Accepting an invite exchanges that roster so both machines learn a reachable address.
- `share listen` on a wildcard bind serves a peer page (`OVERDRIVE_SHARE_UI`, default port listen+1) with `GET /api/peers` and `GET /api/contexts`.
- The Contexts drawer projects the peer's PageIndex tree. Loose items appear only when that peer has not sent a tree yet.
- The page uses the Overdrive mark, favicon, and brand color. The panel title is `LAN`.

### Visual Spec 1.0

- `visual-spec.json` now targets the canonical schema at `https://visualspec.dev/schema/1.0/schema.json`
  (vendored in `skills/plan/references/visual-spec.schema.json` plus offline bundle).
- Overdrive fidelity, classification, and taxonomy coverage use namespaced `extensions.overdrive.*` keys
  (`overdrive-visual-extensions.md`).
- Legacy Overdrive surface schema moved to `overdrive-surface-visual-spec.legacy.schema.json`.
- Plan, execute, verification, and README docs updated for `validation.rules` and Visual Spec core sections.

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

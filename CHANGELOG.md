# Changelog

## 0.3.0 - 2026-09-12

- Replaced JSON store with **SQLite + WAL + FTS5** (`~/.overdrive/experience-v2.db`) and automatic migration from `experience-v1.json`.
- Integrated official **[TurboVec](https://github.com/RyanCodrai/turbovec)** `IdMapIndex` via a prebuilt Rust FFI library shipped per OS/arch (`runtime/lib/`).
- Added hybrid retrieval: FTS5 candidate allowlist → TurboVec SIMD rerank, with fail-open to FTS5-only when the index or embedder is unavailable.
- Added lazy-loaded embeddings (MiniLM-L6 pack download on first use; hashed 256-d fallback; `OVERDRIVE_EMBEDDER=stub` for CI).
- Added **Working Memory** (session-scoped scratch), **Decision Ledger** (`ledger-add` / `ledger-list` + SQLite + `.overdrive/runs/<run>/ledger.md`), **conflict resolution** records, **evidence scoring**, and **Automatic Forgetting** on `session-start`.
- Backend identifier is now `sqlite-fts5-turbovec-v1`. Installers copy runtime + TurboVec library; users never build Go/Rust/Cargo.
- Updated protocol docs, architecture diagrams, and skills to reflect the full Local Runtime stack.

## 0.2.2 - 2026-09-12

- Added plugin logo (`assets/logo.png`) and wired it into Cursor, Claude, and Codex manifests.
- Added Codex marketplace branding color aligned with the logo palette.

## 0.2.1 - 2026-09-12

- Made `/auto-run` a zero-approval-gate runtime policy: once started, Overdrive never asks the user for routine approvals, confirmations, execution-mode choices, continuation, or delivery choices.
- Replaced protected stop conditions with **Autonomous Boundaries**. Unrequested consequential actions are deferred while all safe/reversible work continues.
- Treats consequential actions explicitly included in the initial request as mission-level authorization, avoiding duplicate approval prompts.
- Added deterministic fallback rules for materially underdetermined choices: preserve architecture, minimize scope, maximize reversibility, minimize external effects.
- Propagated auto-run behavior into planning, execution, worktree isolation, branch finishing, commands, architecture docs, README, and agent instructions.
- Added host-authorization handling: platform-enforced prompts cannot be bypassed, so blocked actions are deferred rather than turned into Overdrive approval questions.

## 0.2.0 - 2026-09-12

- Added the automatic local Experience Engine as a transversal runtime capability, not a user-facing skill.
- Added a zero-dependency Go runtime with Git-remote project identity, scoped memory, trust/evidence scoring, secret redaction, atomic persistence, cross-process locking, and hybrid lexical/hashed retrieval.
- Added automatic experience recall/reconciliation to planning, task-scoped recall to execution, similar-incident recall to debugging, and verification-gated capture.
- Preserved the rule that current repository reality always outranks historical experience.
- Added Linux, macOS, and Windows x64/ARM64 prebuilt runtime targets plus Bash/PowerShell installers.
- Added durable memory kinds/scopes, confidence validation, contradiction/deprecation, and deduplication.
- Added Experience Engine architecture and internal protocol documentation.
- Kept semantic infrastructure deliberately lightweight; TurboVec is reserved as a future ANN backend when benchmarks justify it.

## 0.1.0 - 2026-09-08

- Initial release of Overdrive.
- Replaced brainstorming + writing-plans with architecture-aware `plan`.
- Added single-source-of-truth specs with ordered tasks.
- Integrated automatic visual reasoning and Visual Spec JSON schema.
- Added conditional TypeUI guidance integration.
- Added adaptive `execute-plan` engine selection.
- Added `/auto-run` discovery/ruling policy.
- Retained TDD, systematic debugging, verification, reviews, worktrees, and branch finishing disciplines.

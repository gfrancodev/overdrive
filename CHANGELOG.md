# Changelog

## Unreleased

### Validate

- **A circle's chat no longer splits in two.** A member admitted by someone other than the creator could not reach the creator's chat yet (the creator did not know it), hosted the chat itself, and stayed there: nobody else ever heard it, and the dashboard said connected. A broker that fails is now passed over for 30 seconds only, and every 15 seconds each core moves to the broker the election picks, keeping its working link when the move fails. The Connection sheet says when a device hosts the chat with no one else on it.
- Replacing a chat connection closed the new one too: the old connection's error handler dropped whatever link was current, and the core reconnected in a loop.
- **Members learn a removal at once.** A member that receives the new key now fetches the creator's roster right away; before, it kept the removed device as a member until its next roster exchange, up to a minute, while that device still held the previous key.
- **Three cores, end to end.** A new suite runs three real cores on one machine: pairing in a chain (C through B), experience reaching a device that never talked to its author, chat for the whole circle, connection health, a removal with key rotation, and diagnostics without keys. A [cross-machine runbook](docs/CROSS_MACHINE_RUNBOOK.md) covers what needs real computers.
- **The MCP server works with MCP clients.** `overdrive-runtime mcp` only understood messages framed with Content-Length headers, as LSP does, so every client that follows the MCP stdio transport (one JSON message per line: Claude Desktop, Cursor, VS Code, Zed) hung at `initialize`. It now reads either framing and answers in the one the client used, and every tool describes its arguments with an `inputSchema`, which clients that validate the protocol require. Checked with the official MCP Python SDK.
- **Test runs no longer leave hundreds of processes behind.** Go tests that open a terminal started the test binary as the session host; it ignored that role and ran the whole suite again in the background, and since 2.3 a Core keeps live hosts, so nothing stopped them. The test binary now acts as a real host and stops its hosts when the suite ends, and the Python suite stops the hosts its Cores left in temporary homes.
- **Mutation testing runs again.** The gremlins gate never finished on this package: one mutant re-ran the whole suite, and its coverage pass hangs here. `scripts/mutation.sh` now mutates the lines a change touched and runs only the tests that execute each one, from a per-test coverage map.

### Recover & diagnose

- **Connection health.** The menu bar's network icon shows how this device reaches the circle, and opens a Connection sheet with one line per capability (chat, calls, experience, terminals): connected, partly connected, reconnecting, offline or blocked, why, and the fix. Each device in the circle shows whether it answers, refuses connections, times out or has no route. The Core derives it from the mesh switch, the network choice, the chat link and a TCP connect to each member's listener; nothing new travels between devices.
- **Devices on the network can connect once you allow it.** The tray and the session hook start the Core on loopback, so before this, devices on other computers could find each other but not connect: no pairing, sync or shared terminals across machines. The Connection sheet, and Nearby, now offer "Allow devices on this network". The Core rebinds on every interface without restarting, remembers the choice in `share/network.json`, and announces it over mDNS so other devices can say "accepts connections only from itself" instead of a bare refusal. "Accept connections only from this computer" closes it again.
- **Pairing says why it cannot reach a device**, with the same causes and fixes, instead of a raw `connection refused`.
- **Diagnostics.** Copy diagnostics puts a report on the clipboard: version, platform, network, discovery, chat links, circles (key epoch, members, number of shared folders), nearby devices and connection health. It leaves out keys, invite secrets, tokens, and folder paths or names.
- **The dashboard needs no internet.** Three.js, which drew the globe, came from a CDN; the Core now serves it (r170, MIT). Chat, peers and local terminals work with the WAN off, and a browser test runs the dashboard with every request off this computer blocked.
- Local API: `GET /api/connections/health`, `GET /api/diagnostics`, `GET/POST /api/network`. Loopback only.

### Share & collaborate

- **Shared folders from the dashboard.** Circle settings list the folders this device shares, share another one by its path (or `~/…`) or from the projects this device already recorded experience in, and stop sharing one. A folder must exist, and the disk root or the whole home folder is refused. Stopping a folder ends future exports from it; what other devices already received stays with them. CLI: `share circle folder-remove`.
- **Remove a device from the dashboard.** On the creator, each member has Remove. Before anything happens, the Core says who loses access, which devices that one invited (they can go too), and which members run a core that cannot receive the new key. Removing then revokes, rotates the circle key and clears the device's terminal access, as `share circle revoke` does.
- **Key status.** Circle settings show how many times the circle key changed and when, until when the previous key is still accepted, and which members need an update to receive new keys.
- Local API: `GET/POST/DELETE /api/circles/{id}/folders`, `GET/POST /api/circles/{id}/members/{device}/revoke` (GET previews). Loopback only, and none returns key material.
- Dashboard sheets showed controls that were meant to be hidden: all three pairing buttons at once, the circle picker with a single circle, and the circle field on the joining device.

### Pair & trust

- **Pair from the dashboard.** Each device under Nearby has Pair. Both screens show a 6-digit code derived from both identities, both key-agreement keys and a commit-then-reveal nonce exchange, so a device in the middle gets one guess in a million. Nothing joins until both people confirm; the member picks the circle after comparing. The member then signs the admission and seals the circle key, at its current epoch, to the joiner's key-agreement key.
- **Invites no longer carry the circle key.** A new invite is a one-time token: the joiner proves it holds the secret with an HMAC bound to its identity and the time, and gets the same sealed welcome a pairing ends with. A used invite is deleted; a wrong secret does not burn it. Invites from older cores, with the key inside, are still accepted.
- **Explicit circle.** Invites and pairings name their circle; the dashboard keeps an active circle chosen in Circle settings instead of always using the first one, and issuing an invite no longer creates a circle on its own.
- **Get started.** A setup panel shows only the steps the Core still reports as missing (create or join a circle, pair a device, share a folder) and creates circles from the UI. Local API: `GET /api/setup/status`, `GET/POST /api/circles`, `PATCH /api/circles/{id}`, `GET/POST /api/pairing/sessions`, `POST /api/pairing/sessions/{id}/confirm|cancel`. None of them returns key material.
- Concurrent writes of the circle file inside the Core (membership loop, sync, roster, keys, join, pairing) no longer overwrite each other; a roster refresh could undo a key adoption.

### Install & boot

- **Release bundles.** Each release now ships `overdrive-<os>-<arch>.zip` with the skills, plugin manifests, installers, runtime, TurboVec library, and a prebuilt tray built on its native runner. Installing from it needs no Go, Rust, or Docker. The workflow installs every bundle it can run, checks TurboVec loads, and upgrades it again while a Core runs before publishing.
- **The tray starts the Core.** Opening the tray (at login) starts the Core when nothing is serving, so the dashboard is ready without clicking Start.
- **Restarting or upgrading the Core keeps open terminals.** The new Core adopts session hosts that still answer their session socket instead of killing them; before, every Core restart closed every terminal.

### Fixes

- `install.ps1` replaces the runtime, the DLL, and the tray even while they run, by moving the old file aside.

## 2.2.1 - 2026-10-17

### Fixes

- **Windows:** every TurboVec DLL since 0.3.0 was linked without its `od_tv_*` exports, so the runtime panicked on any command that opened the engine (`status`, recall, capture). The DLLs are now built with the MSVC targets and a static C runtime, so they need no Visual C++ Redistributable. The release checks each DLL's exports and imports before publishing, and the DLLs in the repository were replaced.
- The runtime no longer crashes when the TurboVec library loads without its functions: it runs without TurboVec, as when the library is missing.
- **macOS:** the tray never compiled. Its notification code is Objective-C and was compiled as C, and with an exported Go callback the class was defined twice. The Objective-C now lives in `notify_darwin.m`.

## 2.2.0 - 2026-10-17

### Security

- **Revoking a member rotates the circle key.** `share circle revoke` now replaces the circle key after revoking, and `share circle rekey` does it on demand; only the creator rotates. The creator seals the new key to each remaining member with HPKE (ML-KEM-768 + X25519) under a key-agreement key each device derives from its identity, and signs the update. A revoked device cannot read anything sealed after the rotation.
- Cores trade key-agreement records and key updates through a new `keys` exchange, which works for a member that missed a rotation. `share listen` runs the roster and keys exchanges with every peer once a minute, and the creator pushes a new key at once. Receivers accept the previous key for 24 hours.
- Rotating drops this core's pending invites for the circle, reseals queued chat with the new key, and closes remote terminal links still on the old key (viewers reconnect). Invites carry the key epoch.
- **Upgrade note:** members whose core predates rotation cannot receive a new key; revoke and rekey name them. Upgrade every member before revoking anyone.

### Fixes

- Upgrading while the Core runs no longer crashes it. The installer replaced the runtime binary and the TurboVec library by copying over them in place, which truncates a file the running Core has mapped (SIGBUS). It now writes a temporary file and renames it, so the running Core keeps the old file until it restarts.
- Reinstalling or re-registering the tray no longer stops the Core and live terminals on Linux. They run in the tray unit's cgroup, and restarting the unit killed them; the unit now uses `KillMode=process`. The new unit takes effect at the next install.

## 2.1.0 - 2026-10-17

### Security

- **Signed circle membership.** A core now adds a device to its member list only with an admission signed by an active member it already trusts, and marks a member revoked only with a revocation signed by the circle creator. Before, any member running a modified core could add devices to, or revoke anyone from (the creator included), the member list of every peer that synced with it. Cores exchange the proofs through a new `roster` request after a sync or catalog exchange; the member list inside sync, catalog, and join responses only refreshes endpoint, hostname, and last-seen of known members.
- Invites carry the circle creator, so a device invited by another member records the real creator. Cores that joined that way before this release learn it from the member they recorded as creator.
- `share circle revoke --with-invitees` also revokes every device the member brought in.
- A sync request with an invalid signature can no longer change a member's endpoint or hostname: the listener verifies it first.
- **Upgrade note:** each 2.1 core signs admissions for the members already on its list, and the creator signs revocations for members already revoked. Review `share circle list` after upgrading. Older cores keep syncing memories with 2.1 cores, but a member invited from an older core appears on 2.1 cores only once a 2.1 member vouches for it.
- **A device's key can no longer be swapped by a peer.** A device ID is derived from its Ed25519 key, and the member list that peers exchange during sync, or a join request, is now rejected when the key does not derive the ID. Before, an active member running a modified core could replace another member's key on the peers that synced with it, then sign requests as that member and reach the terminal sessions shared with it. A record without a key no longer adds a member either.

### Docs

- `SECURITY.md` and `docs/EXPERIENCE_ENGINE.md` describe signed membership and what a member can still do: inviting stays open to every member by design.

### Fixes

- The circle file is written atomically (temporary file, then rename). Chat, call signaling, and terminal messages reload it on every delivery, and a read during a write used to drop the message; signed membership writes the file more often, which made this visible as an intermittent lost call.
- Tray icon on Linux: a whitelabel logo drawn with partial transparency (for example alpha 254) showed as a white square, because the tray library converts colors correctly only at alpha 0 or 255. The tray now makes each logo pixel fully opaque or fully transparent before handing it over.

## 2.0.3 - 2026-10-17

### Security

- A terminal card posted in chat grants its shares only after the card is saved, and grants the whole audience in one transaction. If saving the card or granting fails, the post fails and nobody gains access; before, a failed post could leave the session shared with part or all of the audience.

### Docs

- `SECURITY.md` states the revocation window: new requests are refused at once, but private mode and a CLI revoke reach already open streams at the next 15-second check.

## 2.0.2 - 2026-10-17

### Security

- **Live terminals need a per-session share.** Being an active circle member no longer opens another member's terminal. The host now serves metadata, control requests, and streams only to devices the session owner shared it with: an invite, or a terminal card posted in a direct conversation (the recipient), a custom group (its members), or the circle channel (active members). Owner and host always pass.
- Removing a participant, or revoking a circle member with `share circle revoke`, deletes its shares and returns any control lease it held to the owner.
- Live remote streams are re-admitted every 15 seconds and closed when the viewer loses its share or membership, or when private mode is turned on.
- **Upgrade note:** sessions shared before this release relied on circle membership alone. Upgrade the host and share them again. Viewers do not need to upgrade.
- The dashboard explains a refused remote attach ("Ask the owner to share this terminal with you.").

### Docs

- `SECURITY.md` now supports 2.0.x and documents the mesh threat model: circle chat is encrypted to the circle, not end to end between two people; there is no forward secrecy; revoking a member does not rotate the circle key.

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

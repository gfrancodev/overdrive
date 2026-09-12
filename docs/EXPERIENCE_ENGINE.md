# Overdrive Experience Engine

Overdrive 1.0 adds a full **Local Runtime** for persistent operational experience without adding a memory-management workflow for the user.

The user still asks Overdrive to plan, execute, debug, review, or auto-run work exactly as before. Experience is retrieved and captured internally as a consequence of normal development.

## Purpose

The Experience Engine exists to make later executions benefit from earlier ones:

```text
execution
  ↓
verified evidence
  ↓
compact experience
  ↓
future recall
  ↓
validate against current repository
  ↓
better next execution
```

It does **not** replace architecture discovery and it does not make historical memory authoritative.

> Current repository reality always wins over historical experience.

## Architecture

The Experience Engine is a transversal runtime capability, not a skill:

```text
                    Overdrive Skills
                         │
        ┌────────────────┼─────────────────┐
        │                │                 │
       plan          execute-plan       debugging
        │                │                 │
        └────────────────┼─────────────────┘
                         │
                  Experience Engine
                         │
       ┌─────────────────┼─────────────────┐
       │                 │                 │
 Working Memory    durable layers     Decision Ledger
 (session)    episodes/lessons/knowledge   (run-local)
       │                 │                 │
       └─────────────────┼─────────────────┘
                         │
              SQLite WAL + FTS5 lexical
                         │
              TurboVec IdMapIndex (official)
                         │
         lazy embeddings / hashed fallback
```

The runtime is a single prebuilt binary per OS/arch plus a bundled TurboVec FFI library. It requires no daemon, Docker, database server, Python runtime, GPU, cloud API, or user-side toolchain.

## Local Runtime stack (1.0)

| Component | Role |
|---|---|
| SQLite + WAL | Durable structured store (`experience-v2.db`) |
| FTS5 | Lexical candidate retrieval on subject/content/evidence |
| [TurboVec](https://github.com/RyanCodrai/turbovec) | Official `IdMapIndex` with allowlist search and incremental sync (`experience-v2.tvim`) |
| Embeddings | Lazy MiniLM-L6 pack download; hashed 256-d fail-open fallback |
| Working Memory | Session scratch populated at mission start; never promoted to durable knowledge |
| Decision Ledger | Run-local rulings in SQLite + `.overdrive/runs/<run>/ledger.md` |
| Automatic Forgetting | GC on `session-start` for stale/deprecated items |

Backend identifier: `sqlite-fts5-turbovec-v1`.

## Memory model

The runtime stores compact records, not conversations.

**Knowledge layer** (kinds): `fact`, `rule`, `decision`, `preference`, `procedure`.

**Lessons layer**: `lesson`, `anti_pattern`.

**Episodes layer**: `episode`.

Scopes:

```text
global
  ↓
organization
  ↓
repository
  ↓
module
```

Repository identity prefers the Git remote, so experience follows the repository across clones and worktrees.

Use `recall --layer knowledge|lessons|episodes|all` to narrow injection when needed.

## Trust and evidence

A memory record carries:

```text
confidence
priority
source
evidence_score
success_count
failure_count
status
last validation time
supporting evidence
```

Explicit user feedback, repository instructions, ADRs, verified execution, tests, and review outcomes receive more weight than agent observations or untrusted external text.

Current contradictions deprecate stale memories, record a `conflicts` row, and remove the vector from TurboVec.

## Automatic lifecycle

### Before work (`session-start`)

Overdrive identifies the repository, runs Automatic Forgetting (GC), seeds Working Memory from mission-relevant recall, and syncs the vector index when available.

### During discovery

Load-bearing historical claims are checked against current code/docs/tests. Matches strengthen confidence; contradictions retire stale records.

### During execution

Each ordered task receives a task-scoped slice of experience rather than the entire store. Run-local decisions go to the Decision Ledger, not durable knowledge.

### After verification

Successful verification can promote compact lessons, procedures, or anti-patterns. Failures and corrections can update confidence or deprecate old assumptions.

The user does not approve individual memories and does not need a remember command.

## Safety

The primary defense is **agent capture discipline**: only compact, verified, reusable operational knowledge enters durable storage. Agents must follow the capture policy in `skills/using-overdrive/references/experience-engine.md` and never send raw prompts, whole source files, full terminal transcripts, credentials, or unvalidated external text.

The runtime performs basic secret redaction before persistence as a **secondary fail-safe**, not the main control.

External text cannot become a high-priority rule merely because the model read it. Trust is tied to source and validation evidence to reduce persistent prompt-injection risk.

Protected sources (`user_feedback`, `project_instruction`, `adr`) are not silently deleted by GC unless already deprecated by contradiction.

## Storage and portability

Default location:

```text
~/.overdrive/
├── bin/
│   ├── overdrive-runtime
│   └── liboverdrive_turbovec_ffi.so   (or .dylib / .dll)
├── lib/
│   ├── liboverdrive_turbovec_ffi.so
│   └── libonnxruntime.so              (lazy ORT sidecar, not bundled in repo)
├── models/minilm-l6-v2/               (lazy MiniLM ONNX + vocab)
│   ├── model.onnx
│   └── vocab.txt
├── experience-v2.db
├── experience-v2.tvim
└── runs/<run>/ledger.md
```

The repository ships prebuilt runtimes and TurboVec libraries for Linux, macOS, and Windows x64/ARM64. The installer selects the local artifacts automatically — **no user build step**.

There is no resident service. The binary starts on demand, performs a small operation, and exits.

## Performance model

- project identity is derived from Git/path metadata;
- scope filtering removes irrelevant repositories before ranking;
- FTS5 narrows candidates before TurboVec rerank;
- embeddings load lazily on first record/recall (stub/hashed in CI);
- writes use SQLite WAL and a cross-platform directory lock;
- TurboVec or embedder failure fails open to FTS5 + hashed vectors.

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `OVERDRIVE_HOME` | `~/.overdrive` | Data directory |
| `OVERDRIVE_EMBEDDER` | *(try MiniLM, fail-open to hashed)* | `stub` (CI/tests), `hashed`, or `minilm` |
| `OVERDRIVE_MODEL_DIR` | `$OVERDRIVE_HOME/models/minilm-l6-v2` | MiniLM ONNX + tokenizer pack |
| `OVERDRIVE_MODEL_URL` | HuggingFace Xenova quant ONNX | Override model download URL |
| `OVERDRIVE_ORT_LIB` | `$OVERDRIVE_HOME/lib/libonnxruntime.so` | ONNX Runtime sidecar (purego, no CGO) |
| `OVERDRIVE_ORT_URL` | ORT 1.18.1 release for `GOOS/GOARCH` | Override ORT archive download |
| `OVERDRIVE_SKIP_EMBED_DOWNLOAD` | unset | Set `1` to disable lazy downloads |
| `OVERDRIVE_TURBOVEC_LIB` | bundled / `~/.overdrive/lib` | TurboVec FFI path |

`status` JSON includes `embedder` (`stub|hashed|minilm`) and `embedder_dim` (256 or 384). When embedding dimension changes, the runtime rebuilds `experience-v2.tvim` from active memories.

## Credits

TurboVec integration uses the official MIT-licensed project by Ryan Codrai: https://github.com/RyanCodrai/turbovec

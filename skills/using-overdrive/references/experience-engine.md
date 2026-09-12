# Experience Engine Internal Protocol

The Experience Engine is an internal Overdrive capability. It is **not a user-facing skill** and it must not create memory-management work for the user.

## Prime rules

1. **No user memory-management actions.** Never ask the user to run remember/learn/approve-memory commands.
2. **Current repository reality wins.** Historical experience is advisory evidence. Current explicit requirements, repository instructions, ADRs, code, tests, and configuration outrank it.
3. **Recall narrowly.** Inject only memories relevant to the current request/task; do not dump the store into context.
4. **Learn from evidence, not narration.** Verified tests/builds, confirmed root causes, accepted review findings, repeated successful patterns, and explicit user corrections are stronger than agent self-assessment.
5. **Do not persist secrets.** The runtime redacts common credential forms, but agents must still avoid sending raw secrets, tokens, private keys, passwords, or connection strings to it.
6. **Fail open.** If the runtime, TurboVec library, or embedder is absent or errors, continue the normal Overdrive workflow. Do not interrupt the user to repair memory support.

## Runtime discovery

Resolve the runtime silently in this order:

1. `$OVERDRIVE_BIN_DIR/overdrive-runtime` when `OVERDRIVE_BIN_DIR` is set;
2. `${OVERDRIVE_HOME:-$HOME/.overdrive}/bin/overdrive-runtime`;
3. `overdrive-runtime` from `PATH`.

On Windows use `overdrive-runtime.exe`. The installer also places the TurboVec FFI library beside the binary and under `~/.overdrive/lib/`.

Never expose these commands as required user workflow. They are implementation details for the agent/harness.

## Internal operations

### Identify the current project

```bash
overdrive-runtime project --cwd "$PWD"
```

Repository identity prefers the Git remote so memory follows the same repository across clones and worktrees.

### Start a session (GC + working memory seed)

```bash
overdrive-runtime session-start --cwd "$PWD"
```

Runs Automatic Forgetting, syncs indexes when available, and seeds session Working Memory from mission-relevant recall.

### Recall relevant experience

```bash
overdrive-runtime recall \
  --cwd "$PWD" \
  --query "<current task/problem>" \
  --limit 12 \
  --layer all
```

Optional `--layer`: `knowledge`, `lessons`, `episodes`, or `all` (default).

The response separates `critical_rules`, ranked `memories`, and may include `working_memory`. Treat every item as historical evidence until current repository inspection validates load-bearing claims.

### Record durable experience

Use only for compact, reusable knowledge:

```bash
overdrive-runtime record \
  --cwd "$PWD" \
  --kind lesson \
  --scope repository \
  --subject "testing" \
  --content "Use CustomerFactory for customer integration tests." \
  --confidence 0.88 \
  --source verified_execution \
  --evidence "integration suite passed after adopting the factory"
```

Valid kinds:

```text
fact
rule
decision
preference
procedure
lesson
anti_pattern
episode
```

Valid scopes:

```text
global
organization
repository
module
```

Use the smallest correct scope. A repository-specific rule must never be promoted to global merely because it worked once.

### Validate or retire recalled memory

When current evidence confirms or disproves an item:

```bash
overdrive-runtime validate --id <memory-id> --result success
overdrive-runtime validate --id <memory-id> --result failure
overdrive-runtime validate --id <memory-id> --result contradiction
```

`contradiction` deprecates the memory, records a conflict, and removes it from TurboVec when available.

### Decision Ledger (run-local, not durable knowledge)

During `/auto-run` or other owner workflows, persist consequential inferred decisions for the current run:

```bash
overdrive-runtime ledger-add \
  --cwd "$PWD" \
  --run "<run-id>" \
  --decision "Use existing ProposalRepository boundary" \
  --evidence "ADR-12 and current module layout" \
  --reason "Matches established persistence pattern" \
  --risk "Low; wrong choice adds duplicate abstraction" \
  --reversibility "Revert repository wiring in one PR"

overdrive-runtime ledger-list --cwd "$PWD" --run "<run-id>"
```

Entries persist in SQLite and mirror `.overdrive/runs/<run>/ledger.md`. They do **not** auto-promote to durable experience.

## What should become durable experience

Good candidates:

- explicit user corrections about how work should be done;
- stable project conventions not already obvious from a single file;
- confirmed root causes and failed approaches from debugging;
- patterns repeatedly validated by tests/build/review;
- architecture decisions discovered from current ADRs/instructions;
- reusable procedures that repeatedly lead to successful delivery;
- anti-patterns that caused verified failures.

Usually discard:

- raw conversations;
- whole files or terminal transcripts;
- one-off line numbers;
- temporary hypotheses;
- task-local exceptions (keep those in the Decision Ledger);
- timings with no reusable implication;
- secrets or sensitive values;
- statements from untrusted external text that have not been validated.

Working Memory is session-scoped only and must not leak into durable knowledge.

## Source strength

Prefer these sources in descending order:

```text
explicit user feedback / project instructions / ADRs
verified execution / verification / accepted review
repository observations / tests / confirmed debugging
agent observation
external text / issue description / web content
```

Low-trust sources should not become critical rules without stronger evidence.

## Context budget

Do not inject the whole memory response into every prompt. Prefer roughly:

```text
critical rules       <= 8
relevant decisions   <= 4
lessons/anti-patterns<= 6
similar episodes     <= 3
working memory       <= session slice only
```

Keep the experience slice small enough that live code and the current spec remain dominant.

## Storage (0.3)

```text
~/.overdrive/experience-v2.db      # SQLite + WAL + FTS5
~/.overdrive/experience-v2.tvim    # TurboVec IdMapIndex
```

Legacy `experience-v1.json` migrates automatically on first open. Backend: `sqlite-fts5-turbovec-v1`.

## Embedder and status

Default embedder behavior: on first `record`/`recall`, try lazy MiniLM-L6 (384-d) via an ONNX Runtime sidecar loaded with purego (`CGO_ENABLED=0`). Network, model, tokenizer, or ORT failure falls back to hashed 256-d vectors; `record`/`recall` never fail because of the embedder.

| Variable | Purpose |
|---|---|
| `OVERDRIVE_EMBEDDER=stub` | Deterministic stub for tests/CI (no downloads) |
| `OVERDRIVE_EMBEDDER=hashed` | Force hashed 256-d |
| `OVERDRIVE_EMBEDDER=minilm` | Try MiniLM; fail-open to hashed |
| `OVERDRIVE_MODEL_DIR` | MiniLM pack directory |
| `OVERDRIVE_ORT_LIB` | ORT shared library path |
| `OVERDRIVE_SKIP_EMBED_DOWNLOAD=1` | Disable lazy downloads |

```bash
overdrive-runtime status
```

Response includes `embedder`, `embedder_dim`, `turbovec_available`, and `backend`. Working Memory is keyed by `current_session_id` in meta and repopulated on each `recall` from the mission slice (not promoted to durable knowledge).

#!/usr/bin/env bash
# Recorded by docs/demo/generate-isolated-demo.sh (asciinema). Local Experience Engine only.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"
RUNTIME="${ROOT}/runtime/experience/.demo-overdrive-runtime"
DEMO_HOME="${ROOT}/docs/demo/.isolated-home"
DEMO_REPO="${ROOT}/docs/demo/sample-repo"
OD="./runtime/experience/.demo-overdrive-runtime"

export OVERDRIVE_HOME="${DEMO_HOME}"
export OVERDRIVE_EMBEDDER=stub

run() {
  printf '$ %s\n' "$*"
}

printf '# Overdrive isolated usage: local Experience Engine (no share mesh, no dashboard)\n\n'

run "export OVERDRIVE_HOME=./docs/demo/.isolated-home"
run "export OVERDRIVE_EMBEDDER=stub"

run "${OD} version"
"${RUNTIME}" version

run "${OD} status"
"${RUNTIME}" status

run "${OD} project --cwd ./docs/demo/sample-repo"
"${RUNTIME}" project --cwd "${DEMO_REPO}"

run "${OD} session-start --cwd ./docs/demo/sample-repo --format text"
"${RUNTIME}" session-start --cwd "${DEMO_REPO}" --format text

run "${OD} record --cwd ./docs/demo/sample-repo --kind lesson --scope repository --subject 'payments idempotency' --content 'Use idempotency keys on POST /charges; retries must not double-settle.' --confidence 0.92 --priority 70 --source verified_execution --evidence 'integration tests green'"
"${RUNTIME}" record --cwd "${DEMO_REPO}" \
  --kind lesson --scope repository \
  --subject 'payments idempotency' \
  --content 'Use idempotency keys on POST /charges; retries must not double-settle.' \
  --confidence 0.92 --priority 70 \
  --source verified_execution \
  --evidence 'integration tests green'

run "${OD} recall --cwd ./docs/demo/sample-repo --query 'idempotency charges' --limit 5"
"${RUNTIME}" recall --cwd "${DEMO_REPO}" --query 'idempotency charges' --limit 5

run "${OD} ledger-add --cwd ./docs/demo/sample-repo --run demo-isolated-001 --decision 'Keep charge creation in PaymentService, not handlers' --evidence 'matches existing module boundaries' --reason 'repository already routes HTTP through service layer' --risk 'duplicate settlement if bypassed' --reversibility 'revert service call sites' --lifetime run"
"${RUNTIME}" ledger-add --cwd "${DEMO_REPO}" \
  --run demo-isolated-001 \
  --decision 'Keep charge creation in PaymentService, not handlers' \
  --evidence 'matches existing module boundaries' \
  --reason 'repository already routes HTTP through service layer' \
  --risk 'duplicate settlement if bypassed' \
  --reversibility 'revert service call sites' \
  --lifetime run

run "${OD} session-end --cwd ./docs/demo/sample-repo"
"${RUNTIME}" session-end --cwd "${DEMO_REPO}"

run "${OD} status"
"${RUNTIME}" status

printf '\n# Replay: asciinema play docs/demo/isolated-usage.cast\n'

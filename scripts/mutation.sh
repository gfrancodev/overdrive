#!/usr/bin/env bash
# Mutation gate for the security-sensitive experience files.
# Every other non-test Go file in the package is excluded so the job stays bounded.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/runtime/experience"

allow=(
  util.go
  share_protocol.go
  sync_logic.go
  decision_lifetime.go
  span_index.go
  handoff.go
  peer_status.go
  share_call.go
  share_chat.go
  share_chat_groups.go
  terminal_control.go
  terminal_env.go
  terminal_workdir.go
  workspace_sync.go
)

exclude_re=()
for file in *.go; do
  case "$file" in
    *_test.go) continue ;;
  esac
  kept=0
  for name in "${allow[@]}"; do
    if [ "$file" = "$name" ]; then
      kept=1
      break
    fi
  done
  if [ "$kept" -eq 0 ]; then
    exclude_re+=("$(printf '%s' "$file" | sed 's/[.[\*^$()+?{|]/\\&/g')")
  fi
done

joined="$(IFS='|'; echo "${exclude_re[*]}")"

if ! command -v gremlins >/dev/null 2>&1; then
  echo "gremlins is not on PATH. Install with: go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0" >&2
  exit 1
fi

gremlins unleash . \
  --exclude-files "$joined" \
  --tags overdrive_fake_ort \
  --workers 2 \
  --timeout-coefficient 4 \
  --test-cpu 1 \
  --threshold-efficacy 85 \
  --threshold-mcover 0 \
  "$@"

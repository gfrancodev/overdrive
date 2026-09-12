#!/usr/bin/env bash
set -euo pipefail

runtime_path() {
  local home="${OVERDRIVE_HOME:-$HOME/.overdrive}"
  if [ -n "${OVERDRIVE_BIN_DIR:-}" ] && [ -x "$OVERDRIVE_BIN_DIR/overdrive-runtime" ]; then
    printf '%s\n' "$OVERDRIVE_BIN_DIR/overdrive-runtime"
    return 0
  fi
  if [ -x "$home/bin/overdrive-runtime" ]; then
    printf '%s\n' "$home/bin/overdrive-runtime"
    return 0
  fi
  if command -v overdrive-runtime >/dev/null 2>&1; then
    command -v overdrive-runtime
    return 0
  fi
  return 1
}

case "${1:-}" in
  session-start)
    if runtime="$(runtime_path 2>/dev/null)"; then
      "$runtime" session-start --cwd "$PWD" --quiet >/dev/null 2>&1 || true
    fi
    cat <<'MSG'
Overdrive is available. Before software-development work, load the using-overdrive skill.
Use plan for new work, execute-plan for an approved spec, and auto-run only when explicitly requested.
The Experience Engine is automatic when its runtime is installed; never ask the user to manage memory manually.
MSG
    ;;
  *) exit 0 ;;
esac

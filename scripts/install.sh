#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=install-common.sh
source "$ROOT/scripts/install-common.sh"

DEST="${OVERDRIVE_SKILLS_DIR:-$HOME/.agents/skills}"
OVERDRIVE_HOME="${OVERDRIVE_HOME:-$HOME/.overdrive}"
BIN_DIR="${OVERDRIVE_BIN_DIR:-$OVERDRIVE_HOME/bin}"
LIB_DIR="${OVERDRIVE_LIB_DIR:-$OVERDRIVE_HOME/lib}"

mkdir -p "$DEST" "$BIN_DIR" "$LIB_DIR" "$OVERDRIVE_HOME"

install_skills_tree "$ROOT" "$DEST"

detect_platform
INSTALLED_NAME="$(runtime_installed_name "$TARGET_OS")"
RUNTIME_OK=0

if install_runtime_for_platform "$ROOT" "$BIN_DIR" "$LIB_DIR" "$TARGET_OS" "$TARGET_ARCH"; then
  RUNTIME_OK=1
  prune_foreign_runtime_artifacts "$BIN_DIR" "$LIB_DIR" "$TARGET_OS" "$TARGET_ARCH"
elif [ "${OVERDRIVE_INSTALL_FROM_SOURCE:-}" = "1" ] && [ "$TARGET_OS" != "windows" ] && command -v go >/dev/null 2>&1; then
  echo "Prebuilt runtime not found; building from source (OVERDRIVE_INSTALL_FROM_SOURCE=1)..." >&2
  (
    cd "$ROOT/runtime/experience"
    CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-s -w' -o "$BIN_DIR/$INSTALLED_NAME" .
  )
  chmod +x "$BIN_DIR/$INSTALLED_NAME"
  if command -v cargo >/dev/null 2>&1 && [ -f "$ROOT/runtime/turbovec-ffi/Cargo.toml" ]; then
    (cd "$ROOT/runtime/turbovec-ffi" && cargo build --release)
    lib_name="$(turbovec_lib_name "$TARGET_OS")"
    dev_lib="$ROOT/runtime/turbovec-ffi/target/release/$lib_name"
    if [ -f "$dev_lib" ]; then
      cp "$dev_lib" "$LIB_DIR/$lib_name"
      cp "$dev_lib" "$BIN_DIR/$lib_name"
    fi
  fi
  prune_foreign_runtime_artifacts "$BIN_DIR" "$LIB_DIR" "$TARGET_OS" "$TARGET_ARCH"
  RUNTIME_OK=1
else
  echo "Warning: Overdrive skills installed, but no prebuilt runtime exists for ${TARGET_OS:-unknown}-${TARGET_ARCH:-unknown}." >&2
  echo "Warning: Install ships only platform-specific binaries; Go source is not copied or built by default." >&2
  if [ -z "$TARGET_OS" ] || [ -z "$TARGET_ARCH" ]; then
    echo "Warning: Unsupported platform for bundled runtime artifacts." >&2
  else
    artifact="$(runtime_artifact_name "$TARGET_OS" "$TARGET_ARCH")"
    echo "Warning: Expected $ROOT/runtime/bin/$artifact" >&2
  fi
fi

if install_tree_has_forbidden_payload "$DEST"; then
  echo "overdrive-runtime: install destination contains forbidden dev payload" >&2
  exit 1
fi

if [ "$RUNTIME_OK" = "1" ] && [ -x "$BIN_DIR/$INSTALLED_NAME" ] 2>/dev/null; then
  "$BIN_DIR/$INSTALLED_NAME" session-start --cwd "$PWD" --quiet >/dev/null 2>&1 || true
fi

printf 'Installed Overdrive skills to %s\n' "$DEST"
if [ "$RUNTIME_OK" = "1" ]; then
  printf 'Installed Overdrive runtime to %s (%s-%s only)\n' "$BIN_DIR/$INSTALLED_NAME" "$TARGET_OS" "$TARGET_ARCH"
else
  printf 'Overdrive runtime was not installed for this platform\n'
fi

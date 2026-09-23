#!/usr/bin/env bash
# Build a slim, platform-specific install tree (skills + one runtime + one TurboVec lib).
# Usage: ./scripts/package-platform.sh [linux-amd64|darwin-arm64|...]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=install-common.sh
source "$ROOT/scripts/install-common.sh"

REQUEST="${1:-}"
if [ -n "$REQUEST" ]; then
  TARGET_OS="${REQUEST%-*}"
  TARGET_ARCH="${REQUEST#*-}"
else
  detect_platform
fi

if [ -z "$TARGET_OS" ] || [ -z "$TARGET_ARCH" ]; then
  echo "Unsupported platform" >&2
  exit 1
fi

OUT="$ROOT/dist/install-${TARGET_OS}-${TARGET_ARCH}"
rm -rf "$OUT"
mkdir -p "$OUT"

install_skills_tree "$ROOT" "$OUT/skills"
mkdir -p "$OUT/scripts"
cp "$ROOT/scripts/install.sh" "$OUT/scripts/install.sh"
cp "$ROOT/scripts/install-common.sh" "$OUT/scripts/install-common.sh"
cp "$ROOT/scripts/install.ps1" "$OUT/scripts/install.ps1" 2>/dev/null || true
cp "$ROOT/README.md" "$OUT/README.md"
cp "$ROOT/LICENSE" "$OUT/LICENSE"
cp -R "$ROOT/.cursor-plugin" "$OUT/.cursor-plugin" 2>/dev/null || true
cp -R "$ROOT/.claude-plugin" "$OUT/.claude-plugin" 2>/dev/null || true
cp -R "$ROOT/.codex-plugin" "$OUT/.codex-plugin" 2>/dev/null || true
cp -R "$ROOT/commands" "$OUT/commands" 2>/dev/null || true
cp -R "$ROOT/hooks" "$OUT/hooks" 2>/dev/null || true

mkdir -p "$OUT/runtime/bin" "$OUT/runtime/lib/${TARGET_OS}-${TARGET_ARCH}"
artifact="$(runtime_artifact_name "$TARGET_OS" "$TARGET_ARCH")"
lib_name="$(turbovec_lib_name "$TARGET_OS")"
cp "$ROOT/runtime/bin/$artifact" "$OUT/runtime/bin/$artifact"
cp "$ROOT/runtime/lib/${TARGET_OS}-${TARGET_ARCH}/$lib_name" "$OUT/runtime/lib/${TARGET_OS}-${TARGET_ARCH}/$lib_name"

if install_tree_has_forbidden_payload "$OUT"; then
  echo "package contains forbidden dev payload" >&2
  exit 1
fi

echo "Packaged slim install tree at $OUT"

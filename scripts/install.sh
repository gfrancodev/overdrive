#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="${OVERDRIVE_SKILLS_DIR:-$HOME/.agents/skills}"
OVERDRIVE_HOME="${OVERDRIVE_HOME:-$HOME/.overdrive}"
BIN_DIR="${OVERDRIVE_BIN_DIR:-$OVERDRIVE_HOME/bin}"
LIB_DIR="${OVERDRIVE_LIB_DIR:-$OVERDRIVE_HOME/lib}"

mkdir -p "$DEST" "$BIN_DIR" "$LIB_DIR" "$OVERDRIVE_HOME"

for skill in "$ROOT"/skills/*; do
  [ -d "$skill" ] || continue
  name="$(basename "$skill")"
  rm -rf "$DEST/$name"
  cp -R "$skill" "$DEST/$name"
done

os_name="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch_name="$(uname -m | tr '[:upper:]' '[:lower:]')"
case "$os_name" in
  linux*) target_os="linux" ;;
  darwin*) target_os="darwin" ;;
  mingw*|msys*|cygwin*) target_os="windows" ;;
  *) target_os="" ;;
esac
case "$arch_name" in
  x86_64|amd64) target_arch="amd64" ;;
  aarch64|arm64) target_arch="arm64" ;;
  *) target_arch="" ;;
esac

turbovec_lib_name() {
  case "$1" in
    windows) echo "overdrive_turbovec_ffi.dll" ;;
    darwin) echo "liboverdrive_turbovec_ffi.dylib" ;;
    *) echo "liboverdrive_turbovec_ffi.so" ;;
  esac
}

runtime_name=""
installed_name="overdrive-runtime"
if [ "$target_os" = "windows" ]; then
  runtime_name="overdrive-runtime-${target_os}-${target_arch}.exe"
  installed_name="overdrive-runtime.exe"
elif [ -n "$target_os" ] && [ -n "$target_arch" ]; then
  runtime_name="overdrive-runtime-${target_os}-${target_arch}"
fi

if [ -n "$runtime_name" ] && [ -f "$ROOT/runtime/bin/$runtime_name" ]; then
  cp "$ROOT/runtime/bin/$runtime_name" "$BIN_DIR/$installed_name"
  chmod +x "$BIN_DIR/$installed_name" 2>/dev/null || true

  lib_name="$(turbovec_lib_name "$target_os")"
  bundled_lib="$ROOT/runtime/lib/${target_os}-${target_arch}/$lib_name"
  if [ -f "$bundled_lib" ]; then
    cp "$bundled_lib" "$LIB_DIR/$lib_name"
    cp "$bundled_lib" "$BIN_DIR/$lib_name"
    chmod +x "$LIB_DIR/$lib_name" 2>/dev/null || true
    chmod +x "$BIN_DIR/$lib_name" 2>/dev/null || true
  fi
elif [ "$target_os" != "windows" ] && command -v go >/dev/null 2>&1; then
  echo "Prebuilt runtime not found; building locally with Go (contributor fallback)..." >&2
  (cd "$ROOT/runtime/experience" && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$BIN_DIR/$installed_name" .)
  chmod +x "$BIN_DIR/$installed_name"
  if command -v cargo >/dev/null 2>&1 && [ -f "$ROOT/runtime/turbovec-ffi/Cargo.toml" ]; then
    (cd "$ROOT/runtime/turbovec-ffi" && cargo build --release)
    lib_name="$(turbovec_lib_name "$target_os")"
    dev_lib="$ROOT/runtime/turbovec-ffi/target/release/$lib_name"
    if [ -f "$dev_lib" ]; then
      cp "$dev_lib" "$LIB_DIR/$lib_name"
      cp "$dev_lib" "$BIN_DIR/$lib_name"
    fi
  fi
else
  echo "Warning: Overdrive skills installed, but no compatible Experience Engine runtime was available." >&2
fi

if [ -x "$BIN_DIR/$installed_name" ] 2>/dev/null; then
  "$BIN_DIR/$installed_name" session-start --cwd "$PWD" --quiet >/dev/null 2>&1 || true
fi

printf 'Installed Overdrive skills to %s\n' "$DEST"
printf 'Installed Overdrive runtime to %s\n' "$BIN_DIR/$installed_name"

#!/usr/bin/env bash
# Shared helpers for install.sh and package-platform.sh.
# Installs only skills plus the runtime binary/library for the current OS/arch.

detect_platform() {
  local os_name arch_name
  os_name="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch_name="$(uname -m | tr '[:upper:]' '[:lower:]')"
  case "$os_name" in
    linux*) TARGET_OS="linux" ;;
    darwin*) TARGET_OS="darwin" ;;
    mingw*|msys*|cygwin*) TARGET_OS="windows" ;;
    *) TARGET_OS="" ;;
  esac
  case "$arch_name" in
    x86_64|amd64) TARGET_ARCH="amd64" ;;
    aarch64|arm64) TARGET_ARCH="arm64" ;;
    *) TARGET_ARCH="" ;;
  esac
}

turbovec_lib_name() {
  case "$1" in
    windows) echo "overdrive_turbovec_ffi.dll" ;;
    darwin) echo "liboverdrive_turbovec_ffi.dylib" ;;
    *) echo "liboverdrive_turbovec_ffi.so" ;;
  esac
}

runtime_artifact_name() {
  local os="$1" arch="$2"
  if [ "$os" = "windows" ]; then
    echo "overdrive-runtime-${os}-${arch}.exe"
  elif [ -n "$os" ] && [ -n "$arch" ]; then
    echo "overdrive-runtime-${os}-${arch}"
  else
    echo ""
  fi
}

runtime_installed_name() {
  if [ "$1" = "windows" ]; then
    echo "overdrive-runtime.exe"
  else
    echo "overdrive-runtime"
  fi
}

install_skills_tree() {
  local root="$1" dest="$2"
  mkdir -p "$dest"
  for skill in "$root"/skills/*; do
    [ -d "$skill" ] || continue
    local name
    name="$(basename "$skill")"
    rm -rf "$dest/$name"
    cp -R "$skill" "$dest/$name"
  done
}

install_runtime_for_platform() {
  local root="$1" bin_dir="$2" lib_dir="$3" os="$4" arch="$5"
  local artifact installed lib_name bundled_lib
  artifact="$(runtime_artifact_name "$os" "$arch")"
  installed="$(runtime_installed_name "$os")"
  if [ -z "$artifact" ] || [ ! -f "$root/runtime/bin/$artifact" ]; then
    return 1
  fi
  mkdir -p "$bin_dir" "$lib_dir"
  cp "$root/runtime/bin/$artifact" "$bin_dir/$installed"
  chmod +x "$bin_dir/$installed" 2>/dev/null || true

  lib_name="$(turbovec_lib_name "$os")"
  bundled_lib="$root/runtime/lib/${os}-${arch}/$lib_name"
  if [ -f "$bundled_lib" ]; then
    cp "$bundled_lib" "$lib_dir/$lib_name"
    cp "$bundled_lib" "$bin_dir/$lib_name"
    chmod +x "$lib_dir/$lib_name" 2>/dev/null || true
    chmod +x "$bin_dir/$lib_name" 2>/dev/null || true
  fi
  return 0
}

prune_foreign_runtime_artifacts() {
  local bin_dir="$1" lib_dir="$2" os="$3" arch="$4"
  local keep_lib keep_installed
  keep_lib="$(turbovec_lib_name "$os")"
  keep_installed="$(runtime_installed_name "$os")"

  if [ -d "$bin_dir" ]; then
    for f in "$bin_dir"/overdrive-runtime-*; do
      [ -e "$f" ] || continue
      rm -f "$f"
    done
    for f in "$bin_dir"/*; do
      [ -e "$f" ] || continue
      base="$(basename "$f")"
      case "$base" in
        "$keep_installed"|"$keep_lib") ;;
        overdrive-runtime*|liboverdrive_turbovec_ffi.*|overdrive_turbovec_ffi.dll)
          rm -f "$f"
          ;;
      esac
    done
  fi

  if [ -d "$lib_dir" ]; then
    for f in "$lib_dir"/*; do
      [ -e "$f" ] || continue
      base="$(basename "$f")"
      if [ "$base" != "$keep_lib" ]; then
        rm -f "$f"
      fi
    done
  fi
}

install_tree_has_forbidden_payload() {
  local dir="$1"
  if [ ! -d "$dir" ]; then
    return 1
  fi
  if find "$dir" -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' -o -name 'Cargo.toml' \) -print -quit | grep -q .; then
    return 0
  fi
  if [ -d "$dir/runtime/experience" ] || [ -d "$dir/runtime/turbovec-ffi" ]; then
    return 0
  fi
  if [ -d "$dir/examples" ] || [ -d "$dir/tests" ]; then
    return 0
  fi
  return 1
}

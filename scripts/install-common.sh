#!/usr/bin/env bash
# Shared helpers for install.sh and package-platform.sh.
# Installs only skills plus the runtime binary/library for the current OS/arch.

# install_file_atomic swaps dest for a copy of src through a temporary file and a rename. A running Core keeps
# its mapped binary and TurboVec library; copying over them in place truncates the file under it (SIGBUS).
install_file_atomic() {
  local src="$1" dest="$2" tmp
  tmp="$(mktemp "${dest}.tmp.XXXXXX")" || return 1
  if ! cp "$src" "$tmp"; then
    rm -f "$tmp"
    return 1
  fi
  chmod +x "$tmp" 2>/dev/null || true
  mv -f "$tmp" "$dest"
}

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
  if [[ "$os" = "windows" ]]; then
    echo "overdrive-runtime-${os}-${arch}.exe"
  elif [[ -n "$os" && -n "$arch" ]]; then
    echo "overdrive-runtime-${os}-${arch}"
  else
    echo ""
  fi
}

runtime_installed_name() {
  if [[ "$1" = "windows" ]]; then
    echo "overdrive-runtime.exe"
  else
    echo "overdrive-runtime"
  fi
}

install_skills_tree() {
  local root="$1" dest="$2"
  mkdir -p "$dest"
  for skill in "$root"/skills/*; do
    [[ -d "$skill" ]] || continue
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
  if [[ -z "$artifact" || ! -f "$root/runtime/bin/$artifact" ]]; then
    return 1
  fi
  mkdir -p "$bin_dir" "$lib_dir"
  install_file_atomic "$root/runtime/bin/$artifact" "$bin_dir/$installed" || return 1

  lib_name="$(turbovec_lib_name "$os")"
  bundled_lib="$root/runtime/lib/${os}-${arch}/$lib_name"
  if [[ -f "$bundled_lib" ]]; then
    install_file_atomic "$bundled_lib" "$lib_dir/$lib_name"
    install_file_atomic "$bundled_lib" "$bin_dir/$lib_name"
  fi
  return 0
}

prune_foreign_runtime_artifacts() {
  local bin_dir="$1" lib_dir="$2" os="$3" arch="$4"
  local keep_lib keep_installed
  keep_lib="$(turbovec_lib_name "$os")"
  keep_installed="$(runtime_installed_name "$os")"

  if [[ -d "$bin_dir" ]]; then
    for f in "$bin_dir"/overdrive-runtime-*; do
      [[ -e "$f" ]] || continue
      rm -f "$f"
    done
    for f in "$bin_dir"/*; do
      [[ -e "$f" ]] || continue
      base="$(basename "$f")"
      case "$base" in
        "$keep_installed"|"$keep_lib") ;;
        overdrive-runtime*|liboverdrive_turbovec_ffi.*|overdrive_turbovec_ffi.dll)
          rm -f "$f"
          ;;
        *) ;;
      esac
    done
  fi

  if [[ -d "$lib_dir" ]]; then
    for f in "$lib_dir"/*; do
      [[ -e "$f" ]] || continue
      base="$(basename "$f")"
      if [[ "$base" != "$keep_lib" ]]; then
        rm -f "$f"
      fi
    done
  fi
}

install_tree_has_forbidden_payload() {
  local dir="$1"
  if [[ ! -d "$dir" ]]; then
    return 1
  fi
  if find "$dir" -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' -o -name 'Cargo.toml' \) -print -quit | grep -q .; then
    return 0
  fi
  if [[ -d "$dir/runtime/experience" || -d "$dir/runtime/turbovec-ffi" ]]; then
    return 0
  fi
  if [[ -d "$dir/examples" || -d "$dir/tests" ]]; then
    return 0
  fi
  return 1
}

tray_artifact_name() {
  local os="$1" arch="$2"
  if [[ "$os" = "windows" ]]; then
    echo "overdrive-tray-${os}-${arch}.exe"
  elif [[ -n "$os" && -n "$arch" ]]; then
    echo "overdrive-tray-${os}-${arch}"
  else
    echo ""
  fi
}

tray_installed_name() {
  if [[ "$1" = "windows" ]]; then
    echo "overdrive-tray.exe"
  else
    echo "overdrive-tray"
  fi
}

# Installs the per-user tray binary. Does not start the Core.
install_tray_for_platform() {
  local root="$1" bin_dir="$2" os="$3" arch="$4"
  local artifact installed src cgo
  artifact="$(tray_artifact_name "$os" "$arch")"
  installed="$(tray_installed_name "$os")"
  if [[ -z "$installed" ]]; then
    return 1
  fi
  mkdir -p "$bin_dir"
  if [[ -n "$artifact" && -f "$root/runtime/bin/$artifact" ]]; then
    install_file_atomic "$root/runtime/bin/$artifact" "$bin_dir/$installed" || return 1
    return 0
  fi
  src="$root/runtime/tray"
  if [[ -f "$src/go.mod" ]] && command -v go >/dev/null 2>&1; then
    cgo=0
    if [[ "$os" = "darwin" ]]; then
      cgo=1
    fi
    if (
      cd "$src"
      CGO_ENABLED="$cgo" GOOS="$os" GOARCH="$arch" \
        go build -buildvcs=false -trimpath -ldflags='-s -w' -o "$bin_dir/$installed.new" ./cmd/overdrive-tray
    ) && mv -f "$bin_dir/$installed.new" "$bin_dir/$installed"; then
      chmod +x "$bin_dir/$installed" 2>/dev/null || true
      return 0
    fi
  fi
  echo "Warning: Overdrive tray was not installed. The Core still runs without a tray." >&2
  return 1
}

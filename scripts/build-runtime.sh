#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$ROOT/runtime/experience"
FFI="$ROOT/runtime/turbovec-ffi"
OUT="$ROOT/runtime/bin"
LIB_OUT="$ROOT/runtime/lib"
mkdir -p "$OUT" "$LIB_OUT"

if ! command -v go >/dev/null 2>&1; then
  echo "Go is required to build Overdrive runtime binaries." >&2
  exit 1
fi

turbovec_lib_name() {
  case "$1" in
    windows) echo "overdrive_turbovec_ffi.dll" ;;
    darwin) echo "liboverdrive_turbovec_ffi.dylib" ;;
    *) echo "liboverdrive_turbovec_ffi.so" ;;
  esac
}

rust_target() {
  case "$1-$2" in
    linux-amd64) echo "x86_64-unknown-linux-gnu" ;;
    linux-arm64) echo "aarch64-unknown-linux-gnu" ;;
    darwin-amd64) echo "x86_64-apple-darwin" ;;
    darwin-arm64) echo "aarch64-apple-darwin" ;;
    windows-amd64) echo "x86_64-pc-windows-gnu" ;;
    windows-arm64) echo "aarch64-pc-windows-gnullvm" ;;
    *) echo "" ;;
  esac
}

ensure_rust_targets() {
  if ! command -v cargo >/dev/null 2>&1; then
    return 0
  fi
  local targets=(
    x86_64-unknown-linux-gnu
    aarch64-unknown-linux-gnu
    x86_64-apple-darwin
    aarch64-apple-darwin
    x86_64-pc-windows-gnu
    aarch64-pc-windows-gnu
    aarch64-pc-windows-gnullvm
  )
  for t in "${targets[@]}"; do
    rustup target add "$t" >/dev/null 2>&1 || true
  done
}

build_turbovec() {
  local goos="$1" goarch="$2"
  local target lib_name lib_dir artifact
  target="$(rust_target "$goos" "$goarch")"
  lib_name="$(turbovec_lib_name "$goos")"
  lib_dir="$LIB_OUT/$goos-$goarch"
  mkdir -p "$lib_dir"

  if [ -z "$target" ]; then
    echo "Skipping TurboVec FFI: unknown target $goos/$goarch" >&2
    return 0
  fi
  if ! command -v cargo >/dev/null 2>&1; then
    echo "Warning: cargo not found; TurboVec FFI for $goos/$goarch will be missing." >&2
    return 0
  fi

  echo "Building TurboVec FFI for $goos/$goarch ($target)"
  (
    cd "$FFI"
    if command -v cargo-zigbuild >/dev/null 2>&1 && command -v zig >/dev/null 2>&1; then
      cargo zigbuild --release --target "$target"
    elif [ "$target" = "x86_64-unknown-linux-gnu" ]; then
      cargo build --release
    else
      cargo build --release --target "$target" || cargo zigbuild --release --target "$target"
    fi
  ) || {
    echo "Warning: TurboVec build failed for $goos/$goarch" >&2
    return 0
  }

  if [ "$target" = "x86_64-unknown-linux-gnu" ]; then
    artifact="$FFI/target/release/$lib_name"
  else
    artifact="$FFI/target/$target/release/$lib_name"
  fi
  if [ -f "$artifact" ]; then
    cp "$artifact" "$lib_dir/$lib_name"
  else
    echo "Warning: TurboVec artifact missing at $artifact" >&2
  fi
}

build_go() {
  local goos="$1" goarch="$2" name="$3"
  echo "Building Go runtime $name"
  (
    cd "$SRC"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -buildvcs=false -trimpath -ldflags='-s -w' -o "$OUT/$name" .
  )
}

ensure_rust_targets

targets=(
  "linux amd64 overdrive-runtime-linux-amd64"
  "linux arm64 overdrive-runtime-linux-arm64"
  "darwin amd64 overdrive-runtime-darwin-amd64"
  "darwin arm64 overdrive-runtime-darwin-arm64"
  "windows amd64 overdrive-runtime-windows-amd64.exe"
  "windows arm64 overdrive-runtime-windows-arm64.exe"
)

should_build() {
  local goos="$1" goarch="$2" name="$3"
  if [ "$#" -le 3 ] || [ "${4:-}" = "" ]; then
    return 0
  fi
  local key="$goos-$goarch"
  local req
  for req in "${@:4}"; do
    if [ "$req" = "$key" ] || [ "$req" = "$name" ]; then
      return 0
    fi
  done
  return 1
}

for spec in "${targets[@]}"; do
  read -r goos goarch name <<<"$spec"
  if ! should_build "$goos" "$goarch" "$name" "$@"; then
    continue
  fi
  build_turbovec "$goos" "$goarch"
  build_go "$goos" "$goarch" "$name"
done

echo "Runtime binaries written to $OUT"
echo "TurboVec libraries written to $LIB_OUT"

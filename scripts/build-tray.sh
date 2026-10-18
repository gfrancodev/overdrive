#!/usr/bin/env bash
# Build the tray only. This does not compile TurboVec or the experience module.
# Usage: ./scripts/build-tray.sh            # host tray (plus Windows trays on Linux)
#        ./scripts/build-tray.sh linux-arm64 # one target; macOS targets need a macOS host (CGO)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$ROOT/runtime/tray"
OUT="$ROOT/runtime/bin"
mkdir -p "$OUT"

if ! command -v go >/dev/null 2>&1; then
  echo "Go is required to build the Overdrive tray." >&2
  exit 1
fi

host_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
host_arch="$(uname -m | tr '[:upper:]' '[:lower:]')"
case "$host_os" in
  linux*) host_os="linux" ;;
  darwin*) host_os="darwin" ;;
  mingw*|msys*|cygwin*) host_os="windows" ;;
  *) echo "Unsupported OS $host_os" >&2; exit 1 ;;
esac
case "$host_arch" in
  x86_64|amd64) host_arch="amd64" ;;
  aarch64|arm64) host_arch="arm64" ;;
  *) echo "Unsupported arch $host_arch" >&2; exit 1 ;;
esac

build_one() {
  local goos="$1" goarch="$2" cgo="$3" name="$4"
  echo "Building tray $name"
  if ! (
    cd "$SRC"
    CGO_ENABLED="$cgo" GOOS="$goos" GOARCH="$goarch" \
      go build -buildvcs=false -trimpath -ldflags='-s -w' -o "$OUT/$name" ./cmd/overdrive-tray
  ); then
    echo "Warning: tray build failed for $goos/$goarch" >&2
    return 0
  fi
}

if [[ -n "${1:-}" ]]; then
  goos="${1%-*}"
  goarch="${1#*-}"
  name="overdrive-tray-${goos}-${goarch}"
  cgo=0
  if [[ "$goos" = "windows" ]]; then
    name="${name}.exe"
  fi
  if [[ "$goos" = "darwin" ]]; then
    cgo=1
  fi
  (
    cd "$SRC"
    CGO_ENABLED="$cgo" GOOS="$goos" GOARCH="$goarch" \
      go build -buildvcs=false -trimpath -ldflags='-s -w' -o "$OUT/$name" ./cmd/overdrive-tray
  )
  echo "Tray binary written to $OUT/$name"
  exit 0
fi

name="overdrive-tray-${host_os}-${host_arch}"
if [[ "$host_os" = "windows" ]]; then
  name="${name}.exe"
fi
cgo=0
if [[ "$host_os" = "darwin" ]]; then
  cgo=1
fi
build_one "$host_os" "$host_arch" "$cgo" "$name"

if [[ "$host_os" = "linux" ]]; then
  build_one windows amd64 0 overdrive-tray-windows-amd64.exe
  build_one windows arm64 0 overdrive-tray-windows-arm64.exe
fi

echo "Tray binaries written to $OUT"

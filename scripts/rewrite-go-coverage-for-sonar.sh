#!/usr/bin/env bash
set -euo pipefail

profile="${1:-runtime/experience/coverage.out}"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

awk '
  NR == 1 { print; next }
  {
    colon = index($0, ":")
    if (colon == 0) { print; next }
    path = substr($0, 1, colon - 1)
    rest = substr($0, colon)
    module = "github.com/gfrancodev/overdrive/"
    if (index(path, module) == 1) {
      path = substr(path, length(module) + 1)
    } else if (path !~ /^\// && path !~ /^runtime\/experience\//) {
      path = "runtime/experience/" path
    }
    print path rest
  }
' "$profile" > "$tmp"
mv "$tmp" "$profile"
trap - EXIT

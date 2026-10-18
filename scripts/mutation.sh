#!/usr/bin/env bash
# Mutation gate for the Go runtime: mutates the lines changed since the merge base with a ref (origin/main by
# default) and runs only the tests that execute each one. Options: see scripts/mutation.py --help.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
exec python3 "$ROOT/scripts/mutation.py" "$@"

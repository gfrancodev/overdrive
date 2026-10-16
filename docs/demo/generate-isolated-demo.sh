#!/usr/bin/env bash
# Maintainer script: build runtime, record cast, render GIF for README.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT}/docs/demo"
RUNTIME="${ROOT}/runtime/experience/.demo-overdrive-runtime"
AGG="${AGG:-/tmp/agg}"

cd "${ROOT}/runtime/experience"
OVERDRIVE_EMBEDDER=stub go build -o "${RUNTIME}" .

rm -rf "${DEMO_DIR}/.isolated-home" "${DEMO_DIR}/sample-repo"
mkdir -p "${DEMO_DIR}/sample-repo"
git -C "${DEMO_DIR}/sample-repo" init -q
git -C "${DEMO_DIR}/sample-repo" remote add origin https://github.com/acme/demo-shop.git
printf '# demo-shop\n' > "${DEMO_DIR}/sample-repo/README.md"

export TERM=xterm-256color
asciinema rec \
  --overwrite \
  --idle-time-limit 1 \
  --cols 100 \
  --rows 28 \
  --title "Overdrive isolated usage" \
  -c "bash ${DEMO_DIR}/record-isolated-demo.sh" \
  "${DEMO_DIR}/isolated-usage.cast"

if [[ ! -x "${AGG}" ]]; then
  echo "agg not found at ${AGG}; download from https://github.com/asciinema/agg/releases" >&2
  exit 1
fi

"${AGG}" \
  --font-size 14 \
  --theme asciinema \
  "${DEMO_DIR}/isolated-usage.cast" \
  "${DEMO_DIR}/isolated-usage.gif"

echo "Wrote ${DEMO_DIR}/isolated-usage.cast and ${DEMO_DIR}/isolated-usage.gif"

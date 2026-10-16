#!/usr/bin/env bash
# Maintainer: record mesh dashboard demo GIF (Playwright + ffmpeg).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"
python3 docs/demo/capture_mesh_ui_demo.py

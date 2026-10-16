#!/usr/bin/env bash
# CI gate: >=82% statement coverage excluding terminal_host.go (PTY supervisor; integration-tested separately).
set -euo pipefail
PROFILE="${1:-runtime/experience/coverage.out}"
python3 - "$PROFILE" <<'PY'
import sys

path = sys.argv[1]
total = covered = 0
with open(path, encoding="utf-8") as fh:
    for line in fh:
        if line.startswith("mode:"):
            continue
        file_part = line.split(":", 1)[0]
        if file_part.endswith("/terminal_host.go"):
            continue
        parts = line.split()
        if len(parts) < 3:
            continue
        statements = int(parts[-2])
        count = int(parts[-1])
        total += statements
        if count > 0:
            covered += statements
if total == 0:
    raise SystemExit("empty coverage profile")
pct = covered * 100.0 / total
print(f"Go coverage (excl. terminal_host.go): {pct:.1f}%")
if pct < 82.0:
    raise SystemExit(f"Go coverage {pct:.1f}% is below 82%")
PY

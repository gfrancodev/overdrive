#!/usr/bin/env bash
# Produce Sonar-compatible lcov for dashboard ESM modules (excludes vendor/).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/runtime/experience/dashboard-js-coverage.lcov"
TMP="${TMPDIR:-/tmp}/overdrive-dashboard-v8-coverage"
rm -rf "$TMP"
mkdir -p "$TMP"
export NODE_V8_COVERAGE="$TMP"
cd "$ROOT"
node --experimental-test-coverage \
  --test-coverage-include='runtime/experience/dashboard/**/*.mjs' \
  --test-coverage-exclude='**/vendor/**' \
  --test tests/mesh_call_signaling.test.mjs tests/mesh_chat_helpers.test.mjs tests/dashboard_sonar_coverage.test.mjs >/dev/null
if command -v npx >/dev/null 2>&1; then
  npx --yes c8@10.1.2 report "$TMP" --reporter=lcovonly --reports-dir="$(dirname "$OUT")" --temp-directory="$TMP" 2>/dev/null || true
fi
if [[ ! -f "$(dirname "$OUT")/lcov.info" ]]; then
  python3 - "$TMP" "$OUT" <<'PY'
import json, os, sys, glob
tmpdir, out = sys.argv[1], sys.argv[2]
entries = []
for path in glob.glob(os.path.join(tmpdir, "coverage-*.json")):
    with open(path, encoding="utf-8") as fh:
        data = json.load(fh)
    for entry in data.get("result", []):
        script = entry.get("url", "").replace("file://", "")
        if "/dashboard/" not in script or "/vendor/" in script:
            continue
        entries.append((script, entry))
if not entries:
    open(out, "w", encoding="utf-8").write("TN:\n")
    sys.exit(0)
lines_out = ["TN:"]
for script, entry in entries:
    lines_out.append(f"SF:{script}")
    by_line = {}
    for fn in entry.get("functions", []):
        for r in fn.get("ranges", []):
            start, end = r.get("startOffset"), r.get("endOffset")
            if start is None or end is None:
                continue
            for off in range(start, end + 1):
                by_line.setdefault(off, 0)
    for off, count in sorted(by_line.items()):
        lines_out.append(f"DA:{off},1")
    lines_out.append("end_of_record")
open(out, "w", encoding="utf-8").write("\n".join(lines_out) + "\n")
PY
else
  mv "$(dirname "$OUT")/lcov.info" "$OUT"
fi
echo "Wrote $OUT"

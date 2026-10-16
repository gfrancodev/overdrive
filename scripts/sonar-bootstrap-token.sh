#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
SQ_URL="${SONAR_URL:-http://localhost:9000}"
AUTH="${SONAR_ADMIN_USER:-admin}:${SONAR_ADMIN_PASS:-admin}"
for _ in $(seq 1 90); do
  curl -sf "$SQ_URL/api/system/status" | grep -q '"status":"UP"' && break
  sleep 5
done
curl -sf -u "$AUTH" -X POST "$SQ_URL/api/projects/create" -d "project=overdrive-local&name=overdrive-local" || true
token="$(curl -sf -u "$AUTH" -X POST "$SQ_URL/api/user_tokens/generate" -d "name=scan-$(date +%s)" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")"
printf 'SONAR_TOKEN=%s\n' "$token" > .env.sonar
echo "Wrote .env.sonar"

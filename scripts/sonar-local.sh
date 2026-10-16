#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
[[ -f .env.sonar ]] && set -a && source .env.sonar && set +a
: "${SONAR_TOKEN:?set SONAR_TOKEN in .env.sonar}"
docker compose -f docker-compose.sonar.yml up -d
for _ in $(seq 1 90); do
  curl -sf http://localhost:9000/api/system/status | grep -q '"status":"UP"' && break
  sleep 5
done
( cd runtime/experience && OVERDRIVE_EMBEDDER=stub OVERDRIVE_SKIP_EMBED_DOWNLOAD=1 \
  go test -tags=overdrive_fake_ort -covermode=atomic -coverprofile=coverage.out ./... )
"$ROOT/scripts/generate-dashboard-js-lcov.sh"
"$ROOT/scripts/rewrite-go-coverage-for-sonar.sh" runtime/experience/coverage.out
docker run --rm --network=overdrive_sonar_default -v "$ROOT:/usr/src" -w /usr/src -e SONAR_TOKEN \
  sonarsource/sonar-scanner-cli:11.1 -Dproject.settings=sonar-project.local.properties
echo "http://localhost:9000/dashboard?id=overdrive-local"

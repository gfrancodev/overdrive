#!/usr/bin/env bash
set -euo pipefail

LOCAL_IP="${LOCAL_IP:-192.168.1.193}"
REMOTE_HOST="${REMOTE_HOST:-franco@192.168.1.155}"
SHARE_PORT="${SHARE_PORT:-7741}"
BIN_LOCAL="${BIN_LOCAL:-/tmp/overdrive-runtime}"
BIN_REMOTE="${BIN_REMOTE:-/home/franco/overdrive-runtime}"
REMOTE_URL="${REMOTE_URL:-https://github.com/gfrancodev/overdrive-p2p-lan-test.git}"

export OVERDRIVE_EMBEDDER=stub
export OVERDRIVE_SKIP_EMBED_DOWNLOAD=1

HOME_A="/tmp/overdrive-p2p-a"
HOME_B="/tmp/overdrive-p2p-b"
REPO_A="/tmp/overdrive-p2p-repo-a"
REPO_B="/home/franco/overdrive-p2p-repo-b"
LISTEN_ADDR="0.0.0.0:${SHARE_PORT}"
PEER_ENDPOINT="${LOCAL_IP}:${SHARE_PORT}"

run_a() { OVERDRIVE_HOME="$HOME_A" OVERDRIVE_SHARE_LISTEN="$LISTEN_ADDR" "$BIN_LOCAL" "$@"; }
run_b() { ssh "$REMOTE_HOST" "OVERDRIVE_HOME='$HOME_B' OVERDRIVE_EMBEDDER=stub OVERDRIVE_SKIP_EMBED_DOWNLOAD=1 '$BIN_REMOTE'" "$@"; }

echo "==> preparando repos"
rm -rf "$HOME_A" "$REPO_A"
mkdir -p "$REPO_A"
git -C "$REPO_A" init -q
git -C "$REPO_A" remote add origin "$REMOTE_URL"
echo "# p2p test" > "$REPO_A/README.md"

ssh "$REMOTE_HOST" "rm -rf '$HOME_B' '$REPO_B' && mkdir -p '$REPO_B' && git -C '$REPO_B' init -q && git -C '$REPO_B' remote add origin '$REMOTE_URL' && echo '# p2p test' > '$REPO_B/README.md'"

echo "==> host A: circle + invite + folder"
OUT=$(run_a share circle create --name "lan-p2p")
CIRCLE_ID=$(echo "$OUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
INVITE=$(run_a share circle invite --circle "$CIRCLE_ID")
CODE=$(echo "$INVITE" | python3 -c "import sys,json; print(json.load(sys.stdin)['code'])")
FP=$(echo "$INVITE" | python3 -c "import sys,json; print(json.load(sys.stdin)['fingerprint'])")
run_a share circle folder-add --circle "$CIRCLE_ID" --folder "$REPO_A" >/dev/null

echo "==> copiando invite para host B"
ssh "$REMOTE_HOST" "mkdir -p '$HOME_B/share/invites'"
scp -q "$HOME_A/share/invites/${CODE}.json" "$REMOTE_HOST:$HOME_B/share/invites/${CODE}.json"

echo "==> host A: listener em $LISTEN_ADDR"
pkill -f "overdrive-runtime share listen" 2>/dev/null || true
sleep 0.5
nohup env OVERDRIVE_HOME="$HOME_A" OVERDRIVE_SHARE_LISTEN="$LISTEN_ADDR" OVERDRIVE_EMBEDDER=stub OVERDRIVE_SKIP_EMBED_DOWNLOAD=1 \
  "$BIN_LOCAL" share listen --cwd "$REPO_A" > /tmp/overdrive-listen-a.log 2>&1 &
LISTEN_PID=$!
sleep 1
if ! ss -ltn | grep -q ":${SHARE_PORT} "; then
  echo "listener não subiu; log:"
  cat /tmp/overdrive-listen-a.log
  kill "$LISTEN_PID" 2>/dev/null || true
  exit 1
fi

echo "==> host B: accept + folder"
run_b share circle accept --code "$CODE" --fingerprint "$FP" --peer "$PEER_ENDPOINT" >/dev/null
run_b share circle folder-add --circle "$CIRCLE_ID" --folder "$REPO_B" >/dev/null

echo "==> host A: grava lição e inicia sessão"
run_a record --cwd "$REPO_A" --kind lesson --scope repository \
  --content "P2P LAN test: always validate JWT before trusting peer sync payloads" \
  --confidence 0.85 --priority 70 --source project_instruction >/dev/null
run_a session-start --cwd "$REPO_A" >/dev/null

echo "==> host B: session-start (pull sync)"
SYNC_OUT=$(run_b session-start --cwd "$REPO_B")
echo "$SYNC_OUT" | python3 -c "import sys,json; s=json.load(sys.stdin).get('share',{}); print('B share mode:', s.get('mode'), 'peer_memories:', s.get('peer_memory_count'), 'endpoints:', s.get('peer_endpoints'))"

echo "==> host B: recall"
RECALL=$(run_b recall --cwd "$REPO_B" --query "JWT peer sync" --limit 5)
PEER_COUNT=$(echo "$RECALL" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('peer_memories',[])))")
echo "peer_memories no recall: $PEER_COUNT"

kill "$LISTEN_PID" 2>/dev/null || true
if [[ "$PEER_COUNT" -ge 1 ]]; then
  echo "==> P2P LAN OK"
  exit 0
fi
echo "==> P2P LAN FALHOU (0 peer memories)"
exit 1

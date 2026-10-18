#!/usr/bin/env bash
# Runs the part of docs/CROSS_MACHINE_RUNBOOK.md that needs no person: three Linux hosts in Docker, each with its
# own address on one network, install a release bundle, start the Core as the tray does, and pair, sync, chat,
# restart, remove and diagnose between them through the dashboard's local API.
#
#   scripts/cross-host/run.sh v2.3.0-rc.2                        # the bundle published with that tag
#   scripts/cross-host/run.sh v2.3.0-rc.2 path/to/overdrive-runtime  # that bundle, this runtime build
#
# KEEP=1 leaves the hosts and their network up. Needs Docker, and internet access to install the bundle.
set -euo pipefail

tag="${1:?usage: run.sh <release tag> [runtime binary]}"
runtime="${2:-}"
here="$(cd "$(dirname "$0")" && pwd)"
net="od-cross-host"
hosts=(od-host-a:studio od-host-b:laptop od-host-c:oldmac)
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "unsupported machine $(uname -m)" >&2; exit 1 ;;
esac

cleanup() {
  [[ "${KEEP:-}" == 1 ]] && return
  for h in "${hosts[@]}"; do docker rm -f "${h%%:*}" >/dev/null 2>&1 || true; done
  docker network rm "$net" >/dev/null 2>&1 || true
}
trap cleanup EXIT

setup_host() {
  local name="$1" hostname="$2"
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker run -d --name "$name" --hostname "$hostname" --network "$net" debian:bookworm-slim sleep infinity >/dev/null
  docker exec "$name" bash -c 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl unzip ca-certificates git procps >/dev/null 2>&1 && useradd -m dev'
  docker exec "$name" su - dev -c "curl -fsLO https://github.com/gfrancodev/overdrive/releases/download/$tag/overdrive-linux-$arch.zip \
    && unzip -q overdrive-linux-$arch.zip && ./install-linux-$arch/scripts/install.sh >/dev/null"
  if [[ -n "$runtime" ]]; then
    docker cp "$runtime" "$name":/home/dev/.overdrive/bin/overdrive-runtime
    docker exec "$name" chown dev:dev /home/dev/.overdrive/bin/overdrive-runtime
  fi
  # The same project on every host, the same remote; the Core as the tray starts it, on loopback.
  docker exec "$name" su - dev -c 'git init -q ~/proj && git -C ~/proj remote add origin https://github.com/acme/cross-host.git \
    && cd ~ && OVERDRIVE_SHARE_LISTEN=127.0.0.1:7741 OVERDRIVE_SHARE_UI=127.0.0.1:7742 \
       nohup ~/.overdrive/bin/overdrive-runtime share listen --cwd ~ > ~/core.log 2>&1 &'
}

docker network inspect "$net" >/dev/null 2>&1 || docker network create "$net" >/dev/null
for h in "${hosts[@]}"; do setup_host "${h%%:*}" "${h##*:}" & done
wait
for h in "${hosts[@]}"; do
  printf '%s: %s\n' "${h##*:}" "$(docker exec "${h%%:*}" su - dev -c '~/.overdrive/bin/overdrive-runtime version')"
done
python3 "$here/check.py" od-host-a od-host-b od-host-c

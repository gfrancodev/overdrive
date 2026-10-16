const NEARBY_FORGOTTEN_STORAGE_KEY = "overdriveNearbyForgotten";
const CIRCLE_HIDDEN_STORAGE_KEY = "overdriveCircleHiddenMembers";

function peerLastSeenMs(peer) {
  const t = Date.parse(peer?.last_seen ? peer.last_seen : "");
  return Number.isFinite(t) ? t : 0;
}

/** Stable dedupe key for LAN discovery rows (device id preferred). */
function discoveryStableKey(peer) {
  const id = String((peer?.device_id) || "").trim();
  if (id) return "id:" + id;
  const name = String((peer && (peer.display_name || peer.friendly_name)) || "")
    .trim()
    .toLowerCase();
  const os = String((peer?.os) || "")
    .trim()
    .toLowerCase();
  const host = String((peer?.host) || "").trim().toLowerCase();
  return "anon:" + name + "|" + os + "|" + host;
}

function dedupeDiscoveredPeers(peers) {
  const map = new Map();
  (peers || []).forEach((peer) => {
    const key = discoveryStableKey(peer);
    const prev = map.get(key);
    if (!prev || peerLastSeenMs(prev) < peerLastSeenMs(peer)) map.set(key, peer);
  });
  return [...map.values()];
}

function nearbyForgottenKeys(peer) {
  const keys = [discoveryStableKey(peer)];
  const id = String((peer?.device_id) || "").trim();
  if (id) keys.push(id);
  return keys;
}

function isNearbyForgotten(peer, forgotten) {
  if (!forgotten?.size) return false;
  return nearbyForgottenKeys(peer).some((key) => forgotten.has(key));
}

function filterForgottenNearby(peers, forgotten) {
  return (peers || []).filter((peer) => !isNearbyForgotten(peer, forgotten));
}

function normalizeMachineLabel(label) {
  return String(label || "")
    .trim()
    .toLowerCase();
}

/** Offline, non-self circle rows may be hidden from the Machines sheet. */
function canForgetCircleMember(peer, isAlive) {
  if (!peer || peer.self || peer.revoked) return false;
  return !isAlive(peer);
}

function filterHiddenCircleMembers(members, hiddenDeviceIds) {
  if (!hiddenDeviceIds?.size) return members || [];
  return (members || []).filter((peer) => !hiddenDeviceIds.has(peer.device_id));
}

function createDismissStore(storageKey) {
  function readSet() {
    try {
      const raw = localStorage.getItem(storageKey);
      if (!raw) return new Set();
      const parsed = JSON.parse(raw);
      if (!Array.isArray(parsed)) return new Set();
      return new Set(parsed.filter((entry) => typeof entry === "string" && entry.trim()));
    } catch {
      return new Set();
    }
  }
  function writeSet(set) {
    try {
      localStorage.setItem(storageKey, JSON.stringify([...set]));
    } catch {
      /* quota / private mode */
    }
  }
  function add(key) {
    const trimmed = String(key || "").trim();
    if (!trimmed) return;
    const set = readSet();
    set.add(trimmed);
    writeSet(set);
  }
  function addAll(keys) {
    keys.forEach((key) => add(key));
  }
  return { read: readSet, add, addAll };
}

export {
  CIRCLE_HIDDEN_STORAGE_KEY,
  NEARBY_FORGOTTEN_STORAGE_KEY,
  canForgetCircleMember,
  createDismissStore,
  dedupeDiscoveredPeers,
  discoveryStableKey,
  filterForgottenNearby,
  filterHiddenCircleMembers,
  isNearbyForgotten,
  nearbyForgottenKeys,
  normalizeMachineLabel,
  peerLastSeenMs
};

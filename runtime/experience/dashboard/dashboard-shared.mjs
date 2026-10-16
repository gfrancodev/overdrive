/** Shared dashboard helpers (prepended to the mesh bundle before chat/call/terminal/app). */

function computerLabel(peer, data) {
  return peer.name || peer.hostname || (peer.self ? data.hostname : "") || "computer";
}

function alive(peer) {
  if (peer.revoked || !peer.endpoint) return false;
  if (!peer.last_seen) return true;
  const t = Date.parse(peer.last_seen);
  return Number.isFinite(t) && (Date.now() - t) < 15 * 60 * 1000;
}

function setInviteCopyStatus(status, copied, when) {
  if (!status) return;
  status.textContent = (copied ? "Copied. It expires " : "Could not copy. It expires ") + when + ". An expired connection is refused.";
}

function circleInviteErrorMessage(res, body) {
  return body.error || (res.status === 403
    ? "Open the dashboard on this computer (127.0.0.1) to create a connection."
    : "Could not create a connection.");
}

function roster(data) {
  const circle = (data.circles || [])[0];
  const members = (circle?.members.slice()) || [];
  if (!members.some((peer) => peer.self)) {
    members.unshift({
      device_id: data.device_id || "self",
      name: data.hostname || "this station",
      hostname: data.hostname || "",
      self: true,
      endpoint: data.listen || ""
    });
  }
  return members;
}

export {
  alive,
  circleInviteErrorMessage,
  computerLabel,
  roster,
  setInviteCopyStatus,
};

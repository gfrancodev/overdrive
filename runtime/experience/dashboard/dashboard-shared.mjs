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

function parseShareHexRgb(hex) {
  const raw = String(hex || "").trim();
  const m = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(raw);
  if (!m) return null;
  let h = m[1];
  if (h.length === 3) h = h.split("").map((c) => c + c).join("");
  return { r: parseInt(h.slice(0, 2), 16), g: parseInt(h.slice(2, 4), 16), b: parseInt(h.slice(4, 6), 16) };
}

function shareCssColor(cssVar, fallback) {
  try {
    const value = getComputedStyle(document.documentElement).getPropertyValue(cssVar).trim();
    return value || fallback;
  } catch {
    return fallback;
  }
}

function applyShareAccentDerivatives(accentHex) {
  const rgb = parseShareHexRgb(accentHex);
  if (!rgb) return;
  const { r, g, b } = rgb;
  const root = document.documentElement.style;
  root.setProperty("--accent", accentHex);
  root.setProperty("--line", `rgba(${r}, ${g}, ${b}, 0.22)`);
  root.setProperty("--focus-border", `rgba(${r}, ${g}, ${b}, 0.9)`);
  root.setProperty("--focus-shadow", `rgba(${r}, ${g}, ${b}, 0.3)`);
  root.setProperty("--accent-soft", `rgba(${r}, ${g}, ${b}, 0.2)`);
  root.setProperty("--accent-mid", `rgba(${r}, ${g}, ${b}, 0.38)`);
  root.setProperty("--accent-ring", `rgba(${r}, ${g}, ${b}, 0.55)`);
  root.setProperty("--accent-glow-18", `rgba(${r}, ${g}, ${b}, 0.18)`);
  root.setProperty("--accent-glow-60", `rgba(${r}, ${g}, ${b}, 0.6)`);
  root.setProperty("--term-insp-accent", accentHex);
}

function applyShareTheme(ui) {
  if (!ui || typeof ui !== "object") return;
  const preset = ui.preset === "enterprise" ? "enterprise" : "orbit";
  document.documentElement.dataset.preset = preset;
  const tokens = ui.tokens || {};
  document.documentElement.dataset.themed = tokens.cyan || tokens.live || ui.themeColor ? "1" : "";
  const barTitle = String(ui.title || "Overdrive").slice(0, 40);
  const title = document.getElementById("barTitle");
  if (title) title.textContent = barTitle;
  document.title = barTitle + " · Circle";
  const logo = ui.logo || "/logo.png";
  document.querySelectorAll(".menubar .mark, #circleApp .mark").forEach((el) => el.setAttribute("src", logo));
  const favicon = ui.favicon || logo;
  document.querySelectorAll('link[rel="icon"]').forEach((el) => el.setAttribute("href", favicon));
  const apple = ui.appleTouchIcon || logo;
  document.querySelectorAll('link[rel="apple-touch-icon"]').forEach((el) => el.setAttribute("href", apple));
  if (typeof ui.themeColor === "string" && /^#[0-9a-fA-F]{3,8}$/.test(ui.themeColor)) {
    let meta = document.querySelector('meta[name="theme-color"]');
    if (!meta) {
      meta = document.createElement("meta");
      meta.setAttribute("name", "theme-color");
      document.head.appendChild(meta);
    }
    meta.setAttribute("content", ui.themeColor);
  }
  const allowed = ["ink", "sea", "foam", "cyan", "live", "mute"];
  allowed.forEach((key) => {
    const value = tokens[key];
    if (typeof value === "string" && /^#[0-9a-fA-F]{3,8}$/.test(value)) {
      document.documentElement.style.setProperty("--" + key, value);
    }
  });
  const accent = tokens.cyan || shareCssColor("--cyan", "");
  if (accent) applyShareAccentDerivatives(accent);
  const live = tokens.live || shareCssColor("--live", "");
  const liveRgb = parseShareHexRgb(live);
  if (liveRgb) {
    document.documentElement.style.setProperty("--live-glow", `rgba(${liveRgb.r}, ${liveRgb.g}, ${liveRgb.b}, 0.7)`);
  }
  const ink = tokens.ink || shareCssColor("--ink", "");
  if (ink && document.body) document.body.style.background = ink;
}

function themeColorHex(cssVar, fallback) {
  const rgb = parseShareHexRgb(shareCssColor(cssVar, ""));
  if (!rgb) return fallback;
  return (rgb.r << 16) | (rgb.g << 8) | rgb.b;
}

// A shade between two colours, for what has no theme token of its own; weight is the share of the first.
function mixHex(a, b, weight) {
  const channel = (shift) => Math.round(((a >> shift) & 255) * weight + ((b >> shift) & 255) * (1 - weight));
  return (channel(16) << 16) | (channel(8) << 8) | channel(0);
}

applyShareTheme(globalThis.OVERDRIVE_WHITELABEL || globalThis.OVERDRIVE_UI || {});

export {
  alive,
  circleInviteErrorMessage,
  computerLabel,
  roster,
  setInviteCopyStatus,
};

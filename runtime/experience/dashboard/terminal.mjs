function shortTerminalPath(wd) {
  if (!wd) return "";
  const norm = String(wd).replaceAll("\\", "/");
  const circlesIdx = norm.indexOf("/circles/");
  if (circlesIdx >= 0) {
    const tail = norm.slice(circlesIdx + "/circles/".length);
    const parts = tail.split("/").filter(Boolean);
    if (parts.length >= 2) {
      return "…/" + parts.slice(1).join("/");
    }
  }
  const wsIdx = norm.indexOf("/workspaces/");
  if (wsIdx >= 0) {
    const after = norm.slice(wsIdx + "/workspaces/".length);
    const slash = after.indexOf("/");
    const tail = slash >= 0 ? after.slice(slash + 1) : "";
    if (tail) return "…/" + tail.replaceAll(/(?:^\||\|$)/g, "");
  }
  const parts = norm.split("/").filter(Boolean);
  if (parts.length <= 2) return wd;
  return "…/" + parts.slice(-2).join("/");
}

function terminalPathBasename(wd) {
  if (!wd || wd === ".") return "";
  const parts = String(wd).replaceAll("\\", "/").split("/").filter(Boolean);
  return parts.length ? parts.at(-1) : "";
}

function terminalLooksLikeOpaqueToken(s) {
  const t = String(s || "").trim();
  if (!t) return true;
  if (/^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(t)) return true;
  if (t.length >= 22 && /^[a-z0-9_-]+$/i.test(t) && !/\s/.test(t)) return true;
  return false;
}

const TERMINAL_ATTACHABLE_STATES = new Set(["RUNNING", "STARTING", "CREATED", "DETACHED", "SUSPENDED"]);
const TERMINAL_ENDED_STATES = new Set(["EXITED", "FAILED", "TERMINATED", "LOST", "STOPPING"]);
const STREAM_STABLE_MS = 5000;
const CONTROL_POLL_MS = 3000;

function terminalSessionAttachable(sess) {
  return !!(sess && TERMINAL_ATTACHABLE_STATES.has(String(sess.state || "").toUpperCase()));
}

function terminalSessionEnded(sess) {
  if (!sess) return false;
  const state = String(sess.state || "").toUpperCase();
  if (!state) return false;
  return TERMINAL_ENDED_STATES.has(state);
}

// Sessions hosted by another device are reached through the local core, which relays to that device.
function terminalSessionApi(sessionId, host, self) {
  const id = encodeURIComponent(sessionId);
  if (host && host !== self) return "/api/v1/terminal/remote/" + encodeURIComponent(host) + "/sessions/" + id;
  return "/api/v1/terminal/sessions/" + id;
}

function friendlyTerminalSessionName(sess, fallback) {
  const raw = (sess?.name) || fallback || "";
  const name = String(raw).trim();
  if (name && !terminalLooksLikeOpaqueToken(name) && !/^(shell|terminal)$/i.test(name)) {
    return name.length > 36 ? name.slice(0, 35) + "…" : name;
  }
  const base = terminalPathBasename(sess?.working_directory);
  if (base) return base;
  return "Terminal";
}

function clampSplitRatio(value) {
  const n = Number(value);
  if (!Number.isFinite(n)) return 0.5;
  return Math.min(0.85, Math.max(0.15, n));
}

function resolveTerminalModuleCtor(mod, name) {
  if (!mod) return null;
  if (typeof mod === "function") return mod;
  if (typeof mod[name] === "function") return mod[name];
  return null;
}

const TERMINAL_ESC = String.fromCodePoint(0x1b);
const TERMINAL_BEL = String.fromCodePoint(0x07);
const TERMINAL_OSC_END = new RegExp(
  TERMINAL_ESC + String.raw`]52;[^` + TERMINAL_BEL + TERMINAL_ESC + String.raw`]*$`,
  "i",
);
const TERMINAL_OSC_STRIP = new RegExp(
  TERMINAL_ESC + String.raw`]52;[^` + TERMINAL_BEL + TERMINAL_ESC + String.raw`]*(?:` + TERMINAL_BEL + "|" + TERMINAL_ESC + String.raw`\\)`,
  "gi",
);

function createTerminalPTYSanitizer() {
  const decoder = new TextDecoder("utf-8", { fatal: false });
  const oscReEnd = TERMINAL_OSC_END;
  const oscStrip = TERMINAL_OSC_STRIP;
  let pending = "";
  return function sanitize(bytes) {
    let s = pending;
    pending = "";
    if (bytes instanceof Uint8Array) {
      s += decoder.decode(bytes, { stream: true });
    } else if (typeof bytes === "string") {
      s += bytes;
    }
    const osc = oscReEnd.exec(s);
    if (osc) {
      pending = osc[0];
      s = s.slice(0, s.length - pending.length);
    } else if (s.endsWith(TERMINAL_ESC)) {
      pending = TERMINAL_ESC;
      s = s.slice(0, -1);
    }
    return s.replaceAll(oscStrip, "");
  };
}

function parseTerminalLease(raw) {
  if (!raw || typeof raw !== "object" || !raw.id) return null;
  return {
    id: raw.id,
    epoch: raw.epoch ?? raw.Epoch ?? 0,
    holder_device_id: raw.holder_device_id || raw.HolderDeviceID || ""
  };
}

function parseTerminalPendingRequesters(body) {
  const raw = body?.pending_control_requesters;
  return Array.isArray(raw) ? raw.filter(Boolean) : [];
}

function terminalSessionStateLabel(state) {
  const s = String(state || "").toLowerCase();
  if (s === "running") return "Running";
  if (s === "failed") return "Failed";
  if (s === "stopped" || s === "exited") return "Ended";
  return state ? String(state) : "";
}

function terminalParticipantRoleLabel(role) {
  const r = String(role || "").toLowerCase();
  if (r === "observer") return "observer";
  if (r === "controller") return "control";
  if (r === "owner") return "owner";
  return role ? String(role) : "observer";
}

function decodeTerminalFrames(buf) {
  const frames = [];
  if (!(buf instanceof ArrayBuffer)) return frames;
  const b = new Uint8Array(buf);
  const view = new DataView(buf);
  let off = 0;
  while (off + 18 <= b.length) {
    if (String.fromCodePoint(b[off], b[off + 1], b[off + 2], b[off + 3]) !== "ODT1") break;
    const type = b[off + 4];
    const seq = Number(view.getBigUint64(off + 6, false));
    const len = view.getUint32(off + 14, false);
    if (off + 18 + len > b.length) break;
    frames.push({ type, sequence: seq, payload: b.slice(off + 18, off + 18 + len) });
    off += 18 + len;
  }
  return frames;
}

function encodeTerminalFrame(type, payloadObj) {
  const payload = new TextEncoder().encode(JSON.stringify(payloadObj));
  const out = new Uint8Array(18 + payload.length);
  out[0] = 79; out[1] = 68; out[2] = 84; out[3] = 49;
  out[4] = type;
  out[5] = 0;
  new DataView(out.buffer).setBigUint64(6, BigInt(0), false);
  new DataView(out.buffer).setUint32(14, payload.length, false);
  out.set(payload, 18);
  return out.buffer;
}

function terminalTitleLabel(raw) {
  const title = String(raw || "").trim();
  if (!title) return "";
  const colon = title.lastIndexOf(": ");
  const tail = colon >= 0 ? title.slice(colon + 2) : title;
  if (/^[~/]/.test(tail)) {
    const parts = tail.split("/").filter(Boolean);
    return parts.length ? parts.at(-1) : tail;
  }
  return tail.length > 28 ? tail.slice(0, 27) + "…" : tail;
}

function terminalSplitLeafCount(node) {
  if (!node) return 0;
  if (node.t === "leaf") return 1;
  return terminalSplitLeafCount(node.a) + terminalSplitLeafCount(node.b);
}

function terminalSplitLeafDepth(node, id, depth) {
  if (!node) return 0;
  if (node.t === "leaf") return node.id === id ? depth : 0;
  return terminalSplitLeafDepth(node.a, id, depth + 1) || terminalSplitLeafDepth(node.b, id, depth + 1);
}

function terminalSplitContainsLeaf(node, id) {
  if (!node) return false;
  if (node.t === "leaf") return node.id === id;
  return terminalSplitContainsLeaf(node.a, id) || terminalSplitContainsLeaf(node.b, id);
}

function terminalSplitLeafIds(node, out) {
  if (!node) return out;
  if (node.t === "leaf") {
    out.push(node.id);
    return out;
  }
  terminalSplitLeafIds(node.a, out);
  terminalSplitLeafIds(node.b, out);
  return out;
}

function applyTerminalSplitFlex(paneA, paneB, ratio) {
  const r = Math.max(0.12, Math.min(0.88, ratio));
  paneA.style.flex = r + " 1 0px";
  paneB.style.flex = (1 - r) + " 1 0px";
}

function replaceTerminalSplitLeaf(node, id, next) {
  if (!node) return node;
  if (node.t === "leaf") return node.id === id ? next : node;
  return {
    t: "split",
    dir: node.dir,
    ratio: node.ratio,
    a: replaceTerminalSplitLeaf(node.a, id, next),
    b: replaceTerminalSplitLeaf(node.b, id, next)
  };
}

function removeTerminalSplitLeaf(node, id) {
  if (!node) return null;
  if (node.t === "leaf") return node.id === id ? null : node;
  const a = removeTerminalSplitLeaf(node.a, id);
  const b = removeTerminalSplitLeaf(node.b, id);
  if (!a) return b;
  if (!b) return a;
  return { t: "split", dir: node.dir, ratio: node.ratio, a, b };
}

function terminalSessionsFromApiBody(body) {
  if (!body || !Array.isArray(body.sessions)) return [];
  return body.sessions;
}

function scheduleDoubleAnimationFrame(cb) {
  requestAnimationFrame(() => requestAnimationFrame(cb));
}

function fitVisibleSplitTabs(splitRoot, tabs, focus) {
  terminalSplitLeafIds(splitRoot, []).forEach((id) => {
    const tab = tabs.get(id);
    if (tab) tab.fitWhenVisible(focus === true);
  });
}

function queueTabStreamConnect(tab, onConnected) {
  if (!tab || tab.dead || tab.ws) return;
  tab.connect().then(onConnected).catch(() => {});
}

function panelDragGeometry(edge, start, dx, dy, minW, minH) {
  let x = start.x;
  let y = start.y;
  let w = start.w;
  let h = start.h;
  if (edge.includes("e")) w = start.w + dx;
  if (edge.includes("s")) h = start.h + dy;
  if (edge.includes("w")) {
    w = start.w - dx;
    x = start.x + dx;
  }
  if (edge.includes("n")) {
    h = start.h - dy;
    y = start.y + dy;
  }
  if (w < minW) {
    if (edge.includes("w")) x -= minW - w;
    w = minW;
  }
  if (h < minH) {
    if (edge.includes("n")) y -= minH - h;
    h = minH;
  }
  return { x, y, w, h };
}

function bindTitlebarDrag(ev, ctx) {
  const { root, geo, ox, oy, snapDrag, applyGeo, fitActiveSoon } = ctx;
  const move = (e) => {
    geo.x = e.clientX - ox;
    geo.y = e.clientY - oy;
    root.style.left = geo.x + "px";
    root.style.top = geo.y + "px";
  };
  const up = () => {
    globalThis.removeEventListener("pointermove", move);
    globalThis.removeEventListener("pointerup", up);
    const snapped = snapDrag(geo.x, geo.y, geo.w, geo.h);
    geo.dock = snapped.dock || "";
    if (!geo.dock) {
      geo.x = snapped.x;
      geo.y = snapped.y;
    }
    applyGeo();
    fitActiveSoon();
  };
  globalThis.addEventListener("pointermove", move);
  globalThis.addEventListener("pointerup", up);
}

function bindPanelEdgeDrag(ev, ctx) {
  const { edge, root, geo, minW, minH, applyGeo, fitActiveSoon } = ctx;
  ev.stopPropagation();
  geo.dock = "";
  geo.expanded = false;
  geo.placed = true;
  const rect = root.getBoundingClientRect();
  geo.x = rect.left;
  geo.y = rect.top;
  geo.w = rect.width;
  geo.h = rect.height;
  applyGeo();
  const sx = ev.clientX;
  const sy = ev.clientY;
  const start = { x: geo.x, y: geo.y, w: geo.w, h: geo.h };
  const move = (e) => {
    const next = panelDragGeometry(edge, start, e.clientX - sx, e.clientY - sy, minW, minH);
    geo.x = next.x;
    geo.y = next.y;
    geo.w = next.w;
    geo.h = next.h;
    root.style.left = next.x + "px";
    root.style.top = next.y + "px";
    root.style.width = next.w + "px";
    root.style.height = next.h + "px";
  };
  const up = () => {
    globalThis.removeEventListener("pointermove", move);
    globalThis.removeEventListener("pointerup", up);
    applyGeo();
    fitActiveSoon();
  };
  globalThis.addEventListener("pointermove", move);
  globalThis.addEventListener("pointerup", up);
}

function terminalResizeObserverCallback(entries, tabs) {
  entries.forEach((entry) => {
    const view = entry.target;
    tabs.forEach((t) => {
      if (t.viewEl === view) t.scheduleResize();
    });
  });
}

function bindSplitGripDrag(ev, ctx) {
  const {
    grip,
    splitEl,
    node,
    dir,
    paneA,
    paneB,
    ratioFromPointer,
    applySplitFlex,
    fitLeavesInNode,
    onFinish,
  } = ctx;
  ev.preventDefault();
  ev.stopPropagation();
  grip.setPointerCapture(ev.pointerId);
  splitEl.classList.add("term-split--resizing");
  document.body.classList.add("term-split-dragging");
  let raf = 0;
  const move = (e) => {
    const next = ratioFromPointer(splitEl, grip, dir, e.clientX, e.clientY);
    if (next == null) return;
    node.ratio = next;
    applySplitFlex(paneA, paneB, next);
    if (raf) return;
    raf = requestAnimationFrame(() => {
      raf = 0;
      fitLeavesInNode(node);
    });
  };
  const up = (e) => {
    try {
      grip.releasePointerCapture(e.pointerId);
    } catch {
      /* already released */
    }
    globalThis.removeEventListener("pointermove", move);
    globalThis.removeEventListener("pointerup", up);
    globalThis.removeEventListener("pointercancel", up);
    splitEl.classList.remove("term-split--resizing");
    document.body.classList.remove("term-split-dragging");
    if (raf) cancelAnimationFrame(raf);
    if (onFinish) onFinish();
    fitLeavesInNode(node);
  };
  globalThis.addEventListener("pointermove", move);
  globalThis.addEventListener("pointerup", up);
  globalThis.addEventListener("pointercancel", up);
}

async function postNewTerminalSession(circleId, projectRoot, cols, rows) {
  return fetch("/api/v1/terminal/sessions", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      name: "Terminal",
      working_directory: "",
      project_root: projectRoot,
      cols,
      rows,
      circle_id: circleId || "",
    }),
  });
}

async function terminalCreateErrorDetail(res) {
  let detail = "Could not start terminal.";
  try {
    const raw = (await res.text()).trim();
    let parsed = null;
    try {
      parsed = JSON.parse(raw);
    } catch {
      /* plain-text http.Error body */
    }
    if (parsed?.error) detail = String(parsed.error);
    else if (raw) detail = raw;
  } catch {
    /* ignore */
  }
  return detail;
}

function createLiveSessions(deps) {
  const FRAME = {
    OUTPUT: 1,
    INPUT: 2,
    RESIZE: 3,
    SNAPSHOT: 4,
    ACK: 5,
    GAP: 6,
    HEARTBEAT: 7,
    CLOSE: 8,
    ERROR: 9
  };
  const TERM_BG = "#1c1c1e";
  const TERM_FG = "#f2f2f7";
  const XTERM_THEME = {
    background: TERM_BG,
    foreground: TERM_FG,
    cursor: TERM_FG,
    cursorAccent: TERM_BG,
    selectionBackground: "rgba(10, 132, 255, 0.38)",
    selectionForeground: "#ffffff",
    black: "#1c1c1e",
    red: "#ff453a",
    green: "#32d74b",
    yellow: "#ffd60a",
    blue: "#0a84ff",
    magenta: "#bf5af2",
    cyan: "#64d2ff",
    white: "#e5e5ea",
    brightBlack: "#636366",
    brightRed: "#ff6961",
    brightGreen: "#30d158",
    brightYellow: "#ffd426",
    brightBlue: "#409cff",
    brightMagenta: "#da8fff",
    brightCyan: "#70d7ff",
    brightWhite: "#ffffff"
  };

  function terminalCtor() {
    return resolveTerminalModuleCtor(globalThis.Terminal, "Terminal");
  }

  function fitAddonCtor() {
    return resolveTerminalModuleCtor(globalThis.FitAddon, "FitAddon");
  }

  const createPTYSanitizer = createTerminalPTYSanitizer;

  const root = document.getElementById("liveSessions");
  const tabsScroll = document.getElementById("termTabsScroll");
  const sessionList = document.getElementById("termSessionList");
  const sideMeta = document.getElementById("termSideMeta");
  const sideParticipants = document.getElementById("termSideParticipants");
  const sideControl = document.getElementById("termSideControl");
  const badgeRec = document.getElementById("termBadgeRec");
  const badgePrivate = document.getElementById("termBadgePrivate");
  const badgeControl = document.getElementById("termBadgeControl");
  const btnNew = document.getElementById("termNewSession");
  const btnTabAdd = document.getElementById("termTabAdd");
  const btnClosePanel = document.getElementById("termClose");
  const btnExpand = document.getElementById("termExpand");
  const btnToggleList = document.getElementById("termToggleList");
  const btnToggleInspector = document.getElementById("termToggleInspector");
  const termLayout = document.getElementById("termLayout");
  const termSidebarList = document.getElementById("termSidebarList");
  const termSidebarInspector = document.getElementById("termSidebarInspector");
  const termStatusPill = document.getElementById("termStatusPill");

  const tabs = new Map();
  let activeTabId = "";
  let pollTimer = 0;
  let listOpen = false;
  let inspectorOpen = false;
  let creatingDefault = false;
  let lastSentSize = { cols: 0, rows: 0 };

  function selfDeviceId() {
    return deps.selfDeviceId?.() || "";
  }

  const parseLease = parseTerminalLease;
  const parsePendingRequesters = parseTerminalPendingRequesters;

  function isSessionOwner(tab) {
    const self = selfDeviceId();
    if (!tab?.meta || !self) return false;
    return tab.meta.owner_device_id === self || tab.meta.host_device_id === self;
  }

  function openInspectorPanel() {
    inspectorOpen = true;
    syncLayoutClasses();
    fitActiveSoon();
  }

  function highlightInspectorParticipants() {
    const list = sideParticipants;
    const block = list?.closest(".term-inspector-block");
    if (!block) return;
    block.classList.add("term-inspector-block--attention");
    block.scrollIntoView({ block: "nearest", behavior: "smooth" });
    setTimeout(() => block.classList.remove("term-inspector-block--attention"), 2400);
  }

  const sessionStateLabel = terminalSessionStateLabel;
  const participantRoleLabel = terminalParticipantRoleLabel;

  function peerLabel(deviceId) {
    if (!deviceId) return "peer";
    const data = deps.getData?.();
    const rosterFn = deps.roster;
    const labelFn = deps.computerLabel;
    if (data && rosterFn && labelFn) {
      const peer = rosterFn(data).find((p) => p.device_id === deviceId);
      if (peer) return labelFn(peer, data);
    }
    const self = selfDeviceId();
    if (self && deviceId === self) return "you";
    return deviceId.slice(0, 12);
  }

  const shortPath = shortTerminalPath;
  const friendlySessionName = friendlyTerminalSessionName;

  function sessionSubline(sess) {
    const wd = shortPath(sess.working_directory);
    const dim = Math.trunc(sess.cols) + "×" + Math.trunc(sess.rows);
    if (wd && wd !== ".") return wd + " · " + dim;
    return dim;
  }

  function statusPillText(tab) {
    const fromTitle = terminalTitleLabel(tab.title);
    if (fromTitle) return fromTitle;
    if (tab.meta) {
      const base = terminalPathBasename(tab.meta.working_directory);
      if (base) return base;
      return friendlySessionName(tab.meta, tab.label);
    }
    return friendlySessionName(null, tab.label);
  }

  function syncLayoutClasses() {
    if (!termLayout) return;
    termLayout.classList.toggle("term-layout--list", listOpen);
    termLayout.classList.toggle("term-layout--inspector", inspectorOpen);
    if (btnToggleList) btnToggleList.setAttribute("aria-pressed", String(listOpen));
    if (btnToggleInspector) btnToggleInspector.setAttribute("aria-pressed", String(inspectorOpen));
    if (termSidebarList) termSidebarList.hidden = !listOpen;
    if (termSidebarInspector) termSidebarInspector.hidden = !inspectorOpen;
  }

  function el(tag, cls, text) {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text != null && text !== "") node.textContent = text;
    return node;
  }

  const decodeFrames = decodeTerminalFrames;

  function encodeInputFrame(payloadObj) {
    return encodeTerminalFrame(FRAME.INPUT, payloadObj);
  }

  function encodeResizeFrame(payloadObj) {
    return encodeTerminalFrame(FRAME.RESIZE, payloadObj);
  }

  function panelVisible() {
    return !!(root && !root.hidden);
  }

  function fitActiveSoon(focus) {
    scheduleDoubleAnimationFrame(() => fitVisibleSplitTabs(splitRoot, tabs, focus));
  }

  let resizeObserver = null;

  let tabSerial = 0;

  const titleLabel = terminalTitleLabel;

  class TermTab {
    constructor(sessionId, label, host) {
      this.sessionId = sessionId;
      this.host = host || "";
      this.serial = ++tabSerial;
      this.title = "";
      this.label = label || "Terminal";
      this.ws = null;
      this.streamToken = "";
      this.meta = null;
      this.lease = null;
      this.pendingRequesters = [];
      this.replay = false;
      this.dead = false;
      this.disposed = false;
      this.recovering = false;
      this.recoverAttempts = 0;
      this.gotOutput = false;
      this.readyToResize = false;
      this.resizeTimer = 0;
      this.lastCols = 0;
      this.lastRows = 0;
      this.connecting = false;
      this.reconnectTimer = 0;
      this.stableTimer = 0;
      this.pendingOutput = [];
      this.sanitize = createPTYSanitizer();
      this.tabWrap = el("div", "term-tab-wrap");
      this.tabEl = el("button", "term-tab", "");
      this.tabEl.type = "button";
      this.tabEl.dataset.sessionId = sessionId;
      this.pinEl = el("button", "term-tab-pin");
      this.pinEl.type = "button";
      this.pinEl.title = "Pin tab";
      this.pinEl.setAttribute("aria-label", "Pin tab");
      this.pinEl.innerHTML = '<svg viewBox="0 0 12 12" aria-hidden="true"><path d="M6 1.2 7.1 4.3 10.2 5.4 7.1 6.5 6 9.6 4.9 6.5 1.8 5.4 4.9 4.3z" fill="currentColor"/></svg>';
      this.closeEl = el("button", "term-tab-close", "×");
      this.closeEl.type = "button";
      this.closeEl.setAttribute("aria-label", "Close tab");
      this.tabWrap.append(this.tabEl, this.pinEl, this.closeEl);
      this.pinEl.addEventListener("click", (ev) => {
        ev.stopPropagation();
        togglePin(sessionId);
      });
      this.viewEl = el("div", "term-view");
      this.viewEl.hidden = true;
      this.hostEl = el("div", "term-xterm");
      this.viewEl.appendChild(this.hostEl);
      this.statusEl = el("div", "term-view-status", "");
      this.viewEl.appendChild(this.statusEl);
      this.endedBtn = null;
      this.tabEl.addEventListener("click", () => selectTab(sessionId));
      this.closeEl.addEventListener("click", (ev) => {
        ev.stopPropagation();
        closeTab(sessionId);
      });
      this.renderLabel();
    }
    ensureMounted() {
      if (this.term) return;
      this.mountXterm();
    }
    displayName() {
      const fromTitle = titleLabel(this.title);
      if (fromTitle) return fromTitle;
      return friendlySessionName(this.meta, this.label);
    }
    renderLabel() {
      const name = this.displayName();
      this.tabEl.textContent = name;
      const detail = this.meta?.working_directory ? this.meta.working_directory : "";
      this.tabEl.title = this.title || detail || name;
    }
    copySelection() {
      const text = this.term?.getSelection();
      if (!text) return false;
      if (navigator.clipboard?.writeText) {
        navigator.clipboard.writeText(text).catch(() => {});
      }
      return true;
    }
    mountXterm() {
      const Terminal = terminalCtor();
      if (!Terminal) {
        this.statusEl.textContent = "xterm.js failed to load.";
        return;
      }
      this.term = new Terminal({
        allowProposedApi: true,
        allowTransparency: true,
        convertEol: false,
        cursorBlink: true,
        cursorStyle: "bar",
        fontSize: 13,
        fontFamily: "SFMono-Regular, ui-monospace, Menlo, 'JetBrains Mono', Consolas, monospace",
        lineHeight: 1.25,
        letterSpacing: 0,
        theme: XTERM_THEME,
        scrollback: 8000,
        macOptionIsMeta: true,
        disableStdin: true,
        drawBoldTextInBrightColors: true,
        windowsMode: false
      });
      const Fit = fitAddonCtor();
      if (Fit) {
        this.fitAddon = new Fit();
        this.term.loadAddon(this.fitAddon);
      }
      this.term.open(this.hostEl);
      if (this.pendingOutput.length) {
        this.pendingOutput.forEach((text) => this.term.write(text));
        this.pendingOutput = [];
      }
      this.term.onData((data) => this.sendInput(data));
      this.term.onTitleChange((title) => {
        this.title = title;
        this.renderLabel();
        if (activeTabId === this.sessionId) updateStatusPill(this);
      });
      this.term.onBell(() => {
        this.tabWrap.classList.add("bell");
        setTimeout(() => this.tabWrap.classList.remove("bell"), 600);
      });
      this.term.attachCustomKeyEventHandler((ev) => {
        if (ev.type !== "keydown") return true;
        const key = ev.key.toLowerCase();
        if (ev.ctrlKey && ev.shiftKey && key === "c") {
          this.copySelection();
          ev.preventDefault();
          return false;
        }
        if (ev.ctrlKey && ev.shiftKey && key === "v") return false;
        if (ev.ctrlKey && ev.shiftKey && (key === "d" || key === "e")) return false;
        if (ev.metaKey && (key === "c" || key === "v")) {
          if (key === "c") this.copySelection();
          return false;
        }
        return true;
      });
    }
    streamActive() {
      if (!this.ws) return false;
      const st = this.ws.readyState;
      return st === WebSocket.CONNECTING || st === WebSocket.OPEN;
    }
    api() {
      return terminalSessionApi(this.sessionId, this.host, selfDeviceId());
    }
    isRemote() {
      const self = selfDeviceId();
      return !!(this.host && this.host !== self);
    }
    canWrite() {
      if (this.replay || this.dead) return false;
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false;
      if (!this.lease) return false;
      const self = selfDeviceId();
      const holder = this.lease.holder_device_id;
      if (!self || !holder || holder === self) return true;
      return !!(this.meta && this.meta.owner_device_id === self);
    }
    syncStdin() {
      if (!this.term) return;
      this.term.options.disableStdin = !this.canWrite();
    }
    fitWhenVisible(focus) {
      if (!this.term || this.viewEl.hidden || !panelVisible() || this.viewEl.classList.contains("term-view--ended")) return;
      const rect = this.hostEl.getBoundingClientRect();
      if (rect.width < 80 || rect.height < 48) return;
      if (this.fitAddon) {
        try { this.fitAddon.fit(); } catch { /* layout not ready */ }
      }
      this.sendResize();
      if (focus && activeTabId === this.sessionId) this.term.focus();
    }
    sendResize() {
      if (!this.term || !this.readyToResize) return;
      const cols = Math.trunc(this.term.cols);
      const rows = Math.trunc(this.term.rows);
      if (cols < 20 || rows < 6) return;
      lastSentSize = { cols, rows };
      if (cols === this.lastCols && rows === this.lastRows) return;
      if (!this.canWrite()) return;
      this.lastCols = cols;
      this.lastRows = rows;
      this.ws.send(encodeResizeFrame({
        lease_id: this.lease.id || "",
        epoch: this.lease.epoch ?? 0,
        cols,
        rows
      }));
    }
    scheduleResize() {
      if (this.resizeTimer) clearTimeout(this.resizeTimer);
      this.resizeTimer = setTimeout(() => {
        this.resizeTimer = 0;
        this.fitWhenVisible();
      }, 80);
    }
    sendInput(bytes) {
      if (!bytes || !this.canWrite()) return;
      const body = {
        lease_id: this.lease.id || this.lease.ID || "",
        epoch: this.lease.epoch ?? this.lease.Epoch ?? 0,
        input_id: "in_" + Date.now() + "_" + Math.random().toString(36).slice(2, 8),
        data: bytes
      };
      this.ws.send(encodeInputFrame(body));
    }
    showEnded(message) {
      this.dead = true;
      this.syncStdin();
      this.disconnect();
      this.viewEl.classList.add("term-view--ended");
      if (this.hostEl) this.hostEl.hidden = true;
      const text = message || "Session ended. Use + to open a new terminal.";
      this.statusEl.replaceChildren();
      this.statusEl.appendChild(el("p", "term-ended-copy", text));
      if (!this.endedBtn) {
        this.endedBtn = el("button", "term-ended-action", "New terminal");
        this.endedBtn.type = "button";
        this.endedBtn.addEventListener("click", (ev) => {
          ev.preventDefault();
          ev.stopPropagation();
          void createSession();
        });
      }
      this.statusEl.appendChild(this.endedBtn);
      this.pendingOutput = [];
      paintDockBadge();
      if (deps.onTabsChanged) deps.onTabsChanged(openTabCount());
      if (activeTabId === this.sessionId) updateStatusPill(this);
    }
    handleFrame(frame) {
      if (!frame || this.dead) return;
      if (frame.type === FRAME.OUTPUT) {
        this.gotOutput = true;
        const text = this.sanitize(frame.payload);
        if (text && this.term) this.term.write(text);
        else if (text) this.pendingOutput.push(text);
        if (this.statusEl.textContent && !/Stream closed|Stream error/i.test(this.statusEl.textContent)) {
          this.statusEl.textContent = "";
        }
      } else if (frame.type === FRAME.SNAPSHOT) {
        return;
      } else if (frame.type === FRAME.ERROR) {
        const msg = new TextDecoder().decode(frame.payload);
        if (/no such file|unavailable|FAILED/i.test(msg)) {
          this.showEnded(msg || "Session ended. Use + to open a new terminal.");
          return;
        }
        this.statusEl.textContent = msg;
      } else if (frame.type === FRAME.GAP) {
        this.statusEl.textContent = "Stream gap. Catching up...";
      }
    }
    async recoverStream() {
      if (this.recovering || this.dead || this.disposed) return;
      this.recovering = true;
      try {
        await this.refreshMeta();
        if (this.disposed || this.dead) return;
        if (terminalSessionEnded(this.meta)) {
          this.showEnded("Session ended. Use + to open a new terminal.");
          updateChrome();
          return;
        }
        this.statusEl.textContent = "";
        await this.connect();
      } catch (err) {
        if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
        this.scheduleRecover();
      } finally {
        this.recovering = false;
      }
    }
    markStable(ws) {
      if (this.stableTimer) clearTimeout(this.stableTimer);
      this.stableTimer = setTimeout(() => {
        this.stableTimer = 0;
        if (this.ws === ws && ws.readyState === WebSocket.OPEN) this.recoverAttempts = 0;
      }, STREAM_STABLE_MS);
    }
    scheduleRecover() {
      if (this.dead || this.disposed || this.reconnectTimer) return;
      this.recoverAttempts = (this.recoverAttempts || 0) + 1;
      if (this.recoverAttempts > 3) {
        this.statusEl.textContent = "Stream closed.";
        return;
      }
      this.reconnectTimer = setTimeout(() => {
        this.reconnectTimer = 0;
        void this.recoverStream();
      }, 250 * this.recoverAttempts);
    }
    applyAttachBody(body) {
      this.streamToken = body.stream_token || "";
      const lease = parseLease(body.lease);
      if (lease) this.lease = lease;
      this.syncStdin();
    }
    resetOutputBeforeReconnect() {
      if (!this.gotOutput) return;
      if (this.term) this.term.reset();
      this.pendingOutput = [];
      this.sanitize = createPTYSanitizer();
      this.gotOutput = false;
    }
    bindStreamWebSocket(ws) {
      ws.binaryType = "arraybuffer";
      ws.onmessage = (ev) => {
        decodeFrames(ev.data).forEach((frame) => this.handleFrame(frame));
      };
      ws.onopen = () => {
        if (this.ws !== ws) return;
        if (/Stream (closed|error)/i.test(this.statusEl.textContent)) this.statusEl.textContent = "";
        this.syncStdin();
        this.markStable(ws);
        setTimeout(() => {
          if (this.ws !== ws) return;
          this.readyToResize = true;
          this.fitWhenVisible();
        }, 50);
      };
      ws.onclose = () => {
        if (this.ws !== ws) return;
        this.ws = null;
        this.readyToResize = false;
        this.syncStdin();
        if (this.dead || this.disposed) return;
        this.scheduleRecover();
      };
      ws.onerror = () => {
        if (this.ws === ws && !this.dead) this.statusEl.textContent = "Stream error.";
      };
    }
    async connect() {
      if (this.dead || this.connecting || this.streamActive()) return;
      this.connecting = true;
      try {
        let attachRes;
        try {
          attachRes = await fetch(this.api() + "/attach", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ capabilities: ["observe", "input", "control"] })
          });
        } catch (err) {
          if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
          this.statusEl.textContent = "Could not attach to session.";
          return;
        }
        if (this.dead || this.disposed) return;
        if (!attachRes.ok) {
          if (attachRes.status === 404 || attachRes.status === 410) {
            this.meta = { ...this.meta, id: this.sessionId, state: "LOST" };
            this.showEnded("Session ended. Use + to open a new terminal.");
            return;
          }
          this.statusEl.textContent = "Could not attach to session.";
          return;
        }
        const body = await attachRes.json();
        if (this.dead || this.disposed) return;
        this.applyAttachBody(body);
        if (!this.streamToken) {
          this.statusEl.textContent = "Missing stream token.";
          return;
        }
        if (this.streamActive()) return;
        const proto = location.protocol === "https:" ? "wss:" : "ws:";
        const url = proto + "//" + location.host + this.api() + "/stream?token=" + encodeURIComponent(this.streamToken);
        this.disconnect();
        this.resetOutputBeforeReconnect();
        this.lastCols = 0;
        this.lastRows = 0;
        const ws = new WebSocket(url);
        this.ws = ws;
        this.bindStreamWebSocket(ws);
      } finally {
        this.connecting = false;
      }
    }
    disconnect() {
      this.connecting = false;
      const ws = this.ws;
      this.ws = null;
      if (!ws) return;
      ws.onopen = null;
      ws.onclose = null;
      ws.onerror = null;
      ws.onmessage = null;
      try { ws.close(); } catch { /* already closed */ }
    }
    dispose() {
      this.disposed = true;
      this.dead = true;
      this.disconnect();
      if (this.resizeTimer) clearTimeout(this.resizeTimer);
      if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
      this.reconnectTimer = 0;
      if (this.stableTimer) clearTimeout(this.stableTimer);
      this.stableTimer = 0;
      if (this.term) {
        try { this.term.dispose(); } catch { /* already gone */ }
        this.term = null;
      }
    }
    async refreshMeta() {
      const res = await fetch(this.api(), { cache: "no-store" });
      if (res.status === 404 || res.status === 410) {
        this.meta = { ...this.meta, id: this.sessionId, state: "LOST" };
        return;
      }
      if (!res.ok || this.disposed) return;
      const body = await res.json();
      if (this.disposed) return;
      this.meta = body.session || null;
      this.lease = parseLease(body.lease);
      this.pendingRequesters = parsePendingRequesters(body);
      this.syncStdin();
      if (this.meta?.name) {
        this.label = this.meta.name;
        this.renderLabel();
      }
      if (!this.dead && terminalSessionEnded(this.meta) && !this.recovering) {
        this.showEnded(this.meta.error_message || "Session ended. Use + to open a new terminal.");
      }
      updateChrome();
      if (activeTabId !== this.sessionId) return;
      renderSide(this);
      updateStatusPill(this);
    }
  }

  const GEO_KEY = "overdrive.term.geo";
  const PIN_KEY = "overdrive.term.pins";
  const BG_OPACITY_KEY = "overdriveTermBgOpacity";
  const TERM_BG_RGB = "28, 28, 30";
  const TERM_BG_OPACITY_MIN = 20;
  const TERM_BG_OPACITY_MAX = 100;

  function clampBgOpacityPercent(pct) {
    const n = Number(pct);
    if (!Number.isFinite(n)) return TERM_BG_OPACITY_MAX;
    return Math.min(TERM_BG_OPACITY_MAX, Math.max(TERM_BG_OPACITY_MIN, Math.round(n)));
  }

  function termBackgroundRgba(alpha) {
    const a = Math.min(1, Math.max(0.2, Number(alpha) || 1));
    if (a >= 0.999) return TERM_BG;
    return "rgba(" + TERM_BG_RGB + ", " + a + ")";
  }

  function loadBgOpacityPercent() {
    try {
      const raw = localStorage.getItem(BG_OPACITY_KEY);
      if (raw == null || raw === "") return TERM_BG_OPACITY_MAX;
      return clampBgOpacityPercent(raw);
    } catch (err) {
      if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
      return TERM_BG_OPACITY_MAX;
    }
  }

  function saveBgOpacityPercent(pct) {
    try { localStorage.setItem(BG_OPACITY_KEY, String(pct)); } catch { /* private mode */ }
  }

  function syncXtermBackgrounds(alpha) {
    const bg = termBackgroundRgba(alpha);
    XTERM_THEME.background = bg;
    XTERM_THEME.cursorAccent = bg;
    tabs.forEach((tab) => {
      if (!tab.term) return;
      const theme = { ...tab.term.options.theme, background: bg, cursorAccent: bg };
      if (typeof tab.term.setOption === "function") {
        tab.term.setOption("theme", theme);
      } else {
        tab.term.options.theme = theme;
      }
      if (typeof tab.term.refresh === "function" && tab.term.rows > 0) {
        tab.term.refresh(0, tab.term.rows - 1);
      }
    });
  }

  function applyTermBgOpacity(percent) {
    const pct = clampBgOpacityPercent(percent);
    const alpha = pct / 100;
    if (root) {
      root.style.setProperty("--term-bg-opacity", String(alpha));
      root.dataset.termBgOpacity = String(pct);
    }
    syncXtermBackgrounds(alpha);
    saveBgOpacityPercent(pct);
    const slider = document.getElementById("termBgOpacity");
    const valueEl = document.getElementById("termBgOpacityValue");
    if (slider && String(slider.value) !== String(pct)) slider.value = String(pct);
    if (slider) {
      slider.setAttribute("aria-valuenow", String(pct));
      slider.setAttribute("aria-valuetext", pct + " percent");
      slider.style.setProperty("--term-opacity-pct", pct + "%");
    }
    if (valueEl) valueEl.textContent = pct + "%";
    return pct;
  }
  const MARGIN = 16;
  const MIN_W = 480;
  const MIN_H = 320;
  let geo = loadGeo();
  let splitRoot = null;
  let focusedLeafId = "";
  const pinned = new Set(loadPins());

  function loadGeo() {
    try {
      const raw = JSON.parse(sessionStorage.getItem(GEO_KEY) || "null");
      if (raw && typeof raw.w === "number") return raw;
    } catch { /* ignore */ }
    return { x: 0, y: 0, w: 860, h: 520, dock: "", pinned: false, tabsBelow: false, expanded: false, placed: false };
  }
  function saveGeo() {
    try { sessionStorage.setItem(GEO_KEY, JSON.stringify(geo)); } catch { /* private mode */ }
  }
  function loadPins() {
    try {
      const raw = JSON.parse(sessionStorage.getItem(PIN_KEY) || "[]");
      return Array.isArray(raw) ? raw : [];
    } catch (err) {
      if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
      return [];
    }
  }
  function savePins() {
    try { sessionStorage.setItem(PIN_KEY, JSON.stringify([...pinned])); } catch { /* private mode */ }
  }
  function viewportBox() {
    return { w: globalThis.innerWidth, h: globalThis.innerHeight };
  }
  function clampGeo() {
    const v = viewportBox();
    geo.w = Math.max(MIN_W, Math.min(geo.w, v.w - MARGIN * 2));
    geo.h = Math.max(MIN_H, Math.min(geo.h, v.h - 120));
    geo.x = Math.max(MARGIN, Math.min(geo.x, v.w - geo.w - MARGIN));
    geo.y = Math.max(36, Math.min(geo.y, v.h - geo.h - 88));
  }
  function applyGeo() {
    if (!root) return;
    root.classList.add("term-placed");
    root.classList.toggle("term-front", !!geo.pinned);
    root.classList.toggle("term-dock-top", geo.dock === "top");
    root.classList.toggle("term-dock-bottom", geo.dock === "bottom");
    root.classList.toggle("term-tabs-below", !!geo.tabsBelow);
    root.classList.toggle("expanded", !!geo.expanded);
    if (geo.dock === "top" || geo.dock === "bottom") {
      root.style.left = "";
      root.style.width = "";
      root.style.height = "";
      root.style.top = "";
      root.style.bottom = "";
    } else {
      clampGeo();
      root.style.left = geo.x + "px";
      root.style.top = geo.y + "px";
      root.style.width = geo.w + "px";
      root.style.height = geo.h + "px";
      root.style.bottom = "auto";
    }
    const pinBtn = document.getElementById("termPinFront");
    const topBtn = document.getElementById("termDockTop");
    const botBtn = document.getElementById("termDockBottom");
    const edgeBtn = document.getElementById("termTabsEdge");
    if (pinBtn) pinBtn.setAttribute("aria-pressed", String(!!geo.pinned));
    if (topBtn) topBtn.setAttribute("aria-pressed", String(geo.dock === "top"));
    if (botBtn) botBtn.setAttribute("aria-pressed", String(geo.dock === "bottom"));
    if (edgeBtn) edgeBtn.setAttribute("aria-pressed", String(!!geo.tabsBelow));
    saveGeo();
  }
  function raise() {
    if (!root) return;
    root.classList.remove("term-back", "term-inactive");
    root.style.zIndex = geo.pinned ? "" : "72";
  }
  function lowerUnlessPinned() {
    if (!root || geo.pinned) return;
    root.classList.add("term-back");
    root.style.zIndex = "";
  }
  function snapDrag(x, y, w, h) {
    const v = viewportBox();
    const nearL = x < 28;
    const nearR = x + w > v.w - 28;
    const nearT = y < 40;
    const nearB = y + h > v.h - 108;
    if (nearT && !nearL && !nearR) return { dock: "top" };
    if (nearB && !nearL && !nearR) return { dock: "bottom" };
    let dockX = x;
    if (nearL) dockX = MARGIN;
    else if (nearR) dockX = v.w - w - MARGIN;
    let dockY = y;
    if (nearT) dockY = 44;
    else if (nearB) dockY = v.h - h - 96;
    return { dock: "", x: dockX, y: dockY };
  }
  function bindWindowChrome() {
    const bar = document.getElementById("termTitlebar");
    if (bar) {
      bar.addEventListener("pointerdown", (ev) => {
        if (ev.button !== 0 || ev.target.closest("button")) return;
        raise();
        const rect = root.getBoundingClientRect();
        geo.dock = "";
        geo.expanded = false;
        geo.placed = true;
        geo.x = rect.left;
        geo.y = rect.top;
        geo.w = rect.width;
        geo.h = rect.height;
        applyGeo();
        const ox = ev.clientX - rect.left;
        const oy = ev.clientY - rect.top;
        bindTitlebarDrag(ev, { root, geo, ox, oy, snapDrag, applyGeo, fitActiveSoon });
      });
    }
    root.querySelectorAll(".term-resize").forEach((handle) => {
      handle.addEventListener("pointerdown", (ev) => {
        if (ev.button !== 0) return;
        ev.preventDefault();
        bindPanelEdgeDrag(ev, {
          edge: handle.dataset.edge || "",
          root,
          geo,
          minW: MIN_W,
          minH: MIN_H,
          applyGeo,
          fitActiveSoon,
        });
      });
    });
    document.addEventListener("pointerdown", (ev) => {
      if (!root || root.hidden) return;
      root.classList.toggle("term-inactive", !root.contains(ev.target));
    });
    const pinBtn = document.getElementById("termPinFront");
    const topBtn = document.getElementById("termDockTop");
    const botBtn = document.getElementById("termDockBottom");
    const edgeBtn = document.getElementById("termTabsEdge");
    const splitV = document.getElementById("termSplitV");
    const splitH = document.getElementById("termSplitH");
    if (pinBtn) {
      pinBtn.addEventListener("click", () => {
        geo.pinned = !geo.pinned;
        if (geo.pinned) raise();
        applyGeo();
      });
    }
    if (topBtn) topBtn.addEventListener("click", () => { geo.dock = geo.dock === "top" ? "" : "top"; geo.expanded = false; applyGeo(); fitActiveSoon(); });
    if (botBtn) botBtn.addEventListener("click", () => { geo.dock = geo.dock === "bottom" ? "" : "bottom"; geo.expanded = false; applyGeo(); fitActiveSoon(); });
    if (edgeBtn) edgeBtn.addEventListener("click", () => { geo.tabsBelow = !geo.tabsBelow; applyGeo(); });
    if (splitV) splitV.addEventListener("click", () => splitFocused("v"));
    if (splitH) splitH.addEventListener("click", () => splitFocused("h"));
    document.querySelectorAll("#termShareChat, #termShareChatBar").forEach((shareBtn) => {
      shareBtn.addEventListener("click", () => shareActiveToChat());
    });
    globalThis.addEventListener("keydown", (ev) => {
      if (!root || root.hidden || !root.contains(document.activeElement)) return;
      if (ev.ctrlKey && ev.shiftKey && ev.key.toLowerCase() === "d") { ev.preventDefault(); splitFocused("v"); }
      if (ev.ctrlKey && ev.shiftKey && ev.key.toLowerCase() === "e") { ev.preventDefault(); splitFocused("h"); }
    });
    if (!geo.placed) {
      const v = viewportBox();
      geo.w = Math.min(860, v.w - 48);
      geo.h = Math.min(520, v.h - 148);
      geo.x = (v.w - geo.w) / 2;
      geo.y = (v.h - geo.h) / 2;
      geo.placed = true;
    }
    applyGeo();
  }

  const leafCount = terminalSplitLeafCount;
  const leafDepth = terminalSplitLeafDepth;
  const containsLeaf = terminalSplitContainsLeaf;
  const leafIds = terminalSplitLeafIds;

  const SPLIT_RATIO_KEY = "overdrive.term.splitRatios";
  const SPLIT_MIN_PANE_PX = 72;

  function splitRatioStorageKey(node) {
    if (node?.t !== "split") return "";
    return leafIds(node, []).sort().join("+");
  }

  function loadSplitRatiosMap() {
    try {
      const raw = JSON.parse(localStorage.getItem(SPLIT_RATIO_KEY) || "{}");
      return raw && typeof raw === "object" ? raw : {};
    } catch (err) {
      if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
      return {};
    }
  }

  function persistSplitRatio(key, ratio) {
    if (!key || !Number.isFinite(ratio)) return;
    try {
      const map = loadSplitRatiosMap();
      map[key] = Math.round(ratio * 1000) / 1000;
      localStorage.setItem(SPLIT_RATIO_KEY, JSON.stringify(map));
    } catch { /* private mode */ }
  }

  function storedSplitRatioForIds(idA, idB) {
    const key = [idA, idB].sort((a, b) => String(a).localeCompare(String(b))).join("+");
    const map = loadSplitRatiosMap();
    const n = Number(map[key]);
    return Number.isFinite(n) ? Math.max(0.12, Math.min(0.88, n)) : null;
  }

  function resolveSplitRatio(node) {
    if (node?.t !== "split") return 0.5;
    if (typeof node.ratio === "number" && Number.isFinite(node.ratio)) {
      return Math.max(0.12, Math.min(0.88, node.ratio));
    }
    const key = splitRatioStorageKey(node);
    const map = loadSplitRatiosMap();
    const stored = key ? Number(map[key]) : Number.NaN;
    const ratio = Number.isFinite(stored) ? stored : 0.5;
    node.ratio = Math.max(0.12, Math.min(0.88, ratio));
    return node.ratio;
  }

  const applySplitFlex = applyTerminalSplitFlex;

  function fitLeavesInNode(node) {
    leafIds(node, []).forEach((id) => {
      const tab = tabs.get(id);
      if (tab) tab.scheduleResize();
    });
    fitActiveSoon();
  }

  function ratioFromPointer(splitEl, grip, dir, clientX, clientY) {
    const rect = splitEl.getBoundingClientRect();
    if (dir === "v") {
      const total = splitEl.clientWidth - grip.offsetWidth;
      if (total < SPLIT_MIN_PANE_PX * 2) return null;
      const x = clientX - rect.left - grip.offsetWidth / 2;
      const clamped = Math.max(SPLIT_MIN_PANE_PX, Math.min(total - SPLIT_MIN_PANE_PX, x));
      return clamped / total;
    }
    const total = splitEl.clientHeight - grip.offsetHeight;
    if (total < SPLIT_MIN_PANE_PX * 2) return null;
    const y = clientY - rect.top - grip.offsetHeight / 2;
    const clamped = Math.max(SPLIT_MIN_PANE_PX, Math.min(total - SPLIT_MIN_PANE_PX, y));
    return clamped / total;
  }

  function bindSplitGrip(grip, splitEl, paneA, paneB, node) {
    const dir = node.dir;
    grip.addEventListener("pointerdown", (ev) => {
      if (ev.button !== 0) return;
      bindSplitGripDrag(ev, {
        grip,
        splitEl,
        node,
        dir,
        paneA,
        paneB,
        ratioFromPointer,
        applySplitFlex,
        fitLeavesInNode,
        onFinish: () => {
          const key = splitRatioStorageKey(node);
          if (key) persistSplitRatio(key, node.ratio);
        },
      });
    });
  }

  const replaceLeaf = replaceTerminalSplitLeaf;
  function retargetLeaf(node, from, to) {
    if (!node || pinned.has(from)) return node;
    if (node.t === "leaf") return node.id === from ? { t: "leaf", id: to } : node;
    return { t: "split", dir: node.dir, ratio: node.ratio, a: retargetLeaf(node.a, from, to), b: retargetLeaf(node.b, from, to) };
  }
  const removeLeaf = removeTerminalSplitLeaf;
  function ensureRoot(sessionId, opts) {
    const preserve = opts?.preserveSplitRoot;
    if (!splitRoot) splitRoot = { t: "leaf", id: sessionId };
    else if (
      !preserve &&
      !containsLeaf(splitRoot, sessionId) &&
      leafCount(splitRoot) === 1 &&
      splitRoot.t === "leaf" &&
      !pinned.has(splitRoot.id)
    ) {
      splitRoot = { t: "leaf", id: sessionId };
    }
    if (opts?.setFocus !== false) focusedLeafId = sessionId;
  }

  function attachVisibleLeaves() {
    leafIds(splitRoot, []).forEach((id) => {
      const tab = tabs.get(id);
      if (!tab || tab.viewEl.hidden) return;
      tab.ensureMounted();
      tab.scheduleResize();
      if (tab.dead) return;
      queueTabStreamConnect(tab, () => {
        refreshLease(tab);
        tab.scheduleResize();
      });
    });
  }

  function splitFocused(dir) {
    const id = focusedLeafId || activeTabId;
    if (!id || !splitRoot) return;
    if (leafCount(splitRoot) >= 4) return;
    if (leafDepth(splitRoot, id, 1) >= 3) return;
    const used = new Set(leafIds(splitRoot, []));
    let other = "";
    tabs.forEach((_, sid) => { if (!other && !used.has(sid)) other = sid; });
    const finish = (sid) => {
      if (!sid || sid === id) return;
      const ratio = storedSplitRatioForIds(id, sid) ?? 0.5;
      splitRoot = replaceLeaf(splitRoot, id, { t: "split", dir, ratio, a: { t: "leaf", id }, b: { t: "leaf", id: sid } });
      renderSplit();
      fitActiveSoon();
    };
    if (other) { finish(other); return; }
    createSession({ forSplit: true }).then((newId) => {
      if (newId && tabs.has(newId) && !containsLeaf(splitRoot, newId)) finish(newId);
    });
  }
  function closePane(sessionId) {
    if (!splitRoot || splitRoot.t === "leaf") return;
    splitRoot = removeLeaf(splitRoot, sessionId);
    const remaining = leafIds(splitRoot, []);
    if (remaining.length && (!remaining.includes(activeTabId) || !remaining.includes(focusedLeafId))) {
      const next = remaining.includes(activeTabId) ? activeTabId : remaining[0];
      focusLeaf(next, { rebuild: true });
      return;
    }
    renderSplit();
    fitActiveSoon();
  }
  function renderSplit() {
    const host = document.getElementById("termViews");
    if (!host) return;
    if (!splitRoot && tabs.size > 0) {
      const fallbackId = activeTabId || tabs.keys().next().value;
      if (fallbackId) ensureRoot(fallbackId);
    }
    const visible = new Set(leafIds(splitRoot, []));
    tabs.forEach((tab, id) => {
      tab.viewEl.hidden = !visible.has(id);
      tab.tabWrap.classList.toggle("active", id === activeTabId);
      tab.tabEl.classList.toggle("active", id === activeTabId);
      tab.tabWrap.classList.toggle("pinned", pinned.has(id));
    });
    host.replaceChildren(buildSplitDOM(splitRoot));
    attachVisibleLeaves();
    fitActiveSoon();
  }
  function buildSplitDOM(node) {
    if (!node) return el("div", "term-pane");
    if (node.t === "leaf") {
      const wrap = el("div", "term-pane" + (node.id === activeTabId ? " focused" : ""));
      wrap.dataset.leafId = node.id;
      const tab = tabs.get(node.id);
      if (tab) {
        wrap.appendChild(tab.viewEl);
        tab.ensureMounted();
      }
      if (splitRoot?.t !== "leaf") {
        const close = el("button", "term-pane-close", "×");
        close.type = "button";
        close.title = "Close pane";
        close.addEventListener("click", (ev) => {
          ev.stopPropagation();
          closePane(node.id);
        });
        wrap.appendChild(close);
      }
      wrap.addEventListener("mousedown", () => focusLeaf(node.id));
      return wrap;
    }
    const wrap = el("div", "term-split term-split--" + node.dir);
    const paneA = buildSplitDOM(node.a);
    const grip = el("div", "term-split-grip");
    grip.setAttribute("role", "separator");
    grip.setAttribute("aria-orientation", node.dir === "v" ? "vertical" : "horizontal");
    grip.setAttribute("aria-label", node.dir === "v" ? "Resize panes horizontally" : "Resize panes vertically");
    const paneB = buildSplitDOM(node.b);
    const ratio = resolveSplitRatio(node);
    applySplitFlex(paneA, paneB, ratio);
    bindSplitGrip(grip, wrap, paneA, paneB, node);
    wrap.append(paneA, grip, paneB);
    return wrap;
  }
  function paintFocus() {
    tabs.forEach((tab, id) => {
      tab.tabWrap.classList.toggle("active", id === activeTabId);
      tab.tabEl.classList.toggle("active", id === activeTabId);
    });
    const host = document.getElementById("termViews");
    if (!host) return;
    host.querySelectorAll(".term-pane[data-leaf-id]").forEach((pane) => {
      pane.classList.toggle("focused", pane.dataset.leafId === activeTabId);
    });
  }
  function focusLeaf(sessionId, opts) {
    const changed = activeTabId !== sessionId;
    activeTabId = sessionId;
    focusedLeafId = sessionId;
    const tab = tabs.get(sessionId);
    if ((opts?.rebuild) || !containsLeaf(splitRoot, sessionId)) renderSplit();
    else paintFocus();
    if (tab) {
      if (changed || (opts?.rebuild)) {
        renderSide(tab);
        updateStatusPill(tab);
      }
      updateChrome();
      fitActiveSoon(true);
    }
    raise();
  }
  function orderTabs() {
    if (!tabsScroll) return;
    const wraps = [...tabs.values()].sort((a, b) => {
      const ap = pinned.has(a.sessionId) ? 0 : 1;
      const bp = pinned.has(b.sessionId) ? 0 : 1;
      if (ap !== bp) return ap - bp;
      return a.serial - b.serial;
    });
    wraps.forEach((tab) => tabsScroll.appendChild(tab.tabWrap));
  }
  function togglePin(sessionId) {
    if (pinned.has(sessionId)) pinned.delete(sessionId);
    else pinned.add(sessionId);
    savePins();
    orderTabs();
    renderSplit();
  }
  function paintDockBadge() {
    const badge = document.getElementById("termCount");
    if (!badge) return;
    let live = 0;
    tabs.forEach((tab) => { if (!tab.dead) live += 1; });
    badge.textContent = live > 0 ? String(live) : "";
  }
  function notifyShare(code) {
    if (typeof globalThis.overdriveComposerStatus !== "function") return;
    const text = {
      shared: "Shared the live terminal",
      "no-target": "Choose a conversation to share the terminal",
      failed: "Could not share the terminal",
      "no-session": "Open a terminal session first"
    }[code];
    if (text) globalThis.overdriveComposerStatus(text, code === "shared" ? 3200 : 4500);
  }
  async function shareActiveToChat() {
    const tab = tabs.get(activeTabId);
    if (!tab) {
      notifyShare("no-session");
      return "no-session";
    }
    const data = deps.getData?.();
    const mesh = data?.circles?.[0];
    if (!mesh?.id) {
      notifyShare("failed");
      return "failed";
    }
    const target = deps.chatTarget?.() || null;
    if (!target || (!target.conversation_id && !target.recipient)) {
      notifyShare("no-target");
      return "no-target";
    }
    const wd = tab.meta?.working_directory;
    const body = {
      mesh_id: mesh.id,
      type: "terminal_session",
      title: tab.displayName(),
      summary: [tab.meta?.state, shortPath(wd)].filter(Boolean).join(" · "),
      session_id: tab.sessionId,
      host: (tab.meta?.host_device_id) || "",
      project: wd || "",
      state: (tab.meta?.state) || ""
    };
    if (target.conversation_id) body.conversation_id = target.conversation_id;
    if (target.recipient) body.recipient = target.recipient;
    try {
      const res = await fetch("/api/chat/messages", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body)
      });
      if (!res.ok) {
        notifyShare("failed");
        return "failed";
      }
      notifyShare("shared");
      if (deps.onShared) await deps.onShared();
      return "shared";
    } catch (err) {
      if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
      notifyShare("failed");
      return "failed";
    }
  }

  function selectTab(sessionId) {
    if (splitRoot && !containsLeaf(splitRoot, sessionId)) {
      let from = focusedLeafId && containsLeaf(splitRoot, focusedLeafId) ? focusedLeafId : "";
      if (!from || pinned.has(from)) from = leafIds(splitRoot, []).find((id) => !pinned.has(id)) || "";
      if (from) splitRoot = retargetLeaf(splitRoot, from, sessionId);
    }
    focusLeaf(sessionId, { rebuild: true });
    if (sessionList) {
      sessionList.querySelectorAll(".term-session-row").forEach((row) => row.classList.remove("active"));
    }
    if (listOpen) refreshSessionList();
    if (deps.onTabsChanged) deps.onTabsChanged(openTabCount());
    paintDockBadge();
  }

  function clearEmptySidePanels() {
    if (sideMeta) sideMeta.textContent = "";
    if (sideParticipants) sideParticipants.replaceChildren();
    if (sideControl) sideControl.replaceChildren();
  }

  function focusTabAfterClose(closedId) {
    if (activeTabId !== closedId) {
      renderSplit();
      return;
    }
    const next = leafIds(splitRoot, [])[0] || tabs.keys().next().value || "";
    activeTabId = "";
    if (next) selectTab(next);
    else {
      clearEmptySidePanels();
      renderSplit();
    }
  }

  function closeTab(sessionId) {
    const tab = tabs.get(sessionId);
    if (!tab) return;
    if (resizeObserver) {
      try { resizeObserver.unobserve(tab.viewEl); } catch { /* ignore */ }
    }
    const self = selfDeviceId();
    const remote = tab.isRemote() || !!(tab.meta?.host_device_id && self && tab.meta.host_device_id !== self);
    const api = tab.api();
    tab.dispose();
    tab.tabWrap.remove();
    tab.viewEl.remove();
    tabs.delete(sessionId);
    pinned.delete(sessionId);
    savePins();
    const ending = remote ? fetch(api + "/detach", { method: "POST" }) : purgeSession(sessionId);
    Promise.resolve(ending).catch(() => {}).then(() => { if (listOpen) refreshSessionList(); });
    splitRoot = removeLeaf(splitRoot, sessionId);
    paintDockBadge();
    focusTabAfterClose(sessionId);
    if (deps.onTabsChanged) deps.onTabsChanged(openTabCount());
  }

  function openTabCount() {
    let live = 0;
    tabs.forEach((tab) => { if (!tab.dead) live += 1; });
    return live;
  }

  function renderSide(tab) {
    if (!tab?.meta) {
      if (sideMeta) sideMeta.textContent = "";
      if (sideParticipants) sideParticipants.replaceChildren();
      if (sideControl) sideControl.replaceChildren();
      return;
    }
    const s = tab.meta;
    if (sideMeta) {
      const state = (s.state || "").toLowerCase();
      const wd = shortPath(s.working_directory);
      sideMeta.textContent = [sessionStateLabel(s.state), wd, s.command].filter(Boolean).join(" · ");
      sideMeta.classList.toggle("term-side-meta--failed", state === "failed");
    }
    if (badgeRec) badgeRec.hidden = !s.recording_paused;
    if (badgePrivate) badgePrivate.hidden = !s.private_mode;
    fetchParticipants(tab);
    renderControlActions(tab);
  }

  async function fetchParticipants(tab) {
    if (!sideParticipants) return;
    let body;
    try {
      const res = await fetch(tab.api() + "/participants", { cache: "no-store" });
      if (!res.ok) return;
      body = await res.json();
    } catch (err) {
      if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
      return;
    }
    if (tab.disposed || tab.sessionId !== activeTabId) return;
    const self = selfDeviceId();
    const isOwner = isSessionOwner(tab) && !tab.isRemote();
    const seen = new Set();
    const items = [];
    (body.participants || []).forEach((p) => {
      const key = p.device_id || p.id;
      if (seen.has(key)) return;
      seen.add(key);
      const label = peerLabel(p.device_id) + " · " + participantRoleLabel(p.role);
      const item = el("li", "", label);
      if (isOwner && p.device_id && p.device_id !== self) item.appendChild(controlHandoffButton(tab, p.device_id));
      items.push(item);
    });
    sideParticipants.replaceChildren(...items);
  }

  // The owner hands the lease to a viewer, or takes it back; the host enforces it on every write.
  function controlHandoffButton(tab, deviceId) {
    const holds = !!(tab.lease && tab.lease.holder_device_id === deviceId);
    const pending = (tab.pendingRequesters || []).includes(deviceId);
    const cls = "term-ctl term-ctl--inline" + (pending && !holds ? " term-ctl--accent" : "");
    const btn = el("button", cls, holds ? "Take back" : "Give control");
    btn.type = "button";
    btn.addEventListener("click", async () => {
      const body = holds
        ? { target_device_id: deviceId, reason: "owner took back control" }
        : { target_device_id: deviceId, epoch: tab.lease ? tab.lease.epoch : 0 };
      try {
        const res = await fetch(tab.api() + "/control/" + (holds ? "revoke" : "grant"), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body)
        });
        if (!res.ok) tab.statusEl.textContent = "Could not change control.";
      } catch (err) {
        if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
        tab.statusEl.textContent = "Could not change control.";
      }
      await refreshLease(tab);
      renderSide(tab);
    });
    return btn;
  }

  function renderControlActions(tab) {
    if (!sideControl) return;
    sideControl.replaceChildren();
    const self = selfDeviceId();
    const isOwner = tab.meta && self && tab.meta.owner_device_id === self;
    const holdsLease = tab.lease && (!tab.lease.holder_device_id || tab.lease.holder_device_id === self);
    if (isOwner && holdsLease) {
      const hint = el("p", "term-ctl-hint", "You control this session.");
      sideControl.appendChild(hint);
      return;
    }
    const base = tab.api() + "/control/";
    const req = el("button", "term-ctl", isOwner ? "Take control" : "Request control");
    req.type = "button";
    req.addEventListener("click", async () => {
      const body = isOwner ? { target_device_id: self, epoch: tab.lease ? tab.lease.epoch : 0 } : {};
      try {
        const res = await fetch(base + (isOwner ? "grant" : "request"), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body)
        });
        if (!res.ok) tab.statusEl.textContent = "Could not change control.";
      } catch (err) {
        if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
        tab.statusEl.textContent = "Could not change control.";
      }
      await refreshLease(tab);
      renderControlActions(tab);
    });
    if (!holdsLease) {
      if (!isOwner) sideControl.appendChild(el("p", "term-ctl-hint", "You're watching. Request control, then the owner gives it to you."));
      sideControl.append(req);
      return;
    }
    const rel = el("button", "term-ctl", "Release control");
    rel.type = "button";
    rel.addEventListener("click", async () => {
      if (!tab.lease) return;
      try {
        await fetch(base + "release", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            lease_id: tab.lease.id || tab.lease.ID,
            epoch: tab.lease.epoch ?? tab.lease.Epoch
          })
        });
      } catch { /* refreshed below */ }
      await refreshLease(tab);
      renderControlActions(tab);
    });
    sideControl.append(rel);
  }

  async function refreshLease(tab) {
    if (!tab || tab.disposed) return;
    let body;
    try {
      const res = await fetch(tab.api(), { cache: "no-store" });
      if (!res.ok) return;
      body = await res.json();
    } catch (err) {
      if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
      return;
    }
    if (tab.disposed) return;
    tab.lease = parseLease(body.lease);
    tab.pendingRequesters = parsePendingRequesters(body);
    tab.syncStdin();
    updateChrome();
  }

  function updateStatusPill(tab) {
    if (!termStatusPill || !tab?.meta) {
      if (termStatusPill) termStatusPill.hidden = true;
      return;
    }
    const st = String(tab.meta.state || "").toLowerCase();
    termStatusPill.textContent = statusPillText(tab);
    termStatusPill.title = tab.title || tab.meta.working_directory || statusPillText(tab);
    termStatusPill.classList.toggle("term-status-pill--live", st === "running");
    termStatusPill.classList.toggle("term-status-pill--failed", st === "failed");
    termStatusPill.hidden = false;
  }

  function syncControlBadge(tab, grantQueue, awaiting) {
    if (!badgeControl) return;
    if (grantQueue) {
      const n = tab.pendingRequesters.length;
      badgeControl.hidden = false;
      badgeControl.disabled = false;
      badgeControl.textContent = n === 1 ? "Grant control" : "Grant control (" + n + ")";
      badgeControl.title = "Open inspector to approve";
      badgeControl.classList.add("term-badge--grant");
      badgeControl.classList.remove("term-badge--waiting");
      badgeControl.setAttribute("aria-label", badgeControl.textContent);
      return;
    }
    if (awaiting) {
      badgeControl.hidden = false;
      badgeControl.disabled = true;
      badgeControl.textContent = "Awaiting approval";
      badgeControl.title = "Waiting for the session owner to grant control";
      badgeControl.classList.add("term-badge--waiting");
      badgeControl.classList.remove("term-badge--grant");
      badgeControl.setAttribute("aria-label", badgeControl.textContent);
      return;
    }
    badgeControl.hidden = true;
    badgeControl.textContent = "";
    badgeControl.classList.remove("term-badge--grant", "term-badge--waiting");
  }

  function updateChrome() {
    const tab = tabs.get(activeTabId);
    const self = selfDeviceId();
    const holdsLease = tab?.lease && (!tab.lease.holder_device_id || tab.lease.holder_device_id === self);
    const owner = isSessionOwner(tab) && tab && !tab.isRemote();
    const running = tab?.meta && String(tab.meta.state).toUpperCase() === "RUNNING";
    const grantQueue = owner && running && (tab.pendingRequesters || []).length;
    const awaiting = running && !holdsLease && !owner;
    syncControlBadge(tab, grantQueue, awaiting);
    if (badgeRec && tab?.meta) badgeRec.hidden = !tab.meta.recording_paused;
    if (badgePrivate && tab?.meta) badgePrivate.hidden = !tab.meta.private_mode;
    if (tab) tab.syncStdin();
  }

  const sessionLive = terminalSessionAttachable;
  const sessionsFromApiBody = terminalSessionsFromApiBody;

  const purgeSession = async (id) => {
    if (!id) return;
    try {
      await fetch("/api/v1/terminal/sessions/" + encodeURIComponent(id), { method: "DELETE" });
    } catch { /* offline: the list refresh shows what survived */ }
  }

  function mergeOpenTabsIntoSessions(sessions) {
    const byId = new Map();
    sessions.forEach((s) => {
      if (s?.id) byId.set(s.id, s);
    });
    tabs.forEach((tab, id) => {
      if (!byId.has(id)) {
        byId.set(id, tab.meta || {
          id,
          name: tab.displayName(),
          state: tab.dead ? "EXITED" : "RUNNING",
          working_directory: tab.meta?.working_directory || ""
        });
      }
    });
    return Array.from(byId.values());
  }

  async function refreshSessionList() {
    if (!sessionList) return [];
    let sessions = [];
    try {
      const res = await fetch("/api/v1/terminal/sessions", { cache: "no-store" });
      if (res.ok) {
        const body = await res.json();
        sessions = sessionsFromApiBody(body);
      }
    } catch { /* offline */ }
    sessions = mergeOpenTabsIntoSessions(sessions);
    sessionList.replaceChildren();
    const live = sessions.filter(sessionLive);
    const dead = sessions.filter((s) => terminalSessionEnded(s));
    if (!sessions.length) {
      sessionList.appendChild(el("p", "term-session-empty", "No sessions yet."));
      return sessions;
    }
    live.forEach((sess) => {
      const open = tabs.get(sess.id);
      const row = el("div", "term-session-row");
      row.classList.toggle("active", sess.id === activeTabId);
      row.classList.toggle("open", !!open);
      const main = el("button", "term-session-main");
      main.type = "button";
      const rowName = open ? open.displayName() : friendlySessionName(sess, "Terminal");
      main.appendChild(el("span", "term-session-name", rowName));
      main.appendChild(el("span", "term-session-sub", sessionSubline(sess)));
      main.addEventListener("click", () => openSession(sess.id, sess.name));
      const kill = el("button", "term-session-kill", "×");
      kill.type = "button";
      kill.title = "End session";
      kill.setAttribute("aria-label", "End session");
      kill.addEventListener("click", async (ev) => {
        ev.stopPropagation();
        if (tabs.has(sess.id)) closeTab(sess.id);
        else await purgeSession(sess.id);
        await refreshSessionList();
      });
      row.append(main, kill);
      sessionList.appendChild(row);
    });
    if (dead.length) {
      sessionList.appendChild(el("p", "term-sidebar-section-label", "Ended"));
      const clear = el("button", "term-sidebar-btn term-clear-dead", "Clear " + dead.length + " ended");
      clear.type = "button";
      clear.addEventListener("click", async () => {
        for (const sess of dead) await purgeSession(sess.id);
        await refreshSessionList();
      });
      sessionList.appendChild(clear);
    }
    return sessions;
  }

  async function openSession(sessionId, name, opts) {
    if (!sessionId) return;
    const focus = opts?.focus !== false;
    let tab = tabs.get(sessionId);
    if (!tab) {
      tab = new TermTab(sessionId, name, opts?.host);
      tabs.set(sessionId, tab);
      if (tabsScroll) tabsScroll.appendChild(tab.tabWrap);
      ensureRoot(sessionId, {
        preserveSplitRoot: !!(opts?.preserveSplitRoot),
        setFocus: !(opts?.preserveSplitRoot),
      });
      orderTabs();
      renderSplit();
      if (!resizeObserver && typeof ResizeObserver !== "undefined") {
        resizeObserver = new ResizeObserver((entries) => terminalResizeObserverCallback(entries, tabs));
      }
      if (resizeObserver) resizeObserver.observe(tab.viewEl);
      if (deps.onTabsChanged) deps.onTabsChanged(openTabCount());
    }
    if (focus || !activeTabId) selectTab(sessionId);
    else tab.tabWrap.classList.toggle("active", sessionId === activeTabId);
    if (!panelVisible()) showPanel();
    await tab.refreshMeta();
    if (terminalSessionEnded(tab.meta)) {
      tab.showEnded("Session ended. Use + to open a new terminal.");
      updateChrome();
      showPanel();
      return;
    }
    await tab.connect();
    await refreshLease(tab);
    showPanel();
  }

  async function adoptCreatedTerminalSession(sess, lease, opts, replacedDead) {
    const forSplit = !!(opts?.forSplit);
    if (!sess?.id) return "";
    await openSession(sess.id, sess.name, { focus: !forSplit, preserveSplitRoot: forSplit });
    const tab = tabs.get(sess.id);
    if (tab && lease) {
      tab.lease = lease;
      tab.syncStdin();
    }
    const bornDead = !!(tab && (tab.dead || terminalSessionEnded(sess)));
    if (tab && !tab.dead && terminalSessionEnded(sess)) {
      tab.showEnded(sess.error_message || "Failed to start terminal.");
    } else if (!forSplit && !bornDead && replacedDead && replacedDead !== sess.id && tabs.has(replacedDead)) {
      closeTab(replacedDead);
    }
    await refreshSessionList();
    return sess.id;
  }

  async function createSession(opts) {
    const replacedDead = activeTabId && tabs.get(activeTabId)?.dead ? activeTabId : "";
    const circle = deps.getData?.();
    const circleId = circle?.circles?.[0]?.id;
    const cols = lastSentSize.cols || 80;
    const rows = lastSentSize.rows || 24;
    const projectRoot = deps.getProjectRoot?.() ?? "";
    let res;
    try {
      res = await postNewTerminalSession(circleId, projectRoot, cols, rows);
    } catch (err) {
      if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
      notifyCreateFailed("Could not start terminal.");
      return "";
    }
    if (!res.ok) {
      notifyCreateFailed(await terminalCreateErrorDetail(res));
      return "";
    }
    const body = await res.json();
    return adoptCreatedTerminalSession(body.session, parseLease(body.lease), opts, replacedDead);
  }

  function notifyCreateFailed(detail) {
    if (sessionList) {
      sessionList.replaceChildren();
      sessionList.appendChild(el("p", "term-session-empty", detail));
    }
    const tab = tabs.get(activeTabId);
    if (tab && !tab.dead) tab.statusEl.textContent = detail;
    if (typeof globalThis.overdriveComposerStatus === "function") globalThis.overdriveComposerStatus(detail, 4500);
  }

  function showPanel() {
    if (!root) return;
    const wasHidden = root.hidden;
    root.hidden = false;
    root.setAttribute("aria-hidden", "false");
    document.getElementById("termApp")?.setAttribute("aria-pressed", "true");
    root.classList.remove("term-inactive");
    raise();
    applyGeo();
    fitActiveSoon(wasHidden);
    paintDockBadge();
  }

  function hidePanel() {
    if (!root) return;
    root.hidden = true;
    root.setAttribute("aria-hidden", "true");
    document.getElementById("termApp")?.setAttribute("aria-pressed", "false");
    paintDockBadge();
  }

  function openPanelWithSessions() {
    showPanel();
    return refreshSessionList().then(() => {
      if (tabs.size === 0) return ensureDefaultSession();
    });
  }

  function togglePanel() {
    if (!root) return;
    if (root.hidden) openPanelWithSessions();
    else hidePanel();
  }

  function openFromHash() {
    if (location.hash.replace("#", "") === "live-sessions" || location.hash === "#terminals") {
      openPanelWithSessions();
    }
  }

  function toggleExpanded() {
    if (!root) return;
    geo.expanded = !geo.expanded;
    geo.dock = "";
    if (geo.expanded) {
      const v = viewportBox();
      geo.w = Math.min(1120, v.w - 36);
      geo.h = Math.min(700, v.h - 112);
      geo.x = (v.w - geo.w) / 2;
      geo.y = Math.max(44, (v.h - geo.h) / 2);
    }
    if (btnExpand) btnExpand.setAttribute("aria-pressed", String(!!geo.expanded));
    applyGeo();
    fitActiveSoon();
  }

  if (btnNew) btnNew.addEventListener("click", () => createSession());
  if (btnTabAdd) btnTabAdd.addEventListener("click", () => createSession());
  if (btnClosePanel) btnClosePanel.addEventListener("click", hidePanel);
  const trafficClose = document.getElementById("termTrafficClose");
  const trafficMin = document.getElementById("termTrafficMin");
  const trafficMax = document.getElementById("termTrafficMax");
  if (trafficClose) trafficClose.addEventListener("click", hidePanel);
  if (trafficMin) trafficMin.addEventListener("click", hidePanel);
  if (trafficMax) trafficMax.addEventListener("click", toggleExpanded);
  if (btnExpand) btnExpand.addEventListener("click", toggleExpanded);
  if (btnToggleList) {
    btnToggleList.addEventListener("click", () => {
      listOpen = !listOpen;
      syncLayoutClasses();
      if (listOpen) refreshSessionList();
      fitActiveSoon();
    });
  }
  if (btnToggleInspector) {
    btnToggleInspector.addEventListener("click", () => {
      inspectorOpen = !inspectorOpen;
      syncLayoutClasses();
      fitActiveSoon();
    });
  }
  if (badgeControl) {
    badgeControl.addEventListener("click", () => {
      if (badgeControl.hidden || badgeControl.disabled) return;
      openInspectorPanel();
      const tab = tabs.get(activeTabId);
      if (tab) renderSide(tab);
      highlightInspectorParticipants();
    });
  }
  const bgOpacitySlider = document.getElementById("termBgOpacity");
  if (bgOpacitySlider) {
    applyTermBgOpacity(loadBgOpacityPercent());
    bgOpacitySlider.addEventListener("input", () => applyTermBgOpacity(bgOpacitySlider.value));
  }
  globalThis.addEventListener("resize", () => { applyGeo(); fitActiveSoon(); });
  syncLayoutClasses();
  bindWindowChrome();

  async function ensureDefaultSession() {
    if (creatingDefault || tabs.size > 0) return;
    creatingDefault = true;
    try {
      const sessions = await refreshSessionList();
      const dead = (sessions || []).filter((s) => terminalSessionEnded(s));
      for (const sess of dead) await purgeSession(sess.id);
      const self = selfDeviceId();
      const mine = (sessions || [])
        .filter((s) => sessionLive(s) && (!self || s.host_device_id === self))
        .sort((a, b) => String(a.created_at).localeCompare(String(b.created_at)));
      if (mine.length) {
        for (let i = 0; i < mine.length; i++) {
          await openSession(mine[i].id, mine[i].name, { focus: i === mine.length - 1 });
        }
        await refreshSessionList();
        return;
      }
      await createSession();
    } finally {
      creatingDefault = false;
    }
  }

  // Control changes made on another device (grant, revoke, a new viewer) have no push channel here.
  async function pollActiveControl() {
    const tab = tabs.get(activeTabId);
    if (!tab || tab.dead || tab.disposed || !panelVisible() || document.hidden) return;
    const before = JSON.stringify(tab.lease);
    await refreshLease(tab);
    if (tabs.get(activeTabId) !== tab || tab.disposed) return;
    if (JSON.stringify(tab.lease) === before) fetchParticipants(tab);
    else renderSide(tab);
  }
  if (!pollTimer) pollTimer = setInterval(() => { pollActiveControl().catch(() => {}); }, CONTROL_POLL_MS);

  globalThis.overdriveOpenTerminal = (payload) => {
    if (!payload || typeof payload !== "object") return;
    const id = payload.sessionId || payload.session_id;
    if (!id) return;
    const focus = payload.focus !== false;
    openSession(id, payload.title || payload.name, { focus, host: payload.host || "" });
  };

  return {
    openSession,
    openTabCount,
    createSession,
    ensureDefaultSession,
    togglePanel,
    showPanel,
    hidePanel,
    raise,
    lowerUnlessPinned,
    openFromHash,
    refreshSessionList,
    shareActiveToChat,
    apply() {
      refreshSessionList();
      const tab = tabs.get(activeTabId);
      if (tab) tab.refreshMeta().catch(() => {});
    },
    destroy() {
      tabs.forEach((tab) => tab.dispose());
      tabs.clear();
      if (resizeObserver) resizeObserver.disconnect();
      if (pollTimer) clearInterval(pollTimer);
    }
  };
}

export {
  shortTerminalPath,
  terminalPathBasename,
  friendlyTerminalSessionName,
  clampSplitRatio,
  terminalLooksLikeOpaqueToken,
  terminalSessionAttachable,
  terminalSessionEnded,
  terminalSessionApi
};

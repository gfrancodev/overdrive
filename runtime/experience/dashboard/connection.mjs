// Connection health in the mesh dashboard: the menu bar's network icon shows the overall state and opens a
// sheet that says, per capability, what works, what does not, why, and the fix. The Core derives all of it.

const HEALTH_LABELS = {
  connected: "Connected",
  degraded: "Partly connected",
  reconnecting: "Reconnecting",
  offline: "Offline",
  blocked: "Blocked",
};

const CAPABILITY_TITLES = { chat: "Chat", calls: "Calls", memory: "Experience", terminal: "Terminals" };

const HEALTH_ACTIONS = {
  resume_mesh: "Resume mesh",
  allow_network: "Allow devices on this network",
  share_folder: "Share a folder",
  open_setup: "Get started",
};

function healthLabel(state) {
  return HEALTH_LABELS[state] || "Checking";
}

function capabilityTitle(id) {
  return CAPABILITY_TITLES[id] || String(id || "");
}

function healthActionLabel(action) {
  return HEALTH_ACTIONS[action] || "";
}

// reachText is how one device in the circle answered this one.
function reachText(peer) {
  switch (peer?.reach) {
    case "ok":
      return peer.rtt_ms > 0 ? "Answers in " + peer.rtt_ms + " ms" : "Answers";
    case "refused":
      return peer.local_only ? "Accepts connections only from itself" : "Refuses connections";
    case "timeout":
      return "Doesn't answer";
    case "unreachable":
      return "No route from this device";
    case "no_address":
      return "No address yet";
    case "self_address":
      return "Registered an address that only works on its own computer";
    default:
      return "Not checked";
  }
}

// connectionSummary is the one line under the sheet title and on the menu bar icon.
function connectionSummary(health) {
  const caps = health?.capabilities || [];
  if (!caps.length) return "Checking the connection…";
  if (health.state === "connected") {
    const n = (health.peers || []).filter((p) => p.reach === "ok").length;
    return n ? "Connected to " + n + " " + (n === 1 ? "device" : "devices") + "." : "Everything is connected.";
  }
  const names = caps.filter((c) => c.state === health.state).map((c) => capabilityTitle(c.id));
  const list = names.length > 1 ? names.slice(0, -1).join(", ") + " and " + names[names.length - 1] : names[0] || "";
  const verb = names.length > 1 ? " are " : " is ";
  return list + verb + healthLabel(health.state).toLowerCase() + ".";
}

// networkText says who can connect to Overdrive on this device.
function networkText(network) {
  if (!network) return "";
  if (!network.lan_access) {
    return "Only this computer can pair, sync experience or open terminals with Overdrive here (" + network.listen + "). Devices on the network see it but can't do those. Circle chat, open only to members, still crosses the network.";
  }
  return "Devices on this network connect to " + (network.address || network.listen) + ". Only circle members get in, and pairing still needs the code confirmed on both screens.";
}

// healthActions is each fix the sheet offers once, in the order of the capabilities that need it.
function healthActions(health) {
  const out = [];
  (health?.capabilities || []).forEach((c) => {
    if (c.action && HEALTH_ACTIONS[c.action] && !out.includes(c.action)) out.push(c.action);
  });
  return out;
}

function diagnosticsText(report) {
  return JSON.stringify(report || {}, null, 2);
}

async function readHealthJSON(res) {
  try {
    return await res.json();
  } catch {
    return {};
  }
}

function createConnectionPanel({ listRow, onOpen, openFolders, openSetup } = {}) {
  const byId = (id) => document.getElementById(id);
  let health = null;

  function setStatus(text, id = "healthStatus") {
    const node = byId(id);
    if (node) node.textContent = text || "";
  }
  function stateDot(state) {
    const node = document.createElement("i");
    node.className = "dot health-" + (state || "checking");
    return node;
  }
  function actionButton(action) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "action-fill";
    button.textContent = healthActionLabel(action);
    button.addEventListener("click", (ev) => {
      ev.stopPropagation();
      runAction(action).catch(() => {});
    });
    return button;
  }

  function renderBar() {
    const btn = byId("barListen");
    const field = byId("barListenField");
    if (!btn) return;
    if (field) field.hidden = false;
    const state = health?.state || "checking";
    btn.dataset.health = state;
    const label = "Connection: " + healthLabel(health?.state) + ". " + connectionSummary(health);
    btn.setAttribute("aria-label", label);
    btn.title = label;
  }
  function renderSheet() {
    const caps = byId("healthCaps");
    const peers = byId("healthPeers");
    if (!caps || !peers) return;
    byId("healthTitle").textContent = "Connection · " + healthLabel(health?.state);
    byId("healthSummary").textContent = connectionSummary(health);
    const actions = healthActions(health);
    const fixes = byId("healthFixes");
    if (fixes) {
      fixes.replaceChildren(...actions.map(actionButton));
      fixes.hidden = actions.length === 0;
    }
    caps.replaceChildren();
    (health?.capabilities || []).forEach((c) => {
      const detail = c.fix ? c.detail + " " + c.fix : c.detail;
      caps.appendChild(listRow({ lead: stateDot(c.state), title: capabilityTitle(c.id) + " · " + healthLabel(c.state), detail, wrap: true }));
    });
    peers.replaceChildren();
    const list = health?.peers || [];
    if (!list.length) peers.appendChild(listRow({ title: "No other device yet", quiet: true }));
    list.forEach((p) => {
      peers.appendChild(listRow({
        lead: stateDot(p.reach === "ok" ? "connected" : "offline"),
        title: p.name,
        detail: reachText(p) + (p.endpoint ? " · " + p.endpoint : ""),
        wrap: true,
      }));
    });
    const network = health?.network;
    byId("healthNetwork").textContent = networkText(network);
    const toggle = byId("networkToggle");
    if (toggle) {
      // A fix above already offers to let the network in.
      toggle.hidden = !network || actions.includes("allow_network");
      toggle.textContent = network?.lan_access ? "Accept connections only from this computer" : "Allow devices on this network";
    }
    const nearbyHint = byId("nearbyNetworkHint");
    if (nearbyHint) nearbyHint.hidden = !network || network.lan_access;
  }
  function render() {
    renderBar();
    renderSheet();
  }

  async function refresh() {
    const res = await fetch("/api/connections/health", { cache: "no-store" });
    if (!res.ok) return;
    health = await readHealthJSON(res);
    render();
  }
  async function setNetwork(lanAccess, statusId = "healthStatus") {
    setStatus(lanAccess ? "Opening this device to the network…" : "Closing this device to the network…", statusId);
    const res = await fetch("/api/network", {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ lan_access: lanAccess }),
    });
    const body = await readHealthJSON(res);
    if (!res.ok) {
      setStatus(body.error || "Could not change who can connect.", statusId);
      return;
    }
    setStatus(lanAccess ? "Devices on this network can connect now. Your system may ask to allow Overdrive through its firewall." : "Only this computer can pair, sync or open terminals with this device now.", statusId);
    await refresh();
  }
  async function runAction(action) {
    switch (action) {
      case "resume_mesh":
        await fetch("/api/mesh/resume", { method: "POST" });
        await refresh();
        break;
      case "allow_network":
        await setNetwork(true);
        break;
      case "share_folder":
        close();
        if (openFolders) openFolders();
        break;
      case "open_setup":
        close();
        if (openSetup) openSetup();
        break;
    }
  }
  async function copyDiagnostics() {
    setStatus("");
    const res = await fetch("/api/diagnostics", { cache: "no-store" });
    if (!res.ok) {
      setStatus("Could not build the diagnostics.");
      return;
    }
    const text = diagnosticsText(await readHealthJSON(res));
    try {
      await navigator.clipboard.writeText(text);
      setStatus("Diagnostics copied. They hold no keys, tokens or folder names.");
    } catch {
      const link = document.createElement("a");
      link.href = URL.createObjectURL(new Blob([text], { type: "application/json" }));
      link.download = "overdrive-diagnostics.json";
      link.click();
      URL.revokeObjectURL(link.href);
      setStatus("Diagnostics saved as overdrive-diagnostics.json. They hold no keys, tokens or folder names.");
    }
  }

  function isOpen() {
    const panel = byId("health");
    return Boolean(panel && !panel.hidden);
  }
  function open() {
    const panel = byId("health");
    if (!panel) return;
    if (onOpen) onOpen();
    setStatus("");
    panel.hidden = false;
    byId("barListen")?.setAttribute("aria-expanded", "true");
    refresh().catch(() => {});
  }
  function close() {
    const panel = byId("health");
    if (!panel || panel.hidden) return;
    panel.hidden = true;
    byId("barListen")?.setAttribute("aria-expanded", "false");
  }

  byId("barListen")?.addEventListener("click", (ev) => {
    ev.stopPropagation();
    if (isOpen()) close();
    else open();
  });
  byId("health")?.addEventListener("click", (ev) => ev.stopPropagation());
  byId("healthClose")?.addEventListener("click", close);
  byId("healthCopy")?.addEventListener("click", () => { copyDiagnostics().catch(() => {}); });
  byId("networkToggle")?.addEventListener("click", () => { setNetwork(!health?.network?.lan_access).catch(() => {}); });
  byId("nearbyAllowNetwork")?.addEventListener("click", () => { setNetwork(true, "nearbyNetworkStatus").catch(() => {}); });
  byId("settingsHealth")?.addEventListener("click", () => open());

  return { refresh, open, close, isOpen };
}

export {
  capabilityTitle,
  connectionSummary,
  createConnectionPanel,
  diagnosticsText,
  healthActionLabel,
  healthActions,
  healthLabel,
  networkText,
  reachText
};

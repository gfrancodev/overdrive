// Onboarding and pairing for the mesh dashboard. The steps come from the Core (/api/setup/status), never from a
// flag kept in the browser, and a pairing always shows the code both people compare before anything joins.

const ACTIVE_CIRCLE_STORAGE_KEY = "overdriveActiveCircle";

const SETUP_STEPS = {
  circle: { title: "Create or join a circle", done: "This device is in a circle" },
  peer: { title: "Pair another device", done: "Another device is in your circle" },
  folder: { title: "Share a project folder", done: "A project folder is shared" },
};

function setupStepText(step) {
  const text = SETUP_STEPS[step?.id] || { title: String(step?.id || ""), done: String(step?.id || "") };
  return step?.done ? text.done : text.title;
}

function setupComplete(status) {
  return status?.next === "ready";
}

function pickActiveCircle(circles, savedId) {
  const list = Array.isArray(circles) ? circles.filter(Boolean) : [];
  return list.find((c) => c.id === savedId) || list[0] || null;
}

function formatPairingCode(code) {
  const digits = String(code || "").replace(/\D/g, "");
  return digits.length === 6 ? digits.slice(0, 3) + " " + digits.slice(3) : digits;
}

function pairingPeerName(view) {
  return String(view?.hostname || view?.device_id || "the other device");
}

// pairingPrompt says what one pairing needs from this person. Actions: "confirm", "cancel", "close".
function pairingPrompt(view) {
  const name = pairingPeerName(view);
  const member = view?.role === "member";
  switch (view?.state) {
    case "connecting":
      return { title: "Connecting to " + name, body: "Exchanging keys…", actions: ["cancel"], code: false, circle: false };
    case "comparing":
      if (view.confirmed) {
        return { title: "Waiting for " + name, body: "You confirmed the code. " + name + " still has to confirm it.", actions: ["cancel"], code: true, circle: false };
      }
      if (member) {
        return {
          title: name + " wants to join",
          body: "Check that " + name + " shows the same code. Only then choose the circle and add it.",
          actions: ["confirm", "cancel"], code: true, circle: true,
        };
      }
      return { title: "Compare this code", body: "Check that " + name + " shows the same code. If it differs, cancel: someone may be in between.", actions: ["confirm", "cancel"], code: true, circle: false };
    case "waiting":
      return { title: "Waiting for " + name, body: "You confirmed the code. " + name + " still has to confirm it.", actions: ["cancel"], code: true, circle: false };
    case "joined":
      return { title: member ? name + " joined" : "This device joined the circle", body: member ? name + " can now chat, call and sync with you." : "You can now chat, call and sync with " + name + ".", actions: ["close"], code: false, circle: false };
    case "rejected":
      return { title: "Pairing cancelled", body: "Nothing was added.", actions: ["close"], code: false, circle: false };
    case "expired":
      return { title: "Pairing expired", body: "Codes are valid for two minutes. Start again from Nearby.", actions: ["close"], code: false, circle: false };
    default:
      return { title: "Pairing failed", body: view?.error ? String(view.error) : "Nothing was added.", actions: ["close"], code: false, circle: false };
  }
}

// pairingToShow picks the session the pairing sheet should show: a device asking to join this one comes first.
function pairingToShow(sessions, dismissed) {
  const list = (Array.isArray(sessions) ? sessions : []).filter((s) => s && !dismissed.has(s.id));
  const open = (s) => ["connecting", "comparing", "waiting"].includes(s.state);
  return list.find((s) => s.role === "member" && s.state === "comparing" && !s.confirmed)
    || list.find((s) => s.role === "joiner" && open(s))
    || list.find(open)
    || null;
}

async function readJSONResponse(res) {
  try {
    return await res.json();
  } catch {
    return {};
  }
}

function createOnboarding({ onCircleChange, openNearby, openConnection } = {}) {
  const byId = (id) => document.getElementById(id);
  const dismissed = new Set();
  let shownPairing = "";
  let circles = [];
  let autoOpened = false;

  function storedCircle() {
    try {
      return globalThis.localStorage?.getItem(ACTIVE_CIRCLE_STORAGE_KEY) || "";
    } catch {
      return "";
    }
  }
  function activeCircleId() {
    return pickActiveCircle(circles, storedCircle())?.id || storedCircle();
  }
  function setActiveCircle(id) {
    try {
      globalThis.localStorage?.setItem(ACTIVE_CIRCLE_STORAGE_KEY, id);
    } catch { /* the choice lasts for this page only */ }
    fillCircleSelects();
    if (onCircleChange) onCircleChange(id);
  }
  function fillCircleSelect(select, selected) {
    if (!select) return;
    select.replaceChildren();
    circles.forEach((c) => {
      const option = document.createElement("option");
      option.value = c.id;
      option.textContent = c.name || c.id;
      option.selected = c.id === selected;
      select.appendChild(option);
    });
    select.disabled = circles.length === 0;
  }
  function fillCircleSelects() {
    fillCircleSelect(byId("activeCircle"), activeCircleId());
    fillCircleSelect(byId("pairingCircle"), activeCircleId());
    const field = byId("activeCircleField");
    if (field) field.hidden = circles.length < 2;
  }
  async function refreshCircles() {
    try {
      const res = await fetch("/api/circles", { cache: "no-store" });
      if (res.ok) circles = (await res.json()).circles || [];
    } catch (err) {
      globalThis.console?.debug?.(err);
    }
    fillCircleSelects();
    return circles;
  }

  function renderSteps(status) {
    const list = byId("setupSteps");
    if (!list) return;
    list.replaceChildren();
    (status?.steps || []).forEach((step) => {
      const item = document.createElement("li");
      item.className = step.done ? "setup-step done" : "setup-step";
      if (status.next === step.id) item.classList.add("next");
      item.textContent = setupStepText(step);
      list.appendChild(item);
    });
    const createRow = byId("setupCreate");
    if (createRow) createRow.hidden = status?.next !== "circle";
    const pairRow = byId("setupPairRow");
    if (pairRow) pairRow.hidden = !["circle", "peer"].includes(status?.next);
    const folderRow = byId("setupFolderRow");
    if (folderRow) folderRow.hidden = status?.next !== "folder";
    const done = byId("setupDone");
    if (done) done.hidden = !setupComplete(status);
  }
  async function refreshSetup() {
    try {
      const res = await fetch("/api/setup/status", { cache: "no-store" });
      if (!res.ok) return null;
      const status = await res.json();
      renderSteps(status);
      if (!autoOpened && !setupComplete(status)) {
        autoOpened = true;
        if (!sessionStorageFlag()) openSetup();
      }
      return status;
    } catch (err) {
      globalThis.console?.debug?.(err);
      return null;
    }
  }
  function sessionStorageFlag() {
    try {
      const seen = globalThis.sessionStorage?.getItem("overdriveSetupShown") === "1";
      globalThis.sessionStorage?.setItem("overdriveSetupShown", "1");
      return seen;
    } catch {
      return false;
    }
  }
  function openSetup() {
    const panel = byId("setup");
    if (panel) panel.hidden = false;
  }
  function closeSetup() {
    const panel = byId("setup");
    if (panel) panel.hidden = true;
  }
  async function createCircle() {
    const field = byId("setupCircleName");
    const status = byId("setupStatus");
    const name = String(field?.value || "").trim();
    if (!name) {
      if (status) status.textContent = "Give the circle a name.";
      return;
    }
    const res = await fetch("/api/circles", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name }) });
    const body = await readJSONResponse(res);
    if (!res.ok) {
      if (status) status.textContent = body.error || "Could not create the circle.";
      return;
    }
    if (field) field.value = "";
    if (status) status.textContent = "Circle " + name + " created.";
    await refreshCircles();
    setActiveCircle(body.circle?.id || "");
    await refreshSetup();
  }

  function renderPairing(view) {
    const panel = byId("pairing");
    if (!panel) return;
    if (!view) {
      panel.hidden = true;
      shownPairing = "";
      return;
    }
    shownPairing = view.id;
    const prompt = pairingPrompt(view);
    byId("pairingTitle").textContent = prompt.title;
    byId("pairingBody").textContent = prompt.body;
    const code = byId("pairingCode");
    code.hidden = !prompt.code || !view.code;
    code.textContent = formatPairingCode(view.code);
    const circleField = byId("pairingCircleField");
    if (circleField) circleField.hidden = !prompt.circle;
    byId("pairingConfirm").hidden = !prompt.actions.includes("confirm");
    byId("pairingConfirm").textContent = view.role === "member" ? "Codes match, add device" : "The codes match";
    byId("pairingCancel").hidden = !prompt.actions.includes("cancel");
    byId("pairingClose").hidden = !prompt.actions.includes("close");
    panel.hidden = false;
  }
  async function pollPairings() {
    try {
      const res = await fetch("/api/pairing/sessions", { cache: "no-store" });
      if (!res.ok) return;
      const sessions = (await res.json()).sessions || [];
      const current = sessions.find((s) => s.id === shownPairing);
      if (current) {
        renderPairing(current);
        if (current.state === "joined") {
          await refreshCircles();
          await refreshSetup();
          if (onCircleChange) onCircleChange(activeCircleId());
        }
        return;
      }
      renderPairing(pairingToShow(sessions, dismissed));
    } catch (err) {
      globalThis.console?.debug?.(err);
    }
  }
  async function startPairing(deviceId) {
    const res = await fetch("/api/pairing/sessions", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ device_id: deviceId }) });
    const body = await readJSONResponse(res);
    if (!res.ok) {
      renderPairing({ id: "local-error", state: "failed", error: body.error || "Could not reach that device." });
      return;
    }
    renderPairing(body.session);
  }
  async function pairingAction(action) {
    if (!shownPairing || shownPairing === "local-error") return;
    const payload = action === "confirm" ? { circle_id: byId("pairingCircle")?.value || "" } : {};
    const res = await fetch("/api/pairing/sessions/" + encodeURIComponent(shownPairing) + "/" + action, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload),
    });
    const body = await readJSONResponse(res);
    if (body.session) renderPairing(body.session);
    else if (!res.ok) byId("pairingBody").textContent = body.error || "Could not update the pairing.";
  }
  function closePairing() {
    if (shownPairing) dismissed.add(shownPairing);
    renderPairing(null);
  }

  byId("setupClose")?.addEventListener("click", closeSetup);
  byId("setupCreateCircle")?.addEventListener("click", () => { createCircle().catch(() => {}); });
  byId("setupPairNearby")?.addEventListener("click", () => { closeSetup(); if (openNearby) openNearby(); });
  byId("setupUseConnection")?.addEventListener("click", () => { closeSetup(); if (openConnection) openConnection(); });
  byId("setupOpen")?.addEventListener("click", openSetup);
  byId("activeCircle")?.addEventListener("change", (event) => setActiveCircle(event.target.value));
  byId("pairingConfirm")?.addEventListener("click", () => { pairingAction("confirm").catch(() => {}); });
  byId("pairingCancel")?.addEventListener("click", () => { pairingAction("cancel").catch(() => {}); });
  byId("pairingClose")?.addEventListener("click", closePairing);

  return { activeCircleId, refreshCircles, refreshSetup, pollPairings, startPairing, openSetup };
}

export {
  ACTIVE_CIRCLE_STORAGE_KEY,
  createOnboarding,
  formatPairingCode,
  pairingPrompt,
  pairingToShow,
  pickActiveCircle,
  setupComplete,
  setupStepText
};

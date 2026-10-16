function parseInvitePayload(payload) {
  if (!payload) return { mode: "voice", conversationId: "" };
  let body = payload;
  if (typeof body === "string") {
    try {
      body = JSON.parse(body);
    } catch (error_) {
      globalThis.console?.debug?.(error_);
      return { mode: "voice", conversationId: "" };
    }
  }
  return {
    mode: body?.mode === "video" ? "video" : "voice",
    conversationId: body?.conversation_id ? String(body.conversation_id) : ""
  };
}

// Synthesized with Web Audio so the dashboard ships no audio assets. Times are in seconds, periods in ms.
const TONE_PATTERNS = {
  "ringing-out": { period: 4000, notes: [{ freqs: [440, 480], at: 0, dur: 1.6 }] },
  "ringing-in": {
    period: 2400,
    notes: [
      { freqs: [880], at: 0, dur: 0.18 },
      { freqs: [660], at: 0.24, dur: 0.18 },
      { freqs: [880], at: 0.48, dur: 0.18 }
    ]
  }
};
const TONE_LEVEL = 0.08;
const LISTEN_ONLY_NOTICE = "No microphone found. You can listen, but the other side can't hear you.";
const VOICE_FALLBACK_NOTICE = "Video is limited to 4 people, so this call continues as voice.";
const PARTY_CAP_NOTICE = "This call can include up to 8 people.";
const DEVICE_STORE_KEY = "overdrive.call.devices";
const VIDEO_PARTY_CAP = 4;
const VOICE_PARTY_CAP = 8;
const GROUP_VIDEO_BITRATE = 350000;

function payloadObject(payload) {
  if (!payload) return {};
  if (typeof payload === "string") {
    try {
      return JSON.parse(payload);
    } catch (error_) {
      globalThis.console?.debug?.(error_);
      return {};
    }
  }
  return payload;
}

async function runSequential(items, step, index = 0) {
  if (index >= items.length) return;
  try {
    await step(items[index]);
  } catch {
    /* step failed */
  }
  await runSequential(items, step, index + 1);
}

function callMeshPeerIDs(groupPeerIDs, peerID) {
  if (groupPeerIDs.length) return groupPeerIDs.slice();
  return peerID ? [peerID] : [];
}

function phaseStickyNotice(phase, listenOnly, stickyNotice) {
  if (listenOnly && phase !== "idle" && phase !== "setup") return LISTEN_ONLY_NOTICE;
  if (phase === "setup") return "";
  return stickyNotice;
}

function parseCallRoster(payload) {
  const body = payloadObject(payload);
  const participants = Array.isArray(body.participants)
    ? body.participants.map((id) => String(id || "").trim()).filter(Boolean)
    : [];
  return {
    hostDeviceId: body.host_device_id ? String(body.host_device_id) : "",
    participants
  };
}

function readDevicePrefs() {
  try {
    const raw = globalThis.localStorage?.getItem(DEVICE_STORE_KEY);
    if (!raw) return { micId: "", camId: "" };
    const parsed = JSON.parse(raw);
    return { micId: parsed.micId || "", camId: parsed.camId || "" };
  } catch (error_) {
    globalThis.console?.debug?.(error_);
    return { micId: "", camId: "" };
  }
}

function writeDevicePrefs(micId, camId) {
  try {
    globalThis.localStorage?.setItem(DEVICE_STORE_KEY, JSON.stringify({ micId: micId || "", camId: camId || "" }));
  } catch (error_) {
    globalThis.console?.debug?.(error_);
  }
}

function stopStream(stream) {
  stream?.getTracks?.().forEach((t) => {
    try { t.stop(); } catch (error_) { globalThis.console?.debug?.(error_); }
  });
}

function createTonePlayer(AudioCtor) {
  let ctx = null;
  let timer = 0;

  function playNotes(notes) {
    const t0 = ctx.currentTime + 0.02;
    notes.forEach((note) => {
      const start = t0 + note.at;
      const end = start + note.dur;
      const gain = ctx.createGain();
      gain.gain.setValueAtTime(0, start);
      gain.gain.linearRampToValueAtTime(TONE_LEVEL, start + 0.02);
      gain.gain.setValueAtTime(TONE_LEVEL, end - 0.04);
      gain.gain.linearRampToValueAtTime(0, end);
      gain.connect(ctx.destination);
      note.freqs.forEach((freq) => {
        const osc = ctx.createOscillator();
        osc.type = "sine";
        osc.frequency.value = freq;
        osc.connect(gain);
        osc.start(start);
        osc.stop(end + 0.05);
      });
    });
  }

  function stop() {
    if (timer) clearTimeout(timer);
    timer = 0;
    if (!ctx) return;
    const closing = ctx;
    ctx = null;
    Promise.resolve(closing.close()).catch(() => {});
  }

  function start(kind) {
    stop();
    const pattern = TONE_PATTERNS[kind];
    if (!AudioCtor || !pattern) return;
    try {
      ctx = new AudioCtor();
    } catch (error_) {
      globalThis.console?.debug?.(error_);
      ctx = null;
      return;
    }
    if (ctx.state === "suspended" && ctx.resume) ctx.resume().catch(() => {});
    const loop = () => {
      if (!ctx) return;
      playNotes(pattern.notes);
      timer = setTimeout(loop, pattern.period);
    };
    loop();
  }

  return { start, stop };
}

function emptyMediaStream() {
  if (typeof MediaStream !== "undefined") return new MediaStream();
  return { getTracks: () => [], getAudioTracks: () => [], getVideoTracks: () => [], addTrack() {}, removeTrack() {} };
}

function isMissingDevice(err) {
  return Boolean(err) && (err.name === "NotFoundError" || err.name === "OverconstrainedError");
}

function streamHasLiveAudio(stream) {
  return stream.getAudioTracks().some((t) => t.readyState === "live");
}

async function probeCallMediaAccess(mediaDevices, audio, wantVideo) {
  try {
    const stream = await mediaDevices.getUserMedia({
      audio,
      video: wantVideo ? { facingMode: "user" } : false
    });
    stopStream(stream);
    return false;
  } catch (err) {
    if (wantVideo && isMissingDevice(err)) {
      try {
        const stream = await mediaDevices.getUserMedia({ audio, video: false });
        stopStream(stream);
        return false;
      } catch (error_) {
        if (!isMissingDevice(error_)) throw error_;
        return true;
      }
    }
    if (!isMissingDevice(err)) throw err;
    return true;
  }
}

async function openCallUserMedia(mediaDevices, audio, video, wantVideo) {
  try {
    const stream = await mediaDevices.getUserMedia({ audio, video });
    return { stream, listenOnly: !streamHasLiveAudio(stream) };
  } catch (err) {
    if (!isMissingDevice(err)) throw err;
    if (!wantVideo) {
      return { stream: emptyMediaStream(), listenOnly: true };
    }
    try {
      const stream = await mediaDevices.getUserMedia({ audio, video: false });
      return { stream, listenOnly: !streamHasLiveAudio(stream) };
    } catch (error_) {
      if (!isMissingDevice(error_)) throw error_;
      return { stream: emptyMediaStream(), listenOnly: true };
    }
  }
}

function playMedia(el) {
  if (!el || typeof el.play !== "function") return;
  const pending = el.play();
  if (pending?.catch) pending.catch(() => {});
}

function callPeerInitials(text) {
  const words = String(text || "").trim().split(/[\s_\-.]+/).filter(Boolean);
  if (!words.length) return "?";
  const first = Array.from(words[0])[0] || "";
  const second = words.length > 1 ? Array.from(words[1])[0] || "" : "";
  return (first + second).toUpperCase();
}

function formatCallTimer(ms) {
  const sec = Math.floor(ms / 1000);
  const m = String(Math.floor(sec / 60)).padStart(2, "0");
  const s = String(sec % 60).padStart(2, "0");
  return m + ":" + s;
}

/** When a screen track is on stage, which side owns the main feed (local share vs remote). */
function screenStageMode(localScreenVisible, remoteScreenVisible) {
  if (localScreenVisible) return "local";
  if (remoteScreenVisible) return "remote";
  return "";
}

function pipScreenVideoId(screenMode) {
  if (screenMode === "local") return "callLocalScreen";
  if (screenMode === "remote") return "callRemoteScreen";
  return "";
}

/** Camera tile to float beside a full-screen share (YouTube-style companion). */
function pipCompanionVideoId(screenMode, hasLocalCamera, hasRemoteCamera) {
  if (!screenMode) return "";
  if (screenMode === "local") {
    if (hasLocalCamera) return "callLocalVideo";
    if (hasRemoteCamera) return "callRemoteVideo";
  } else {
    if (hasRemoteCamera) return "callRemoteVideo";
    if (hasLocalCamera) return "callLocalVideo";
  }
  return "";
}

function supportsDocumentPictureInPicture(win) {
  return Boolean(win?.documentPictureInPicture?.requestWindow);
}

const SCREEN_PIP_LIMITATION =
  "Screen mini player works best in Chrome or Edge (Picture-in-Picture). Safari and Firefox use the draggable overlay.";

const CALL_NAT_NOTE =
  "Calls use STUN on the circle mesh. Set OVERDRIVE_TURN_URLS when peers are behind NAT.";

function meshIceServers(cfg) {
  const fallback = [{ urls: "stun:stun.l.google.com:19302" }];
  const list = Array.isArray(cfg?.iceServers) ? cfg.iceServers.filter((server) => server?.urls) : [];
  return list.length ? list : fallback;
}

function createMeshCall(deps) {
  deps = deps || {};
  const PeerConnectionCtor = deps.RTCPeerConnection || (typeof RTCPeerConnection === "undefined" ? null : RTCPeerConnection);
  const mediaDevices = deps.mediaDevices || (typeof navigator === "undefined" ? null : navigator.mediaDevices);
  const tones = createTonePlayer(deps.AudioContext || (typeof AudioContext === "undefined" ? null : AudioContext));
  let listenOnly = false;
  function readCallRuntime() {
    if (deps.callConfig) return deps.callConfig;
    if (typeof window !== "undefined" && window.OVERDRIVE_CALL) return window.OVERDRIVE_CALL;
    return { turnConfigured: false, pipNote: SCREEN_PIP_LIMITATION, natNote: CALL_NAT_NOTE };
  }
  const iceServers = meshIceServers(readCallRuntime());
  const CALL_REPLAY_MAX_MS = 90 * 1000;
  let meshID = "";
  let selfID = "";
  let peerID = "";
  let peerLabel = "";
  let callID = "";
  let role = "idle";
  /** @type {Map<string, RTCPeerConnection>} */
  const peerConnections = new Map();
  let localStream = null;
  let screenTrack = null;
  let eventSince = 0;
  let eventsPrimed = false;
  let pollTimer = null;
  let timerStart = 0;
  let timerHandle = null;
  let callMode = "voice";
  let linkedConversationID = "";
  let groupPeerIDs = [];
  /** @type {Map<string, object>} */
  const pendingRemoteOffers = new Map();
  /** @type {Map<string, object[]>} */
  const pendingIceCandidates = new Map();
  let lastOutbound = null;
  const pendingRenegotiation = new Set();
  let hostID = "";
  let stickyNotice = "";
  let setupResolve = null;
  let localAudioOwned = false;
  let localVideoOwned = false;
  let pendingModAction = "";
  let screenPipMode = "";
  let pipDragBound = false;

  const panel = () => document.getElementById("callPanel");
  const $ = (id) => document.getElementById(id);

  function videoVisible(id) {
    const el = $(id);
    return Boolean(el && !el.hidden && el.srcObject);
  }

  function syncScreenStage() {
    const stage = $("callStage");
    if (!stage) return;
    const mode = screenStageMode(
      videoVisible("callLocalScreen"),
      videoVisible("callRemoteScreen")
    );
    if (mode) stage.dataset.screenMode = mode;
    else delete stage.dataset.screenMode;
    const pipBtn = $("callPip");
    if (pipBtn) pipBtn.hidden = !mode;
    if (!mode) exitScreenPip(false).catch(() => {});
    else syncPipOverlay();
  }

  function syncPipOverlay() {
    const stage = $("callStage");
    if (!stage?.dataset?.screenMode) return;
    const mode = stage.dataset.screenMode;
    const companion = pipCompanionVideoId(
      mode,
      videoVisible("callLocalVideo"),
      videoVisible("callRemoteVideo")
    );
    ["callLocalVideo", "callRemoteVideo"].forEach((id) => {
      const el = $(id);
      if (!el) return;
      const pip = companion === id && screenPipMode !== "screen";
      el.classList.toggle("call-stage-pip", pip);
    });
  }

  function updatePipButton() {
    const btn = $("callPip");
    if (!btn) return;
    const docked = screenPipMode === "screen";
    btn.setAttribute("aria-pressed", docked ? "true" : "false");
    const label = typeof btn.querySelector === "function" ? btn.querySelector("span") : null;
    if (label) label.textContent = docked ? "Dock share" : "Mini player";
    btn.title = SCREEN_PIP_LIMITATION;
  }

  function onLeaveNativePip() {
    if (screenPipMode !== "screen") return;
    exitScreenPip(false).catch(() => {});
  }

  async function exitScreenPip(_userInitiated) {
    screenPipMode = "";
    delete $("callStage")?.dataset?.screenPip;
    const float = $("callPipFloat");
    const pipVid = $("callPipVideo");
    if (float) float.hidden = true;
    if (pipVid) pipVid.srcObject = null;
    if (typeof document !== "undefined" && document.pictureInPictureElement) {
      try {
        await document.exitPictureInPicture();
      } catch (error_) {
        globalThis.console?.debug?.(error_);
      }
    }
    updatePipButton();
    syncPipOverlay();
  }

  function showFloatScreenPip(sourceVideo) {
    const float = $("callPipFloat");
    const pipVid = $("callPipVideo");
    if (!float || !pipVid || !sourceVideo?.srcObject) return false;
    pipVid.srcObject = sourceVideo.srcObject;
    pipVid.muted = true;
    playMedia(pipVid);
    float.hidden = false;
    if (!float.style.left) {
      float.style.right = "20px";
      float.style.bottom = "120px";
    }
    bindPipDrag();
    return true;
  }

  function bindPipDrag() {
    if (pipDragBound) return;
    const float = $("callPipFloat");
    const grip = $("callPipDrag");
    if (!float || !grip) return;
    pipDragBound = true;
    let startX = 0;
    let startY = 0;
    let originLeft = 0;
    let originTop = 0;
    const onMove = (ev) => {
      const dx = ev.clientX - startX;
      const dy = ev.clientY - startY;
      float.style.left = originLeft + dx + "px";
      float.style.top = originTop + dy + "px";
      float.style.right = "auto";
      float.style.bottom = "auto";
    };
    const onUp = () => {
      globalThis.removeEventListener("pointermove", onMove);
      globalThis.removeEventListener("pointerup", onUp);
    };
    grip.addEventListener("pointerdown", (ev) => {
      ev.preventDefault();
      const rect = float.getBoundingClientRect?.() || { left: 0, top: 0 };
      startX = ev.clientX;
      startY = ev.clientY;
      originLeft = rect.left;
      originTop = rect.top;
      float.style.left = originLeft + "px";
      float.style.top = originTop + "px";
      float.style.right = "auto";
      float.style.bottom = "auto";
      globalThis.addEventListener("pointermove", onMove);
      globalThis.addEventListener("pointerup", onUp);
    });
  }

  async function toggleScreenPip() {
    if (screenPipMode === "screen") {
      await exitScreenPip(true);
      return;
    }
    const stage = $("callStage");
    const mode = stage?.dataset?.screenMode;
    if (!mode) return;
    const video = $(pipScreenVideoId(mode));
    if (!video?.srcObject) return;
    stage.dataset.screenPip = "screen";
    screenPipMode = "screen";
    syncPipOverlay();
    let nativeOk = false;
    if (typeof video.requestPictureInPicture === "function") {
      try {
        video.addEventListener("leavepictureinpicture", onLeaveNativePip, { once: true });
        await video.requestPictureInPicture();
        nativeOk = true;
      } catch (error_) {
        globalThis.console?.debug?.(error_);
      }
    }
    if (!nativeOk) showFloatScreenPip(video);
    updatePipButton();
  }

  function focusStageForScreen() {
    const live = document.getElementById("meshLive");
    if (!live?.classList?.contains?.("call-joined")) return;
    live.dataset.focus = "stage";
    const btn = $("callFocus");
    if (btn) {
      btn.setAttribute("aria-pressed", "true");
      const label = typeof btn.querySelector === "function" ? btn.querySelector("span") : null;
      if (label) label.textContent = "Chat";
    }
  }

  async function postSignal(kind, payload, target) {
    const body = {
      mesh_id: meshID,
      target_device_id: target || peerID,
      call_id: callID,
      kind,
      payload: payload || {}
    };
    if (groupPeerIDs.length > 1 && kind === "invite") {
      body.target_device_ids = groupPeerIDs.slice();
      delete body.target_device_id;
    }
    if (linkedConversationID && kind === "invite") {
      body.conversation_id = linkedConversationID;
    }
    const res = await fetch("/api/call/signal", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    });
    if (!res.ok) throw new Error("signal failed");
    const data = await res.json();
    if (data.call_id && !callID) callID = data.call_id;
    return data;
  }

  function syncCallRuntimeHints() {
    const cfg = readCallRuntime();
    const nat = $("callNatHint");
    if (nat) {
      nat.hidden = cfg.turnConfigured === true;
      nat.textContent = cfg.natNote || CALL_NAT_NOTE;
    }
    const pip = $("callPip");
    if (pip) pip.title = cfg.pipNote || SCREEN_PIP_LIMITATION;
  }

  function setVisible(open) {
    const node = panel();
    if (!node) return;
    node.hidden = !open;
    node.setAttribute("aria-hidden", open ? "false" : "true");
    const live = document.getElementById("meshLive");
    if (!live) return;
    live.classList.toggle("call-joined", open);
    if (open && !live.dataset.focus) live.dataset.focus = "chat";
  }

  function setStatus(text) {
    const node = $("callStatus");
    if (node) node.textContent = text;
  }

  function setError(text) {
    const node = $("callError");
    const label = $("callErrorText");
    if (!node) return;
    const msg = text || "";
    if (label) label.textContent = msg;
    else node.textContent = msg;
    node.hidden = !msg;
    const notice = msg === LISTEN_ONLY_NOTICE || msg === VOICE_FALLBACK_NOTICE;
    node.classList.toggle("call-error--notice", notice);
    node.classList.toggle("call-error--alert", Boolean(msg) && !notice);
  }

  // Phase drives which header/stage/controls the CSS shows (idle, ringing-in, ringing-out, active, error).
  function setPhase(phase) {
    panel()?.setAttribute("data-phase", phase);
    const retry = $("callRetry");
    if (retry) retry.hidden = phase !== "error";
    const setup = $("callSetup");
    if (setup) setup.hidden = phase !== "setup";
    const camField = $("callCamField");
    if (camField) camField.hidden = phase !== "setup" || callMode !== "video";
    if (phase === "idle") stickyNotice = "";
    if (phase !== "error") {
      setError(phaseStickyNotice(phase, listenOnly, stickyNotice));
    }
  }

  function partySize(peers) {
    return peers.length + (selfID ? 1 : 0);
  }

  function uniquePeers(targetIDs) {
    const out = [];
    const seen = new Set();
    targetIDs.forEach((id) => {
      if (!id || id === selfID || seen.has(id)) return;
      seen.add(id);
      out.push(id);
    });
    return out;
  }

  function chosenDevice(selectId, prefKey) {
    const node = $(selectId);
    if (node?.dataset?.ready === "true") return node.value || "";
    return readDevicePrefs()[prefKey] || "";
  }

  function chosenMic() {
    return chosenDevice("callMicSelect", "micId");
  }

  function chosenCam() {
    return chosenDevice("callCamSelect", "camId");
  }

  const audioConstraints = (micId) => {
    const audio = { echoCancellation: true, noiseSuppression: true, autoGainControl: true };
    if (micId) audio.deviceId = { exact: micId };
    return audio;
  }

  function videoConstraints() {
    const group = groupPeerIDs.length > 1;
    const video = {
      facingMode: "user",
      width: { ideal: group ? 640 : 1280 },
      height: { ideal: group ? 360 : 720 }
    };
    const camId = chosenCam();
    if (camId) video.deviceId = { exact: camId };
    return video;
  }

  const CALL_DEVICE_SELECT_IDS = new Set(["callMicSelect", "callCamSelect"]);
  let callDeviceSelectBound = false;

  function callDeviceSelectParts(selectId) {
    const select = $(selectId);
    if (!select) return null;
    const wrap = select.closest?.(".call-device-select");
    if (!wrap) return null;
    return {
      select,
      trigger: wrap.querySelector(".call-device-select__trigger"),
      label: wrap.querySelector(".call-device-select__label"),
      panel: wrap.querySelector(".call-device-select__panel")
    };
  }

  function syncCallDeviceSelectLabel(selectId) {
    const parts = callDeviceSelectParts(selectId);
    if (!parts?.label || !parts.select) return;
    const opt = parts.select.selectedOptions?.[0];
    parts.label.textContent = opt?.textContent || "";
  }

  function closeCallDeviceSelectPanel(selectId) {
    const parts = callDeviceSelectParts(selectId);
    if (!parts?.panel || !parts.trigger) return;
    parts.panel.hidden = true;
    parts.trigger.setAttribute("aria-expanded", "false");
  }

  function closeAllCallDeviceSelectPanels() {
    CALL_DEVICE_SELECT_IDS.forEach((id) => closeCallDeviceSelectPanel(id));
  }

  function renderCallDeviceSelectPanel(selectId) {
    const parts = callDeviceSelectParts(selectId);
    if (!parts?.panel || !parts.select) return;
    const { panel, select } = parts;
    panel.replaceChildren();
    const frag = document.createDocumentFragment();
    [...select.options].forEach((opt) => {
      const item = document.createElement("button");
      item.type = "button";
      item.className = "call-device-select__option";
      item.dataset.value = opt.value;
      if (opt.selected) item.setAttribute("aria-current", "true");
      item.textContent = opt.textContent;
      item.addEventListener("click", (ev) => {
        ev.stopPropagation();
        select.value = opt.value;
        syncCallDeviceSelectLabel(selectId);
        renderCallDeviceSelectPanel(selectId);
        closeCallDeviceSelectPanel(selectId);
      });
      frag.appendChild(item);
    });
    panel.appendChild(frag);
  }

  function openCallDeviceSelectPanel(selectId) {
    const parts = callDeviceSelectParts(selectId);
    if (!parts?.panel || !parts.trigger) return;
    closeAllCallDeviceSelectPanels();
    renderCallDeviceSelectPanel(selectId);
    parts.panel.hidden = false;
    parts.trigger.setAttribute("aria-expanded", "true");
  }

  function bindCallDeviceSelects() {
    if (callDeviceSelectBound) return;
    callDeviceSelectBound = true;
    CALL_DEVICE_SELECT_IDS.forEach((selectId) => {
      const parts = callDeviceSelectParts(selectId);
      if (!parts?.trigger) return;
      parts.trigger.addEventListener("click", (ev) => {
        ev.stopPropagation();
        if (parts.panel.hidden) openCallDeviceSelectPanel(selectId);
        else closeCallDeviceSelectPanel(selectId);
      });
      parts.trigger.addEventListener("keydown", (ev) => {
        if (ev.key === "ArrowDown" || ev.key === "Enter" || ev.key === " ") {
          ev.preventDefault();
          openCallDeviceSelectPanel(selectId);
        } else if (ev.key === "Escape") {
          closeCallDeviceSelectPanel(selectId);
        }
      });
    });
    document.addEventListener("click", () => closeAllCallDeviceSelectPanels());
    document.addEventListener("keydown", (ev) => {
      if (ev.key === "Escape") closeAllCallDeviceSelectPanels();
    });
  }

  function fillDeviceSelect(select, devices, savedId, fallbackLabel, includeBlank) {
    if (!select) return;
    if (typeof select.replaceChildren === "function") select.replaceChildren();
    const add = (value, label) => {
      const opt = document.createElement("option");
      opt.value = value;
      opt.textContent = label;
      select.appendChild(opt);
    };
    if (includeBlank !== false) add("", fallbackLabel);
    devices.forEach((device) => add(device.deviceId, device.label || fallbackLabel));
    select.value = savedId || "";
    if (select.dataset) select.dataset.ready = "true";
    if (select.id && CALL_DEVICE_SELECT_IDS.has(select.id)) {
      bindCallDeviceSelects();
      syncCallDeviceSelectLabel(select.id);
      renderCallDeviceSelectPanel(select.id);
      closeCallDeviceSelectPanel(select.id);
    }
  }

  function finishSetupWait(value) {
    const resolve = setupResolve;
    setupResolve = null;
    if (resolve) resolve(value);
  }

  function waitForSetupConfirm() {
    return new Promise((resolve) => {
      setupResolve = resolve;
      if (globalThis.__overdriveAutoSetup === true) {
        setTimeout(() => finishSetupWait(), 0);
      }
    });
  }

  function describeCallError(err, deviceOverride) {
    const device = deviceOverride || (callMode === "video" ? "camera or microphone" : "microphone");
    const name = err?.name;
    const msg = err?.message;
    if (name === "NotAllowedError" || name === "SecurityError") {
      return "Access to the " + device + " was blocked. Allow it in the browser and try again.";
    }
    if (name === "NotFoundError" || name === "OverconstrainedError") {
      return "No " + device + " found in this browser. Connect one, or open the dashboard in Chrome or Firefox.";
    }
    if (name === "NotReadableError" || name === "AbortError") {
      return "Your " + device + " is in use by another app.";
    }
    if (msg === "media unavailable") return "This browser can't use the " + device + " here. Open the dashboard in Chrome or Firefox.";
    if (msg === "webrtc unavailable") return "This browser doesn't support calls.";
    if (msg === "signal failed") return "Couldn't reach the other device.";
    return "Something went wrong. Try again.";
  }

  function setPeerName(text) {
    const node = $("callPeerName");
    if (node) node.textContent = text || "Peer";
    const avatar = $("callAvatar");
    if (avatar) avatar.textContent = callPeerInitials(text || "Peer");
  }

  function startTimer() {
    timerStart = Date.now();
    stopTimer();
    timerHandle = setInterval(() => {
      const node = $("callTimer");
      if (node) node.textContent = formatCallTimer(Date.now() - timerStart);
    }, 1000);
  }

  function stopTimer() {
    if (timerHandle) clearInterval(timerHandle);
    timerHandle = null;
    const node = $("callTimer");
    if (node) node.textContent = "00:00";
  }

  function syncCallModeUI() {
    const voice = $("callVoiceOnly");
    const label = $("callVoiceLabel");
    const grid = $("callRemoteGrid");
    const multi = peerConnections.size > 1 || groupPeerIDs.length > 1;
    if (label) {
      if (callMode === "video") label.textContent = multi ? "Group video" : "Video call";
      else label.textContent = multi ? "Group voice" : "Voice call";
    }
    if (voice) {
      const remote = $("callRemoteVideo");
      const hasRemoteVideo = Boolean(remote?.srcObject && !remote.hidden);
      const hasGrid = Boolean(grid && !grid.hidden && grid.childElementCount);
      const local = $("callLocalVideo");
      const hasLocalVideo = Boolean(local && !local.hidden);
      const hasScreen = videoVisible("callLocalScreen") || videoVisible("callRemoteScreen");
      voice.hidden = hasRemoteVideo || hasGrid || hasLocalVideo || hasScreen;
    }
    const chatBtn = $("callChatToggle");
    if (chatBtn) chatBtn.hidden = !linkedConversationID;
    syncScreenStage();
  }

  function showLocalPreview(show) {
    const local = $("callLocalVideo");
    if (!local || !localStream) return;
    if (show) {
      local.srcObject = localStream;
      local.hidden = false;
      const voice = $("callVoiceOnly");
      if (voice) voice.hidden = true;
      $("callCamera")?.setAttribute("aria-pressed", "true");
    } else {
      local.hidden = true;
      $("callCamera")?.setAttribute("aria-pressed", "false");
    }
    syncScreenStage();
  }

  function peerLabelFor(id) {
    if (deps.peerLabel) return deps.peerLabel(id) || id;
    return id;
  }

  function attachRemoteStream(remotePeerID, stream) {
    const multi = peerConnections.size > 1 || groupPeerIDs.length > 1;
    const grid = $("callRemoteGrid");
    const single = $("callRemoteVideo");
    if (multi && grid) {
      grid.hidden = false;
      if (single) single.hidden = true;
      let tile = grid.querySelector('[data-peer="' + remotePeerID + '"]');
      if (!tile) {
        tile = document.createElement("div");
        tile.className = "call-remote-tile";
        tile.dataset.peer = remotePeerID;
        const video = document.createElement("video");
        video.playsInline = true;
        video.autoplay = true;
        tile.appendChild(video);
        const cap = document.createElement("span");
        cap.textContent = peerLabelFor(remotePeerID);
        tile.appendChild(cap);
        grid.appendChild(tile);
      }
      const video = tile.querySelector("video");
      if (video) {
        video.srcObject = stream;
        playMedia(video);
      }
      $("callVoiceOnly").hidden = true;
      return;
    }
    if (single) {
      // A hidden <video> still plays audio, so voice-only calls keep the voice stage visible.
      const hasVideo = Boolean((stream?.getVideoTracks?.()?.length ?? 0));
      if (single.srcObject !== stream && stream?.addEventListener) {
        stream.addEventListener("addtrack", () => attachRemoteStream(remotePeerID, stream));
      }
      single.srcObject = stream;
      playMedia(single);
      single.hidden = !hasVideo;
      if (hasVideo) $("callVoiceOnly").hidden = true;
    }
    syncScreenStage();
  }

  function showRinging(inbound) {
    role = inbound ? "ringing-in" : "ringing-out";
    setPhase(role);
    setVisible(true);
    const video = callMode === "video";
    if (inbound) setStatus(video ? "Incoming video call" : "Incoming call");
    else setStatus(video ? "Video calling…" : "Calling…");
    syncCallModeUI();
    $("callAccept").hidden = !inbound;
    $("callDecline").hidden = !inbound;
    $("callEnd").hidden = inbound;
    $("callMute").hidden = inbound;
    $("callCamera").hidden = inbound;
    $("callScreen").hidden = inbound;
    tones.start(role);
  }

  function showActive() {
    tones.stop();
    role = "active";
    setPhase("active");
    setStatus(callMode === "video" ? "Video call" : "Voice call");
    $("callAccept").hidden = true;
    $("callDecline").hidden = true;
    $("callEnd").hidden = false;
    $("callMute").hidden = false;
    $("callMute").disabled = listenOnly;
    $("callCamera").hidden = false;
    $("callScreen").hidden = false;
    const focus = $("callFocus");
    if (focus) focus.hidden = false;
    startTimer();
    syncCallModeUI();
    refreshHostBar();
  }

  async function probePermissions(wantVideo) {
    if (!mediaDevices?.getUserMedia) throw new Error("media unavailable");
    listenOnly = await probeCallMediaAccess(mediaDevices, audioConstraints(""), wantVideo);
    if (listenOnly) setError(LISTEN_ONLY_NOTICE);
  }

  async function loadDeviceLabels(wantVideo) {
    const prefs = readDevicePrefs();
    let devices = [];
    if (typeof mediaDevices?.enumerateDevices === "function") {
      try {
        devices = await mediaDevices.enumerateDevices();
      } catch (error_) {
        globalThis.console?.debug?.(error_);
      }
    }
    fillDeviceSelect(
      $("callMicSelect"),
      devices.filter((device) => device.kind === "audioinput"),
      prefs.micId,
      "System microphone"
    );
    if (wantVideo) {
      fillDeviceSelect(
        $("callCamSelect"),
        devices.filter((device) => device.kind === "videoinput"),
        prefs.camId,
        "System camera"
      );
    }
  }

  async function captureDevices(wantVideo) {
    const micId = chosenMic();
    const camId = wantVideo ? chosenCam() : "";
    writeDevicePrefs(micId, camId || readDevicePrefs().camId);
    if (listenOnly && !wantVideo) {
      localStream = emptyMediaStream();
      showLocalPreview(false);
      return;
    }
    if (!mediaDevices?.getUserMedia) throw new Error("media unavailable");
    const audio = listenOnly ? false : audioConstraints(micId);
    const video = wantVideo ? videoConstraints() : false;
    const opened = await openCallUserMedia(mediaDevices, audio, video, wantVideo);
    localStream = opened.stream;
    listenOnly = opened.listenOnly;
    if (listenOnly) setError(LISTEN_ONLY_NOTICE);
    showLocalPreview(wantVideo && localStream.getVideoTracks().some((t) => t !== screenTrack && t.readyState === "live"));
  }

  async function prepareDevices(wantVideo) {
    setPhase("setup");
    setVisible(true);
    setStatus(wantVideo ? "Check your camera and microphone" : "Check your microphone");
    ["callAccept", "callDecline", "callEnd", "callMute", "callCamera", "callScreen", "callChatToggle"].forEach((id) => {
      const btn = $(id);
      if (btn) btn.hidden = true;
    });
    await probePermissions(wantVideo);
    await loadDeviceLabels(wantVideo);
    const choice = await waitForSetupConfirm();
    if (choice === "cancelled") return false;
    await captureDevices(wantVideo);
    return true;
  }

  async function ensureMedia(wantVideo) {
    const needVideo = Boolean(wantVideo) || callMode === "video";
    const hasAudio = localStream?.getAudioTracks().some((t) => t.readyState === "live");
    const hasVideo = localStream?.getVideoTracks().some((t) => t !== screenTrack && t.readyState === "live");
    if (localStream && (hasAudio || listenOnly) && (!needVideo || hasVideo)) {
      showLocalPreview(needVideo && hasVideo);
      return localStream;
    }
    if (localStream) {
      localStream.getTracks().forEach((t) => {
        if (t !== screenTrack) t.stop();
      });
      localStream = null;
    }
    if (!mediaDevices?.getUserMedia) throw new Error("media unavailable");
    try {
      localStream = await mediaDevices.getUserMedia({
        audio: listenOnly ? false : audioConstraints(chosenMic()),
        video: needVideo ? videoConstraints() : false
      });
      listenOnly = !localStream.getAudioTracks().some((t) => t.readyState === "live");
    } catch (err) {
      if (!isMissingDevice(err)) throw err;
      localStream = needVideo
        ? await mediaDevices.getUserMedia({ video: videoConstraints(), audio: false }).catch(() => emptyMediaStream())
        : emptyMediaStream();
      listenOnly = true;
      setError(LISTEN_ONLY_NOTICE);
    }
    showLocalPreview(needVideo && localStream.getVideoTracks().some((t) => t !== screenTrack));
    return localStream;
  }

  async function capVideoSender(pc, track) {
    if (track?.kind !== "video" || !pc?.getSenders) return;
    const sender = pc.getSenders().find((item) => item.track === track);
    if (!sender?.getParameters || !sender.setParameters) return;
    try {
      const params = sender.getParameters();
      if (!params.encodings?.length) params.encodings = [{}];
      params.encodings[0].maxBitrate = groupPeerIDs.length > 1 ? GROUP_VIDEO_BITRATE : 2500000;
      await sender.setParameters(params);
    } catch (error_) {
      globalThis.console?.debug?.(error_);
    }
  }

  // Without a local track the offer would carry no media section, so the peer could not send us its audio.
  function addReceiveOnlyTransceivers(conn) {
    if (typeof conn.addTransceiver !== "function") return;
    const kinds = new Set(conn.getSenders().map((s) => s.track?.kind).filter(Boolean));
    if (!kinds.has("audio")) conn.addTransceiver("audio", { direction: "recvonly" });
    if (callMode === "video" && !kinds.has("video")) conn.addTransceiver("video", { direction: "recvonly" });
  }

  async function ensurePC(remotePeerID) {
    const wantVideo = callMode === "video";
    let pc = peerConnections.get(remotePeerID);
    if (pc) {
      const stream = await ensureMedia(wantVideo);
      const kinds = new Set(pc.getSenders().map((s) => s.track?.kind).filter(Boolean));
      stream.getTracks().forEach((track) => {
        if (!kinds.has(track.kind)) pc.addTrack(track, stream);
      });
      return pc;
    }
    if (!PeerConnectionCtor) throw new Error("webrtc unavailable");
    pc = new PeerConnectionCtor({ iceServers });
    pc.onicecandidate = (ev) => {
      if (!ev.candidate) return;
      postSignal("ice", { candidate: ev.candidate.toJSON() }, remotePeerID).catch(() => {});
    };
    pc.ontrack = (ev) => {
      const track = ev.track;
      const stream = ev.streams?.[0];
      if (track?.kind === "video" && track.contentHint === "detail") {
        attachScreenStream(remotePeerID, stream || { getVideoTracks: () => [track] });
        return;
      }
      if (stream) attachRemoteStream(remotePeerID, stream);
    };
    pc.onconnectionstatechange = () => {
      if (pc?.connectionState === "failed") setStatus("Connection failed");
    };
    peerConnections.set(remotePeerID, pc);
    const stream = await ensureMedia(wantVideo);
    await Promise.all(stream.getTracks().map(async (track) => {
      pc.addTrack(track, stream);
      await capVideoSender(pc, track);
    }));
    syncCallModeUI();
    return pc;
  }

  async function offerToPeer(remotePeerID) {
    const conn = await ensurePC(remotePeerID);
    addReceiveOnlyTransceivers(conn);
    if (conn.localDescription) return conn;
    const offer = await conn.createOffer();
    await conn.setLocalDescription(offer);
    await postSignal("offer", { sdp: offer.sdp, type: offer.type }, remotePeerID);
    return conn;
  }

  function shouldOfferTo(remotePeerID) {
    if (groupPeerIDs.length <= 1) return true;
    return String(selfID) < String(remotePeerID);
  }

  async function startOutboundMany(targetIDs, label, mode, conversationId) {
    if (!meshID || !targetIDs.length) return;
    const peers = uniquePeers(targetIDs);
    if (!peers.length) return;
    lastOutbound = { targetIDs: targetIDs.slice(), label, mode, conversationId };
    if (partySize(peers) > VOICE_PARTY_CAP) {
      showFailure("Could not start call", PARTY_CAP_NOTICE);
      throw new Error(PARTY_CAP_NOTICE);
    }
    stickyNotice = "";
    callMode = mode === "video" ? "video" : "voice";
    if (callMode === "video" && partySize(peers) > VIDEO_PARTY_CAP) {
      callMode = "voice";
      stickyNotice = VOICE_FALLBACK_NOTICE;
    }
    peerConnections.forEach((pc) => pc.close());
    peerConnections.clear();
    groupPeerIDs = peers;
    hostID = selfID;
    peerID = groupPeerIDs[0] || "";
    peerLabel = label || (groupPeerIDs.length > 1 ? "Group call" : peerID);
    linkedConversationID = conversationId || "";
    setPeerName(peerLabel);
    callID = "";
    try {
      const ready = await prepareDevices(callMode === "video");
      if (!ready) return;
      showRinging(false);
      const participants = [selfID, ...groupPeerIDs].filter(Boolean);
      const data = await postSignal("invite", {
        mode: callMode,
        host_device_id: selfID,
        participants
      }, peerID);
      callID = data.call_id || callID;
      await Promise.all(groupPeerIDs.map(async (tid) => {
        if (shouldOfferTo(tid)) await offerToPeer(tid);
        else await ensurePC(tid);
      }));
    } catch (err) {
      const reason = err?.message === PARTY_CAP_NOTICE ? PARTY_CAP_NOTICE : describeCallError(err);
      await endCall(Boolean(callID));
      showFailure("Could not start call", reason);
      throw err;
    }
  }

  function showFailure(title, reason) {
    setVisible(true);
    setPhase("error");
    setStatus(title);
    setError(reason);
    ["callAccept", "callDecline", "callMute", "callCamera", "callScreen", "callChatToggle", "callEnd", "callFocus"].forEach((id) => {
      const btn = $(id);
      if (btn) btn.hidden = true;
    });
    const hostBar = $("callHostBar");
    if (hostBar) hostBar.hidden = true;
    const banner = $("callModBanner");
    if (banner) banner.hidden = true;
    const setup = $("callSetup");
    if (setup) setup.hidden = true;
    const retry = $("callRetry");
    if (retry) retry.hidden = !lastOutbound;
  }

  async function retryOutbound() {
    if (!lastOutbound) return;
    const { targetIDs, label, mode, conversationId } = lastOutbound;
    return startOutboundMany(targetIDs, label, mode, conversationId);
  }

  async function startOutbound(targetID, label, mode, conversationId) {
    return startOutboundMany([targetID], label, mode, conversationId || "");
  }

  function queueRemoteOffer(remotePeerID, payload) {
    if (!remotePeerID || !payload?.sdp) return;
    pendingRemoteOffers.set(remotePeerID, payload);
  }

  function queueIceCandidate(remotePeerID, payload) {
    if (!remotePeerID || !payload?.candidate) return;
    const list = pendingIceCandidates.get(remotePeerID) || [];
    list.push(payload);
    pendingIceCandidates.set(remotePeerID, list);
  }

  async function flushPendingIce(remotePeerID) {
    const list = pendingIceCandidates.get(remotePeerID) || [];
    pendingIceCandidates.delete(remotePeerID);
    await runSequential(list, (payload) => addIce(remotePeerID, payload));
  }

  async function renegotiatePeer(remotePeerID) {
    const conn = peerConnections.get(remotePeerID);
    if (!conn || conn.signalingState === "closed") return;
    const offer = await conn.createOffer();
    await conn.setLocalDescription(offer);
    await postSignal("offer", { sdp: offer.sdp, type: offer.type }, remotePeerID);
  }

  // A track added while the first offer is still unanswered would never reach the peer,
  // so the new offer waits until the answer lands.
  async function requestRenegotiation(remotePeerID) {
    if (role === "active") return renegotiatePeer(remotePeerID);
    pendingRenegotiation.add(remotePeerID);
  }

  async function connectAfterAccept() {
    const peers = callMeshPeerIDs(groupPeerIDs, peerID);
    const mesh = peers.length > 1;
    await runSequential(peers, async (id) => {
      const queued = pendingRemoteOffers.get(id);
      if (mesh && String(selfID) < String(id) && !queued) {
        await offerToPeer(id);
        return;
      }
      if (queued) {
        pendingRemoteOffers.delete(id);
        await applyRemoteDescription(id, queued, true);
      } else {
        await ensurePC(id);
      }
      await flushPendingIce(id);
    });
  }

  async function acceptInbound() {
    const ready = await prepareDevices(callMode === "video");
    if (!ready) return;
    await postSignal("accept", {}, peerID);
    await connectAfterAccept();
    showActive();
  }

  async function applyRemoteDescription(remotePeerID, payload, skipRingingGuard) {
    if (!payload?.sdp) return;
    if (!skipRingingGuard && role === "ringing-in") {
      queueRemoteOffer(remotePeerID, payload);
      return;
    }
    const conn = await ensurePC(remotePeerID);
    await conn.setRemoteDescription({ type: payload.type || "answer", sdp: payload.sdp });
    if ((payload.type || "answer") === "offer") {
      const answer = await conn.createAnswer();
      await conn.setLocalDescription(answer);
      await postSignal("answer", { sdp: answer.sdp, type: answer.type }, remotePeerID);
      showActive();
    }
    await flushPendingIce(remotePeerID);
    if ((payload.type || "answer") === "answer" && pendingRenegotiation.delete(remotePeerID)) {
      await renegotiatePeer(remotePeerID);
    }
  }

  async function addIce(remotePeerID, payload) {
    const pc = peerConnections.get(remotePeerID);
    if (!pc || !payload?.candidate) return;
    if (!pc.remoteDescription) {
      queueIceCandidate(remotePeerID, payload);
      return;
    }
    try {
      await pc.addIceCandidate(payload.candidate);
    } catch { /* stale candidate */ }
  }

  function teardown() {
    tones.stop();
    listenOnly = false;
    localAudioOwned = false;
    localVideoOwned = false;
    pendingModAction = "";
    hostID = "";
    stickyNotice = "";
    finishSetupWait("cancelled");
    const mute = $("callMute");
    if (mute) mute.disabled = false;
    stopTimer();
    if (pollTimer) clearInterval(pollTimer);
    pollTimer = null;
    peerConnections.forEach((pc) => pc.close());
    peerConnections.clear();
    groupPeerIDs = [];
    linkedConversationID = "";
    if (localStream) {
      localStream.getTracks().forEach((t) => t.stop());
      localStream = null;
    }
    if (screenTrack) {
      screenTrack.onended = null;
      screenTrack.stop();
      screenTrack = null;
    }
    $("callScreen")?.setAttribute("aria-pressed", "false");
    pendingRenegotiation.clear();
    const remote = $("callRemoteVideo");
    const local = $("callLocalVideo");
    const localScreen = $("callLocalScreen");
    const remoteScreen = $("callRemoteScreen");
    const grid = $("callRemoteGrid");
    const banner = $("callModBanner");
    const hostBar = $("callHostBar");
    if (banner) banner.hidden = true;
    if (hostBar) hostBar.hidden = true;
    if (grid) {
      grid.replaceChildren();
      grid.hidden = true;
    }
    if (remote) {
      remote.srcObject = null;
      remote.hidden = true;
    }
    if (local) {
      local.srcObject = null;
      local.hidden = true;
    }
    if (localScreen) {
      localScreen.srcObject = null;
      localScreen.hidden = true;
    }
    if (remoteScreen) {
      remoteScreen.srcObject = null;
      remoteScreen.hidden = true;
    }
    delete $("callStage")?.dataset?.screenMode;
    delete $("callStage")?.dataset?.screenPip;
    screenPipMode = "";
    const pipFloat = $("callPipFloat");
    if (pipFloat) pipFloat.hidden = true;
    const pipVid = $("callPipVideo");
    if (pipVid) pipVid.srcObject = null;
    const voice = $("callVoiceOnly");
    if (voice) voice.hidden = false;
    role = "idle";
    setPhase("idle");
    callID = "";
    peerID = "";
    callMode = "voice";
    pendingRemoteOffers.clear();
    pendingIceCandidates.clear();
    syncCallModeUI();
  }

  async function endCall(sendHangup) {
    if (sendHangup && callID) {
      let targets = [];
      if (groupPeerIDs.length) targets = groupPeerIDs;
      else if (peerID) targets = [peerID];
      await Promise.all(targets.map(async (tid) => {
        try { await postSignal("hangup", {}, tid); } catch { /* offline */ }
      }));
    }
    teardown();
    setVisible(false);
  }

  async function broadcast(kind, payload) {
    const targets = callMeshPeerIDs(groupPeerIDs, peerID);
    await Promise.all(targets.map(async (tid) => {
      try { await postSignal(kind, payload, tid); } catch { /* offline */ }
    }));
  }

  function mediaStatePayload() {
    const audioTrack = localStream?.getAudioTracks?.().find((t) => t.readyState === "live");
    const camera = localStream?.getVideoTracks?.().find((t) => t !== screenTrack && t.readyState === "live");
    return {
      audio: Boolean(audioTrack?.enabled),
      video: Boolean(camera && camera.enabled !== false),
      screen: Boolean(screenTrack)
    };
  }

  async function broadcastMediaState() {
    if (!callID) return;
    await broadcast("media-state", mediaStatePayload());
  }

  async function toggleMute() {
    const btn = $("callMute");
    if (!localStream || !btn) return;
    const audioTrack = localStream.getAudioTracks()[0];
    if (!audioTrack) return;
    localAudioOwned = true;
    audioTrack.enabled = !audioTrack.enabled;
    btn.setAttribute("aria-pressed", audioTrack.enabled ? "false" : "true");
    await broadcastMediaState();
  }

  async function forceMute() {
    if (localAudioOwned) return;
    const audioTrack = localStream?.getAudioTracks?.()[0];
    if (audioTrack) audioTrack.enabled = false;
    $("callMute")?.setAttribute("aria-pressed", "true");
    await broadcastMediaState();
  }

  async function replaceVideoTrack(track) {
    if (!localStream) localStream = emptyMediaStream();
    localStream.getVideoTracks().forEach((t) => {
      if (t === screenTrack) return;
      localStream.removeTrack(t);
      if (t !== track) t.stop();
    });
    if (track) localStream.addTrack(track);
    const renegotiate = [];
    peerConnections.forEach((pc, remotePeerID) => {
      const sender = pc.getSenders().find((s) => s.track?.kind === "video" && s.track !== screenTrack && s.track.contentHint !== "detail");
      if (sender?.replaceTrack) sender.replaceTrack(track).catch(() => {});
      else if (track) pc.addTrack(track, localStream);
      renegotiate.push(remotePeerID);
    });
    await Promise.all(renegotiate.map(async (remotePeerID) => {
      if (track) await requestRenegotiation(remotePeerID);
    }));
    showLocalPreview(Boolean(track));
  }

  async function enableCamera() {
    if (!localStream) await ensureMedia(false);
    const existing = localStream?.getVideoTracks?.().find((t) => t !== screenTrack && t.readyState === "live");
    if (existing) {
      existing.enabled = true;
      $("callCamera")?.setAttribute("aria-pressed", "true");
      showLocalPreview(true);
      await broadcastMediaState();
      return;
    }
    if (!mediaDevices?.getUserMedia) return;
    const vStream = await mediaDevices.getUserMedia({ video: videoConstraints(), audio: false });
    const videoTrack = vStream.getVideoTracks()[0];
    if (!videoTrack) return;
    callMode = "video";
    await replaceVideoTrack(videoTrack);
    $("callCamera")?.setAttribute("aria-pressed", "true");
    await broadcastMediaState();
  }

  async function forceCameraOff() {
    if (localVideoOwned) return;
    await replaceVideoTrack(null);
    $("callCamera")?.setAttribute("aria-pressed", "false");
    await broadcastMediaState();
  }

  async function toggleCamera() {
    const btn = $("callCamera");
    if (!btn) return;
    localVideoOwned = true;
    if (btn.getAttribute("aria-pressed") === "true") {
      localVideoOwned = false;
      await replaceVideoTrack(null);
      btn.setAttribute("aria-pressed", "false");
      localVideoOwned = true;
      await broadcastMediaState();
      return;
    }
    await enableCamera();
  }

  async function stopScreenShare() {
    const sharing = screenTrack;
    if (!sharing) return;
    sharing.onended = null;
    sharing.stop();
    peerConnections.forEach((pc, remotePeerID) => {
      const sender = pc.getSenders().find((s) => s.track === sharing);
      if (sender && typeof pc.removeTrack === "function") pc.removeTrack(sender);
      pendingRenegotiation.add(remotePeerID);
    });
    screenTrack = null;
    const localScreen = $("callLocalScreen");
    if (localScreen) {
      localScreen.srcObject = null;
      localScreen.hidden = true;
    }
    $("callScreen")?.setAttribute("aria-pressed", "false");
    const voice = $("callVoiceOnly");
    if (voice && callMode !== "video" && $("callLocalVideo")?.hidden !== false) voice.hidden = false;
    await Promise.all([...peerConnections.keys()].map(async (remotePeerID) => {
      if (role === "active") await renegotiatePeer(remotePeerID);
    }));
    await broadcastMediaState();
    syncScreenStage();
  }

  async function toggleScreenShare() {
    if (screenTrack) return stopScreenShare();
    return shareScreen();
  }

  async function shareScreen() {
    if (!mediaDevices?.getDisplayMedia) {
      const err = new Error("screen unavailable");
      err.name = "NotSupportedError";
      throw err;
    }
    const display = await mediaDevices.getDisplayMedia({ video: true, audio: false });
    const track = display.getVideoTracks()[0];
    if (!track) return;
    track.contentHint = "detail";
    screenTrack = track;
    track.onended = () => {
      stopScreenShare().catch(() => {});
    };
    $("callScreen")?.setAttribute("aria-pressed", "true");
    const localScreen = $("callLocalScreen");
    if (localScreen) {
      localScreen.srcObject = display;
      localScreen.hidden = false;
    }
    const voice = $("callVoiceOnly");
    if (voice) voice.hidden = true;
    await Promise.all([...peerConnections.entries()].map(async ([remotePeerID, pc]) => {
      pc.addTrack(track, display);
      await capVideoSender(pc, track);
      await requestRenegotiation(remotePeerID);
    }));
    await broadcastMediaState();
    focusStageForScreen();
    syncScreenStage();
  }

  function attachScreenStream(remotePeerID, stream) {
    const multi = groupPeerIDs.length > 1 || peerConnections.size > 1;
    if (multi) {
      const grid = $("callRemoteGrid");
      if (!grid) return;
      grid.hidden = false;
      let tile = typeof grid.querySelector === "function"
        ? grid.querySelector('[data-peer="' + remotePeerID + '"][data-kind="screen"]')
        : null;
      if (!tile) {
        tile = document.createElement("div");
        tile.className = "call-remote-tile call-remote-tile--screen";
        tile.dataset.peer = remotePeerID;
        tile.dataset.kind = "screen";
        const video = document.createElement("video");
        video.playsInline = true;
        video.autoplay = true;
        tile.appendChild(video);
        const cap = document.createElement("span");
        cap.textContent = peerLabelFor(remotePeerID) + " · Screen";
        tile.appendChild(cap);
        grid.appendChild(tile);
      }
      const video = tile.querySelector ? tile.querySelector("video") : null;
      if (video) {
        video.srcObject = stream;
        playMedia(video);
      }
      return;
    }
    const el = $("callRemoteScreen");
    if (!el) return;
    el.srcObject = stream;
    el.hidden = false;
    playMedia(el);
    focusStageForScreen();
    syncScreenStage();
  }

  function handleMediaState(remotePeerID, payload) {
    const body = payloadObject(payload);
    if (body.screen === false && groupPeerIDs.length <= 1) {
      const el = $("callRemoteScreen");
      if (el) el.hidden = true;
      syncScreenStage();
    }
    const grid = $("callRemoteGrid");
    const tile = grid?.querySelector?.('[data-peer="' + remotePeerID + '"]');
    if (tile) tile.classList.toggle("is-muted", body.audio === false);
  }

  function showModBanner(action) {
    const banner = $("callModBanner");
    const text = $("callModText");
    if (!banner) return;
    const camera = action === "camera-on-request";
    if (text) {
      text.textContent = camera
        ? "The host asked you to turn your camera on."
        : "The host asked you to unmute.";
    }
    banner.hidden = false;
  }

  async function confirmModRequest() {
    const action = pendingModAction;
    pendingModAction = "";
    const banner = $("callModBanner");
    if (banner) banner.hidden = true;
    if (action === "unmute-request") {
      localAudioOwned = true;
      const audioTrack = localStream?.getAudioTracks?.()[0];
      if (audioTrack) audioTrack.enabled = true;
      $("callMute")?.setAttribute("aria-pressed", "false");
      await broadcastMediaState();
      return;
    }
    if (action === "camera-on-request") {
      localVideoOwned = true;
      await enableCamera();
    }
  }

  function dismissModRequest() {
    pendingModAction = "";
    const banner = $("callModBanner");
    if (banner) banner.hidden = true;
  }

  async function handleMod(payload) {
    const body = payloadObject(payload);
    const target = body.target_device_id ? String(body.target_device_id) : "";
    if (target && selfID && target !== selfID) return;
    if (body.action === "mute") {
      await forceMute();
      return;
    }
    if (body.action === "camera-off") {
      await forceCameraOff();
      return;
    }
    if (body.action === "unmute-request" || body.action === "camera-on-request") {
      pendingModAction = body.action;
      showModBanner(body.action);
    }
  }

  function refreshHostBar() {
    const bar = $("callHostBar");
    if (!bar) return;
    const hosting = role === "active" && hostID && hostID === selfID && groupPeerIDs.length > 0;
    bar.hidden = !hosting;
    const select = $("callHostTarget");
    if (!hosting || !select) return;
    fillDeviceSelect(select, groupPeerIDs.map((id) => ({ deviceId: id, label: peerLabelFor(id) })), groupPeerIDs[0], "", false);
  }

  async function sendMod(action) {
    const target = $("callHostTarget")?.value || peerID;
    if (!target || hostID !== selfID) return;
    await postSignal("mod", { action, target_device_id: target }, target);
  }

  function toggleCallFocus() {
    const live = document.getElementById("meshLive");
    if (!live) return;
    const current = live.dataset.focus || "chat";
    const next = current === "stage" ? "chat" : "stage";
    live.dataset.focus = next;
    const btn = $("callFocus");
    if (!btn) return;
    btn.setAttribute("aria-pressed", next === "stage" ? "true" : "false");
    const label = typeof btn.querySelector === "function" ? btn.querySelector("span") : null;
    if (label) label.textContent = next === "stage" ? "Chat" : "Stage";
  }

  async function promoteHost() {
    const remaining = [selfID, ...groupPeerIDs].filter(Boolean).sort((a, b) => String(a).localeCompare(String(b)));
    const next = remaining[0] || "";
    hostID = next;
    if (next && next === selfID) await broadcast("host", { host_device_id: next });
    refreshHostBar();
  }

  const CALL_EVENT_HANDLERS = {
    invite(ev, remotePeer) {
      const meta = parseInvitePayload(ev.payload);
      const roster = parseCallRoster(ev.payload);
      callMode = meta.mode;
      if (meta.conversationId) linkedConversationID = meta.conversationId;
      if (roster.hostDeviceId) hostID = roster.hostDeviceId;
      else if (!hostID) hostID = remotePeer;
      if (roster.participants.length) {
        groupPeerIDs = roster.participants.filter((id) => id && id !== selfID);
      } else if (remotePeer && !groupPeerIDs.includes(remotePeer)) {
        groupPeerIDs = [remotePeer];
      }
      if (role !== "ringing-in" || callID !== ev.call_id) showRinging(true);
    },
    accept() {
      showActive();
    },
    async offer(ev, remotePeer) {
      await applyRemoteDescription(remotePeer, ev.payload);
    },
    async answer(ev, remotePeer) {
      await applyRemoteDescription(remotePeer, ev.payload);
    },
    async ice(ev, remotePeer) {
      await addIce(remotePeer, ev.payload);
    },
    async hangup(ev, remotePeer) { await peerLeft(remotePeer); },
    async reject(ev, remotePeer) { await peerLeft(remotePeer); },
    async busy(ev, remotePeer) { await peerLeft(remotePeer); },
    "media-state": async function mediaState(ev, remotePeer) { handleMediaState(remotePeer, ev.payload); },
    async mod(ev) { await handleMod(ev.payload); },
    host(ev) {
      const body = payloadObject(ev.payload);
      if (body.host_device_id) hostID = String(body.host_device_id);
      refreshHostBar();
    }
  };

  async function peerLeft(remotePeer) {
    if (groupPeerIDs.length > 1 && groupPeerIDs.includes(remotePeer)) {
      const wasHost = hostID && remotePeer === hostID;
      groupPeerIDs = groupPeerIDs.filter((id) => id !== remotePeer);
      const pc = peerConnections.get(remotePeer);
      if (pc) pc.close();
      peerConnections.delete(remotePeer);
      const grid = $("callRemoteGrid");
      if (grid && typeof grid.querySelectorAll === "function") {
        grid.querySelectorAll('[data-peer="' + remotePeer + '"]').forEach((el) => el.remove());
      } else {
        grid?.querySelector?.('[data-peer="' + remotePeer + '"]')?.remove();
      }
      if (peerID === remotePeer) peerID = groupPeerIDs[0] || "";
      if (wasHost) await promoteHost();
      if (groupPeerIDs.length) {
        syncCallModeUI();
        refreshHostBar();
        return;
      }
    }
    await endCall(false);
  }

  function replyBusy(ev) {
    fetch("/api/call/signal", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ mesh_id: meshID, target_device_id: ev.from_device_id, call_id: ev.call_id, kind: "busy", payload: {} })
    }).catch(() => {});
  }

  function primeCallReplayEvents(body, events) {
    if (eventsPrimed) return events;
    eventsPrimed = true;
    const fresh = (sess) => Date.now() - Date.parse(sess.updated_at || "") < CALL_REPLAY_MAX_MS;
    const live = new Set((body.sessions || []).filter(fresh).map((sess) => sess.call_id));
    return events.filter((ev) => live.has(ev.call_id));
  }

  async function dispatchCallEvent(ev) {
    if (ev.from_device_id === selfID) return;
    if (role === "idle" && ev.kind !== "invite") return;
    if (callID && ev.call_id && ev.call_id !== callID) {
      if (ev.kind === "invite" && role !== "idle") replyBusy(ev);
      if (ev.kind !== "invite" || role !== "idle") return;
      callID = "";
      peerID = "";
    }
    const remotePeer = ev.from_device_id || peerID;
    if (!callID && ev.call_id) callID = ev.call_id;
    if (peerID === "" && remotePeer) {
      peerID = remotePeer;
      setPeerName(peerLabelFor(remotePeer));
    }
    const handler = CALL_EVENT_HANDLERS[ev.kind];
    if (!handler) return;
    await handler(ev, remotePeer);
  }

  async function pollEvents() {
    if (!meshID) return;
    const res = await fetch("/api/call/events?mesh_id=" + encodeURIComponent(meshID) + "&since=" + eventSince, { cache: "no-store" });
    if (!res.ok) return;
    const body = await res.json();
    if (body.seq) eventSince = body.seq;
    let events = primeCallReplayEvents(body, body.events || []);
    await runSequential(events, (ev) => dispatchCallEvent(ev));
  }

  function ensurePoll() {
    if (pollTimer) return;
    pollTimer = setInterval(() => { pollEvents().catch(() => {}); }, 1200);
  }

  function openCallChat() {
    if (!linkedConversationID) return;
    if (deps.openConversation) deps.openConversation(linkedConversationID);
  }

  function bind() {
    $("callClose")?.addEventListener("click", () => endCall(true).catch(() => {}));
    $("callEnd")?.addEventListener("click", () => endCall(true).catch(() => {}));
    $("callDecline")?.addEventListener("click", () => {
      postSignal("reject", {}, peerID).catch(() => {}).finally(() => {
        endCall(false).catch(() => {});
      });
    });
    $("callAccept")?.addEventListener("click", () => acceptInbound().catch(async (err) => {
      const reason = describeCallError(err);
      await endCall(true);
      lastOutbound = null;
      showFailure("Could not join call", reason);
    }));
    $("callRetry")?.addEventListener("click", () => retryOutbound().catch(() => {}));
    $("callMute")?.addEventListener("click", () => toggleMute().catch(() => {}));
    $("callCamera")?.addEventListener("click", () => {
      setError("");
      return toggleCamera().catch((err) => setError(describeCallError(err, "camera")));
    });
    $("callScreen")?.addEventListener("click", () => {
      setError("");
      return toggleScreenShare().catch((err) => {
        if (err?.name === "NotAllowedError") return;
        if (err?.name === "NotSupportedError") {
          setError("This browser can't share the screen. Open the dashboard in Chrome or Firefox.");
          return;
        }
        setError("Couldn't share the screen. Try again.");
      });
    });
    $("callPip")?.addEventListener("click", () => toggleScreenPip().catch(() => {}));
    $("callPipClose")?.addEventListener("click", () => exitScreenPip(true).catch(() => {}));
    $("callChatToggle")?.addEventListener("click", () => openCallChat());
    $("callSetupContinue")?.addEventListener("click", () => finishSetupWait());
    $("callModConfirm")?.addEventListener("click", () => confirmModRequest().catch((err) => setError(describeCallError(err, "camera"))));
    $("callModDismiss")?.addEventListener("click", () => dismissModRequest());
    $("callFocus")?.addEventListener("click", () => toggleCallFocus());
    $("callHostMute")?.addEventListener("click", () => sendMod("mute").catch(() => {}));
    $("callHostCamera")?.addEventListener("click", () => sendMod("camera-off").catch(() => {}));
    $("callHostUnmute")?.addEventListener("click", () => sendMod("unmute-request").catch(() => {}));
    $("callHostCameraOn")?.addEventListener("click", () => sendMod("camera-on-request").catch(() => {}));
  }

  bind();
  syncCallRuntimeHints();

  return {
    apply(ctx) {
      meshID = ctx.meshID || "";
      selfID = ctx.selfID || "";
      const busyElsewhere = role !== "idle" && ctx.incoming && callID && ctx.incoming.call_id !== callID;
      if (ctx.incoming?.call_id && !busyElsewhere) {
        const inc = ctx.incoming;
        const sameIncoming = callID === inc.call_id && (role === "ringing-in" || role === "active");
        callID = inc.call_id;
        peerID = inc.peer_device_id || peerID;
        if (inc.mode) callMode = inc.mode === "video" ? "video" : "voice";
        if (inc.conversation_id) linkedConversationID = String(inc.conversation_id);
        setPeerName(peerLabelFor(peerID));
        if (!sameIncoming) showRinging(true);
        ensurePoll();
      }
      ensurePoll();
    },
    startCall(targetID, label, mode, conversationId) {
      ensurePoll();
      return startOutbound(targetID, label, mode, conversationId);
    },
    startGroupCall(targetIDs, label, mode, conversationId) {
      ensurePoll();
      return startOutboundMany(targetIDs, label, mode, conversationId);
    },
    openFromHash() {
      if (location.hash === "#call") setVisible(role !== "idle");
    },
    retry: retryOutbound,
    teardown
  };
}

export {
  parseInvitePayload,
  createMeshCall,
  runSequential,
  screenStageMode,
  pipScreenVideoId,
  pipCompanionVideoId,
  supportsDocumentPictureInPicture,
  SCREEN_PIP_LIMITATION,
  meshIceServers,
  CALL_NAT_NOTE
};

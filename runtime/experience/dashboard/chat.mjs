const FILE_KIND_STRATEGIES = [
  { kind: "image", match: (ext, m) => m.startsWith("image/") || ["png", "jpg", "jpeg", "gif", "webp", "svg", "avif", "heic"].includes(ext) },
  { kind: "audio", match: (ext, m) => m.startsWith("audio/") || ["mp3", "wav", "ogg", "m4a", "flac"].includes(ext) },
  { kind: "video", match: (ext, m) => m.startsWith("video/") || ["mp4", "webm", "mov", "mkv"].includes(ext) },
  { kind: "pdf", match: (ext, m) => m === "application/pdf" || ext === "pdf" },
  { kind: "markdown", match: (ext, m) => m === "text/markdown" || ext === "md" || ext === "markdown" },
  { kind: "json", match: (ext) => ["json", "yaml", "yml", "toml"].includes(ext) },
  { kind: "sheet", match: (ext) => ["xls", "xlsx", "csv", "ods", "numbers"].includes(ext) },
  { kind: "slides", match: (ext) => ["ppt", "pptx", "key"].includes(ext) },
  { kind: "zip", match: (ext) => ["zip", "tar", "gz", "tgz", "rar", "7z"].includes(ext) },
  { kind: "exe", match: (ext) => ["exe", "dmg", "apk", "deb", "rpm", "appimage"].includes(ext) },
  { kind: "font", match: (ext) => ["ttf", "otf", "woff", "woff2"].includes(ext) },
  { kind: "code", match: (ext) => ["go", "py", "rs", "js", "jsx", "ts", "tsx", "rb", "java", "c", "h", "cpp", "cc", "sh", "bash", "css", "html", "sql"].includes(ext) },
  { kind: "text", match: (ext, m) => m.startsWith("text/") || ext === "txt" },
  { kind: "doc", match: (ext) => ["doc", "docx", "odt", "rtf", "pages"].includes(ext) }
];

function fileKind(name, mime) {
  const ext = String(name || "").split(".").pop().toLowerCase();
  const m = String(mime || "").toLowerCase();
  for (const rule of FILE_KIND_STRATEGIES) {
    if (rule.match(ext, m)) return rule.kind;
  }
  return "doc";
}

function sortConversationsByActivity(list) {
  const at = (conv) => {
    const t = Date.parse(conv.last_timestamp || "");
    return Number.isFinite(t) ? t : 0;
  };
  return (list || []).slice().sort((a, b) => at(b) - at(a));
}

const VOICE_BAR_COUNT = 36;
const VOICE_BAR_FLOOR = 0.12;
const REC_BAR_W = 3;
const REC_BAR_GAP = 2;
const REC_SAMPLE_MS = 90;
const REC_MAX_BARS = 160;
const VOICE_CAPTURE_AUDIO_CONSTRAINTS = {
  echoCancellation: true,
  noiseSuppression: true,
  autoGainControl: true,
  voiceIsolation: true
};

function waveformPeaks(samples, bins) {
  const count = Math.max(1, Math.trunc(bins));
  const out = new Array(count).fill(0);
  const len = samples ? samples.length : 0;
  if (!len) return out.fill(VOICE_BAR_FLOOR);
  const step = len / count;
  let loudest = 0;
  for (let b = 0; b < count; b++) {
    const start = Math.floor(b * step);
    const end = Math.max(start + 1, Math.floor((b + 1) * step));
    let peak = 0;
    for (let i = start; i < end && i < len; i++) {
      const v = Math.abs(samples[i]);
      if (v > peak) peak = v;
    }
    out[b] = peak;
    if (peak > loudest) loudest = peak;
  }
  return out.map((p) => (loudest > 0 ? Math.max(VOICE_BAR_FLOOR, p / loudest) : VOICE_BAR_FLOOR));
}

// Loudness of one analyser frame (bytes centered on 128); the square root lifts quiet speech into view.
function recordingLevel(timeDomain) {
  const len = timeDomain ? timeDomain.length : 0;
  if (!len) return VOICE_BAR_FLOOR;
  let sum = 0;
  for (let i = 0; i < len; i++) {
    const v = (timeDomain[i] - 128) / 128;
    sum += v * v;
  }
  const level = Math.min(1, Math.sqrt(Math.sqrt(sum / len)) * 1.4);
  return Math.max(VOICE_BAR_FLOOR, level);
}

function formatVoiceTime(sec) {
  const s = Number.isFinite(sec) && sec > 0 ? Math.floor(sec) : 0;
  return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
}

const INLINE_MEDIA_KINDS = new Set(["image", "audio", "video", "pdf"]);
const INLINE_MEDIA_MIME = {
  image: /^image\/(png|jpe?g|gif|webp|avif|heic|bmp)$/,
  audio: /^audio\/[a-z0-9.+-]+(;.*)?$/,
  video: /^video\/[a-z0-9.+-]+(;.*)?$/
};

// Sender-supplied MIME types must never turn an attachment into a same-origin
// document (text/html, image/svg+xml in a blob: URL), so inline media is pinned
// to a type that matches the rendered element.
function inlineMediaMime(kind, name, mime) {
  const m = String(mime || "").trim().toLowerCase();
  const ext = String(name || "").split(".").pop().toLowerCase();
  if (kind === "pdf") return "application/pdf";
  if (kind === "image" && (m === "image/svg+xml" || ext === "svg")) return "image/svg+xml";
  const rule = INLINE_MEDIA_MIME[kind];
  if (rule?.test(m)) return m;
  return "application/octet-stream";
}

function chatMemberCountLabel(n) {
  const count = Number(n) || 0;
  if (count <= 0) return "";
  return count === 1 ? "1 member" : count + " members";
}

function peerPresenceMeta(member, circleName, peerOnlineFn) {
  const on = peerOnlineFn(member);
  return (on ? "Online" : "Offline") + (circleName ? " · " + circleName : "");
}

function openDirectConversationMeta(conv, selfID, circle, circleName, peerOnlineFn) {
  if (conv?.kind === "project") return circleName;
  const parts = String(conv?.conversation_id || "").split(":");
  if (parts[0] !== "direct" || parts.length < 3) return circleName;
  const other = parts[1] === selfID ? parts[2] : parts[1];
  const member = circle?.members ? circle.members.find((item) => item.device_id === other) : null;
  if (!member) return circleName;
  return peerPresenceMeta(member, circleName, peerOnlineFn);
}

function matchInlineAt(text, from) {
  const rest = text.slice(from);
  let m = /^!\[([^\]]*)\]\((https?:\/\/[^\s)]+)\)/.exec(rest);
  if (m) return { len: m[0].length, kind: "img", alt: m[1], href: m[2] };
  m = /^\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/.exec(rest);
  if (m) return { len: m[0].length, kind: "link", label: m[1], href: m[2] };
  m = /^`([^`]+)`/.exec(rest);
  if (m) return { len: m[0].length, kind: "code", text: m[1] };
  m = /^\*\*([^*]+)\*\*/.exec(rest);
  if (m) return { len: m[0].length, kind: "strong", text: m[1] };
  m = /^\*([^*]+)\*/.exec(rest);
  if (m) return { len: m[0].length, kind: "em", text: m[1] };
  m = /^https?:\/\/[^\s<]+/.exec(rest);
  if (m) return { len: m[0].length, kind: "url", href: m[0] };
  return null;
}

function scanInlineText(text) {
  const src = String(text || "");
  const parts = [];
  let pos = 0;
  while (pos < src.length) {
    const hit = matchInlineAt(src, pos);
    if (hit?.len > 0) {
      parts.push(hit);
      pos += hit.len;
      continue;
    }
    const next = src.slice(pos).search(/!\[|\[|`|\*\*|https?:\/\//);
    let end = next < 0 ? src.length : pos + next;
    if (end <= pos) end = pos + 1;
    parts.push({ kind: "text", len: end - pos, text: src.slice(pos, end) });
    pos = end;
  }
  return parts;
}

const YOUTUBE_VIDEO_ID_RE = /^[\w-]{11}$/;
const VIMEO_VIDEO_ID_RE = /^\d{1,20}$/;
const VIDEO_EMBED_ALLOWED_HOSTS = new Set([
  "youtube.com",
  "www.youtube.com",
  "m.youtube.com",
  "music.youtube.com",
  "youtu.be",
  "www.youtu.be",
  "vimeo.com",
  "www.vimeo.com",
  "player.vimeo.com",
]);

function sanitizeYouTubeVideoId(raw) {
  const id = String(raw || "").trim();
  return YOUTUBE_VIDEO_ID_RE.test(id) ? id : "";
}

function sanitizeVimeoVideoId(raw) {
  const id = String(raw || "").trim();
  return VIMEO_VIDEO_ID_RE.test(id) ? id : "";
}

function youtubeEmbedSpec(id, watchUrl) {
  return {
    provider: "youtube",
    videoId: id,
    watchUrl: watchUrl || "https://www.youtube.com/watch?v=" + id,
    embedUrl: "https://www.youtube-nocookie.com/embed/" + id,
    thumbUrl: "https://i.ytimg.com/vi/" + id + "/hqdefault.jpg",
    title: "YouTube",
  };
}

function vimeoEmbedSpec(id, watchUrl) {
  return {
    provider: "vimeo",
    videoId: id,
    watchUrl: watchUrl || "https://vimeo.com/" + id,
    embedUrl: "https://player.vimeo.com/video/" + id,
    thumbUrl: "",
    title: "Vimeo",
  };
}

function parseVideoEmbedUrl(raw) {
  const cleaned = String(raw || "").trim().replace(/[),.;]+$/, "");
  let u;
  try {
    u = new URL(cleaned);
  } catch {
    return null;
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") return null;
  const host = u.hostname.toLowerCase();
  if (!VIDEO_EMBED_ALLOWED_HOSTS.has(host)) return null;

  if (host === "youtu.be" || host === "www.youtu.be") {
    const id = sanitizeYouTubeVideoId(u.pathname.slice(1).split("/")[0]);
    return id ? youtubeEmbedSpec(id, cleaned) : null;
  }
  if (host.endsWith("youtube.com")) {
    let id = "";
    if (u.pathname.startsWith("/watch")) {
      id = sanitizeYouTubeVideoId(u.searchParams.get("v"));
    } else if (u.pathname.startsWith("/embed/")) {
      id = sanitizeYouTubeVideoId(u.pathname.slice(7).split("/")[0]);
    } else if (u.pathname.startsWith("/shorts/")) {
      id = sanitizeYouTubeVideoId(u.pathname.slice(8).split("/")[0]);
    } else if (u.pathname.startsWith("/live/")) {
      id = sanitizeYouTubeVideoId(u.pathname.slice(6).split("/")[0]);
    }
    return id ? youtubeEmbedSpec(id, cleaned) : null;
  }

  if (host === "player.vimeo.com" && u.pathname.startsWith("/video/")) {
    const id = sanitizeVimeoVideoId(u.pathname.slice(7).split("/")[0]);
    return id ? vimeoEmbedSpec(id, cleaned) : null;
  }
  if (host === "vimeo.com" || host === "www.vimeo.com") {
    const m = /^\/(\d+)/.exec(u.pathname);
    const id = m ? sanitizeVimeoVideoId(m[1]) : "";
    return id ? vimeoEmbedSpec(id, cleaned) : null;
  }
  return null;
}

const MESH_CHAT_MIN_WIDTH = 280;
const MESH_CHAT_SPLIT_TAIL = 160;
const MESH_STAGE_MIN_WIDTH = 320;
const MESH_STAGE_MAX_WIDTH = 960;

function meshSplitPaneWidth(focus, clientX, rectLeft, rectWidth) {
  const x = clientX - rectLeft;
  if (focus === "stage") {
    return Math.max(MESH_STAGE_MIN_WIDTH, Math.min(MESH_STAGE_MAX_WIDTH, x));
  }
  return Math.max(MESH_CHAT_MIN_WIDTH, Math.min(rectWidth - MESH_CHAT_SPLIT_TAIL, x));
}

function createMeshChat(deps) {
  const CARD_TYPES = {
    context: "Context", checkpoint: "Checkpoint", decision: "Decision", handoff: "Handoff", code: "Code",
    terminal_session: "Live session"
  };
  const CIRCLE_GROUP_ID = "group:circle";
  const CHAT_FILE_MAX_BYTES = 4 * 1024 * 1024;
  const AUDIO_RECORD_MAX_MS = 5 * 60 * 1000;
  const WIDGET_SIZE_KEY = "messagesWidgetSize";
  const WIDGET_POS_KEY = "messagesWidgetPos";
  const EMOJI_RECENT_KEY = "overdriveEmojiRecent";
  const EMOJI_RECENT_MAX = 36;
  let meshID = "";
  let selfID = "";
  let openConversation = "";
  let threadSig = "";
  let pinned = false;
  let drafting = false;
  let expanded = false;
  let lastMessages = [];
  let conversations = [];
  let circle = null;
  let data = null;
  let peerOptions = [];
  let composeFilter = "";
  let pendingFile = null;
  let emojiOpen = false;
  let emojiCatalog = null;
  let emojiPickerReady = false;
  let emojiActiveTab = "recent";
  let emojiGridCache = {};
  let audioRecording = false;
  let audioRecorder = null;
  let audioChunks = [];
  let audioStream = null;
  let audioContext = null;
  let audioAnalyser = null;
  let audioVizRaf = 0;
  let audioRecordTimer = null;
  let audioSkipSend = false;
  let micSuppressClick = false;
  let composerDragDepth = 0;
  let composerStatusTimer = null;
  let composeMode = "direct";
  let composePicked = new Set();
  let chatGroups = [];
  let renderedThreadKey = null;
  let forceThreadScroll = false;
  let sendingMessage = false;
  let audioStarting = false;
  let creatingGroup = false;
  const conversationDrafts = new Map();

  function clearComposerStatus() {
    const status = document.getElementById("composerStatus");
    if (composerStatusTimer) {
      clearTimeout(composerStatusTimer);
      composerStatusTimer = null;
    }
    if (!status) return;
    status.textContent = "";
    status.hidden = true;
  }
  function setComposerStatus(text, opts) {
    const status = document.getElementById("composerStatus");
    if (!status) return;
    if (composerStatusTimer) {
      clearTimeout(composerStatusTimer);
      composerStatusTimer = null;
    }
    const msg = String(text || "").trim();
    if (!msg) {
      clearComposerStatus();
      return;
    }
    status.textContent = msg;
    status.hidden = false;
    const ttl = opts?.ttlMs;
    if (ttl > 0) {
      composerStatusTimer = setTimeout(() => clearComposerStatus(), ttl);
    }
  }
  const openAudioFilePicker = () => {
    const audioInput = document.getElementById("messageAudioFile");
    if (audioInput) audioInput.click();
  }
  const microphoneAlreadyDenied = async () => {
    if (!navigator.permissions?.query) return false;
    try {
      const result = await navigator.permissions.query({ name: "microphone" });
      return result.state === "denied";
    } catch (error_) {
      globalThis.console?.debug?.(error_);
      return false;
    }
  }

  function el(tag, className, text) {
    return deps.el(tag, className, text);
  }
  function currentData() {
    return data || deps.getData?.() || {};
  }
  function newestId() {
    const sorted = sortConversationsByActivity(conversations);
    return sorted.length ? sorted[0].conversation_id : "";
  }
  function conversationTitle(conv) {
    if (conv.name && (conv.kind === "group" || conv.kind === "circle")) return conv.name;
    if (conv.kind === "circle" || conv.conversation_id === CIRCLE_GROUP_ID) return "Circle";
    if (conv.kind === "group") return "Group";
    if (conv.kind === "project") return conv.project_id || "Project";
    const parts = String(conv.conversation_id || "").split(":");
    if (parts[0] === "direct" && parts.length >= 3) {
      const other = parts[1] === selfID ? parts[2] : parts[1];
      const member = deps.roster(currentData()).find((item) => item.device_id === other);
      return member ? deps.computerLabel(member, currentData()) : "Direct";
    }
    return conv.preview || "Conversation";
  }
  function paintUnread() {
    const unread = conversations.reduce((sum, conv) => sum + (Number(conv.unread) || 0), 0);
    const quiet = document.getElementById("msgQuiet");
    if (quiet) {
      quiet.textContent = unread ? String(unread) : "";
      if (unread) quiet.setAttribute("aria-label", unread + " unread");
      else quiet.removeAttribute("aria-label");
    }
    const badge = document.getElementById("msgCount");
    if (badge) badge.textContent = unread ? String(unread) : "";
  }
  function updateHeaderDrafting(title, meta, circleName) {
    const peer = document.getElementById("messagePeer")?.value || "";
    const label = document.getElementById("messagePeerLabel")?.textContent || "";
    const isGroup = composeMode === "group";
    if (peer && label) title.textContent = label;
    else if (isGroup) title.textContent = "New group";
    else title.textContent = "New message";
    const member = peer && circle?.members
      ? circle.members.find((item) => item.device_id === peer)
      : null;
    if (peer && member) meta.textContent = peerPresenceMeta(member, circleName, peerOnline);
    else if (peer) meta.textContent = circleName;
    else meta.textContent = isGroup ? "Name and members" : "Choose a device";
  }

  function updateHeaderOpenConv(title, meta, circleName) {
    const conv = conversations.find((item) => item.conversation_id === openConversation);
    title.textContent = conv ? conversationTitle(conv) : "Messages";
    if (conv && (conv.kind === "group" || conv.kind === "circle")) {
      const n = (conv.member_device_ids?.length) || 0;
      const countLabel = chatMemberCountLabel(n) || "Group";
      meta.textContent = countLabel + (circleName ? " · " + circleName : "");
      return;
    }
    meta.textContent = openDirectConversationMeta(conv, selfID, circle, circleName, peerOnline);
  }

  function updateHeader() {
    const title = document.getElementById("msgTitle");
    const meta = document.getElementById("msgMeta");
    if (!title || !meta) return;
    const circleName = (circle?.name) || "";
    if (!meshID) {
      title.textContent = "Messages";
      meta.textContent = "No circle yet";
      return;
    }
    if (drafting) {
      updateHeaderDrafting(title, meta, circleName);
      return;
    }
    updateHeaderOpenConv(title, meta, circleName);
  }
  function syncComposeModeUI() {
    const direct = document.getElementById("composeTabDirect");
    const group = document.getElementById("composeTabGroup");
    const title = document.getElementById("composeTitle");
    const nameWrap = document.getElementById("composeGroupNameWrap");
    const footer = document.getElementById("composeFooter");
    const isGroup = composeMode === "group";
    direct?.classList.toggle("active", !isGroup);
    group?.classList.toggle("active", isGroup);
    direct?.setAttribute("aria-selected", isGroup ? "false" : "true");
    group?.setAttribute("aria-selected", isGroup ? "true" : "false");
    if (title) title.textContent = isGroup ? "New group" : "New message";
    if (nameWrap) nameWrap.hidden = !isGroup;
    if (footer) footer.hidden = !isGroup;
    syncComposeCreateButton();
    updateHeader();
    renderComposeChannels();
    renderComposeContacts(composeFilter);
  }
  const isComposeSheetOpen = () => {
    const sheet = document.getElementById("composeSheet");
    return Boolean(sheet?.classList.contains("compose-sheet--open") && !sheet.hidden);
  }
  function openComposeSheet() {
    const panel = document.getElementById("messages");
    const sheet = document.getElementById("composeSheet");
    if (sheet) {
      sheet.classList.add("compose-sheet--open");
      sheet.hidden = false;
      sheet.setAttribute("aria-hidden", "false");
    }
    panel?.classList.add("compose-open");
    document.getElementById("composeBack")?.removeAttribute("hidden");
    syncComposeModeUI();
    document.getElementById("composeSearch")?.focus();
  }
  const closeComposeSheet = () => {
    const panel = document.getElementById("messages");
    const sheet = document.getElementById("composeSheet");
    if (sheet) {
      sheet.classList.remove("compose-sheet--open");
      sheet.hidden = true;
      sheet.setAttribute("aria-hidden", "true");
    }
    panel?.classList.remove("compose-open");
    const back = document.getElementById("composeBack");
    if (back) back.setAttribute("hidden", "");
  }
  function composerBusy() {
    const field = document.getElementById("messageText");
    return Boolean((field?.value || "").trim()) || Boolean(pendingFile) || audioRecording || audioStarting;
  }
  function selectConversation(id = "") {
    const next = id;
    if (next === openConversation) return;
    if (audioRecording) cancelAudioRecording();
    if (pendingFile) clearPendingFile();
    const field = document.getElementById("messageText");
    if (field) {
      if (openConversation && field.value.trim()) conversationDrafts.set(openConversation, field.value);
      else conversationDrafts.delete(openConversation);
      field.value = conversationDrafts.get(next) || "";
      field.style.height = "";
    }
    closeEmojiPanel();
    openConversation = next;
    syncComposerSendMic();
  }
  function exitComposeFlow() {
    drafting = false;
    pinned = false;
    selectConversation(newestId());
    threadSig = "";
    closeComposeSheet();
    syncNewButton();
    updateHeader();
    if (openConversation && meshID) loadThread(openConversation, expanded).catch(() => {});
    else renderThread(lastMessages);
  }
  function dismissComposeSheet() {
    if (drafting) exitComposeFlow();
    else closeComposeSheet();
  }
  const contactInitial = (label) => {
    const ch = String(label || "?").trim().charAt(0);
    return ch ? ch.toUpperCase() : "?";
  }
  function setRecipientValue(value, text, opts) {
    const silent = opts?.silent;
    const hidden = document.getElementById("messagePeer");
    const label = document.getElementById("messagePeerLabel");
    if (hidden) hidden.value = value || "";
    if (label) label.textContent = text || "";
    renderComposeContacts(composeFilter);
    updateHeader();
    if (!silent && hidden) hidden.dispatchEvent(new Event("change", { bubbles: true }));
  }
  function peerOnline(peer) {
    return deps.peerAlive ? deps.peerAlive(peer) : Boolean(peer?.endpoint);
  }
  function presenceText(peer) {
    if (deps.peerPresenceLabel) return deps.peerPresenceLabel(peer);
    return peerOnline(peer) ? "online" : "offline";
  }
  function appendPresenceBadge(parent, peer) {
    const on = peerOnline(peer);
    const badge = el("span", "compose-presence " + (on ? "online" : "offline"));
    const dot = el("span", "presence-dot " + (on ? "on" : "off"), "");
    dot.setAttribute("aria-hidden", "true");
    badge.append(dot, document.createTextNode(on ? "Online" : "Offline"));
    parent.appendChild(badge);
  }
  function openGroupConversation(conversationId) {
    if (!conversationId) return;
    drafting = false;
    pinned = true;
    selectConversation(conversationId);
    threadSig = "";
    closeComposeSheet();
    syncNewButton();
    updateHeader();
    renderConversationList();
    if (meshID) loadThread(conversationId, true).catch(() => {});
  }
  function renderComposeChannels() {
    const list = document.getElementById("composeChannels");
    if (!list || !meshID) {
      list?.replaceChildren();
      return;
    }
    list.replaceChildren();
    const circleRow = el("button", "compose-contact");
    circleRow.type = "button";
    circleRow.dataset.value = CIRCLE_GROUP_ID;
    circleRow.setAttribute("aria-selected", openConversation === CIRCLE_GROUP_ID ? "true" : "false");
    const avatar = el("span", "compose-avatar", "#");
    const text = el("div", "compose-contact-text");
    text.append(el("strong", "", "Circle channel"));
    text.append(el("small", "", "Everyone in this circle"));
    circleRow.append(avatar, text);
    circleRow.addEventListener("click", () => openGroupConversation(CIRCLE_GROUP_ID));
    list.appendChild(circleRow);
  }
  function renderComposeContacts(filter) {
    const list = document.getElementById("composeContacts");
    const hidden = document.getElementById("messagePeer");
    if (!list || !hidden) return;
    composeFilter = String(filter || "");
    const current = hidden.value;
    const groupMode = composeMode === "group";
    const members = ((circle?.members) || [])
      .filter((peer) => peer.device_id && !peer.self && !peer.revoked);
    peerOptions = members.map((peer) => ({
      peer,
      value: peer.device_id,
      label: deps.computerLabel(peer, currentData()),
      sub: peer.hostname || peer.device_id,
      online: peerOnline(peer)
    }));
    const q = composeFilter.trim().toLowerCase();
    let visible = q
      ? peerOptions.filter((opt) => {
          const pres = presenceText(opt.peer);
          return opt.label.toLowerCase().includes(q) || opt.sub.toLowerCase().includes(q) || pres.includes(q);
        })
      : peerOptions.slice();
    visible.sort((a, b) => {
      if (a.online !== b.online) return a.online ? -1 : 1;
      return a.label.localeCompare(b.label);
    });
    list.replaceChildren();
    if (!visible.length) {
      const empty = el("p", "compose-empty", peerOptions.length ? "No devices match your search" : "No other device in this circle");
      list.appendChild(empty);
      return;
    }
    function activateComposeContact(opt) {
      if (groupMode) {
        if (composePicked.has(opt.value)) composePicked.delete(opt.value);
        else composePicked.add(opt.value);
        syncComposeCreateButton();
        renderComposeContacts(composeFilter);
        return;
      }
      setRecipientValue(opt.value, opt.label);
      closeComposeSheet();
      document.getElementById("messageText")?.focus();
    }
    const renderBlock = (title, items) => {
      if (!items.length) return;
      if (title) list.appendChild(el("p", "compose-section-label compose-section-label--minor", title));
      for (const opt of items) {
        const row = el("button", "compose-contact" + (opt.online ? "" : " compose-contact-offline"));
        row.type = "button";
        row.setAttribute("role", "option");
        row.dataset.value = opt.value;
        const picked = composePicked.has(opt.value);
        let ariaSelected = "false";
        if (groupMode) ariaSelected = picked ? "true" : "false";
        else if (opt.value === current) ariaSelected = "true";
        row.setAttribute("aria-selected", ariaSelected);
        if (groupMode && picked) row.classList.add("compose-picked");
        const avatar = el("span", "compose-avatar", contactInitial(opt.label));
        const text = el("div", "compose-contact-text");
        text.append(el("strong", "", opt.label));
        text.append(el("small", "", opt.sub));
        appendPresenceBadge(row, opt.peer);
        row.append(avatar, text);
        row.addEventListener("click", () => activateComposeContact(opt));
        list.appendChild(row);
      }
    };
    if (q) {
      renderBlock("", visible);
    } else {
      renderBlock("Online", visible.filter((opt) => opt.online));
      renderBlock("Offline", visible.filter((opt) => !opt.online));
    }
  }
  function syncComposerSendMic() {
    const field = document.getElementById("messageText");
    const send = document.getElementById("messageSend");
    const mic = document.getElementById("messageAudio");
    const hasPayload = Boolean((field?.value || "").trim()) || pendingFile;
    if (send) send.hidden = !hasPayload || audioRecording;
    if (mic) mic.hidden = hasPayload && !audioRecording;
  }
  function syncRecordingChrome() {
    const field = document.getElementById("messageText");
    const rec = document.getElementById("composerRec");
    const time = document.getElementById("composerRecTime");
    const pill = document.querySelector(".composer-pill");
    if (pill) pill.classList.toggle("composer-recording-active", audioRecording);
    if (field) field.hidden = audioRecording;
    if (rec) rec.hidden = !audioRecording;
    if (time && !audioRecording) time.textContent = formatVoiceTime(0);
    syncComposerSendMic();
  }
  function teardownAudioVisualizer() {
    if (audioVizRaf) {
      cancelAnimationFrame(audioVizRaf);
      audioVizRaf = 0;
    }
    audioAnalyser = null;
    if (audioContext) {
      audioContext.close().catch(() => {});
      audioContext = null;
    }
  }
  function setupAudioVisualizer(stream) {
    const canvas = document.getElementById("composerAudioViz");
    if (!canvas || !stream) return;
    teardownAudioVisualizer();
    const Ctx = globalThis.AudioContext || globalThis.webkitAudioContext;
    if (!Ctx) return;
    audioContext = new Ctx();
    const source = audioContext.createMediaStreamSource(stream);
    audioAnalyser = audioContext.createAnalyser();
    audioAnalyser.fftSize = 512;
    source.connect(audioAnalyser);
    syncRecordingChrome();
    const frame = new Uint8Array(audioAnalyser.fftSize);
    const time = document.getElementById("composerRecTime");
    const started = performance.now();
    const levels = [];
    let lastSample = 0;
    let peak = VOICE_BAR_FLOOR;
    const draw = (now) => {
      if (!audioRecording || !audioAnalyser) return;
      audioAnalyser.getByteTimeDomainData(frame);
      peak = Math.max(peak, recordingLevel(frame));
      if (now - lastSample >= REC_SAMPLE_MS) {
        levels.push(peak);
        if (levels.length > REC_MAX_BARS) levels.shift();
        peak = VOICE_BAR_FLOOR;
        lastSample = now;
      }
      if (time) time.textContent = formatVoiceTime((now - started) / 1000);
      paintRecordingWave(canvas, levels);
      audioVizRaf = requestAnimationFrame(draw);
    };
    audioVizRaf = requestAnimationFrame(draw);
  }
  // Same bars as the voice player: newest on the right, empty slots drawn as quiet floor bars.
  const paintRecordingWave = (canvas, levels) => {
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    const dpr = globalThis.devicePixelRatio || 1;
    const w = Math.round((canvas.clientWidth || 120) * dpr);
    const h = Math.round((canvas.clientHeight || 28) * dpr);
    if (canvas.width !== w) canvas.width = w;
    if (canvas.height !== h) canvas.height = h;
    ctx.clearRect(0, 0, w, h);
    const barW = REC_BAR_W * dpr;
    const step = barW + REC_BAR_GAP * dpr;
    const slots = Math.max(1, Math.floor((w + REC_BAR_GAP * dpr) / step));
    const shown = levels.slice(-slots);
    const offset = slots - shown.length;
    const minH = 3 * dpr;
    for (let i = 0; i < slots; i++) {
      const level = i < offset ? VOICE_BAR_FLOOR : shown[i - offset];
      const bh = Math.max(minH, level * h);
      const x = w - (slots - i) * step + REC_BAR_GAP * dpr;
      ctx.fillStyle = i < offset ? "rgba(255, 255, 255, 0.32)" : "#fff";
      ctx.beginPath();
      if (ctx.roundRect) ctx.roundRect(x, (h - bh) / 2, barW, bh, barW / 2);
      else ctx.rect(x, (h - bh) / 2, barW, bh);
      ctx.fill();
    }
  }
  function clearAudioRecordTimer() {
    if (audioRecordTimer) {
      clearTimeout(audioRecordTimer);
      audioRecordTimer = null;
    }
  }
  function hasOutboundRecipient() {
    if (openConversation && !drafting) return true;
    const recipient = document.getElementById("messagePeer")?.value || "";
    return Boolean(recipient.trim());
  }
  function syncChrome() {
    const panel = document.getElementById("messages");
    if (panel) panel.classList.toggle("expanded", expanded);
    const list = document.getElementById("conversationList");
    if (list) list.hidden = !expanded;
    const pressed = String(expanded);
    const button = document.getElementById("msgExpand");
    if (button) {
      button.setAttribute("aria-pressed", pressed);
      button.classList.toggle("is-expanded", expanded);
      button.setAttribute("aria-label", expanded ? "Collapse conversation" : "Expand conversation");
      button.title = expanded ? "Collapse" : "Expand";
    }
  }
  function syncNewButton() {
    const newer = document.getElementById("msgNew");
    if (!newer) return;
    newer.classList.toggle("is-cancel", drafting);
    newer.setAttribute("aria-label", drafting ? "Cancel new message" : "New message");
    newer.title = drafting ? "Cancel" : "New message";
  }
  function threadEmptyLabel() {
    if (drafting) return "New message";
    if (!meshID) return "No circle yet.";
    if (expanded && !conversations.length) return "";
    return "No messages yet.";
  }
  function renderConversationList() {
    const list = document.getElementById("conversationList");
    if (!list) return;
    list.replaceChildren();
    const group = el("div", "conv-list-group");
    if (!meshID) {
      group.appendChild(el("p", "conv-list-empty", "No circle yet."));
      list.appendChild(group);
      return;
    }
    if (!conversations.length) {
      group.appendChild(el("p", "conv-list-empty", "No conversations yet."));
      list.appendChild(group);
      return;
    }
    sortConversationsByActivity(conversations).forEach((conv) => {
      const btn = el("button", "conv" + (conv.conversation_id === openConversation && !drafting ? " active" : ""));
      btn.type = "button";
      btn.append(el("strong", "", conversationTitle(conv)));
      const bits = [];
      if (conv.unread) bits.push(conv.unread + " unread");
      if (conv.preview) bits.push(conv.preview);
      btn.appendChild(el("small", "", bits.join(" · ")));
      btn.addEventListener("click", () => {
        drafting = false;
        pinned = true;
        selectConversation(conv.conversation_id);
        threadSig = "";
        syncNewButton();
        updateHeader();
        renderConversationList();
        loadThread(openConversation, true).catch(() => {});
      });
      group.appendChild(btn);
    });
    list.appendChild(group);
  }
  const stamp = (iso) => {
    const t = Date.parse(iso || "");
    if (!Number.isFinite(t)) return "";
    const date = new Date(t);
    const now = new Date();
    const sameDay = date.getFullYear() === now.getFullYear() && date.getMonth() === now.getMonth() && date.getDate() === now.getDate();
    if (sameDay) return date.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
    return date.toLocaleString([], { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
  }
  function senderName(msg) {
    const id = msg.senderDeviceId || "";
    if (!id || id === selfID) return "";
    const member = deps.roster(currentData()).find((item) => item.device_id === id);
    return member ? deps.computerLabel(member, currentData()) : "";
  }
  const sessionTitleLabel = (raw) => {
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
  const looksLikeSlug = (value) => {
    const s = String(value || "").trim();
    return s.length > 0 && s.length <= 48 && /^[a-z0-9][a-z0-9._-]*$/.test(s);
  }
  function terminalSessionTitle(payload) {
    const raw = String(payload.title || payload.sessionId || payload.session_id || "").trim();
    const project = String(payload.project || "").trim();
    const fromTitle = sessionTitleLabel(raw);
    const fromProject = sessionTitleLabel(project);
    if (fromTitle && (!looksLikeSlug(fromTitle) || !fromProject || fromTitle.toLowerCase() !== fromProject.toLowerCase())) {
      return fromTitle;
    }
    if (fromProject && !looksLikeSlug(fromProject)) return fromProject;
    if (fromTitle && !looksLikeSlug(fromTitle)) return fromTitle;
    return "Terminal";
  }
  const terminalStateMeta = (state) => {
    const key = String(state || "").trim().toUpperCase();
    const labels = {
      CREATED: "Created",
      STARTING: "Starting",
      RUNNING: "Running",
      DETACHED: "Detached",
      SUSPENDED: "Suspended",
      STOPPING: "Stopping",
      EXITED: "Ended",
      FAILED: "Failed",
      TERMINATED: "Ended",
      LOST: "Unavailable"
    };
    const tone = {
      RUNNING: "live",
      STARTING: "pending",
      CREATED: "pending",
      DETACHED: "idle",
      SUSPENDED: "idle",
      STOPPING: "pending",
      EXITED: "ended",
      FAILED: "ended",
      TERMINATED: "ended",
      LOST: "ended"
    };
    return { key, label: labels[key] || (key ? key.charAt(0) + key.slice(1).toLowerCase() : ""), tone: tone[key] || "idle" };
  }
  const terminalSessionPath = (payload) => {
    const project = String(payload.project || "").trim();
    if (project) return project;
    const summary = String(payload.summary || "").trim();
    const parts = summary.split("·").map((bit) => bit.trim()).filter(Boolean);
    return parts.find((bit) => /^[~./]/.test(bit) || bit.includes("/")) || "";
  }
  const terminalIcon = () => {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 20 20");
    svg.setAttribute("aria-hidden", "true");
    svg.classList.add("msg-terminal-icon");
    const frame = document.createElementNS("http://www.w3.org/2000/svg", "path");
    frame.setAttribute("d", "M4.5 5.5h11a1.5 1.5 0 0 1 1.5 1.5v8a1.5 1.5 0 0 1-1.5 1.5h-11A1.5 1.5 0 0 1 3 15V7a1.5 1.5 0 0 1 1.5-1.5z");
    frame.setAttribute("fill", "none");
    frame.setAttribute("stroke", "currentColor");
    frame.setAttribute("stroke-width", "1.5");
    frame.setAttribute("stroke-linejoin", "round");
    const prompt = document.createElementNS("http://www.w3.org/2000/svg", "path");
    prompt.setAttribute("d", "M6.5 9.5 8.2 11 6.5 12.5M9.5 12.5h3.5");
    prompt.setAttribute("fill", "none");
    prompt.setAttribute("stroke", "currentColor");
    prompt.setAttribute("stroke-width", "1.5");
    prompt.setAttribute("stroke-linecap", "round");
    prompt.setAttribute("stroke-linejoin", "round");
    svg.append(frame, prompt);
    return svg;
  }
  function terminalSessionCard(card) {
    const node = el("article", "msg-card msg-card--terminal");
    const row = el("div", "msg-terminal-row");
    row.appendChild(terminalIcon());
    const body = el("div", "msg-terminal-body");
    body.appendChild(el("div", "msg-terminal-eyebrow", card.kind || CARD_TYPES.terminal_session));
    body.appendChild(el("div", "msg-terminal-title", card.title));
    const meta = el("div", "msg-terminal-meta");
    const state = terminalStateMeta(card.state);
    if (state.label) {
      const pill = el("span", "msg-terminal-status");
      if (state.tone) pill.dataset.tone = state.tone;
      pill.appendChild(el("span", "msg-terminal-status-dot", ""));
      pill.appendChild(document.createTextNode(state.label));
      meta.appendChild(pill);
    }
    if (card.path) {
      const path = el("span", "msg-terminal-path", card.path);
      path.title = card.path;
      meta.appendChild(path);
    }
    if (meta.childNodes.length) body.appendChild(meta);
    row.appendChild(body);
    node.appendChild(row);
    const actions = el("div", "msg-card-actions");
    const openBtn = el("button", "msg-card-btn", "Open terminal");
    openBtn.type = "button";
    openBtn.addEventListener("click", () => {
      if (typeof globalThis.overdriveOpenTerminal === "function") {
        globalThis.overdriveOpenTerminal({ ...card.terminal, title: card.rawTitle || card.title, focus: false });
      }
    });
    actions.appendChild(openBtn);
    node.appendChild(actions);
    return node;
  }
  function cardView(msg) {
    const payload = msg.payload && typeof msg.payload === "object" ? msg.payload : {};
    const type = msg.type || "text";
    if (type === "terminal_session") {
      const rawTitle = String(payload.title || payload.sessionId || payload.session_id || "Session").trim();
      const title = terminalSessionTitle(payload);
      const path = terminalSessionPath(payload);
      const state = String(payload.state || "").trim();
      return {
        kind: CARD_TYPES.terminal_session,
        title,
        rawTitle,
        path,
        state,
        terminal: payload
      };
    }
    if (CARD_TYPES[type]) {
      return { kind: CARD_TYPES[type], title: String(payload.title || "").trim(), summary: String(payload.summary || "").trim() };
    }
    return null;
  }
  let mermaidReady = false;
  const fileIcon = (kind) => {
    const paths = {
      image: "M4 6.5h12v9H4zM7 11.5l1.6-1.6 1.4 1.4 2-2.2 2 2.4",
      video: "M4 6.5h8.5v7H4zM12.5 9l3.5-1.6v5.2L12.5 11",
      audio: "M7 14.5V7l7-1.5v9",
      pdf: "M6 3.5h5l3 3V16H6zM11 3.5V7h3",
      markdown: "M4 6h12v8H4zM6.5 12V8.5L8 10l1.5-1.5V12M12 12V8.5L13.2 10",
      code: "M7 7.5 4.5 10 7 12.5M13 7.5 15.5 10 13 12.5",
      json: "M7 5.5c-1.2 0-2 .8-2 2v1.2C5 10 4 10.5 4 11s1 1 1 2.3V14.5c0 1.2.8 2 2 2M13 5.5c1.2 0 2 .8 2 2v1.2c0 1.3 1 1.8 1 2.3s-1 1-1 2.3V14.5c0 1.2-.8 2-2 2",
      sheet: "M4 5h12v10H4zM4 8.5h12M8.5 8.5V15",
      slides: "M3.5 5.5h13v8h-13zM8 16.5h4",
      zip: "M8 3.5h4v13H8zM8 6.5h4M8 9h4M8 11.5h4",
      exe: "M6 4.5h6l2 2v9H6zM8.5 10.5l3 1.5-3 1.5z",
      font: "M5 15 8.2 5h1.6L13 15M6.4 11.5h5.2",
      text: "M6 4.5h8M6 8h8M6 11.5h5",
      doc: "M6 3.5h5l3 3V16H6z"
    };
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 20 20");
    svg.setAttribute("aria-hidden", "true");
    svg.classList.add("msg-file-icon");
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", paths[kind] || paths.doc);
    path.setAttribute("fill", "none");
    path.setAttribute("stroke", "currentColor");
    path.setAttribute("stroke-width", "1.5");
    path.setAttribute("stroke-linecap", "round");
    path.setAttribute("stroke-linejoin", "round");
    svg.appendChild(path);
    return svg;
  }
  const blobURLCache = new Map();
  function blobURL(data, mime, key) {
    const cacheKey = key ? key + "\n" + mime : "";
    if (cacheKey && blobURLCache.has(cacheKey)) return blobURLCache.get(cacheKey).url;
    const bin = Uint8Array.from(atob(data), (c) => c.codePointAt(0));
    const url = URL.createObjectURL(new Blob([bin], { type: mime || "application/octet-stream" }));
    if (cacheKey) blobURLCache.set(cacheKey, { url, id: key });
    return url;
  }
  function pruneBlobURLs(messages) {
    const keep = new Set(messages.map((msg) => msg.messageId || ""));
    blobURLCache.forEach((entry, cacheKey) => {
      if (keep.has(entry.id)) return;
      URL.revokeObjectURL(entry.url);
      blobURLCache.delete(cacheKey);
    });
  }
  const byteSize = (data) => {
    const n = Math.floor(String(data || "").length * 3 / 4);
    if (n < 1024) return n + " B";
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
    return (n / (1024 * 1024)).toFixed(1) + " MB";
  }
  let chatMediaReturnFocus = null;
  const chatMediaExpandIcon = () => {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("width", "16");
    svg.setAttribute("height", "16");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", "M9 3H3v6M15 3h6v6M21 15v6h-6M9 21H3v-6");
    path.setAttribute("fill", "none");
    path.setAttribute("stroke", "currentColor");
    path.setAttribute("stroke-width", "1.7");
    path.setAttribute("stroke-linecap", "round");
    path.setAttribute("stroke-linejoin", "round");
    svg.appendChild(path);
    return svg;
  }
  const highlightCodeSource = (source, lang) => {
    const name = String(lang || "").trim() || "text";
    const code = document.createElement("code");
    code.textContent = source;
    if (globalThis.hljs && name) {
      try {
        const highlighted = globalThis.hljs.highlight(source, { language: name, ignoreIllegals: true });
        code.innerHTML = highlighted.value;
      } catch (err) {
        if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
        code.textContent = source;
      }
    }
    return code;
  }
  function closeChatMediaModal() {
    const modal = document.getElementById("chatMediaModal");
    if (!modal || modal.hidden) return;
    modal.hidden = true;
    document.body.classList.remove("chat-media-modal-open");
    const body = document.getElementById("chatMediaModalBody");
    if (body) body.replaceChildren();
    const returnFocus = chatMediaReturnFocus;
    chatMediaReturnFocus = null;
    if (returnFocus && typeof returnFocus.focus === "function") {
      try { returnFocus.focus({ preventScroll: true }); } catch (error_) {
      globalThis.console?.debug?.(error_);
      returnFocus.focus(); }
    }
  }
  function fillChatMediaModal(spec) {
    const body = document.getElementById("chatMediaModalBody");
    const title = document.getElementById("chatMediaModalTitle");
    const copyBtn = document.getElementById("chatMediaModalCopy");
    if (!body || !title) return;
    body.replaceChildren();
    title.textContent = String(spec.title || "").trim();
    const kind = spec.kind || "media";
    const source = String(spec.source || "");
    if (copyBtn) {
      const showCopy = kind === "code" && source;
      copyBtn.hidden = !showCopy;
      copyBtn.onclick = () => {
        if (!source) return;
        navigator.clipboard.writeText(source).catch(() => {});
      };
    }
    if (kind === "code") {
      const pre = document.createElement("pre");
      pre.className = "chat-media-modal__code";
      pre.appendChild(highlightCodeSource(source, spec.lang));
      body.appendChild(pre);
      return;
    }
    if (kind === "mermaid") {
      const box = document.createElement("div");
      box.className = "chat-media-modal__mermaid mermaid";
      box.textContent = source;
      body.appendChild(box);
      if (globalThis.mermaid) {
        if (!mermaidReady) {
          globalThis.mermaid.initialize({ startOnLoad: false, theme: "dark", securityLevel: "strict" });
          mermaidReady = true;
        }
        globalThis.mermaid.run({ nodes: [box] }).catch(() => {});
      }
      return;
    }
    if (kind === "image") {
      const img = document.createElement("img");
      img.className = "chat-media-modal__image";
      img.src = spec.url || "";
      img.alt = spec.alt || "";
      body.appendChild(img);
      return;
    }
    if (kind === "pdf") {
      const frame = document.createElement("iframe");
      frame.className = "chat-media-modal__pdf";
      frame.title = spec.title || "PDF";
      frame.src = spec.url || "";
      body.appendChild(frame);
      return;
    }
    if (kind === "video") {
      const video = document.createElement("video");
      video.className = "chat-media-modal__video";
      video.controls = true;
      video.src = spec.url || "";
      body.appendChild(video);
      return;
    }
    if (kind === "embed" && spec.embedUrl) {
      const frame = document.createElement("iframe");
      frame.className = "chat-media-modal__embed";
      frame.title = spec.title || "Vídeo incorporado";
      frame.src = spec.embedUrl;
      frame.setAttribute("allow", "accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture");
      frame.setAttribute("allowfullscreen", "");
      frame.setAttribute("referrerpolicy", "strict-origin-when-cross-origin");
      body.appendChild(frame);
    }
  }
  function openChatMediaModal(spec) {
    const modal = document.getElementById("chatMediaModal");
    if (!modal) return;
    chatMediaReturnFocus = document.activeElement;
    fillChatMediaModal(spec);
    modal.hidden = false;
    document.body.classList.add("chat-media-modal-open");
    const closeBtn = document.getElementById("chatMediaModalClose");
    if (closeBtn) closeBtn.focus();
  }
  function bindCodeHeaderExpand(langEl, spec) {
    if (!langEl || langEl.dataset.mediaExpandBound) return;
    langEl.dataset.mediaExpandBound = "1";
    langEl.classList.add("msg-code-lang--action");
    langEl.setAttribute("role", "button");
    langEl.setAttribute("tabindex", "0");
    langEl.setAttribute("aria-label", "Ampliar código");
    const open = () => openChatMediaModal(spec);
    langEl.addEventListener("click", (ev) => {
      ev.preventDefault();
      ev.stopPropagation();
      open();
    });
    langEl.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter" || ev.key === " ") {
        ev.preventDefault();
        open();
      }
    });
  }
  function wrapRichMedia(content, spec) {
    const kind = spec.kind || "media";
    const shell = el("div", "msg-media-shell msg-media-shell--" + kind);
    shell.appendChild(content);
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "msg-media-expand";
    btn.setAttribute("aria-label", spec.expandLabel || "Ampliar");
    btn.title = spec.expandLabel || "Ampliar";
    btn.appendChild(chatMediaExpandIcon());
    btn.addEventListener("click", (ev) => {
      ev.preventDefault();
      ev.stopPropagation();
      openChatMediaModal(spec);
    });
    shell.appendChild(btn);
    if (kind === "code") {
      const langEl = content.querySelector(".msg-code-lang");
      bindCodeHeaderExpand(langEl, spec);
    }
    return shell;
  }
  function msgImageNode(src, alt, title) {
    const img = document.createElement("img");
    img.className = "msg-image";
    img.alt = alt || "";
    img.src = src;
    return wrapRichMedia(img, {
      kind: "image",
      url: src,
      alt: alt || "",
      title: title || alt || "Imagem",
      expandLabel: "Ampliar imagem",
    });
  }
  function msgVideoNode(src, title) {
    const video = document.createElement("video");
    video.className = "msg-video";
    video.controls = true;
    video.src = src;
    return wrapRichMedia(video, {
      kind: "video",
      url: src,
      title: title || "Vídeo",
      expandLabel: "Ampliar vídeo",
    });
  }
  function msgPdfNode(src, title) {
    const frame = document.createElement("iframe");
    frame.className = "msg-pdf";
    frame.title = title || "PDF";
    frame.src = src;
    return wrapRichMedia(frame, {
      kind: "pdf",
      url: src,
      title: title || "PDF",
      expandLabel: "Ampliar PDF",
    });
  }
  function msgVideoEmbedNode(spec) {
    const shell = el("div", "msg-video-embed msg-video-embed--" + spec.provider);
    const frame = el("div", "msg-video-embed__frame");
    const play = document.createElement("button");
    play.type = "button";
    play.className = "msg-video-embed__play";
    play.setAttribute("aria-label", "Reproduzir vídeo");
    if (spec.thumbUrl) {
      const thumb = document.createElement("img");
      thumb.className = "msg-video-embed__thumb";
      thumb.alt = "";
      thumb.src = spec.thumbUrl;
      frame.appendChild(thumb);
    } else {
      frame.appendChild(el("span", "msg-video-embed__label", spec.provider === "vimeo" ? "Vimeo" : "YouTube"));
    }
    frame.appendChild(play);
    const loadPlayer = () => {
      if (frame.dataset.loaded === "1") return;
      frame.dataset.loaded = "1";
      frame.replaceChildren();
      const iframe = document.createElement("iframe");
      iframe.className = "msg-video-embed__iframe";
      iframe.src = spec.embedUrl;
      iframe.title = spec.title || "Vídeo incorporado";
      iframe.setAttribute("allow", "accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture");
      iframe.setAttribute("allowfullscreen", "");
      iframe.setAttribute("referrerpolicy", "strict-origin-when-cross-origin");
      frame.appendChild(iframe);
    };
    play.addEventListener("click", (ev) => {
      ev.preventDefault();
      ev.stopPropagation();
      loadPlayer();
    });
    shell.appendChild(frame);
    const watch = document.createElement("a");
    watch.className = "msg-video-embed__watch";
    watch.href = spec.watchUrl;
    watch.target = "_blank";
    watch.rel = "noopener noreferrer";
    watch.textContent = spec.provider === "vimeo" ? "Abrir no Vimeo" : "Abrir no YouTube";
    shell.appendChild(watch);
    return wrapRichMedia(shell, {
      kind: "embed",
      embedUrl: spec.embedUrl,
      watchUrl: spec.watchUrl,
      title: spec.title,
      expandLabel: "Ampliar vídeo",
    });
  }
  function bindChatMediaModal() {
    const modal = document.getElementById("chatMediaModal");
    if (!modal || modal.dataset.mediaModalBound === "1") return;
    modal.dataset.mediaModalBound = "1";
    modal.addEventListener("click", (ev) => {
      if (ev.target.closest("#chatMediaModalClose, #chatMediaModalBackdrop")) {
        ev.preventDefault();
        closeChatMediaModal();
      }
      ev.stopPropagation();
    });
    document.addEventListener("keydown", (ev) => {
      if (ev.key !== "Escape" || modal.hidden) return;
      ev.preventDefault();
      ev.stopImmediatePropagation();
      closeChatMediaModal();
    }, true);
    modal.addEventListener("keydown", (ev) => {
      if (ev.key !== "Tab" || modal.hidden) return;
      const focusable = modal.querySelectorAll("button:not([hidden]), [href], input, select, textarea, [tabindex]:not([tabindex='-1'])");
      const list = Array.from(focusable).filter((node) => !node.disabled && node.offsetParent !== null);
      if (list.length < 2) return;
      const first = list[0];
      const last = list.at(-1);
      if (ev.shiftKey && document.activeElement === first) {
        ev.preventDefault();
        last.focus();
      } else if (!ev.shiftKey && document.activeElement === last) {
        ev.preventDefault();
        first.focus();
      }
    });
  }
  function mediaURLNode(url) {
    const clean = url.replace(/[),.;]+$/, "");
    const path = clean.split("?")[0].toLowerCase();
    if (/\.(png|jpe?g|gif|webp|svg|avif)$/.test(path)) {
      return msgImageNode(clean, "", "Imagem");
    }
    if (/\.(mp4|webm|mov)$/.test(path)) {
      return msgVideoNode(clean, "Vídeo");
    }
    if (/\.(mp3|wav|ogg|m4a)$/.test(path)) {
      const audio = document.createElement("audio");
      audio.className = "msg-audio";
      audio.controls = true;
      audio.src = clean;
      return audio;
    }
    if (path.endsWith(".pdf")) {
      return msgPdfNode(clean, "PDF");
    }
    const videoEmbed = parseVideoEmbedUrl(clean);
    if (videoEmbed) return msgVideoEmbedNode(videoEmbed);
    let host = clean;
    let tail = "";
    try {
      const u = new URL(clean);
      host = u.host;
      tail = (u.pathname + u.search).slice(0, 80);
    } catch { /* keep raw */ }
    const a = document.createElement("a");
    a.className = "msg-link-card";
    a.href = clean;
    a.target = "_blank";
    a.rel = "noopener noreferrer";
    a.appendChild(el("span", "msg-link-host", host));
    if (tail && tail !== "/") a.appendChild(el("span", "msg-link-path", tail));
    return a;
  }
  function appendInlineToken(parent, match) {
    if (match.kind === "img") {
      parent.appendChild(msgImageNode(match.href, match.alt, match.alt || "Imagem"));
      return;
    }
    if (match.kind === "link") {
      const embed = parseVideoEmbedUrl(match.href);
      if (embed) {
        parent.appendChild(msgVideoEmbedNode(embed));
        return;
      }
      const a = document.createElement("a");
      a.href = match.href;
      a.target = "_blank";
      a.rel = "noopener noreferrer";
      a.textContent = match.label;
      parent.appendChild(a);
      return;
    }
    if (match.kind === "code") {
      const code = document.createElement("code");
      code.textContent = match.text;
      parent.appendChild(code);
      return;
    }
    if (match.kind === "strong") {
      const strong = document.createElement("strong");
      strong.textContent = match.text;
      parent.appendChild(strong);
      return;
    }
    if (match.kind === "em") {
      const em = document.createElement("em");
      em.textContent = match.text;
      parent.appendChild(em);
      return;
    }
    if (match.kind === "url") parent.appendChild(mediaURLNode(match.href));
  }

  function appendInlines(parent, text) {
    for (const part of scanInlineText(text)) {
      if (part.kind === "text") parent.appendChild(document.createTextNode(part.text));
      else appendInlineToken(parent, part);
    }
  }
  function codeBlock(lang, source) {
    const aliases = { py: "python", js: "javascript", jsx: "javascript", ts: "typescript", tsx: "typescript", rs: "rust", sh: "bash", yml: "yaml", rb: "ruby", h: "c", cc: "cpp" };
    const rawLang = String(lang || "").trim().toLowerCase();
    const name = aliases[rawLang] || rawLang;
    if (name === "mermaid") {
      const box = document.createElement("div");
      box.className = "msg-mermaid mermaid";
      box.textContent = source;
      if (globalThis.mermaid) {
        if (!mermaidReady) {
          globalThis.mermaid.initialize({ startOnLoad: false, theme: "dark", securityLevel: "strict" });
          mermaidReady = true;
        }
        globalThis.mermaid.run({ nodes: [box] }).catch(() => {});
      }
      return wrapRichMedia(box, {
        kind: "mermaid",
        source,
        title: "Diagram",
        expandLabel: "Ampliar diagrama",
      });
    }
    const pre = document.createElement("pre");
    pre.className = "msg-code";
    pre.appendChild(el("div", "msg-code-lang", name || "text"));
    pre.appendChild(highlightCodeSource(source, name));
    return wrapRichMedia(pre, {
      kind: "code",
      lang: name,
      source,
      title: name || "text",
      expandLabel: "Ampliar código",
    });
  }
  function renderMarkdown(text) {
    const frag = document.createDocumentFragment();
    const parts = String(text || "").split(/```/);
    parts.forEach((part, index) => {
      if (index % 2 === 1) {
        const nl = part.indexOf("\n");
        const lang = nl === -1 ? part.trim() : part.slice(0, nl).trim();
        const source = nl === -1 ? "" : part.slice(nl + 1).replace(/\n$/, "");
        frag.appendChild(codeBlock(lang, source));
        return;
      }
      const lines = part.replace(/\s+$/, "").split("\n");
      let i = 0;
      while (i < lines.length) {
        const line = lines[i];
        if (!line.trim()) { i += 1; continue; }
        if (/^\|.+\|$/.test(line.trim()) && i + 1 < lines.length && /^(?:\|)?\s*:?-{3,}/.test(lines[i + 1])) {
          const table = document.createElement("table");
          table.className = "msg-table";
          const head = document.createElement("tr");
          line.trim().replaceAll(/(?:^\||\|$)/g, "").split("|").forEach((cell) => {
            const th = document.createElement("th");
            appendInlines(th, cell.trim());
            head.appendChild(th);
          });
          table.appendChild(head);
          i += 2;
          while (i < lines.length && /^\|.+\|$/.test(lines[i].trim())) {
            const tr = document.createElement("tr");
            lines[i].trim().replaceAll(/(?:^\||\|$)/g, "").split("|").forEach((cell) => {
              const td = document.createElement("td");
              appendInlines(td, cell.trim());
              tr.appendChild(td);
            });
            table.appendChild(tr);
            i += 1;
          }
          frag.appendChild(table);
          continue;
        }
        const headingRe = /^(#{1,3})\s+(.*)$/;
        const heading = headingRe.exec(line);
        if (heading) {
          const h = document.createElement("h" + (heading[1].length + 2));
          appendInlines(h, heading[2]);
          frag.appendChild(h);
          i += 1;
          continue;
        }
        if (/^>\s?/.test(line)) {
          const quote = document.createElement("blockquote");
          const buf = [];
          while (i < lines.length && /^>\s?/.test(lines[i])) {
            buf.push(lines[i].replace(/^>\s?/, ""));
            i += 1;
          }
          appendInlines(quote, buf.join(" "));
          frag.appendChild(quote);
          continue;
        }
        if (/^[-*]\s+/.test(line) || /^\d+\.\s+/.test(line)) {
          const ordered = /^\d+\.\s+/.test(line);
          const list = document.createElement(ordered ? "ol" : "ul");
          while (i < lines.length && (ordered ? /^\d+\.\s+/.test(lines[i]) : /^[-*]\s+/.test(lines[i]))) {
            const li = document.createElement("li");
            appendInlines(li, lines[i].replace(ordered ? /^\d+\.\s+/ : /^[-*]\s+/, ""));
            list.appendChild(li);
            i += 1;
          }
          frag.appendChild(list);
          continue;
        }
        const buf = [line];
        i += 1;
        while (i < lines.length && lines[i].trim() && !/^(#{1,3}\s|>\s?|[-*]\s|\d+\.\s|\|)/.test(lines[i])) {
          buf.push(lines[i]);
          i += 1;
        }
        const p = document.createElement("p");
        appendInlines(p, buf.join(" "));
        frag.appendChild(p);
      }
    });
    return frag;
  }
  function fileCard(name, mime, data, key) {
    const kind = fileKind(name, mime);
    const card = el("a", "msg-file-card");
    card.href = data ? blobURL(data, "application/octet-stream", key) : "#";
    card.download = name;
    card.rel = "noopener";
    card.appendChild(fileIcon(kind));
    const meta = el("span", "msg-file-meta");
    meta.appendChild(el("span", "msg-file-name", name));
    meta.appendChild(el("span", "msg-file-size", byteSize(data)));
    card.appendChild(meta);
    return card;
  }
  const voicePeaksCache = new Map();
  let playingVoice = null;
  let peaksDecoder = null;
  const VOICE_PLAY_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M5 3.2v9.6c0 .5.5.8.9.5l7.4-4.8a.6.6 0 0 0 0-1L5.9 2.7c-.4-.3-.9 0-.9.5z" fill="currentColor"/></svg>';
  const VOICE_PAUSE_ICON = '<svg viewBox="0 0 16 16" aria-hidden="true"><rect x="4" y="3" width="3" height="10" rx="1" fill="currentColor"/><rect x="9" y="3" width="3" height="10" rx="1" fill="currentColor"/></svg>';

  async function decodeVoice(url, key) {
    if (key && voicePeaksCache.has(key)) return voicePeaksCache.get(key);
    const Ctor = typeof OfflineAudioContext === "undefined" ? null : OfflineAudioContext;
    if (!Ctor) return null;
    if (!peaksDecoder) peaksDecoder = new Ctor(1, 1, 44100);
    const buf = await (await fetch(url)).arrayBuffer();
    const audioBuf = await peaksDecoder.decodeAudioData(buf);
    const result = { peaks: waveformPeaks(audioBuf.getChannelData(0), VOICE_BAR_COUNT), duration: audioBuf.duration };
    if (key) voicePeaksCache.set(key, result);
    return result;
  }

  function voiceMessageNode(url, key) {
    const root = el("div", "msg-voice");
    const audio = document.createElement("audio");
    audio.preload = "metadata";
    audio.src = url;
    const play = el("button", "msg-voice-play");
    play.type = "button";
    play.setAttribute("aria-label", "Play voice message");
    play.innerHTML = VOICE_PLAY_ICON;
    const wave = el("div", "msg-voice-wave");
    wave.setAttribute("role", "slider");
    wave.setAttribute("aria-label", "Voice message position");
    wave.tabIndex = 0;
    const bars = [];
    for (let i = 0; i < VOICE_BAR_COUNT; i++) {
      const bar = el("span", "msg-voice-bar");
      bar.style.setProperty("--h", String(VOICE_BAR_FLOOR));
      bars.push(bar);
      wave.appendChild(bar);
    }
    const time = el("span", "msg-voice-time", "0:00");
    root.append(play, wave, time, audio);
    let duration = 0;

    const knownDuration = () => duration || (Number.isFinite(audio.duration) ? audio.duration : 0);
    const paint = () => {
      const total = knownDuration();
      const ratio = total ? Math.min(1, audio.currentTime / total) : 0;
      const played = Math.round(ratio * bars.length);
      bars.forEach((bar, i) => bar.classList.toggle("played", i < played));
      wave.setAttribute("aria-valuenow", String(Math.round(ratio * 100)));
      const playing = !audio.paused && !audio.ended;
      time.textContent = formatVoiceTime(playing || audio.currentTime > 0 ? audio.currentTime : total);
    };
    const setPlaying = (on) => {
      root.classList.toggle("playing", on);
      play.innerHTML = on ? VOICE_PAUSE_ICON : VOICE_PLAY_ICON;
      play.setAttribute("aria-label", on ? "Pause voice message" : "Play voice message");
    };
    const seekTo = (ratio) => {
      const total = knownDuration();
      if (!total) return;
      audio.currentTime = Math.max(0, Math.min(total, ratio * total));
      paint();
    };

    decodeVoice(url, key).then((info) => {
      if (!info) return;
      duration = info.duration;
      info.peaks.forEach((p, i) => bars[i]?.style.setProperty("--h", String(p)));
      paint();
    }).catch(() => {});

    audio.addEventListener("loadedmetadata", paint);
    audio.addEventListener("timeupdate", paint);
    audio.addEventListener("play", () => {
      if (playingVoice && playingVoice !== audio) playingVoice.pause();
      playingVoice = audio;
      setPlaying(true);
    });
    audio.addEventListener("pause", () => setPlaying(false));
    audio.addEventListener("ended", () => {
      setPlaying(false);
      audio.currentTime = 0;
      paint();
    });
    play.addEventListener("click", () => {
      if (audio.paused) audio.play().catch(() => {});
      else audio.pause();
    });
    wave.addEventListener("click", (ev) => {
      const rect = wave.getBoundingClientRect();
      if (rect.width) seekTo((ev.clientX - rect.left) / rect.width);
    });
    wave.addEventListener("keydown", (ev) => {
      const total = knownDuration();
      if (!total) return;
      if (ev.key === "ArrowRight" || ev.key === "ArrowLeft") {
        ev.preventDefault();
        seekTo((audio.currentTime + (ev.key === "ArrowRight" ? 5 : -5)) / total);
      }
    });
    return root;
  }

  function inlineMediaURL(kind, name, mime, data, key) {
    const safe = inlineMediaMime(kind, name, mime);
    if (safe === "image/svg+xml") return "data:image/svg+xml;base64," + data;
    return blobURL(data, safe, key);
  }
  function safeMessageNode(msg) {
    try {
      return messageNode(msg);
    } catch (error_) {
      globalThis.console?.debug?.(error_);
      return el("article", "bubble", "This message could not be displayed.");
    }
  }
  function messageNode(msg) {
    const card = cardView(msg);
    if (card) {
      if (card.terminal) return terminalSessionCard(card);
      const node = el("article", "msg-card");
      if (card.kind) node.appendChild(el("div", "msg-kind", card.kind));
      if (card.title && card.title.toLowerCase() !== String(card.kind || "").toLowerCase()) node.appendChild(el("div", "msg-title", card.title));
      if (card.summary) node.appendChild(el("p", "", card.summary));
      return node;
    }
    const payload = msg.payload && typeof msg.payload === "object" ? msg.payload : {};
    if ((msg.type || "") === "file") {
      const node = el("article", "bubble");
      const caption = String(payload.text || "").trim();
      if (caption) node.appendChild(renderMarkdown(caption));
      const name = String(payload.fileName || "file").trim();
      const mime = String(payload.mimeType || "application/octet-stream").trim();
      const data = String(payload.data || "").trim();
      const kind = fileKind(name, mime);
      const key = msg.messageId || "";
      if (!data) {
        node.appendChild(fileCard(name, mime, "", key));
        return node;
      }
      const url = INLINE_MEDIA_KINDS.has(kind) ? inlineMediaURL(kind, name, mime, data, key) : "";
      if (kind === "image") {
        node.appendChild(msgImageNode(url, name, name));
      } else if (kind === "audio") {
        node.classList.add("bubble--voice");
        node.appendChild(voiceMessageNode(url, key));
      } else if (kind === "video") {
        node.appendChild(msgVideoNode(url, name));
      } else if (kind === "pdf") {
        node.appendChild(msgPdfNode(url, name));
      } else if (kind === "markdown") {
        node.appendChild(renderMarkdown(new TextDecoder().decode(Uint8Array.from(atob(data), (c) => c.codePointAt(0)))));
      } else if (kind === "code" || kind === "json") {
        const source = new TextDecoder().decode(Uint8Array.from(atob(data), (c) => c.codePointAt(0)));
        node.appendChild(codeBlock(name.split(".").pop(), source));
      } else {
        node.appendChild(fileCard(name, mime, data, key));
      }
      return node;
    }
    const node = el("article", "bubble");
    const text = String(payload.text || "");
    if (text.trim()) node.appendChild(renderMarkdown(text));
    return node;
  }
  const sameGroup = (a, b) => {
    if (!a || !b) return false;
    const left = a.senderDeviceId || "";
    const right = b.senderDeviceId || "";
    if (!left || left !== right) return false;
    const start = Date.parse(a.timestamp || "");
    const end = Date.parse(b.timestamp || "");
    if (!Number.isFinite(start) || !Number.isFinite(end)) return false;
    return Math.abs(end - start) < 8 * 60 * 1000;
  }
  function renderThread(messages) {
    const pane = document.getElementById("threadPane");
    if (!pane) return;
    const threadKey = drafting ? "" : openConversation;
    const nearBottom = pane.scrollHeight - pane.scrollTop - pane.clientHeight < 48;
    const stick = forceThreadScroll || threadKey !== renderedThreadKey || nearBottom;
    forceThreadScroll = false;
    renderedThreadKey = threadKey;
    const previousTop = pane.scrollTop;
    pruneBlobURLs(messages);
    pane.replaceChildren();
    if (!messages.length) {
      const hint = threadEmptyLabel();
      if (hint) pane.appendChild(el("p", "chat-empty", hint));
      return;
    }
    const shown = expanded ? messages : messages.slice(-5);
    let group = null;
    let previous = null;
    shown.forEach((msg) => {
      const mine = !!(msg.senderDeviceId && msg.senderDeviceId === selfID);
      if (!sameGroup(previous, msg)) {
        group = el("div", "msg-group" + (mine ? " mine" : ""));
        const name = senderName(msg);
        if (name) group.appendChild(el("div", "msg-sender", name));
        pane.appendChild(group);
      }
      group.appendChild(safeMessageNode(msg));
      const prior = group.querySelector(".msg-time");
      if (prior) prior.remove();
      const label = stamp(msg.timestamp);
      if (label) {
        const time = el("time", "msg-time", label);
        if (msg.timestamp) time.dateTime = msg.timestamp;
        group.appendChild(time);
      }
      previous = msg;
    });
    pane.scrollTop = stick ? pane.scrollHeight : previousTop;
  }
  async function loadThread(id, mark) {
    if (!meshID || !id) return;
    const messagesURL = "/api/chat/messages?mesh_id=" + encodeURIComponent(meshID) + "&conversation_id=" + encodeURIComponent(id) + (mark ? "&mark_read=1" : "");
    let body;
    try {
      const res = await fetch(messagesURL, { cache: "no-store" });
      if (!res.ok) return;
      body = await res.json();
    } catch (error_) {
      globalThis.console?.debug?.(error_);
      return;
    }
    if (drafting || id !== openConversation) return;
    const messages = body.messages || [];
    if (mark) {
      const conv = conversations.find((item) => item.conversation_id === id);
      if (conv?.unread) {
        conv.unread = 0;
        paintUnread();
        renderConversationList();
      }
    }
    const sig = (expanded ? "open" : "live") + "\n" + (drafting ? "draft" : id) + "\n" + messages.map((msg) => msg.messageId || "").join("\n");
    const pane = document.getElementById("threadPane");
    lastMessages = messages;
    if (sig === threadSig && pane?.childElementCount) return;
    threadSig = sig;
    renderThread(messages);
  }
  function fillRecipients() {
    renderComposeContacts(composeFilter);
  }
  function growComposer() {
    const field = document.getElementById("messageText");
    if (!field) return;
    field.style.height = "0px";
    field.style.height = Math.max(28, Math.min(field.scrollHeight, 88)) + "px";
    syncComposerSendMic();
  }
  const insertAtCursor = (field, text) => {
    if (!field || !text) return;
    const start = field.selectionStart;
    const end = field.selectionEnd;
    const val = field.value || "";
    field.value = val.slice(0, start) + text + val.slice(end);
    const pos = start + text.length;
    field.selectionStart = pos;
    field.selectionEnd = pos;
    field.dispatchEvent(new Event("input", { bubbles: true }));
    field.focus();
  }
  function readEmojiRecent() {
    try {
      const raw = localStorage.getItem(EMOJI_RECENT_KEY);
      const list = raw ? JSON.parse(raw) : [];
      return Array.isArray(list) ? list.filter((g) => typeof g === "string" && g) : [];
    } catch (error_) {
      globalThis.console?.debug?.(error_);
      return [];
    }
  }
  function writeEmojiRecent(list) {
    try {
      localStorage.setItem(EMOJI_RECENT_KEY, JSON.stringify(list.slice(0, EMOJI_RECENT_MAX)));
    } catch (error_) {
      globalThis.console?.debug?.(error_);
      }
  }
  function rememberEmoji(glyph) {
    const list = readEmojiRecent().filter((g) => g !== glyph);
    list.unshift(glyph);
    writeEmojiRecent(list);
    emojiGridCache.recent = null;
  }
  function loadEmojiCatalog() {
    if (emojiCatalog) return emojiCatalog;
    const node = document.getElementById("emojiCatalog");
    if (!node) return null;
    try {
      emojiCatalog = JSON.parse(node.textContent || "");
    } catch (error_) {
      globalThis.console?.debug?.(error_);
      emojiCatalog = null;
    }
    return emojiCatalog;
  }
  function emojiEntriesForTab(tabId, query) {
    const catalog = loadEmojiCatalog();
    if (!catalog?.emojis) return [];
    const q = String(query || "").trim().toLowerCase();
    if (q) {
      const hits = [];
      const tabs = catalog.tabs || [];
      for (const id of tabs) {
        if (id === "recent") continue;
        const rows = catalog.emojis[id] || [];
        for (const row of rows) {
          const glyph = row[0];
          const tags = String(row[1] || "").toLowerCase();
          if (glyph.includes(q) || tags.includes(q)) hits.push([glyph, row[1] || glyph]);
        }
      }
      return hits;
    }
    if (tabId === "recent") {
      const known = new Map();
      for (const id of catalog.tabs || []) {
        for (const row of catalog.emojis[id] || []) known.set(row[0].replaceAll("\uFE0F", ""), row);
      }
      const seen = new Set();
      const out = [];
      for (const g of readEmojiRecent()) {
        const row = known.get(g.replaceAll("\uFE0F", ""));
        if (!row || seen.has(row[0])) continue;
        seen.add(row[0]);
        out.push([row[0], row[1] || row[0]]);
      }
      return out;
    }
    return (catalog.emojis[tabId] || []).map((row) => [row[0], row[1] || row[0]]);
  }
  function renderEmojiGrid() {
    const wrap = document.getElementById("emojiGridWrap");
    const search = document.getElementById("emojiSearch");
    if (!wrap) return;
    const query = search ? search.value : "";
    const tabId = query.trim() ? "search" : emojiActiveTab;
    const cacheKey = tabId + "::" + query.trim().toLowerCase();
    if (emojiGridCache[cacheKey]) {
      wrap.replaceChildren(emojiGridCache[cacheKey]);
      return;
    }
    const entries = emojiEntriesForTab(emojiActiveTab, query);
    const grid = el("div", "emoji-grid");
    if (entries.length) {
      entries.forEach(([glyph, label]) => {
        const pick = el("button", "emoji-cell", glyph);
        pick.type = "button";
        pick.setAttribute("role", "option");
        pick.setAttribute("aria-label", label || glyph);
        pick.addEventListener("click", () => pickEmoji(glyph));
        grid.appendChild(pick);
      });
    } else {
      const empty = el("p", "emoji-grid-empty", query.trim() ? "No emoji found" : "No recent emoji yet");
      grid.appendChild(empty);
    }
    emojiGridCache[cacheKey] = grid;
    wrap.replaceChildren(grid);
  }
  function setEmojiTab(tabId) {
    const catalog = loadEmojiCatalog();
    if (!catalog) return;
    emojiActiveTab = tabId;
    const tabs = document.getElementById("emojiTabs");
    if (tabs) {
      tabs.querySelectorAll(".emoji-tab").forEach((btn) => {
        const on = btn.dataset.tab === tabId;
        btn.setAttribute("aria-selected", on ? "true" : "false");
        btn.tabIndex = on ? 0 : -1;
      });
    }
    renderEmojiGrid();
  }
  function ensureEmojiPicker() {
    if (emojiPickerReady) return loadEmojiCatalog() != null;
    const catalog = loadEmojiCatalog();
    if (!catalog) return false;
    const tabsEl = document.getElementById("emojiTabs");
    const search = document.getElementById("emojiSearch");
    if (!tabsEl) return false;
    tabsEl.replaceChildren();
    (catalog.tabs || []).forEach((tabId) => {
      const btn = el("button", "emoji-tab", catalog.icons?.[tabId] ?? "•");
      btn.type = "button";
      btn.dataset.tab = tabId;
      btn.setAttribute("role", "tab");
      const label = catalog.labels?.[tabId] ?? tabId;
      btn.setAttribute("aria-label", label);
      btn.title = label;
      btn.setAttribute("aria-selected", tabId === emojiActiveTab ? "true" : "false");
      btn.tabIndex = tabId === emojiActiveTab ? 0 : -1;
      btn.addEventListener("click", (ev) => {
        ev.stopPropagation();
        if (search) search.value = "";
        emojiGridCache = {};
        setEmojiTab(tabId);
      });
      tabsEl.appendChild(btn);
    });
    if (search && !search.dataset.bound) {
      search.dataset.bound = "1";
      search.addEventListener("input", () => {
        emojiGridCache = {};
        renderEmojiGrid();
      });
      search.addEventListener("click", (ev) => ev.stopPropagation());
    }
    emojiPickerReady = true;
    setEmojiTab(emojiActiveTab);
    return true;
  }
  function pickEmoji(glyph) {
    const field = document.getElementById("messageText");
    insertAtCursor(field, glyph);
    rememberEmoji(glyph);
    closeEmojiPanel();
  }
  function closeEmojiPanel() {
    emojiOpen = false;
    const panel = document.getElementById("emojiPanel");
    const button = document.getElementById("messageEmoji");
    const search = document.getElementById("emojiSearch");
    if (panel) panel.hidden = true;
    if (button) button.setAttribute("aria-expanded", "false");
    if (search) search.value = "";
    emojiGridCache = {};
  }
  function openEmojiPanel() {
    const panel = document.getElementById("emojiPanel");
    const button = document.getElementById("messageEmoji");
    if (!panel || !button) return;
    if (!ensureEmojiPicker()) return;
    emojiOpen = true;
    panel.hidden = false;
    button.setAttribute("aria-expanded", "true");
    renderEmojiGrid();
  }
  function toggleEmojiPanel() {
    if (emojiOpen) closeEmojiPanel();
    else openEmojiPanel();
  }
  function stopAudioRecording(silent) {
    const btn = document.getElementById("messageAudio");
    clearAudioRecordTimer();
    if (audioRecorder?.state !== "inactive") {
      try {
        audioRecorder.stop();
      } catch (error_) {
      globalThis.console?.debug?.(error_);
      }
    } else if (!silent) {
      audioRecording = false;
      teardownAudioVisualizer();
      syncRecordingChrome();
      if (btn) {
        btn.classList.remove("composer-recording");
        btn.setAttribute("aria-pressed", "false");
      }
    }
  }
  function cancelAudioRecording() {
    if (!audioRecording) return;
    audioSkipSend = true;
    stopAudioRecording(false);
  }
  function releaseAudioStream() {
    if (audioStream) {
      audioStream.getTracks().forEach((track) => track.stop());
      audioStream = null;
    }
  }
  const revokePendingPreview = (file) => {
    if (file?.previewUrl) {
      try {
        URL.revokeObjectURL(file.previewUrl);
      } catch (error_) {
        globalThis.console?.debug?.(error_);
      }
    }
  }
  function clearPendingFile() {
    revokePendingPreview(pendingFile);
    pendingFile = null;
    audioSkipSend = true;
    stopAudioRecording(true);
    releaseAudioStream();
    teardownAudioVisualizer();
    audioRecorder = null;
    audioChunks = [];
    audioRecording = false;
    audioSkipSend = false;
    clearAudioRecordTimer();
    const audioBtn = document.getElementById("messageAudio");
    if (audioBtn) {
      audioBtn.classList.remove("composer-recording");
      audioBtn.setAttribute("aria-pressed", "false");
    }
    syncRecordingChrome();
    const tray = document.getElementById("composerAttachments");
    const input = document.getElementById("messageFile");
    const audioInput = document.getElementById("messageAudioFile");
    if (tray) {
      tray.replaceChildren();
      tray.hidden = true;
    }
    if (input) input.value = "";
    if (audioInput) audioInput.value = "";
    syncComposerSendMic();
  }
  function renderPendingFile() {
    const tray = document.getElementById("composerAttachments");
    if (!tray) return;
    tray.replaceChildren();
    if (!pendingFile) {
      tray.hidden = true;
      return;
    }
    const kind = pendingFile.kind || fileKind(pendingFile.name, pendingFile.mime);
    const chip = el("div", "composer-chip");
    if (kind === "image" && pendingFile.previewUrl) {
      chip.classList.add("composer-chip--media");
      const thumb = el("img", "composer-chip-thumb");
      thumb.src = pendingFile.previewUrl;
      thumb.alt = "";
      chip.appendChild(thumb);
    } else if (kind === "video" && pendingFile.previewUrl) {
      chip.classList.add("composer-chip--media");
      const thumb = document.createElement("video");
      thumb.className = "composer-chip-thumb";
      thumb.src = pendingFile.previewUrl;
      thumb.muted = true;
      thumb.playsInline = true;
      thumb.preload = "metadata";
      thumb.setAttribute("aria-hidden", "true");
      chip.appendChild(thumb);
    }
    const label = pendingFile.mime?.startsWith("audio/")
      ? "Audio · " + pendingFile.name
      : pendingFile.name;
    chip.appendChild(el("span", "composer-chip-label", label));
    const remove = el("button", "", "×");
    remove.type = "button";
    remove.setAttribute("aria-label", "Remove attachment");
    remove.addEventListener("click", clearPendingFile);
    chip.appendChild(remove);
    tray.appendChild(chip);
    tray.hidden = false;
    syncComposerSendMic();
  }
  const readFileAsBase64 = async (file) => {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => {
        const raw = reader.result;
        const result = typeof raw === "string" ? raw : "";
        const base64 = result.includes(",") ? result.split(",", 2)[1] : "";
        resolve(base64);
      };
      reader.onerror = () => reject(reader.error || new Error("read failed"));
      reader.readAsDataURL(file);
    });
  }
  async function attachFile(file) {
    if (!file) return;
    if (file.size > CHAT_FILE_MAX_BYTES) {
      setComposerStatus("File is over 4 MB");
      clearPendingFile();
      return;
    }
    try {
      const data = await readFileAsBase64(file);
      revokePendingPreview(pendingFile);
      const mime = file.type || "application/octet-stream";
      const kind = fileKind(file.name || "attachment", mime);
      let previewUrl = "";
      if (kind === "image" || kind === "video") previewUrl = URL.createObjectURL(file);
      pendingFile = {
        name: file.name || "attachment",
        mime,
        data,
        kind,
        previewUrl
      };
      clearComposerStatus();
      renderPendingFile();
    } catch (err) {
      if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
      setComposerStatus("Could not read file");
      clearPendingFile();
    }
  }
  function setDraft(on) {
    drafting = on;
    pinned = on ? true : pinned;
    if (on) {
      selectConversation("");
      threadSig = "";
      clearComposerStatus();
      setRecipientValue("", "", { silent: true });
      openComposeSheet();
    } else {
      closeComposeSheet();
    }
    syncNewButton();
    updateHeader();
    if (on) renderThread([]);
    else syncComposerSendMic();
  }
  function applyOutboundTarget(payload) {
    if (openConversation && !drafting) {
      payload.conversation_id = openConversation;
      return true;
    }
    const recipient = document.getElementById("messagePeer")?.value || "";
    if (!recipient) return false;
    payload.recipient = recipient;
    return true;
  }
  const chatErrorText = async (res, fallback) => {
    let detail = "";
    if (res.status >= 400 && res.status < 500) {
      try {
        detail = String(await res.text()).trim().split("\n")[0].slice(0, 120);
      } catch (error_) {
      globalThis.console?.debug?.(error_);
      }
    }
    return detail ? fallback + ": " + detail : fallback;
  }
  async function postChatPayload(payload) {
    if (!meshID) {
      setComposerStatus("No circle yet");
      return false;
    }
    clearComposerStatus();
    try {
      const res = await fetch("/api/chat/messages", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      });
      if (!res.ok) {
        setComposerStatus(await chatErrorText(res, "Message was not sent"));
        return false;
      }
      const body = await res.json();
      if (body.message?.conversationId) openConversation = body.message.conversationId;
      drafting = false;
      pinned = true;
      threadSig = "";
      forceThreadScroll = true;
      syncNewButton();
      closeComposeSheet();
      if (deps.onSent) await deps.onSent();
      return true;
    } catch (err) {
      if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
      setComposerStatus("Message was not sent");
      return false;
    }
  }
  async function autoSendVoiceFile(file) {
    if (!file) return;
    if (file.size > CHAT_FILE_MAX_BYTES) {
      setComposerStatus("Voice note is over 4 MB");
      return;
    }
    let data;
    try {
      data = await readFileAsBase64(file);
    } catch (err) {
      if (err != null && globalThis.console?.debug) globalThis.console.debug(err);
      setComposerStatus("Could not read recording");
      return;
    }
    const payload = {
      mesh_id: meshID,
      type: "file",
      file_name: file.name || "voice.webm",
      mime_type: file.type || "audio/webm",
      data
    };
    if (!applyOutboundTarget(payload)) {
      openComposeSheet();
      setComposerStatus("Choose a recipient to send your voice note");
      pendingFile = {
        name: payload.file_name,
        mime: payload.mime_type,
        data
      };
      renderPendingFile();
      return;
    }
    setComposerStatus("Sending voice note…");
    const ok = await postChatPayload(payload);
    if (ok) {
      clearPendingFile();
      closeEmojiPanel();
      syncComposerSendMic();
    }
  }
  async function sendMessage(ev) {
    ev.preventDefault();
    const field = document.getElementById("messageText");
    const message = (field.value || "").trim();
    if (!message && !pendingFile) return;
    let payload;
    if (pendingFile) {
      payload = {
        mesh_id: meshID,
        type: "file",
        file_name: pendingFile.name,
        mime_type: pendingFile.mime,
        data: pendingFile.data
      };
      if (message) payload.text = message;
    } else {
      payload = { mesh_id: meshID, type: "text", text: message };
    }
    if (!applyOutboundTarget(payload)) {
      openComposeSheet();
      return;
    }
    if (sendingMessage) return;
    sendingMessage = true;
    let ok = false;
    try {
      ok = await postChatPayload(payload);
    } finally {
      sendingMessage = false;
    }
    if (!ok) return;
    conversationDrafts.delete(openConversation);
    field.value = "";
    field.style.height = "";
    clearPendingFile();
    closeEmojiPanel();
    syncComposerSendMic();
  }
  function syncComposeCreateButton() {
    const btn = document.getElementById("composeCreateGroup");
    const name = (document.getElementById("composeGroupName")?.value || "").trim();
    if (!btn) return;
    btn.disabled = creatingGroup || composePicked.size < 1 || !name;
  }
  async function createGroupFromCompose() {
    const name = (document.getElementById("composeGroupName")?.value || "").trim();
    if (creatingGroup || !meshID || !name || composePicked.size < 1) return;
    creatingGroup = true;
    syncComposeCreateButton();
    try {
      const res = await fetch("/api/chat/groups", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          mesh_id: meshID,
          name,
          member_device_ids: Array.from(composePicked)
        })
      });
      if (!res.ok) {
        setComposerStatus(await chatErrorText(res, "Could not create group"));
        return;
      }
      const body = await res.json();
      composePicked = new Set();
      composeMode = "direct";
      const nameField = document.getElementById("composeGroupName");
      if (nameField) nameField.value = "";
      syncComposeCreateButton();
      if (body.group) chatGroups.push(body.group);
      if (deps.onSent) await deps.onSent();
      openGroupConversation(body.group?.conversation_id || body.group?.conversationId);
    } catch (error_) {
      globalThis.console?.debug?.(error_);
      setComposerStatus("Could not create group");
    } finally {
      creatingGroup = false;
      syncComposeCreateButton();
    }
  }
  async function refreshChatGroups() {
    if (!meshID) {
      chatGroups = [];
      return;
    }
    try {
      const res = await fetch("/api/chat/groups?mesh_id=" + encodeURIComponent(meshID), { cache: "no-store" });
      if (!res.ok) return;
      const body = await res.json();
      chatGroups = body.groups || [];
    } catch (error_) {
      globalThis.console?.debug?.(error_);
      chatGroups = [];
    }
  }
  function callTargets() {
    if (openConversation && !drafting) {
      const conv = conversations.find((item) => item.conversation_id === openConversation);
      if (conv && (conv.kind === "group" || conv.kind === "circle")) {
        const members = conv.kind === "circle"
          ? ((circle?.members) || []).filter((peer) => !peer.revoked).map((peer) => peer.device_id)
          : conv.member_device_ids || [];
        return {
          conversationId: openConversation,
          label: conversationTitle(conv),
          targets: members.filter((id) => id && id !== selfID)
        };
      }
      const parts = String(openConversation || "").split(":");
      if (parts[0] === "direct" && parts.length >= 3) {
        const other = parts[1] === selfID ? parts[2] : parts[1];
        const member = deps.roster(currentData()).find((item) => item.device_id === other);
        return {
          conversationId: openConversation,
          label: member ? deps.computerLabel(member, currentData()) : "Direct",
          targets: other ? [other] : []
        };
      }
    }
    const peer = document.getElementById("messagePeer")?.value || "";
    const label = document.getElementById("messagePeerLabel")?.textContent || "";
    return { conversationId: "", label, targets: peer ? [peer] : [] };
  }
  function bindComposeSheet() {
    const back = document.getElementById("composeBack");
    const search = document.getElementById("composeSearch");
    const tabDirect = document.getElementById("composeTabDirect");
    const tabGroup = document.getElementById("composeTabGroup");
    const createBtn = document.getElementById("composeCreateGroup");
    const groupName = document.getElementById("composeGroupName");
    tabDirect?.addEventListener("click", () => {
      composeMode = "direct";
      composePicked = new Set();
      syncComposeModeUI();
    });
    tabGroup?.addEventListener("click", () => {
      composeMode = "group";
      syncComposeModeUI();
    });
    createBtn?.addEventListener("click", () => createGroupFromCompose().catch(() => {}));
    groupName?.addEventListener("input", syncComposeCreateButton);
    if (back) {
      back.addEventListener("click", () => dismissComposeSheet());
    }
    if (search) {
      search.addEventListener("input", () => renderComposeContacts(search.value));
    }
    const head = document.querySelector(".chat-widget .chat-id");
    if (head) {
      head.addEventListener("click", () => {
        if (!drafting) return;
        openComposeSheet();
      });
      head.style.cursor = "pointer";
    }
    document.addEventListener("keydown", (ev) => {
      if (ev.key !== "Escape" || !isComposeSheetOpen() || audioRecording || emojiOpen) return;
      ev.preventDefault();
      dismissComposeSheet();
    });
  }
  const preferredAudioMime = () => {
    if (typeof MediaRecorder === "undefined") return "";
    if (MediaRecorder.isTypeSupported("audio/webm;codecs=opus")) return "audio/webm;codecs=opus";
    if (MediaRecorder.isTypeSupported("audio/webm")) return "audio/webm";
    if (MediaRecorder.isTypeSupported("audio/ogg;codecs=opus")) return "audio/ogg;codecs=opus";
    if (MediaRecorder.isTypeSupported("audio/ogg")) return "audio/ogg";
    return "";
  }
  async function startAudioRecording() {
    const btn = document.getElementById("messageAudio");
    if (audioRecording || audioStarting) return;
    if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === "undefined") {
      setComposerStatus("Recording not supported in this browser", { ttlMs: 4500 });
      openAudioFilePicker();
      return;
    }
    audioStarting = true;
    if (await microphoneAlreadyDenied()) {
      audioStarting = false;
      setComposerStatus("Microphone permission denied", { ttlMs: 5000 });
      return;
    }
    try {
      audioStream = await navigator.mediaDevices.getUserMedia({ audio: VOICE_CAPTURE_AUDIO_CONSTRAINTS });
      audioStarting = false;
      const mime = preferredAudioMime();
      audioChunks = [];
      audioSkipSend = false;
      audioRecorder = mime ? new MediaRecorder(audioStream, { mimeType: mime }) : new MediaRecorder(audioStream);
      audioRecorder.ondataavailable = (ev) => {
        if (ev.data?.size) audioChunks.push(ev.data);
      };
      audioRecorder.onstop = async () => {
        const type = (audioRecorder?.mimeType) || mime || "audio/webm";
        const blob = new Blob(audioChunks, { type });
        const skip = audioSkipSend;
        releaseAudioStream();
        teardownAudioVisualizer();
        audioRecorder = null;
        audioChunks = [];
        audioRecording = false;
        audioSkipSend = false;
        clearAudioRecordTimer();
        syncRecordingChrome();
        if (btn) {
          btn.classList.remove("composer-recording");
          btn.setAttribute("aria-pressed", "false");
        }
        if (skip) {
          setComposerStatus("Recording canceled", { ttlMs: 2800 });
          return;
        }
        if (!blob.size) {
          setComposerStatus("Recording was empty", { ttlMs: 3500 });
          return;
        }
        const ext = type.includes("ogg") ? "ogg" : "webm";
        const file = new File([blob], "voice-" + Date.now() + "." + ext, { type: blob.type || type });
        await autoSendVoiceFile(file);
      };
      audioRecorder.onerror = () => {
        setComposerStatus("Could not record audio", { ttlMs: 3500 });
        clearPendingFile();
      };
      audioRecorder.start(250);
      audioRecording = true;
      setupAudioVisualizer(audioStream);
      if (btn) {
        btn.classList.add("composer-recording");
        btn.setAttribute("aria-pressed", "true");
      }
      clearAudioRecordTimer();
      audioRecordTimer = setTimeout(() => {
        if (!audioRecording) return;
        setComposerStatus("Maximum recording length reached", { ttlMs: 4000 });
        stopAudioRecording(false);
      }, AUDIO_RECORD_MAX_MS);
      const hint = hasOutboundRecipient()
        ? "Recording… release mic or tap again to send"
        : "Recording… choose a recipient to send";
      setComposerStatus(hint);
    } catch (err) {
      audioStarting = false;
      audioRecording = false;
      audioRecorder = null;
      releaseAudioStream();
      teardownAudioVisualizer();
      syncRecordingChrome();
      if (btn) {
        btn.classList.remove("composer-recording");
        btn.setAttribute("aria-pressed", "false");
      }
      const name = err?.name ? String(err.name) : "";
      if (name === "NotAllowedError" || name === "PermissionDeniedError" || name === "SecurityError") {
        setComposerStatus("Microphone permission denied", { ttlMs: 5000 });
      } else {
        setComposerStatus("Microphone unavailable", { ttlMs: 4000 });
      }
    }
  }
  function bindComposerDragDrop() {
    const form = document.getElementById("messageForm");
    const composer = document.querySelector(".composer-wa");
    const main = document.querySelector(".composer-pill");
    const highlight = main || composer || form;
    const zones = [form, composer, main].filter(Boolean);
    if (!zones.length || !highlight) return;
    const transferHasFiles = (ev) => {
      const types = ev.dataTransfer?.types;
      return types && Array.from(types).includes("Files");
    }
    function setDragHighlight(on) {
      if (on) highlight.classList.add("composer-drag-over");
      else highlight.classList.remove("composer-drag-over");
    }
    zones.forEach((zone) => {
      zone.addEventListener("dragenter", (ev) => {
        if (!transferHasFiles(ev)) return;
        ev.preventDefault();
        composerDragDepth += 1;
        setDragHighlight(true);
      });
      zone.addEventListener("dragover", (ev) => {
        if (!transferHasFiles(ev)) return;
        ev.preventDefault();
        if (ev.dataTransfer) ev.dataTransfer.dropEffect = "copy";
      });
      zone.addEventListener("dragleave", () => {
        composerDragDepth = Math.max(0, composerDragDepth - 1);
        if (composerDragDepth === 0) setDragHighlight(false);
      });
      zone.addEventListener("drop", (ev) => {
        ev.preventDefault();
        composerDragDepth = 0;
        setDragHighlight(false);
        const file = ev.dataTransfer?.files?.[0];
        if (file) attachFile(file).catch(() => {});
      });
    });
  }
  function bindComposerExtras() {
    const emojiBtn = document.getElementById("messageEmoji");
    const attachBtn = document.getElementById("messageAttach");
    const terminalBtn = document.getElementById("messageTerminal");
    const fileInput = document.getElementById("messageFile");
    const audioBtn = document.getElementById("messageAudio");
    const audioInput = document.getElementById("messageAudioFile");
    if (emojiBtn) {
      emojiBtn.addEventListener("click", (ev) => {
        ev.stopPropagation();
        toggleEmojiPanel();
      });
    }
    if (attachBtn && fileInput) {
      attachBtn.addEventListener("click", () => fileInput.click());
      fileInput.addEventListener("change", () => {
        const file = fileInput.files?.[0];
        if (file) attachFile(file).catch(() => {});
      });
    }
    if (terminalBtn) {
      terminalBtn.addEventListener("click", () => {
        const hasLive = typeof globalThis.overdriveLiveTerminalOpen === "function" && globalThis.overdriveLiveTerminalOpen();
        if (hasLive && typeof globalThis.overdriveShareLiveTerminal === "function") {
          Promise.resolve(globalThis.overdriveShareLiveTerminal()).catch(() => {});
          return;
        }
        if (typeof globalThis.overdriveShowLiveTerminal === "function") {
          Promise.resolve(globalThis.overdriveShowLiveTerminal()).catch(() => {});
          setComposerStatus("Use Send to chat to share this terminal live", { ttlMs: 4500 });
        }
      });
    }
    globalThis.overdriveComposerStatus = (text, ttlMs) => setComposerStatus(text, { ttlMs: ttlMs || 4000 });
    if (audioBtn) {
      const HOLD_RELEASE_MS = 320;
      let micPressAt = 0;
      let micHoldRelease = false;
      let micStartedThisGesture = false;
      audioBtn.addEventListener("pointerdown", (ev) => {
        if (ev.button !== 0 || ev.shiftKey) return;
        micPressAt = Date.now();
        micHoldRelease = false;
        micStartedThisGesture = false;
        if (!audioRecording) {
          ev.preventDefault();
          startAudioRecording().catch(() => {});
          micStartedThisGesture = true;
        }
        try {
          audioBtn.setPointerCapture(ev.pointerId);
        } catch (error_) {
      globalThis.console?.debug?.(error_);
      }
      });
      audioBtn.addEventListener("pointerup", (ev) => {
        if (ev.button !== 0 || ev.shiftKey) return;
        const held = micPressAt ? Date.now() - micPressAt : 0;
        micPressAt = 0;
        if (audioRecording && held >= HOLD_RELEASE_MS) {
          micHoldRelease = true;
          micSuppressClick = true;
          stopAudioRecording(false);
        }
        try {
          if (audioBtn.hasPointerCapture(ev.pointerId)) audioBtn.releasePointerCapture(ev.pointerId);
        } catch (error_) {
      globalThis.console?.debug?.(error_);
      }
      });
      audioBtn.addEventListener("pointercancel", () => {
        micPressAt = 0;
      });
      audioBtn.addEventListener("click", (ev) => {
        ev.stopPropagation();
        if (ev.shiftKey && audioInput) {
          audioInput.click();
          return;
        }
        if (micSuppressClick || micHoldRelease) {
          micSuppressClick = false;
          micHoldRelease = false;
          micStartedThisGesture = false;
          ev.preventDefault();
          return;
        }
        if (micStartedThisGesture) {
          micStartedThisGesture = false;
          ev.preventDefault();
          return;
        }
        if (audioRecording) stopAudioRecording(false);
        else startAudioRecording().catch(() => {});
      });
      audioBtn.addEventListener("keydown", (ev) => {
        if (ev.key === "Escape" && audioRecording) {
          ev.preventDefault();
          cancelAudioRecording();
          return;
        }
        if (ev.key !== "Enter" && ev.key !== " ") return;
        ev.preventDefault();
        if (audioRecording) stopAudioRecording(false);
        else startAudioRecording().catch(() => {});
      });
    }
    if (audioInput) {
      audioInput.addEventListener("change", () => {
        const file = audioInput.files?.[0];
        if (file) attachFile(file).catch(() => {});
      });
    }
    bindComposerDragDrop();
    document.addEventListener("click", (ev) => {
      if (!emojiOpen) return;
      const panel = document.getElementById("emojiPanel");
      if (panel && (panel.contains(ev.target) || (emojiBtn?.contains(ev.target)))) return;
      closeEmojiPanel();
    });
    document.addEventListener("keydown", (ev) => {
      if (audioRecording && ev.key === "Escape") {
        ev.preventDefault();
        cancelAudioRecording();
        if (audioBtn) audioBtn.focus();
        return;
      }
      if (!emojiOpen) return;
      if (ev.key === "Escape") {
        ev.preventDefault();
        closeEmojiPanel();
        if (emojiBtn) emojiBtn.focus();
      }
    });
  }
  function restoreWidgetSize() {
    const panel = document.getElementById("messages");
    if (!panel) return;
    try {
      const raw = localStorage.getItem(WIDGET_SIZE_KEY);
      if (!raw) return;
      const size = JSON.parse(raw);
      if (size.width) panel.style.width = size.width;
      if (size.height) panel.style.height = size.height;
    } catch (error_) {
      globalThis.console?.debug?.(error_);
      }
  }
  function restoreWidgetPosition() {
    const panel = document.getElementById("messages");
    if (!panel) return;
    try {
      const raw = localStorage.getItem(WIDGET_POS_KEY);
      if (!raw) return;
      const pos = JSON.parse(raw);
      if (!Number.isFinite(pos.left) || !Number.isFinite(pos.top)) return;
      const maxL = Math.max(8, globalThis.innerWidth - panel.offsetWidth - 8);
      const maxT = Math.max(72, globalThis.innerHeight - panel.offsetHeight - 8);
      const left = Math.min(maxL, Math.max(8, pos.left));
      const top = Math.min(maxT, Math.max(72, pos.top));
      panel.classList.add("is-positioned");
      panel.style.right = "auto";
      panel.style.left = left + "px";
      panel.style.top = top + "px";
    } catch (error_) {
      globalThis.console?.debug?.(error_);
      }
  }
  function saveWidgetPosition(panel) {
    if (!panel?.classList.contains("is-positioned")) return;
    const rect = panel.getBoundingClientRect();
    try {
      localStorage.setItem(WIDGET_POS_KEY, JSON.stringify({ left: Math.round(rect.left), top: Math.round(rect.top) }));
    } catch (error_) {
      globalThis.console?.debug?.(error_);
      }
  }
  function bindWidgetDrag() {
    const panel = document.getElementById("messages");
    const handle = document.getElementById("msgDragHandle");
    if (!panel || !handle) return;
    let startX = 0;
    let startY = 0;
    let startL = 0;
    let startT = 0;
    function onMove(ev) {
      const w = panel.offsetWidth;
      const h = panel.offsetHeight;
      const maxL = Math.max(8, globalThis.innerWidth - w - 8);
      const maxT = Math.max(72, globalThis.innerHeight - h - 8);
      const left = Math.min(maxL, Math.max(8, startL + (ev.clientX - startX)));
      const top = Math.min(maxT, Math.max(72, startT + (ev.clientY - startY)));
      panel.style.left = left + "px";
      panel.style.top = top + "px";
    }
    function onUp() {
      panel.classList.remove("is-dragging");
      document.removeEventListener("pointermove", onMove);
      document.removeEventListener("pointerup", onUp);
      saveWidgetPosition(panel);
    }
    handle.addEventListener("pointerdown", (ev) => {
      if (ev.button !== 0) return;
      if (ev.target.closest("button, a, input, textarea, select")) return;
      ev.preventDefault();
      const rect = panel.getBoundingClientRect();
      panel.classList.add("is-positioned", "is-dragging");
      panel.style.right = "auto";
      panel.style.left = rect.left + "px";
      panel.style.top = rect.top + "px";
      startX = ev.clientX;
      startY = ev.clientY;
      startL = rect.left;
      startT = rect.top;
      handle.setPointerCapture(ev.pointerId);
      document.addEventListener("pointermove", onMove);
      document.addEventListener("pointerup", onUp);
    });
  }
  function bindWidgetResize() {
    const handle = document.getElementById("msgResize");
    const panel = document.getElementById("messages");
    if (!handle || !panel) return;
    let startX = 0;
    let startY = 0;
    let startW = 0;
    let startH = 0;
    let splitDrag = false;
    function onMove(ev) {
      const live = document.getElementById("meshLive");
      const joined = live?.classList.contains("call-joined") && !panel.hidden;
      if (joined) {
        splitDrag = true;
        const rect = live.getBoundingClientRect();
        const focus = live.getAttribute("data-focus") || "chat";
        const paneW = meshSplitPaneWidth(focus, ev.clientX, rect.left, rect.width);
        if (focus === "stage") live.style.setProperty("--mesh-stage", paneW + "px");
        else live.style.setProperty("--mesh-chat", paneW + "px");
        return;
      }
      splitDrag = false;
      const w = Math.max(280, Math.min(globalThis.innerWidth - 24, startW + (ev.clientX - startX)));
      const h = Math.max(168, Math.min(globalThis.innerHeight - 100, startH + (ev.clientY - startY)));
      panel.style.width = w + "px";
      panel.style.height = h + "px";
    }
    function onUp(ev) {
      document.removeEventListener("pointermove", onMove);
      document.removeEventListener("pointerup", onUp);
      document.body.classList.remove("mesh-split-dragging");
      try {
        if (ev?.pointerId != null) handle.releasePointerCapture(ev.pointerId);
      } catch (error_) {
        globalThis.console?.debug?.(error_);
      }
      if (splitDrag) {
        splitDrag = false;
        return;
      }
      try {
        localStorage.setItem(WIDGET_SIZE_KEY, JSON.stringify({ width: panel.style.width, height: panel.style.height }));
      } catch (error_) {
      globalThis.console?.debug?.(error_);
      }
    }
    handle.addEventListener("pointerdown", (ev) => {
      if (ev.button !== 0) return;
      ev.preventDefault();
      const rect = panel.getBoundingClientRect();
      startX = ev.clientX;
      startY = ev.clientY;
      startW = rect.width;
      startH = rect.height;
      try {
        handle.setPointerCapture(ev.pointerId);
      } catch (error_) {
        globalThis.console?.debug?.(error_);
      }
      document.body.classList.add("mesh-split-dragging");
      document.addEventListener("pointermove", onMove);
      document.addEventListener("pointerup", onUp);
    });
  }
  function bind() {
    bindChatMediaModal();
    bindComposeSheet();
    bindComposerExtras();
    const form = document.getElementById("messageForm");
    if (form) form.addEventListener("submit", (ev) => { sendMessage(ev).catch(() => {}); });
    const field = document.getElementById("messageText");
    if (field) {
      field.addEventListener("input", growComposer);
      field.addEventListener("keydown", (ev) => {
        if (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing) {
          ev.preventDefault();
          if (field.form) field.form.requestSubmit();
        }
      });
    }
    const newer = document.getElementById("msgNew");
    if (newer) {
      newer.addEventListener("click", () => {
        if (drafting) {
          exitComposeFlow();
          return;
        }
        setDraft(true);
        if (field) field.focus();
      });
    }
    const expandBtn = document.getElementById("msgExpand");
    if (expandBtn) {
      expandBtn.addEventListener("click", (ev) => {
        ev.stopPropagation();
        toggleExpand();
      });
    }
    document.getElementById("msgClose")?.addEventListener("click", () => {
      cancelAudioRecording();
      closeEmojiPanel();
    });
    restoreWidgetSize();
    restoreWidgetPosition();
    bindWidgetResize();
    bindWidgetDrag();
    closeComposeSheet();
    syncNewButton();
    syncComposerSendMic();
    syncChrome();
  }
  function expand() {
    if (expanded) return;
    expanded = true;
    syncChrome();
    threadSig = "";
    forceThreadScroll = true;
    renderThread(lastMessages);
    if (!drafting && openConversation && meshID) loadThread(openConversation, true).catch(() => {});
  }
  function collapse() {
    if (!expanded) return;
    expanded = false;
    syncChrome();
    threadSig = "";
    forceThreadScroll = true;
    renderThread(lastMessages);
  }
  function toggleExpand() {
    if (expanded) collapse();
    else expand();
  }
  async function apply(ctx) {
    meshID = ctx.meshID || "";
    selfID = ctx.selfID || "";
    circle = ctx.circle || null;
    data = ctx.data || null;
    conversations = ctx.conversations || [];
    await refreshChatGroups();
    paintUnread();
    renderConversationList();
    fillRecipients();
    if (!drafting) {
      const exists = openConversation && conversations.some((conv) => conv.conversation_id === openConversation);
      if (!exists) {
        pinned = false;
        selectConversation(newestId());
      } else if (!pinned && !composerBusy()) {
        selectConversation(newestId());
      }
    }
    updateHeader();
    syncChrome();
    if (drafting) return;
    if (openConversation && meshID) await loadThread(openConversation, expanded);
    else renderThread([]);
  }

  bind();
  return {
    expand,
    collapse,
    toggleExpand,
    isExpanded() { return expanded; },
    apply,
    callTargets,
    outboundTarget() {
      const payload = {};
      if (!applyOutboundTarget(payload)) return null;
      return payload;
    },
    openConversationById(id) { openGroupConversation(id); }
  };
}

export {
  createMeshChat,
  fileKind,
  FILE_KIND_STRATEGIES,
  formatVoiceTime,
  inlineMediaMime,
  meshSplitPaneWidth,
  MESH_CHAT_MIN_WIDTH,
  MESH_CHAT_SPLIT_TAIL,
  MESH_STAGE_MIN_WIDTH,
  MESH_STAGE_MAX_WIDTH,
  parseVideoEmbedUrl,
  recordingLevel,
  sanitizeVimeoVideoId,
  sanitizeYouTubeVideoId,
  scanInlineText,
  sortConversationsByActivity,
  VOICE_BAR_FLOOR,
  waveformPeaks,
};

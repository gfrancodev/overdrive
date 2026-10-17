"""Unit tests for dashboard call/chat pure helpers (Node ESM)."""

from __future__ import annotations

import json
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
CHAT_MJS = ROOT / "runtime" / "experience" / "dashboard" / "chat.mjs"
CALL_MJS = ROOT / "runtime" / "experience" / "dashboard" / "call.mjs"
MACHINES_MJS = ROOT / "runtime" / "experience" / "dashboard" / "machines.mjs"


def _node_eval(script: str) -> str:
    node = shutil.which("node")
    if not node:
        pytest.skip("Node.js not available")
    proc = subprocess.run(
        [node, "--input-type=module", "-e", script],
        cwd=ROOT,
        capture_output=True,
        text=True,
        check=False,
    )
    if proc.returncode != 0:
        raise AssertionError(proc.stderr or proc.stdout)
    return proc.stdout.strip()


def test_video_embed_url_parse_and_sanitize():
    out = _node_eval(
        f"""
        import {{
          parseVideoEmbedUrl,
          sanitizeYouTubeVideoId,
          sanitizeVimeoVideoId,
        }} from {json.dumps(str(CHAT_MJS))};
        console.log(JSON.stringify({{
          ytWatch: parseVideoEmbedUrl("https://www.youtube.com/watch?v=dQw4w9WgXcQ."),
          ytShort: parseVideoEmbedUrl("https://youtube.com/shorts/dQw4w9WgXcQ"),
          ytBe: parseVideoEmbedUrl("https://youtu.be/dQw4w9WgXcQ"),
          vimeo: parseVideoEmbedUrl("https://vimeo.com/123456789"),
          player: parseVideoEmbedUrl("https://player.vimeo.com/video/987654321"),
          evilHost: parseVideoEmbedUrl("https://evil.com/embed/dQw4w9WgXcQ"),
          evilId: parseVideoEmbedUrl("https://www.youtube.com/watch?v=../../etc/passwd"),
          sanitizeYt: [
            sanitizeYouTubeVideoId("dQw4w9WgXcQ"),
            sanitizeYouTubeVideoId("short"),
            sanitizeYouTubeVideoId("dQw4w9WgXcQ<script>"),
          ],
          sanitizeVm: [sanitizeVimeoVideoId("123"), sanitizeVimeoVideoId("12a3")],
        }}));
        """
    )
    data = json.loads(out)
    assert data["ytWatch"]["provider"] == "youtube"
    assert data["ytWatch"]["videoId"] == "dQw4w9WgXcQ"
    assert data["ytWatch"]["embedUrl"] == "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"
    assert data["ytShort"]["videoId"] == "dQw4w9WgXcQ"
    assert data["ytBe"]["embedUrl"].startswith("https://www.youtube-nocookie.com/embed/")
    assert data["vimeo"]["provider"] == "vimeo"
    assert data["vimeo"]["embedUrl"] == "https://player.vimeo.com/video/123456789"
    assert data["player"]["videoId"] == "987654321"
    assert data["evilHost"] is None
    assert data["evilId"] is None
    assert data["sanitizeYt"] == ["dQw4w9WgXcQ", "", ""]
    assert data["sanitizeVm"] == ["123", ""]


def test_video_embed_markup_in_message_renderer():
    chat = CHAT_MJS.read_text(encoding="utf-8")
    css = (ROOT / "runtime/experience/dashboard/chat.css").read_text(encoding="utf-8")
    block = chat[chat.index("function mediaURLNode(url)"):chat.index("function appendInlineToken(parent, match)")]
    assert "parseVideoEmbedUrl(clean)" in block
    assert "msgVideoEmbedNode" in block
    assert "youtube-nocookie.com/embed/" in chat
    assert "spec.embedUrl" in chat
    assert "msg-video-embed__iframe" in chat
    link = chat[chat.index("function appendInlineToken(parent, match)"):chat.index("function appendInlines(parent, text)")]
    assert "parseVideoEmbedUrl(match.href)" in link
    assert ".msg-video-embed__play" in css
    assert ".chat-media-modal__embed" in css


def test_inline_scan_advances_past_unclosed_markers():
    out = _node_eval(
        f"""
        import {{ scanInlineText }} from {json.dumps(str(CHAT_MJS))};
        const samples = ["see [this", "start **bold", "tick `code", "go https://", "say **ok** now"];
        const scanned = Object.fromEntries(samples.map((text) => [text, scanInlineText(text).reduce((n, part) => n + part.len, 0)]));
        console.log(JSON.stringify(scanned));
        """
    )
    data = json.loads(out)
    for text, consumed in data.items():
        assert consumed == len(text)


def test_file_kind_audio_webm_before_video_extension():
    out = _node_eval(
        f"""
        import {{ fileKind }} from {json.dumps(str(CHAT_MJS))};
        const cases = [
          ["voice-123.webm", "audio/webm", "audio"],
          ["clip.webm", "video/webm", "video"],
          ["note.ogg", "audio/ogg", "audio"],
          ["photo.png", "image/png", "image"],
        ];
        console.log(JSON.stringify(cases.map(([n,m,e]) => [fileKind(n,m), e])));
        """
    )
    rows = json.loads(out)
    for actual, expected in rows:
        assert actual == expected


def test_voice_waveform_peaks_and_time_format():
    out = _node_eval(
        f"""
        import {{ waveformPeaks, formatVoiceTime }} from {json.dumps(str(CHAT_MJS))};
        const samples = new Float32Array(400);
        for (let i = 0; i < 400; i++) samples[i] = i < 200 ? 0.1 * Math.sin(i) : 0.8 * Math.sin(i);
        const peaks = waveformPeaks(samples, 4);
        const silent = waveformPeaks(new Float32Array(10), 5);
        console.log(JSON.stringify({{
          peaks: peaks.map((p) => Math.round(p * 100) / 100),
          silent,
          times: [formatVoiceTime(0), formatVoiceTime(7.4), formatVoiceTime(65), formatVoiceTime(NaN), formatVoiceTime(Infinity)],
        }}));
        """
    )
    data = json.loads(out)
    assert len(data["peaks"]) == 4
    assert data["peaks"][2] == 1
    assert data["peaks"][3] == 1
    assert data["peaks"][0] < 0.25
    assert data["peaks"][0] >= 0.12
    assert all(p == 0.12 for p in data["silent"])
    assert len(data["silent"]) == 5
    assert data["times"] == ["0:00", "0:07", "1:05", "0:00", "0:00"]


def test_recorded_audio_uses_voice_player_not_native_controls():
    chat = CHAT_MJS.read_text(encoding="utf-8")
    start = chat.index("  function fileMessageBubble(msg, payload) {")
    message = chat[start:chat.index("  const sameGroup = (a, b) => {")]
    assert "voiceMessageNode(url, key)" in message
    assert "audio.controls = true" not in message
    css = (ROOT / "runtime/experience/dashboard/chat.css").read_text(encoding="utf-8")
    assert ".msg-voice-play" in css
    assert ".msg-voice-bar.played" in css


def test_parse_invite_payload_modes():
    out = _node_eval(
        f"""
        import {{ parseInvitePayload }} from {json.dumps(str(CALL_MJS))};
        console.log(JSON.stringify([
          parseInvitePayload({{ mode: "video", conversation_id: "direct:a:b" }}),
          parseInvitePayload('{{"mode":"voice"}}'),
          parseInvitePayload(null),
        ]));
        """
    )
    data = json.loads(out)
    assert data[0] == {"mode": "video", "conversationId": "direct:a:b"}
    assert data[1] == {"mode": "voice", "conversationId": ""}
    assert data[2] == {"mode": "voice", "conversationId": ""}


def test_composer_terminal_share_copy():
    html = (ROOT / "runtime/experience/dashboard/chat.html").read_text(encoding="utf-8")
    shell = (ROOT / "runtime/experience/dashboard/shell.html").read_text(encoding="utf-8")
    chat = CHAT_MJS.read_text(encoding="utf-8")
    term = (ROOT / "runtime/experience/dashboard/terminal.mjs").read_text(encoding="utf-8")
    app = (ROOT / "runtime/experience/dashboard/app.mjs").read_text(encoding="utf-8")
    assert 'aria-label="Open live terminal"' in html
    assert 'title="Open live terminal, then Send to chat"' in html
    assert "composer-term-label" not in html
    assert "Share terminal" in app
    assert "Use Send to chat to share this terminal live" in chat
    assert any(("Share terminal" in chat, "overdriveShareLiveTerminal" in chat))
    assert 'id="termShareChatBar"' in shell
    assert "Send the live terminal to this chat" in shell
    assert "conversation_id" in term
    assert "Shared the live terminal" in term


def test_terminal_connect_single_flight_guard():
    text = (ROOT / "runtime/experience/dashboard/terminal.mjs").read_text(encoding="utf-8")
    assert "this.connecting" in text
    assert "streamActive()" in text


def _terminal_fn(text: str, signature: str) -> str:
    start = text.index(signature)
    nxt = text.find("\n  function ", start + len(signature))
    nxt_method = text.find("\n    async ", start + len(signature))
    ends = [i for i in (nxt, nxt_method) if i != -1]
    return text[start:min(ends)] if ends else text[start:]


def test_terminal_split_shortcuts_never_reach_shell():
    text = (ROOT / "runtime/experience/dashboard/terminal.mjs").read_text(encoding="utf-8")
    # xterm would otherwise send Ctrl+D (EOF) to the shell before the window shortcut splits.
    assert 'if (ev.ctrlKey && ev.shiftKey && (key === "d" || key === "e")) return false;' in text
    assert "root.contains(document.activeElement)" in text


def test_terminal_reconnect_resets_replayed_screen_and_backs_off():
    text = (ROOT / "runtime/experience/dashboard/terminal.mjs").read_text(encoding="utf-8")
    connect = _terminal_fn(text, "async connect() {")
    assert "this.resetOutputBeforeReconnect()" in connect
    assert "this.term.reset()" in _terminal_fn(text, "resetOutputBeforeReconnect() {")
    assert "this.lastCols = 0;" in connect
    assert "this.recoverAttempts = 0" not in connect
    bind = _terminal_fn(text, "bindStreamWebSocket(ws) {")
    assert "this.scheduleRecover()" in bind
    assert "setTimeout(" in _terminal_fn(text, "scheduleRecover() {")


def test_terminal_replay_output_does_not_reset_reconnect_budget():
    text = (ROOT / "runtime/experience/dashboard/terminal.mjs").read_text(encoding="utf-8")
    # Every attach replays recent output; resetting on output let a socket that drops right after replay loop forever.
    frame = text[text.index("    handleFrame(frame) {"):text.index("    async recoverStream() {")]
    assert "recoverAttempts" not in frame
    stable = text[text.index("    markStable(ws) {"):text.index("    scheduleRecover() {")]
    assert "STREAM_STABLE_MS" in stable
    assert "this.ws === ws" in stable
    assert "this.recoverAttempts = 0" in stable
    connect = _terminal_fn(text, "async connect() {")
    bind = _terminal_fn(text, "bindStreamWebSocket(ws) {")
    assert "this.bindStreamWebSocket(ws)" in connect
    assert "this.markStable(ws)" in bind


def test_terminal_split_uses_created_session_id():
    text = (ROOT / "runtime/experience/dashboard/terminal.mjs").read_text(encoding="utf-8")
    split = _terminal_fn(text, "function splitFocused(dir) {")
    assert "createSession({ forSplit: true }).then((newId)" in split
    assert "!containsLeaf(splitRoot, newId)" in split
    assert (
        'return (sess?.id) || "";' in text
        or 'return (sess && sess.id) || "";' in text
        or 'if (!sess?.id) return "";' in text
    )


def test_terminal_lease_comes_from_session_payload():
    text = (ROOT / "runtime/experience/dashboard/terminal.mjs").read_text(encoding="utf-8")
    lease = _terminal_fn(text, "async function refreshLease(tab) {")
    assert "/events?from_seq=0" not in lease
    assert "tab.lease = parseLease(body.lease);" in lease


def test_terminal_close_tab_ends_or_detaches_session():
    text = (ROOT / "runtime/experience/dashboard/terminal.mjs").read_text(encoding="utf-8")
    close = _terminal_fn(text, "function closeTab(sessionId) {")
    assert "purgeSession(sessionId)" in close
    assert '"/detach"' in close
    assert "focusTabAfterClose(sessionId)" in close
    assert "leafIds(splitRoot, [])[0]" in text


def test_terminal_resize_does_not_steal_focus():
    text = (ROOT / "runtime/experience/dashboard/terminal.mjs").read_text(encoding="utf-8")
    assert "fitWhenVisible(focus) {" in text
    assert "if (focus && activeTabId === this.sessionId) this.term.focus();" in text
    assert 'globalThis.addEventListener("resize", () => { applyGeo(); fitActiveSoon(); });' in text


def test_terminal_output_before_mount_is_buffered():
    text = (ROOT / "runtime/experience/dashboard/terminal.mjs").read_text(encoding="utf-8")
    assert "this.pendingOutput.push(text)" in text
    assert "this.pendingOutput.forEach((text) => this.term.write(text));" in text


def test_terminal_session_ended_versus_attachable():
    out = _node_eval(
        f"""
        import {{
          terminalSessionAttachable,
          terminalSessionEnded,
        }} from {json.dumps(str(ROOT / "runtime/experience/dashboard/terminal.mjs"))};
        const states = ["RUNNING", "STARTING", "CREATED", "DETACHED", "SUSPENDED", "EXITED", "FAILED", "TERMINATED", "LOST", "STOPPING"];
        console.log(JSON.stringify({{
          attachable: states.filter((state) => terminalSessionAttachable({{ state }})),
          ended: states.filter((state) => terminalSessionEnded({{ state }})),
          missing: [terminalSessionAttachable(null), terminalSessionEnded(null), terminalSessionEnded({{}})],
        }}));
        """
    )
    data = json.loads(out)
    assert data["attachable"] == ["RUNNING", "STARTING", "CREATED", "DETACHED", "SUSPENDED"]
    assert data["ended"] == ["EXITED", "FAILED", "TERMINATED", "LOST", "STOPPING"]
    assert data["missing"] == [False, False, False]


def test_terminal_ended_pane_is_centered_empty_state():
    term = (ROOT / "runtime/experience/dashboard/terminal.mjs").read_text(encoding="utf-8")
    css = (ROOT / "runtime/experience/dashboard/dashboard.css").read_text(encoding="utf-8")
    assert "term-view--ended" in term
    assert "term-ended-action" in term
    assert "Session ended. Use + to open a new terminal." in term
    assert 'setOption("theme"' in term
    assert "--term-bg-opacity" in term
    assert ".term-view--ended .term-xterm { display: none; }" in css
    assert ".term-view--ended .term-view-status" in css
    assert "overflow-x: hidden;" in css
    assert "background: transparent;" in css.split(".term-view-status {", 1)[1].split("}", 1)[0]


def test_terminal_bg_opacity_slider_wires_transparency():
    term = TERMINAL_MJS.read_text(encoding="utf-8")
    css = (ROOT / "runtime/experience/dashboard/dashboard.css").read_text(encoding="utf-8")
    opacity = _terminal_fn(term, "function applyTermBgOpacity(percent) {")
    assert "allowTransparency: true" in term
    assert 'root.style.setProperty("--term-bg-opacity"' in opacity
    assert "syncXtermBackgrounds(alpha)" in opacity
    assert "saveBgOpacityPercent(pct)" in opacity
    assert 'getElementById("termBgOpacity")' in term
    assert "bgOpacitySlider.addEventListener(\"input\"" in term
    assert "overdriveTermBgOpacity" in term
    assert "calc(var(--term-bg-opacity, 1) * 0.72)" in css
    viewport_sel = ".term-xterm .xterm-viewport"
    assert viewport_sel in css
    viewport = css.split(viewport_sel + " {", 1)[1].split("}", 1)[0]
    assert "background-color: transparent;" in viewport


def test_terminal_path_helpers():
    out = _node_eval(
        f"""
        import {{
          shortTerminalPath,
          terminalPathBasename,
          friendlyTerminalSessionName,
          clampSplitRatio,
        }} from {json.dumps(str(ROOT / "runtime/experience/dashboard/terminal.mjs"))};
        console.log(JSON.stringify({{
          short: shortTerminalPath("/a/b/c/d"),
          base: terminalPathBasename("/work/sessions/abc"),
          name: friendlyTerminalSessionName({{ working_directory: "/repo/pkg" }}, ""),
          ratio: clampSplitRatio(9),
        }}));
        """
    )
    data = json.loads(out)
    assert data["short"].endswith("c/d")
    assert data["base"] == "abc"
    assert data["name"] == "pkg"
    assert data["ratio"] == 0.85


def test_inline_media_mime_never_trusts_sender_document_types():
    out = _node_eval(
        f"""
        import {{ inlineMediaMime }} from {json.dumps(str(CHAT_MJS))};
        console.log(JSON.stringify([
          inlineMediaMime("pdf", "report.pdf", "text/html"),
          inlineMediaMime("image", "x.png", "text/html"),
          inlineMediaMime("image", "x.png", "image/png"),
          inlineMediaMime("image", "logo.svg", "text/html"),
          inlineMediaMime("audio", "voice.webm", "audio/webm;codecs=opus"),
          inlineMediaMime("video", "clip.mp4", "text/html"),
        ]));
        """
    )
    assert json.loads(out) == [
        "application/pdf",
        "application/octet-stream",
        "image/png",
        "image/svg+xml",
        "audio/webm;codecs=opus",
        "application/octet-stream",
    ]


def test_conversations_sorted_by_last_activity():
    out = _node_eval(
        f"""
        import {{ sortConversationsByActivity }} from {json.dumps(str(CHAT_MJS))};
        const list = [
          {{ conversation_id: "group:circle", last_timestamp: "2026-10-14T20:19:37Z" }},
          {{ conversation_id: "direct:a:b", last_timestamp: "2026-10-14T20:24:04Z" }},
          {{ conversation_id: "group:grp_x", last_timestamp: "" }},
        ];
        console.log(JSON.stringify(sortConversationsByActivity(list).map((c) => c.conversation_id)));
        """
    )
    assert json.loads(out) == ["direct:a:b", "group:circle", "group:grp_x"]


CALL_HARNESS = """
const nodes = new Map();
function node(id) {
  if (!nodes.has(id)) {
    const classes = new Set();
    const el = {
      id, hidden: true, childElementCount: 0, srcObject: null, attrs: {}, dataset: {},
      classList: {
        add: (c) => classes.add(c),
        remove: (c) => classes.delete(c),
        toggle: (c, on) => (on ? classes.add(c) : classes.delete(c)),
        contains: (c) => classes.has(c),
      },
      setAttribute(k, v) {
        this.attrs[k] = v;
        if (k.startsWith("data-") && k.length > 5) {
          const key = k.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase());
          this.dataset[key] = v;
        }
      },
      getAttribute(k) { return this.attrs[k]; },
      removeAttribute(k) {
        delete this.attrs[k];
        if (k.startsWith("data-") && k.length > 5) {
          const key = k.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase());
          delete this.dataset[key];
        }
      },
      listeners: {}, addEventListener(type, fn) { this.listeners[type] = fn; },
      replaceChildren() {}, appendChild() {}, querySelector() { return null; },
    };
    const dataAttrKey = (name) => String(name).replace(/([A-Z])/g, "-$1").toLowerCase();
    const rawDataset = {};
    el.dataset = new Proxy(rawDataset, {
      set(target, key, value) {
        if (typeof key !== "string") return false;
        target[key] = value;
        el.attrs[`data-${dataAttrKey(key)}`] = value;
        return true;
      },
      deleteProperty(target, key) {
        if (typeof key !== "string") return false;
        delete target[key];
        delete el.attrs[`data-${dataAttrKey(key)}`];
        return true;
      },
    });
    let textContent = "";
    Object.defineProperty(el, "textContent", {
      get() { return textContent; },
      set(v) {
        textContent = v;
        if (id === "callErrorText") {
          const banner = nodes.get("callError");
          if (banner) banner.textContent = v;
        }
      },
    });
    nodes.set(id, el);
  }
  return nodes.get(id);
}
async function click(id) { await node(id).listeners.click?.(); await new Promise((r) => setTimeout(r, 0)); }
globalThis.document = {
  getElementById: node,
  createElement: () => node("tmp" + Math.random()),
  addEventListener() {},
  exitPictureInPicture: async () => {},
};
globalThis.location = { hash: "" };
let pollTick = null;
globalThis.setInterval = (fn) => { pollTick = fn; return 1; };
globalThis.clearInterval = () => {};
const posted = [];
let eventsBody = { events: [], sessions: [], seq: 0 };
let inviteStatus = 200;
globalThis.fetch = async (url, opts) => {
  if (String(url).startsWith("/api/call/events")) return { ok: true, json: async () => eventsBody };
  const body = JSON.parse(opts.body);
  posted.push(body);
  if (body.kind === "invite" && inviteStatus !== 200) return { ok: false, status: inviteStatus, json: async () => ({}) };
  return { ok: true, json: async () => ({ call_id: "call_1" }) };
};
class FakePC {
  constructor() { this.closed = false; this.remoteDescription = null; this.signalingState = "stable"; }
  getSenders() { return []; }
  addTrack() {}
  async createOffer() { return { sdp: "o", type: "offer" }; }
  async createAnswer() { return { sdp: "a", type: "answer" }; }
  async setLocalDescription() {}
  async setRemoteDescription(d) { this.remoteDescription = d; }
  close() { this.closed = true; }
}
const track = { kind: "audio", readyState: "live", stop() {}, enabled: true };
const mediaDevices = { async getUserMedia() { return { getTracks: () => [track], getAudioTracks: () => [track], getVideoTracks: () => [] }; } };
const store = new Map();
globalThis.localStorage = {
  getItem: (k) => (store.has(k) ? store.get(k) : null),
  setItem: (k, v) => store.set(k, String(v)),
  removeItem: (k) => store.delete(k),
};
globalThis.__overdriveAutoSetup = true;
const call = createMeshCall({ RTCPeerConnection: FakePC, mediaDevices });
call.apply({ meshID: "circle_x", selfID: "dev_self" });
async function poll(body) { eventsBody = body; await pollTick(); await new Promise((r) => setTimeout(r, 0)); }
"""


def _call_harness(body: str) -> dict:
    out = _node_eval(
        f"""
        import {{ createMeshCall }} from {json.dumps(str(CALL_MJS))};
        {CALL_HARNESS}
        {body}
        """
    )
    return json.loads(out)


def test_call_first_poll_ignores_history_of_ended_calls():
    data = _call_harness(
        """
        await poll({ seq: 3, sessions: [], events: [
          { seq: 1, call_id: "call_old", kind: "invite", from_device_id: "dev_b", payload: { mode: "voice" } },
        ] });
        console.log(JSON.stringify({ panelHidden: node("callPanel").hidden, status: node("callStatus").textContent }));
        """
    )
    assert data["panelHidden"] is True
    assert data["status"] == ""


def test_group_call_survives_one_member_hanging_up():
    data = _call_harness(
        """
        await poll({ seq: 0, sessions: [], events: [] });
        await call.startGroupCall(["dev_b", "dev_c"], "Squad", "voice", "group:grp_1");
        await poll({ seq: 1, sessions: [], events: [{ seq: 1, call_id: "call_1", kind: "hangup", from_device_id: "dev_b" }] });
        const afterOne = node("callPanel").hidden;
        await poll({ seq: 2, sessions: [], events: [{ seq: 2, call_id: "call_other", kind: "hangup", from_device_id: "dev_c" }] });
        const afterForeign = node("callPanel").hidden;
        await poll({ seq: 3, sessions: [], events: [{ seq: 3, call_id: "call_1", kind: "hangup", from_device_id: "dev_c" }] });
        console.log(JSON.stringify({ afterOne, afterForeign, afterAll: node("callPanel").hidden }));
        """
    )
    assert data == {"afterOne": False, "afterForeign": False, "afterAll": True}


def test_failed_call_start_reports_instead_of_ringing_forever():
    data = _call_harness(
        """
        inviteStatus = 500;
        let threw = false;
        try { await call.startCall("dev_b", "Bob", "voice", "direct:dev_b:dev_self"); } catch (_) { threw = true; }
        console.log(JSON.stringify({ threw, visible: !node("callPanel").hidden, status: node("callStatus").textContent }));
        """
    )
    assert data == {"threw": True, "visible": True, "status": "Could not start call"}


def test_failed_call_start_explains_media_error_and_offers_retry():
    data = _call_harness(
        """
        mediaDevices.getUserMedia = async () => { const e = new Error("denied"); e.name = "NotAllowedError"; throw e; };
        try { await call.startCall("dev_b", "Bob", "voice", ""); } catch (_) {}
        const failed = {
          phase: node("callPanel").attrs["data-phase"],
          detail: node("callError").textContent,
          retryHidden: node("callRetry").hidden,
          muteHidden: node("callMute").hidden,
          hangups: posted.filter((p) => p.kind === "hangup").length,
          invites: posted.filter((p) => p.kind === "invite").length,
        };
        mediaDevices.getUserMedia = async () => ({ getTracks: () => [track], getAudioTracks: () => [track], getVideoTracks: () => [] });
        await call.retry();
        console.log(JSON.stringify({ failed, after: node("callPanel").attrs["data-phase"], invites: posted.filter((p) => p.kind === "invite").length }));
        """
    )
    # The browser permission prompt comes first; the other device must not ring when access is refused.
    assert data["failed"] == {
        "phase": "error",
        "detail": "Access to the microphone was blocked. Allow it in the browser and try again.",
        "retryHidden": False,
        "muteHidden": True,
        "hangups": 0,
        "invites": 0,
    }
    assert data["after"] == "ringing-out"
    assert data["invites"] == 1


def test_call_without_microphone_joins_listen_only():
    data = _call_harness(
        """
        const transceivers = [];
        class ListenPC extends FakePC { addTransceiver(kind, init) { transceivers.push([kind, init && init.direction]); } }
        const call2 = createMeshCall({ RTCPeerConnection: ListenPC, mediaDevices });
        call2.apply({ meshID: "circle_x", selfID: "dev_self" });
        let asked = 0;
        mediaDevices.getUserMedia = async () => { asked++; const e = new Error("no device"); e.name = "NotFoundError"; throw e; };
        await call2.startCall("dev_b", "Bob", "voice", "");
        console.log(JSON.stringify({
          phase: node("callPanel").attrs["data-phase"],
          invites: posted.filter((p) => p.kind === "invite").length,
          offers: posted.filter((p) => p.kind === "offer").length,
          notice: node("callError").textContent,
          noticeHidden: node("callError").hidden,
          transceivers,
          asked,
        }));
        """
    )
    assert data["phase"] == "ringing-out"
    assert data["invites"] == 1
    assert data["offers"] == 1
    assert data["notice"] == "No microphone found. You can listen, but the other side can't hear you."
    assert data["noticeHidden"] is False
    assert data["transceivers"] == [["audio", "recvonly"]]
    assert data["asked"] == 1


def test_call_plays_ringback_and_stops_on_answer():
    data = _call_harness(
        """
        const contexts = [];
        class FakeAudioContext {
          constructor() { this.state = "running"; this.currentTime = 0; this.closed = false; this.oscillators = 0; this.destination = {}; contexts.push(this); }
          createOscillator() { this.oscillators++; return { frequency: { value: 0 }, type: "", connect() {}, start() {}, stop() {} }; }
          createGain() { return { gain: { value: 0, setValueAtTime() {}, linearRampToValueAtTime() {}, exponentialRampToValueAtTime() {} }, connect() {}, disconnect() {} }; }
          resume() { return Promise.resolve(); }
          close() { this.closed = true; return Promise.resolve(); }
        }
        const call2 = createMeshCall({ RTCPeerConnection: FakePC, mediaDevices, AudioContext: FakeAudioContext });
        call2.apply({ meshID: "circle_x", selfID: "dev_self" });
        await poll({ seq: 0, sessions: [], events: [] });
        await call2.startCall("dev_b", "Bob", "voice", "");
        const ringing = { contexts: contexts.length, oscillators: contexts[0] ? contexts[0].oscillators : 0, closed: contexts[0] ? contexts[0].closed : null };
        await poll({ seq: 1, sessions: [], events: [{ seq: 1, call_id: "call_1", kind: "accept", from_device_id: "dev_b" }] });
        console.log(JSON.stringify({ ringing, closedAfterAnswer: contexts[0].closed }));
        """
    )
    assert data["ringing"]["contexts"] == 1
    assert data["ringing"]["oscillators"] > 0
    assert data["ringing"]["closed"] is False
    assert data["closedAfterAnswer"] is True


def test_remote_audio_is_played_explicitly():
    data = _call_harness(
        """
        let pcRef = null;
        const call2 = createMeshCall({ RTCPeerConnection: class extends FakePC { constructor() { super(); pcRef = this; } }, mediaDevices });
        call2.apply({ meshID: "circle_x", selfID: "dev_self" });
        await call2.startCall("dev_b", "Bob", "voice", "");
        let played = 0;
        node("callRemoteVideo").play = () => { played++; return Promise.resolve(); };
        pcRef.ontrack({ streams: [{ getVideoTracks: () => [], addEventListener() {} }] });
        console.log(JSON.stringify({ played }));
        """
    )
    assert data == {"played": 1}


def _screen_harness(body: str) -> dict:
    return _call_harness(
        """
        const screenTrack = { kind: "video", readyState: "live", stopped: false, stop() { this.stopped = true; }, onended: null };
        mediaDevices.getDisplayMedia = async () => ({ getVideoTracks: () => [screenTrack], getTracks: () => [screenTrack] });
        """
        + body
    )


def test_screen_share_unsupported_browser_explains_why():
    data = _screen_harness(
        """
        mediaDevices.getDisplayMedia = async () => { const e = new Error("Not supported"); e.name = "NotSupportedError"; throw e; };
        await call.startCall("dev_b", "Bob", "voice", "");
        await click("callScreen");
        console.log(JSON.stringify({ detail: node("callError").textContent }));
        """
    )
    assert data["detail"] == "This browser can't share the screen. Open the dashboard in Chrome or Firefox."


def test_screen_shared_while_ringing_reaches_peer_after_answer():
    data = _screen_harness(
        """
        await poll({ seq: 0, sessions: [], events: [] });
        await call.startCall("dev_b", "Bob", "voice", "");
        await click("callScreen");
        const offersBefore = posted.filter((p) => p.kind === "offer").length;
        await poll({ seq: 2, sessions: [], events: [
          { seq: 1, call_id: "call_1", kind: "accept", from_device_id: "dev_b" },
          { seq: 2, call_id: "call_1", kind: "answer", from_device_id: "dev_b", payload: { type: "answer", sdp: "a" } },
        ] });
        console.log(JSON.stringify({ offersBefore, offersAfter: posted.filter((p) => p.kind === "offer").length }));
        """
    )
    assert data == {"offersBefore": 1, "offersAfter": 2}


def test_screen_button_toggles_share_off():
    data = _screen_harness(
        """
        await call.startCall("dev_b", "Bob", "voice", "");
        await click("callScreen");
        const on = node("callScreen").attrs["aria-pressed"];
        await click("callScreen");
        console.log(JSON.stringify({ on, off: node("callScreen").attrs["aria-pressed"], stopped: screenTrack.stopped }));
        """
    )
    assert data == {"on": "true", "off": "false", "stopped": True}


def test_screen_stage_mode_helpers():
    out = _node_eval(
        f"""
        import {{ screenStageMode, pipScreenVideoId, pipCompanionVideoId, SCREEN_PIP_LIMITATION, meshIceServers, CALL_NAT_NOTE }} from {json.dumps(str(CALL_MJS))};
        console.log(JSON.stringify({{
          modes: [screenStageMode(true, false), screenStageMode(false, true), screenStageMode(false, false)],
          screenIds: [pipScreenVideoId("local"), pipScreenVideoId("remote"), pipScreenVideoId("")],
          companion: pipCompanionVideoId("remote", true, true),
          pipDoc: SCREEN_PIP_LIMITATION.includes("Chrome") && SCREEN_PIP_LIMITATION.includes("Safari"),
          stun: meshIceServers(null)[0].urls,
          turn: meshIceServers({{ iceServers: [{{ urls: "stun:stun.l.google.com:19302" }}, {{ urls: "turn:example:3478" }}] }}).length,
          nat: CALL_NAT_NOTE.includes("OVERDRIVE_TURN_URLS"),
        }}));
        """
    )
    data = json.loads(out)
    assert data["modes"] == ["local", "remote", ""]
    assert data["screenIds"] == ["callLocalScreen", "callRemoteScreen", ""]
    assert data["companion"] == "callRemoteVideo"
    assert data["pipDoc"] is True
    assert data["stun"] == "stun:stun.l.google.com:19302"
    assert data["turn"] == 2
    assert data["nat"] is True


def test_screen_share_sets_stage_mode_and_shows_pip_control():
    data = _screen_harness(
        """
        await call.startCall("dev_b", "Bob", "voice", "");
        await click("callScreen");
        console.log(JSON.stringify({
          mode: node("callStage").attrs["data-screen-mode"],
          pipHidden: node("callPip").hidden,
        }));
        """
    )
    assert data == {"mode": "local", "pipHidden": False}


def test_screen_pip_toggle_uses_native_player_when_available():
    data = _screen_harness(
        """
        await call.startCall("dev_b", "Bob", "voice", "");
        await click("callScreen");
        let pipCalls = 0;
        node("callLocalScreen").requestPictureInPicture = async () => { pipCalls++; };
        await click("callPip");
        const popped = node("callStage").attrs["data-screen-pip"];
        await click("callPip");
        const docked = node("callStage").attrs["data-screen-pip"];
        console.log(JSON.stringify({ pipCalls, popped, docked: docked === undefined ? null : docked }));
        """
    )
    assert data["pipCalls"] == 1
    assert data["popped"] == "screen"
    assert data["docked"] is None


def test_screen_pip_falls_back_to_float_overlay():
    data = _screen_harness(
        """
        await call.startCall("dev_b", "Bob", "voice", "");
        await click("callScreen");
        await click("callPip");
        console.log(JSON.stringify({
          floatHidden: node("callPipFloat").hidden,
          pipVideo: Boolean(node("callPipVideo").srcObject),
        }));
        """
    )
    assert data["floatHidden"] is False
    assert data["pipVideo"] is True


def test_outbound_call_asks_for_media_before_ringing_peer():
    data = _call_harness(
        """
        const order = [];
        const gum = mediaDevices.getUserMedia;
        mediaDevices.getUserMedia = async (c) => { order.push("media"); return gum(c); };
        const origFetch = globalThis.fetch;
        globalThis.fetch = async (url, opts) => { if (opts && opts.body) order.push(JSON.parse(opts.body).kind); return origFetch(url, opts); };
        await call.startCall("dev_b", "Bob", "voice", "");
        console.log(JSON.stringify(order));
        """
    )
    assert data[0] == "media"
    assert "invite" in data
    assert data.index("invite") > data.index("media")


def test_accept_with_denied_microphone_does_not_tell_caller_accepted():
    data = _call_harness(
        """
        call.apply({ meshID: "circle_x", selfID: "dev_self", incoming: { call_id: "call_9", peer_device_id: "dev_b", mode: "voice" } });
        mediaDevices.getUserMedia = async () => { const e = new Error("denied"); e.name = "NotAllowedError"; throw e; };
        await click("callAccept");
        console.log(JSON.stringify({
          kinds: posted.map((p) => p.kind),
          phase: node("callPanel").attrs["data-phase"],
          detail: node("callError").textContent,
        }));
        """
    )
    assert "accept" not in data["kinds"]
    assert data["phase"] == "error"
    assert data["detail"] == "Access to the microphone was blocked. Allow it in the browser and try again."


def test_camera_permission_failure_is_reported_during_call():
    data = _call_harness(
        """
        await call.startCall("dev_b", "Bob", "voice", "");
        mediaDevices.getUserMedia = async (c) => {
          if (c && c.video) { const e = new Error("denied"); e.name = "NotAllowedError"; throw e; }
          return { getTracks: () => [track], getAudioTracks: () => [track], getVideoTracks: () => [] };
        };
        await click("callCamera");
        console.log(JSON.stringify({ detail: node("callError").textContent, hidden: node("callError").hidden, pressed: node("callCamera").attrs["aria-pressed"] || "false" }));
        """
    )
    assert data == {
        "detail": "Access to the camera was blocked. Allow it in the browser and try again.",
        "hidden": False,
        "pressed": "false",
    }


def test_voice_call_remote_audio_keeps_voice_stage_visible():
    data = _call_harness(
        """
        let pcRef = null;
        const Orig = FakePC;
        const call2 = createMeshCall({ RTCPeerConnection: class extends Orig { constructor() { super(); pcRef = this; } }, mediaDevices });
        call2.apply({ meshID: "circle_x", selfID: "dev_self" });
        await call2.startCall("dev_b", "Bob", "voice", "");
        node("callVoiceOnly").hidden = false;
        const audioOnly = { getVideoTracks: () => [], addEventListener() {} };
        pcRef.ontrack({ streams: [audioOnly] });
        const voice = { remoteHidden: node("callRemoteVideo").hidden, voiceHidden: node("callVoiceOnly").hidden };
        const withVideo = { getVideoTracks: () => [{ kind: "video" }], addEventListener() {} };
        pcRef.ontrack({ streams: [withVideo] });
        console.log(JSON.stringify({ voice, video: { remoteHidden: node("callRemoteVideo").hidden, voiceHidden: node("callVoiceOnly").hidden } }));
        """
    )
    assert data["voice"] == {"remoteHidden": True, "voiceHidden": False}
    assert data["video"] == {"remoteHidden": False, "voiceHidden": True}


def test_setup_phase_before_invite_passes_saved_device_ids():
    data = _call_harness(
        """
        globalThis.__overdriveAutoSetup = false;
        localStorage.setItem("overdrive.call.devices", JSON.stringify({ micId: "mic-1", camId: "cam-1" }));
        mediaDevices.enumerateDevices = async () => [
          { kind: "audioinput", deviceId: "mic-1", label: "MacBook Microphone" },
          { kind: "videoinput", deviceId: "cam-1", label: "FaceTime HD Camera" },
        ];
        const seen = [];
        mediaDevices.getUserMedia = async (constraints) => {
          seen.push({ phase: node("callPanel").attrs["data-phase"], constraints, invites: posted.filter((p) => p.kind === "invite").length });
          const videoTrack = { kind: "video", readyState: "live", enabled: true, stop() {} };
          const tracks = constraints && constraints.video ? [track, videoTrack] : [track];
          return {
            getTracks: () => tracks,
            getAudioTracks: () => [track],
            getVideoTracks: () => (constraints && constraints.video ? [videoTrack] : []),
          };
        };
        const pending = call.startCall("dev_b", "Bob", "video", "");
        await new Promise((r) => setTimeout(r, 0));
        const during = { phase: node("callPanel").attrs["data-phase"], invites: posted.filter((p) => p.kind === "invite").length };
        await click("callSetupContinue");
        await pending;
        console.log(JSON.stringify({ during, seen, invites: posted.filter((p) => p.kind === "invite").length }));
        """
    )
    assert data["during"] == {"phase": "setup", "invites": 0}
    assert data["invites"] == 1
    matched = [
        item["constraints"]
        for item in data["seen"]
        if item["constraints"].get("audio", {}).get("deviceId", {}).get("exact") == "mic-1"
        and item["constraints"].get("video", {}).get("deviceId", {}).get("exact") == "cam-1"
        and item["constraints"]["audio"].get("echoCancellation") is True
        and item["constraints"]["audio"].get("noiseSuppression") is True
        and item["constraints"]["audio"].get("autoGainControl") is True
    ]
    assert matched


def test_group_mesh_offers_only_from_lower_device_id():
    data = _call_harness(
        """
        await poll({ seq: 0, sessions: [], events: [] });
        call.apply({ meshID: "circle_x", selfID: "dev_m" });
        await call.startGroupCall(["dev_a", "dev_z"], "Trio", "voice", "group:grp_1");
        const invite = posted.find((p) => p.kind === "invite");
        const offers = posted.filter((p) => p.kind === "offer").map((p) => p.target_device_id);
        await poll({ seq: 1, sessions: [], events: [{
          seq: 1, call_id: "call_1", kind: "offer", from_device_id: "dev_a",
          payload: { type: "offer", sdp: "from-a" },
        }] });
        console.log(JSON.stringify({
          host: invite && invite.payload.host_device_id,
          participants: invite && invite.payload.participants,
          offers,
          offersToLower: posted.filter((p) => p.kind === "offer" && p.target_device_id === "dev_a").length,
          answersToLower: posted.filter((p) => p.kind === "answer" && p.target_device_id === "dev_a").length,
        }));
        """
    )
    assert data["host"] == "dev_m"
    assert data["participants"] == ["dev_m", "dev_a", "dev_z"]
    assert data["offers"] == ["dev_z"]
    assert data["offersToLower"] == 0
    assert data["answersToLower"] == 1


def test_camera_on_request_waits_for_confirm_before_get_user_media():
    data = _call_harness(
        """
        let gum = 0;
        mediaDevices.getUserMedia = async () => { gum += 1; return { getTracks: () => [track], getAudioTracks: () => [track], getVideoTracks: () => [{ kind: "video", readyState: "live", enabled: true, stop() {} }] }; };
        await poll({ seq: 0, sessions: [], events: [] });
        await poll({ seq: 1, sessions: [], events: [{
          seq: 1, call_id: "call_9", kind: "invite", from_device_id: "dev_host",
          payload: { mode: "video", host_device_id: "dev_host", participants: ["dev_host", "dev_self"] },
        }] });
        const before = gum;
        await poll({ seq: 2, sessions: [], events: [{
          seq: 2, call_id: "call_9", kind: "mod", from_device_id: "dev_host",
          payload: { action: "camera-on-request", target_device_id: "dev_self" },
        }] });
        const mid = { gum, hidden: node("callModBanner").hidden, text: node("callModText").textContent };
        await click("callModConfirm");
        console.log(JSON.stringify({ before, mid, after: gum }));
        """
    )
    assert data["before"] == 0
    assert data["mid"]["gum"] == 0
    assert data["mid"]["hidden"] is False
    assert "camera" in data["mid"]["text"].lower()
    assert data["after"] >= 1


def test_focus_toggle_and_screen_tile_stay_separate_from_camera():
    data = _call_harness(
        """
        const added = [];
        class TrackPC extends FakePC {
          addTrack(track) { added.push(track); }
          getSenders() { return added.map((t) => ({ track: t })); }
        }
        const camTrack = { kind: "video", readyState: "live", enabled: true, contentHint: "", stop() {} };
        const screenTrack = { kind: "video", readyState: "live", enabled: true, contentHint: "", stop() { this.stopped = true; } };
        const display = { getVideoTracks: () => [screenTrack], getTracks: () => [screenTrack] };
        mediaDevices.getUserMedia = async (constraints) => {
          if (constraints && constraints.video) {
            return { getTracks: () => [track, camTrack], getAudioTracks: () => [track], getVideoTracks: () => [camTrack] };
          }
          return { getTracks: () => [track], getAudioTracks: () => [track], getVideoTracks: () => [] };
        };
        mediaDevices.getDisplayMedia = async () => display;
        const call2 = createMeshCall({ RTCPeerConnection: TrackPC, mediaDevices });
        call2.apply({ meshID: "circle_x", selfID: "dev_self" });
        node("meshLive").setAttribute("data-focus", "chat");
        await call2.startCall("dev_b", "Bob", "video", "");
        await click("callScreen");
        const autoStage = node("meshLive").getAttribute("data-focus");
        await click("callFocus");
        const chat = node("meshLive").getAttribute("data-focus");
        await click("callFocus");
        const stage = node("meshLive").getAttribute("data-focus");
        console.log(JSON.stringify({
          autoStage, stage, chat,
          cameraHidden: node("callLocalVideo").hidden,
          screenHidden: node("callLocalScreen").hidden,
          separate: node("callLocalVideo").srcObject !== node("callLocalScreen").srcObject,
          screenIsDisplay: node("callLocalScreen").srcObject === display,
          hint: screenTrack.contentHint,
          addedScreen: added.includes(screenTrack),
          addedCamera: added.includes(camTrack),
        }));
        """
    )
    assert data["autoStage"] == "stage"
    assert data["stage"] == "stage"
    assert data["chat"] == "chat"
    assert data["cameraHidden"] is False
    assert data["screenHidden"] is False
    assert data["separate"] is True
    assert data["screenIsDisplay"] is True
    assert data["hint"] == "detail"
    assert data["addedScreen"] is True
    assert data["addedCamera"] is True


def test_invite_during_active_call_answers_busy():
    data = _call_harness(
        """
        await poll({ seq: 0, sessions: [], events: [] });
        await call.startCall("dev_b", "Bob", "voice", "");
        await poll({ seq: 1, sessions: [], events: [{ seq: 1, call_id: "call_2", kind: "invite", from_device_id: "dev_c", payload: { mode: "voice" } }] });
        await new Promise((r) => setTimeout(r, 0));
        const busy = posted.find((p) => p.kind === "busy");
        console.log(JSON.stringify({ status: node("callStatus").textContent, busy: busy ? [busy.call_id, busy.target_device_id] : null }));
        """
    )
    assert data["status"] == "Calling…"
    assert data["busy"] == ["call_2", "dev_c"]


CHAT_HARNESS = """
function fakeEl(id) {
  const store = { id, value: "", hidden: false, textContent: "", style: {}, dataset: {}, children: [], attrs: {},
    scrollTop: 0, scrollHeight: 0, clientHeight: 0, childElementCount: 0 };
  const classes = new Set();
  store.classList = { add: (c) => classes.add(c), remove: (c) => classes.delete(c), toggle: (c, on) => (on ? classes.add(c) : classes.delete(c)), contains: (c) => classes.has(c) };
  return new Proxy(store, {
    get(t, k) {
      if (k in t) return t[k];
      if (k === "setAttribute") return (a, v) => { t.attrs[a] = v; };
      if (k === "getAttribute") return (a) => t.attrs[a];
      if (k === "replaceChildren") return (...c) => { t.children = c; t.childElementCount = c.length; };
      if (k === "appendChild" || k === "append") return (...c) => { t.children.push(...c); t.childElementCount = t.children.length; return c[0]; };
      if (k === "querySelector") return () => null;
      if (k === "querySelectorAll") return () => [];
      if (k === "closest") return () => null;
      return () => {};
    },
    set(t, k, v) { t[k] = v; return true; },
  });
}
const nodes = new Map();
const byId = (id) => { if (!nodes.has(id)) nodes.set(id, fakeEl(id)); return nodes.get(id); };
globalThis.document = { getElementById: byId, querySelector: () => null, createElement: (tag) => fakeEl(tag),
  createTextNode: (t) => ({ text: t }), createDocumentFragment: () => fakeEl("frag"), addEventListener() {}, body: fakeEl("body") };
globalThis.window = globalThis;
globalThis.localStorage = { getItem: () => null, setItem() {} };
const fetchLog = [];
const pending = [];
globalThis.fetch = (url) => new Promise((resolve) => {
  fetchLog.push(String(url));
  pending.push({ url: String(url), resolve: (messages) => resolve({ ok: true, json: async () => ({ messages }) }) });
});
const el = (tag, cls, text) => { const n = fakeEl(tag); if (text !== undefined) n.textContent = text; return n; };
const chat = createMeshChat({ el, roster: () => [], computerLabel: (m) => m.device_id });
const convs = [
  { conversation_id: "direct:dev_a:dev_self", kind: "direct", last_timestamp: "2026-10-14T20:00:00Z" },
  { conversation_id: "direct:dev_b:dev_self", kind: "direct", last_timestamp: "2026-10-14T19:00:00Z" },
];
const ctx = (list) => ({ meshID: "circle_x", selfID: "dev_self", circle: { members: [] }, conversations: list, data: {} });
const flush = () => new Promise((r) => setTimeout(r, 0));
async function settle(messagesFor) {
  for (let i = 0; i < 6; i += 1) {
    await flush();
    while (pending.length) {
      const req = pending.shift();
      if (req.url.startsWith("/api/chat/groups")) req.resolve([]);
      else req.resolve(messagesFor(req.url));
    }
  }
}
"""


def _chat_harness(body: str) -> dict:
    out = _node_eval(
        f"""
        import {{ createMeshChat }} from {json.dumps(str(CHAT_MJS))};
        {CHAT_HARNESS}
        {body}
        """
    )
    return json.loads(out)


def test_unpinned_conversation_does_not_switch_under_a_typed_draft():
    data = _chat_harness(
        """
        let p = chat.apply(ctx(convs));
        await settle(() => []);
        await p;
        byId("messageText").value = "half-written reply to A";
        const newer = [{ ...convs[1], last_timestamp: "2026-10-14T21:00:00Z" }, convs[0]];
        p = chat.apply(ctx(newer));
        await settle(() => []);
        await p;
        console.log(JSON.stringify({ target: chat.outboundTarget(), text: byId("messageText").value }));
        """
    )
    assert data["target"] == {"conversation_id": "direct:dev_a:dev_self"}
    assert data["text"] == "half-written reply to A"


def test_stale_thread_response_does_not_overwrite_new_conversation():
    data = _chat_harness(
        """
        let p = chat.apply(ctx(convs));
        await flush(); await flush();
        const groups = pending.shift(); groups.resolve([]);
        await flush(); await flush();
        const slowA = pending.shift();
        chat.openConversationById("direct:dev_b:dev_self");
        await flush();
        const fastB = pending.shift();
        fastB.resolve([{ messageId: "m_b", senderDeviceId: "dev_b", type: "text", payload: { text: "from B" }, timestamp: "2026-10-14T19:00:00Z" }]);
        await flush(); await flush();
        slowA.resolve([{ messageId: "m_a", senderDeviceId: "dev_a", type: "text", payload: { text: "from A" }, timestamp: "2026-10-14T20:00:00Z" }]);
        await p; await flush(); await flush();
        const texts = [];
        const walk = (n) => { if (!n) return; if (typeof n.text === "string") texts.push(n.text); (n.children || []).forEach(walk); };
        walk(byId("threadPane"));
        console.log(JSON.stringify({ urls: [slowA.url.includes("dev_a"), fastB.url.includes("dev_b")], texts, mine: chat.outboundTarget() }));
        """
    )
    assert data["urls"] == [True, True]
    assert data["mine"] == {"conversation_id": "direct:dev_b:dev_self"}
    assert data["texts"] == ["from B"]


EMOJIS_JSON = ROOT / "runtime" / "experience" / "dashboard" / "emojis.json"


def test_emoji_catalog_has_only_color_presentation_glyphs():
    catalog = json.loads(EMOJIS_JSON.read_text(encoding="utf-8"))
    glyphs = {row[0] for rows in catalog["emojis"].values() for row in rows}
    # Plain text symbols with no color emoji glyph render as black outlines.
    for text_only in ("☇", "♁", "★", "♔", "☐"):
        assert text_only not in glyphs, text_only
    # Emoji with default text presentation must carry U+FE0F.
    for bare, qualified in (("☀", "☀️"), ("♀", "♀️"), ("⚡", "⚡")):
        assert qualified in glyphs, qualified
        if bare != qualified:
            assert bare not in glyphs, bare
    assert catalog["icons"]["symbols"] == "❤️"


TERMINAL_MJS = ROOT / "runtime" / "experience" / "dashboard" / "terminal.mjs"


def test_terminal_session_api_routes_remote_hosts_through_peer_proxy():
    out = _node_eval(
        f"""
        import {{ terminalSessionApi }} from {json.dumps(str(TERMINAL_MJS))};
        console.log(JSON.stringify([
          terminalSessionApi("ts 1", "", "dev_me"),
          terminalSessionApi("ts1", "dev_me", "dev_me"),
          terminalSessionApi("ts1", "dev_host", "dev_me"),
          terminalSessionApi("ts1", "dev_host", ""),
        ]));
        """
    )
    assert json.loads(out) == [
        "/api/v1/terminal/sessions/ts%201",
        "/api/v1/terminal/sessions/ts1",
        "/api/v1/terminal/remote/dev_host/sessions/ts1",
        "/api/v1/terminal/remote/dev_host/sessions/ts1",
    ]


def test_terminal_attach_failure_explains_missing_share():
    out = _node_eval(
        f"""
        import {{ terminalAttachFailureText }} from {json.dumps(str(TERMINAL_MJS))};
        console.log(JSON.stringify([403, 500, 0].map(terminalAttachFailureText)));
        """
    )
    assert json.loads(out) == [
        "Ask the owner to share this terminal with you.",
        "Could not attach to session.",
        "Could not attach to session.",
    ]
    text = TERMINAL_MJS.read_text(encoding="utf-8")
    assert "this.statusEl.textContent = terminalAttachFailureText(attachRes.status);" in text


def test_terminal_tab_uses_host_aware_session_api_everywhere():
    text = TERMINAL_MJS.read_text(encoding="utf-8")
    body = text[text.index("  class TermTab {"):text.index("export {")]
    # Purge and create are local-only; every per-session call must follow the tab's host.
    per_session = body.count('"/api/v1/terminal/sessions/" + encodeURIComponent(')
    assert per_session == 1, per_session
    assert "host: payload.host" in body
    assert "remote/" in text


def test_recording_level_is_rms_with_voice_floor():
    out = _node_eval(
        f"""
        import {{ recordingLevel, VOICE_BAR_FLOOR }} from {json.dumps(str(CHAT_MJS))};
        const silence = new Uint8Array(64).fill(128);
        const loud = Uint8Array.from({{ length: 64 }}, (_, i) => (i % 2 ? 255 : 0));
        const soft = Uint8Array.from({{ length: 64 }}, (_, i) => (i % 2 ? 140 : 116));
        console.log(JSON.stringify([recordingLevel(silence), recordingLevel(loud), recordingLevel(soft), VOICE_BAR_FLOOR]));
        """
    )
    silence, loud, soft, floor = json.loads(out)
    assert silence == floor
    assert loud == 1
    assert floor < soft < loud


def test_recording_composer_matches_voice_player_style():
    html = (ROOT / "runtime/experience/dashboard/chat.html").read_text(encoding="utf-8")
    js = CHAT_MJS.read_text(encoding="utf-8")
    css = (ROOT / "runtime/experience/dashboard/chat.css").read_text(encoding="utf-8")
    assert 'id="composerRec"' in html
    assert 'id="composerRecTime"' in html
    assert "getByteFrequencyData" not in js
    assert "getByteTimeDomainData" in js
    assert "rgba(255, 107, 107" not in js
    assert ".composer-rec-dot" in css


def test_voice_recording_requests_native_noise_suppression():
    text = CHAT_MJS.read_text(encoding="utf-8")
    rec = text[text.index("async function startAudioRecording() {") : text.index("  function bindComposerDragDrop() {")]
    assert "VOICE_CAPTURE_AUDIO_CONSTRAINTS" in rec
    assert "echoCancellation: true" in text
    assert "noiseSuppression: true" in text
    assert "autoGainControl: true" in text
    assert "voiceIsolation: true" in text
    assert "getUserMedia({ audio: true })" not in rec


def test_mesh_joined_resize_clamps_chat_and_stage_splits():
    out = _node_eval(
        f"""
        import {{
          meshSplitPaneWidth,
          MESH_CHAT_MIN_WIDTH,
          MESH_CHAT_SPLIT_TAIL,
          MESH_STAGE_MIN_WIDTH,
          MESH_STAGE_MAX_WIDTH,
        }} from {json.dumps(str(CHAT_MJS))};
        const w = 900;
        console.log(JSON.stringify({{
          chatMin: meshSplitPaneWidth("chat", 0, 0, w),
          chatMid: meshSplitPaneWidth("chat", 500, 0, w),
          chatMax: meshSplitPaneWidth("chat", 2000, 0, w),
          stageMin: meshSplitPaneWidth("stage", 0, 0, w),
          stageMid: meshSplitPaneWidth("stage", 500, 0, w),
          stageMax: meshSplitPaneWidth("stage", 2000, 0, w),
          constants: [MESH_CHAT_MIN_WIDTH, MESH_CHAT_SPLIT_TAIL, MESH_STAGE_MIN_WIDTH, MESH_STAGE_MAX_WIDTH],
        }}));
        """
    )
    data = json.loads(out)
    assert data["constants"] == [280, 160, 320, 960]
    assert data["chatMin"] == 280
    assert data["chatMid"] == 500
    assert data["chatMax"] == 740
    assert data["stageMin"] == 320
    assert data["stageMid"] == 500
    assert data["stageMax"] == 960


def test_mesh_joined_resize_handle_and_drag_wiring():
    html = (ROOT / "runtime/experience/dashboard/chat.html").read_text(encoding="utf-8")
    css = (ROOT / "runtime/experience/dashboard/chat.css").read_text(encoding="utf-8")
    js = CHAT_MJS.read_text(encoding="utf-8")
    assert 'id="msgResize"' in html
    idx = html.index('id="msgResize"')
    resize_tag = html[max(0, idx - 80) : html.index(">", idx) + 1]
    assert 'type="button"' in resize_tag
    assert 'role="separator"' not in resize_tag
    assert ".mesh-live.call-joined .chat-resize" in css
    assert "z-index: 25" in css.split(".mesh-live.call-joined .chat-resize", 1)[1].split("}", 1)[0]
    assert "width: 10px" in css
    assert "mesh-split-dragging" in css
    assert "mesh-split-dragging" in js
    assert "meshSplitPaneWidth" in js
    assert 'live.style.setProperty("--mesh-chat"' in js
    assert 'live.style.setProperty("--mesh-stage"' in js
    assert "setPointerCapture" in js


def test_call_error_banner_uses_apple_notice_and_alert_variants():
    html = (ROOT / "runtime/experience/dashboard/chat.html").read_text(encoding="utf-8")
    call = CALL_MJS.read_text(encoding="utf-8")
    css = (ROOT / "runtime/experience/dashboard/chat.css").read_text(encoding="utf-8")
    assert 'id="callErrorText"' in html
    assert "call-error-icon" in html
    assert "call-error--notice" in call
    assert "call-error--alert" in call
    assert ".call-error--notice" in css
    call_error_block = css.split(".call-error {", 1)[1].split(".call-error-icon", 1)[0]
    assert "backdrop-filter" in call_error_block
    assert "#ffd9d6" not in css


def test_call_setup_device_select_uses_custom_listbox():
    html = (ROOT / "runtime/experience/dashboard/chat.html").read_text(encoding="utf-8")
    call = CALL_MJS.read_text(encoding="utf-8")
    css = (ROOT / "runtime/experience/dashboard/chat.css").read_text(encoding="utf-8")
    assert 'id="callMicSelectPanel"' in html
    assert 'class="call-device-select__trigger"' in html
    assert 'id="callCamSelectPanel"' in html
    assert 'class="call-device-select__native"' in html
    assert "CALL_DEVICE_SELECT_IDS" in call
    assert "renderCallDeviceSelectPanel" in call
    assert ".call-device-select__panel" in css
    device_select_panel_css = css.split(".call-device-select__panel", 1)[1]
    assert ".call-device-select__option" in device_select_panel_css
    assert 'createElement("button")' in call
    assert "call-device-select__option" in call


def test_composer_image_video_attachment_thumbnails():
    chat = CHAT_MJS.read_text(encoding="utf-8")
    css = (ROOT / "runtime/experience/dashboard/chat.css").read_text(encoding="utf-8")
    block = chat[chat.index("function renderPendingFile()"):chat.index("const readFileAsBase64")]
    assert "composer-chip--media" in block
    assert "composer-chip-thumb" in block
    assert "URL.createObjectURL(file)" in chat
    assert "revokePendingPreview" in chat
    assert ".composer-chip-thumb" in css
    assert ".composer-chip--media" in css


def test_nearby_forget_persists_hidden_peers_in_local_storage():
    app = (ROOT / "runtime/experience/dashboard/app.mjs").read_text(encoding="utf-8")
    machines = MACHINES_MJS.read_text(encoding="utf-8")
    css = (ROOT / "runtime/experience/dashboard/dashboard.css").read_text(encoding="utf-8")
    assert 'const NEARBY_HIDDEN_KEY = "overdriveNearbyHidden"' in app
    assert "const nearbyPeerKey = (peer) =>" in app
    assert "function forgetNearbyPeer(peer)" in app
    assert "function forgetCircleMember(peer)" in app
    assert "function rowForgetButton(" in app
    assert "function dedupeDiscoveredPeers(peers)" in machines
    assert "nearby-clear-hidden" in app
    assert "/api/discovery/forget" in app
    assert ".row-forget" in css
    assert ".machine-row-shell" in css
    assert ".nearby-clear-hidden" in css


def test_machines_dedupe_and_forget_filters():
    out = _node_eval(
        f"""
        import {{
          dedupeDiscoveredPeers,
          filterForgottenNearby,
          canForgetCircleMember,
          discoveryStableKey,
        }} from {json.dumps(str(MACHINES_MJS))};
        const peers = [
          {{ device_id: "dev_a", display_name: "Ada", last_seen: "2020-01-01T00:00:00Z" }},
          {{ device_id: "dev_a", display_name: "Ada", last_seen: "2026-01-01T00:00:00Z" }},
          {{ display_name: "Bea", os: "linux", last_seen: "2026-01-02T00:00:00Z" }},
        ];
        const deduped = dedupeDiscoveredPeers(peers);
        const hidden = new Set(["id:dev_a"]);
        const visible = filterForgottenNearby(deduped, hidden);
        console.log(JSON.stringify({{
          dedupedCount: deduped.length,
          newest: deduped.find((p) => p.device_id === "dev_a")?.last_seen,
          visibleCount: visible.length,
          key: discoveryStableKey({{ device_id: "dev_x" }}),
          canHideOffline: canForgetCircleMember({{ self: false, revoked: false }}, () => false),
          cantHideSelf: canForgetCircleMember({{ self: true, revoked: false }}, () => false),
        }}));
        """
    )
    data = json.loads(out)
    assert data["dedupedCount"] == 2
    assert data["newest"] == "2026-01-01T00:00:00Z"
    assert data["visibleCount"] == 1
    assert data["key"] == "id:dev_x"
    assert data["canHideOffline"] is True
    assert data["cantHideSelf"] is False


def test_boot_reads_roster_and_alive_without_temporal_dead_zone():
    shared = (ROOT / "runtime/experience/dashboard/dashboard-shared.mjs").read_text(encoding="utf-8")
    app = (ROOT / "runtime/experience/dashboard/app.mjs").read_text(encoding="utf-8")
    go = (ROOT / "runtime/experience/share_dashboard.go").read_text(encoding="utf-8")
    assert "dashboardSharedJS" in go
    assert app.index("const meshChat = createMeshChat") > 0
    assert "function roster(" not in app
    assert "function alive(" not in app
    assert shared.index("function roster(") != -1
    assert shared.index("function alive(") != -1
    assert shared.index("function computerLabel(") != -1


def test_chat_boot_survives_widget_restore_and_toast_closes_before_dock():
    shell = (ROOT / "runtime/experience/dashboard/shell.html").read_text(encoding="utf-8")
    toast = shell.index('<output class="toast" id="toast"')
    close = shell.index("</output>", toast)
    dock = shell.index('id="dock"')
    assert toast < close < dock
    assert "_unused" not in CHAT_MJS.read_text(encoding="utf-8")
    out = _node_eval(
        f"""
        import {{ createMeshChat }} from {json.dumps(str(CHAT_MJS))};
        const style = {{}};
        const classList = {{
          _s: new Set(),
          add(name) {{ this._s.add(name); }},
          remove(name) {{ this._s.delete(name); }},
          contains(name) {{ return this._s.has(name); }},
          toggle(name, on) {{
            if (on) this.add(name); else this.remove(name);
            return this.contains(name);
          }},
        }};
        const panel = {{
          style, classList, hidden: true, offsetWidth: 320, offsetHeight: 480,
          addEventListener() {{}},
          getBoundingClientRect() {{ return {{ left: 8, top: 72, width: 320, height: 480 }}; }},
        }};
        const els = {{ messages: panel }};
        globalThis.document = {{
          getElementById(id) {{ return els[id] || null; }},
          addEventListener() {{}},
          querySelector() {{ return null; }},
        }};
        globalThis.localStorage = {{
          getItem() {{ return JSON.stringify({{ width: "360px", height: "520px", left: 12, top: 80 }}); }},
          setItem() {{}},
        }};
        createMeshChat({{
          el() {{ return {{}}; }},
          roster() {{ return []; }},
          computerLabel() {{ return ""; }},
          peerAlive() {{ return false; }},
          getData() {{ return {{}}; }},
        }});
        console.log(panel.style.width + " " + panel.style.height);
        """
    )
    assert out == "360px 520px"


def test_mesh_call_signaling_node_runner():
    node = shutil.which("node")
    if not node:
        pytest.skip("Node.js not available")
    proc = subprocess.run(
        [node, "--test", str(ROOT / "tests" / "mesh_call_signaling.test.mjs")],
        cwd=ROOT,
        capture_output=True,
        text=True,
        check=False,
    )
    assert proc.returncode == 0, proc.stderr or proc.stdout


def test_dashboard_async_fire_and_forget_uses_catch():
    """Sonar javascript:S9383 — async calls in handlers must not float."""
    app = (ROOT / "runtime/experience/dashboard/app.mjs").read_text(encoding="utf-8")
    chat = CHAT_MJS.read_text(encoding="utf-8")
    call = CALL_MJS.read_text(encoding="utf-8")
    term = TERMINAL_MJS.read_text(encoding="utf-8")
    assert "loadContexts().catch(() => {})" in app
    assert "setInterval(() => { refresh().catch(() => {})" in app
    assert "postMesh(\"/api/mesh/pause\").catch(() => {})" in app
    assert "copyConnection().catch(() => {})" in app
    assert "loadThread(openConversation, true).catch(() => {})" in chat
    assert "sendMessage(ev).catch(() => {})" in chat
    assert "attachFile(file).catch(() => {})" in chat
    assert "endCall(true).catch(() => {})" in call
    assert "addEventListener(\"click\", async () =>" not in term
    assert "createSession().catch(() => {})" in term
    assert "refreshSessionList().catch(() => {})" in term


def test_terminal_owner_grant_control_badge_opens_inspector():
    html = (ROOT / "runtime" / "experience" / "dashboard" / "shell.html").read_text(encoding="utf-8")
    js = (ROOT / "runtime" / "experience" / "dashboard" / "terminal.mjs").read_text(encoding="utf-8")
    css = (ROOT / "runtime" / "experience" / "dashboard" / "dashboard.css").read_text(encoding="utf-8")
    term_badge_prefix = html.split("termBadgeControl")[0][-80:]
    assert 'id="termBadgeControl"' in html
    assert "<button" in term_badge_prefix
    assert "Grant control" in js
    assert "pending_control_requesters" in js
    assert "term-badge--grant" in css
    assert "term-inspector-block--attention" in css
    assert "term-inspector-group" in css
    assert "--term-insp-accent" in css

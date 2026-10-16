/**
 * Two-peer offer / answer / ICE exchange against the dashboard call contract.
 * Run: node --test tests/mesh_call_signaling.test.mjs
 */
import assert from "node:assert/strict";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import test from "node:test";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const callMjs = join(root, "runtime/experience/dashboard/call.mjs");
const { createMeshCall } = await import(pathToFileURL(callMjs).href);

function makeDom() {
  const nodes = new Map();
  const ids = [
    "callPanel", "callStatus", "callError", "callErrorText", "callRetry", "callSetup", "callCamField",
    "callPeerName", "callAccept", "callDecline", "callMute", "callCamera", "callScreen", "callChatToggle",
    "callEnd", "callFocus", "callHostBar", "callModBanner", "callRemoteVideo", "callLocalVideo",
    "callLocalScreen", "callRemoteScreen", "callRemoteGrid", "callVoiceOnly", "callVoiceLabel",
    "callStage", "callPip", "callPipFloat", "callPipVideo", "callNatHint", "callSetupContinue",
    "callModConfirm", "callModDismiss", "callModText", "callMicSelect", "callCamSelect",
    "meshLive", "callControls", "callTimer", "callClose", "callHostMute", "callHostCamera",
    "callHostUnmute", "callHostCameraOn", "callLinkHint", "callTrust",
  ];
  for (const id of ids) {
    const el = {
      id,
      _hidden: id === "callPanel",
      attrs: id === "callPanel" ? { "data-phase": "idle", "aria-hidden": "true" } : {},
      classList: {
        _c: new Set(),
        add(c) { this._c.add(c); },
        remove(c) { this._c.delete(c); },
        toggle(c, on) { (on ? this.add(c) : this.remove(c)); },
        contains(c) { return this._c.has(c); },
      },
      setAttribute(k, v) { this.attrs[k] = v; },
      getAttribute(k) { return this.attrs[k]; },
      removeAttribute(k) { delete this.attrs[k]; },
      listeners: {},
      addEventListener(type, fn) { this.listeners[type] = fn; },
      replaceChildren() {},
      appendChild() {},
      querySelector() { return null; },
      selectedOptions: [{ textContent: "" }],
      dataset: {},
      style: {},
      textContent: "",
    };
    Object.defineProperty(el, "hidden", {
      get() { return el._hidden; },
      set(v) {
        el._hidden = v;
        if (id === "callPanel") el.attrs["aria-hidden"] = v ? "true" : "false";
      },
    });
    nodes.set(id, el);
  }
  return nodes;
}

function installGlobals(nodes) {
  globalThis.document = {
    getElementById: (id) => nodes.get(id) || null,
    createElement: () => ({ appendChild() {}, style: {} }),
    addEventListener() {},
    exitPictureInPicture: async () => {},
  };
  globalThis.location = { hash: "" };
  globalThis.localStorage = {
    _s: new Map(),
    getItem(k) { return this._s.has(k) ? this._s.get(k) : null; },
    setItem(k, v) { this._s.set(k, String(v)); },
    removeItem(k) { this._s.delete(k); },
  };
  globalThis.setInterval = (fn) => {
    globalThis.__pollTick = fn;
    return 1;
  };
  globalThis.clearInterval = () => {};
  globalThis.__overdriveAutoSetup = true;
}

class MeshPeerConnection {
  constructor(selfID, remotePeerID) {
    this.selfID = selfID;
    this.remotePeerID = remotePeerID;
    this.localDescription = null;
    this.remoteDescription = null;
    this.iceCandidates = [];
    this.pendingRemoteIce = [];
    this.closed = false;
    this.onicecandidate = null;
    this.ontrack = null;
    this.onconnectionstatechange = null;
    this._tracks = [];
  }

  getSenders() {
    return this._tracks.map((track) => ({ track }));
  }

  addTrack(track) {
    this._tracks.push(track);
  }

  addTransceiver() {}

  async createOffer() {
    return { type: "offer", sdp: `offer-${this.selfID}-to-${this.remotePeerID}` };
  }

  async createAnswer() {
    return { type: "answer", sdp: `answer-${this.selfID}-to-${this.remotePeerID}` };
  }

  async setLocalDescription(desc) {
    this.localDescription = desc;
  }

  async setRemoteDescription(desc) {
    this.remoteDescription = desc;
    for (const cand of this.pendingRemoteIce) {
      await this.addIceCandidate(cand);
    }
    this.pendingRemoteIce = [];
  }

  async addIceCandidate(candidate) {
    if (!candidate) return;
    if (!this.remoteDescription) {
      this.pendingRemoteIce.push(candidate);
      return;
    }
    this.iceCandidates.push(candidate);
  }

  close() {
    this.closed = true;
  }
}

function createMeshHub(meshID) {
  const events = [];
  let seq = 0;
  let callID = "call_mesh_test";
  const sinceByPeer = new Map();
  const icePosts = [];

  function record(fromDevice, body) {
    if (body.kind === "invite") {
      callID = body.call_id || callID;
      const targets = body.target_device_ids || (body.target_device_id ? [body.target_device_id] : []);
      for (const _target of targets) {
        seq += 1;
        events.push({
          seq,
          call_id: callID,
          kind: "invite",
          from_device_id: fromDevice,
          payload: body.payload || {},
        });
      }
      return;
    }
    if (body.kind === "ice") {
      const cand = body.payload?.candidate;
      if (!cand || cand.candidate === "") return;
      icePosts.push({ from: fromDevice, target: body.target_device_id });
    }
    seq += 1;
    events.push({
      seq,
      call_id: callID,
      kind: body.kind,
      from_device_id: fromDevice,
      payload: body.payload || {},
    });
  }

  async function fetchAs(deviceID, url, opts) {
    const path = String(url);
    if (path.startsWith("/api/call/events")) {
      const since = sinceByPeer.get(deviceID) || 0;
      const fresh = events.filter((ev) => ev.seq > since);
      const nextSeq = events.length ? events[events.length - 1].seq : since;
      sinceByPeer.set(deviceID, nextSeq);
      return {
        ok: true,
        json: async () => ({
          seq: nextSeq,
          sessions: [{ call_id: callID, updated_at: new Date().toISOString() }],
          events: fresh,
        }),
      };
    }
    if (path === "/api/call/signal" && opts?.method === "POST") {
      const body = JSON.parse(opts.body);
      record(deviceID, body);
      return { ok: true, json: async () => ({ call_id: callID }) };
    }
    return { ok: true, json: async () => ({}) };
  }

  return {
    meshID,
    events,
    icePosts,
    fetchAs,
  };
}

const track = { kind: "audio", readyState: "live", stop() {}, enabled: true };
const mediaDevices = {
  async getUserMedia() {
    return {
      getTracks: () => [track],
      getAudioTracks: () => [track],
      getVideoTracks: () => [],
    };
  },
};

async function pollPeer(pollFn) {
  globalThis.__meshFetchOwner = globalThis.__pollPeer;
  await pollFn();
  await new Promise((r) => setTimeout(r, 0));
}

test("two createMeshCall peers exchange offer, answer, and ICE", async () => {
  const peerA = "dev_alpha";
  const peerB = "dev_beta";
  const hub = createMeshHub("circle_signal_test");

  const nodesA = makeDom();
  installGlobals(nodesA);
  const docA = globalThis.document;
  let pcA = null;
  let pollA = null;
  globalThis.fetch = async (url, opts) => hub.fetchAs(peerA, url, opts);
  const callA = createMeshCall({
    RTCPeerConnection: class extends MeshPeerConnection {
      constructor() {
        super(peerA, peerB);
        pcA = this;
      }
    },
    mediaDevices,
  });
  callA.apply({ meshID: hub.meshID, selfID: peerA });
  pollA = globalThis.__pollTick;
  globalThis.__pollPeer = peerA;

  const nodesB = makeDom();
  let pcB = null;
  let pollB = null;
  const callB = (() => {
    installGlobals(nodesB);
    globalThis.fetch = async (url, opts) => hub.fetchAs(peerB, url, opts);
    const meshCall = createMeshCall({
      RTCPeerConnection: class extends MeshPeerConnection {
        constructor() {
          super(peerB, peerA);
          pcB = this;
        }
      },
      mediaDevices,
    });
    meshCall.apply({ meshID: hub.meshID, selfID: peerB });
    pollB = globalThis.__pollTick;
    return meshCall;
  })();
  const docB = globalThis.document;

  function usePeer(peer, pollFn) {
    globalThis.document = peer === peerA ? docA : docB;
    globalThis.__pollPeer = peer;
    globalThis.fetch = async (url, opts) => hub.fetchAs(peer, url, opts);
    return pollFn;
  }

  await usePeer(peerA, async () => {
    await callA.startCall(peerB, "Beta", "voice", `direct:${peerA}:${peerB}`);
    await pollPeer(pollA);
  })();

  await usePeer(peerB, async () => {
    await pollPeer(pollB);
    await nodesB.get("callAccept").listeners.click?.();
    await pollPeer(pollB);
  })();

  await usePeer(peerA, async () => {
    await pollPeer(pollA);
  })();

  await usePeer(peerB, async () => {
    await pollPeer(pollB);
  })();

  assert.equal(pcA?.remoteDescription?.type, "answer");
  assert.equal(pcB?.remoteDescription?.type, "offer");
  assert.match(pcA.remoteDescription.sdp, /answer-dev_beta/);
  assert.match(pcB.remoteDescription.sdp, /offer-dev_alpha/);

  let icePosted = false;
  pcA.onicecandidate = (ev) => {
    if (!ev.candidate) {
      icePosted = true;
      return;
    }
    globalThis.fetch = async (url, opts) => hub.fetchAs(peerA, url, opts);
    hub.fetchAs(peerA, "/api/call/signal", {
      method: "POST",
      body: JSON.stringify({
        mesh_id: hub.meshID,
        target_device_id: peerB,
        call_id: "call_mesh_test",
        kind: "ice",
        payload: { candidate: ev.candidate },
      }),
    });
  };
  usePeer(peerA, () => {});
  pcA.onicecandidate({ candidate: { candidate: "candidate:1", sdpMid: "0", sdpMLineIndex: 0 } });
  await usePeer(peerB, async () => {
    await pollPeer(pollB);
  })();
  assert.ok(pcB.iceCandidates.length >= 1, "ICE from A reaches B");
  pcA.onicecandidate({ candidate: null });
  assert.equal(icePosted, true);
  assert.equal(hub.icePosts.filter((p) => p.from === peerA).length, 1);

  assert.equal(nodesA.get("callPanel").attrs["data-phase"], "active");
  assert.equal(nodesB.get("callPanel").attrs["data-phase"], "active");
});

test("lower device_id sends the first mesh offer in a group", async () => {
  const meshID = "circle_group_offer";
  const posted = [];
  const nodes = makeDom();
  installGlobals(nodes);
  globalThis.fetch = async (url, opts) => {
    if (String(url).startsWith("/api/call/events")) {
      return { ok: true, json: async () => ({ seq: 0, sessions: [], events: [] }) };
    }
    if (opts?.body) posted.push(JSON.parse(opts.body));
    return { ok: true, json: async () => ({ call_id: "call_g" }) };
  };

  class FakePC {
    constructor() { this.localDescription = null; }
    getSenders() { return []; }
    addTrack() {}
    addTransceiver() {}
    async createOffer() { return { sdp: "o", type: "offer" }; }
    async setLocalDescription(d) { this.localDescription = d; }
    async setRemoteDescription() {}
    close() {}
  }

  const call = createMeshCall({ RTCPeerConnection: FakePC, mediaDevices });
  call.apply({ meshID, selfID: "dev_m" });
  await call.startGroupCall(["dev_a", "dev_z"], "Trio", "voice", "group:g1");
  const offers = posted.filter((p) => p.kind === "offer").map((p) => p.target_device_id);
  assert.deepEqual(offers, ["dev_z"]);
});

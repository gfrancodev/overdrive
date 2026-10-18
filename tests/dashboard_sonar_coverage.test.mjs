/**
 * Targeted coverage for dashboard helpers introduced in Sonar new-code period.
 */
import assert from "node:assert/strict";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import test from "node:test";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const sharedMjs = join(root, "runtime/experience/dashboard/dashboard-shared.mjs");
const appMjs = join(root, "runtime/experience/dashboard/app.mjs");
const callMjs = join(root, "runtime/experience/dashboard/call.mjs");
const terminalMjs = join(root, "runtime/experience/dashboard/terminal.mjs");
const onboardingMjs = join(root, "runtime/experience/dashboard/onboarding.mjs");
const circleSettingsMjs = join(root, "runtime/experience/dashboard/circle_settings.mjs");

function fakeEl(id = "node") {
  return {
    id,
    hidden: false,
    textContent: "",
    clientHeight: 240,
    className: "",
    attrs: {},
    children: [],
    style: {},
    replaceChildren() {},
    appendChild() {},
    append() {},
    addEventListener() {},
    setAttribute(k, v) {
      this.attrs[k] = v;
    },
    getAttribute(k) {
      return this.attrs[k];
    },
  };
}

function installAppBootGlobals() {
  globalThis.OVERDRIVE_UI = { preset: "enterprise", title: "Test" };
  globalThis.setInterval = () => 0;
  globalThis.clearInterval = () => {};
  globalThis.localStorage = {
    _m: new Map(),
    getItem(k) {
      return this._m.has(k) ? this._m.get(k) : null;
    },
    setItem(k, v) {
      this._m.set(k, String(v));
    },
  };
  globalThis.fetch = async () => ({
    ok: true,
    json: async () => ({ circles: [{ name: "C", members: [] }], events: [], hostname: "host" }),
  });
  globalThis.document = {
    documentElement: { dataset: {}, style: { setProperty() {} } },
    getElementById: () => fakeEl(),
    createElement: (tag) => fakeEl(tag),
    querySelectorAll: () => [],
    addEventListener() {},
  };
}

test("app invite helpers", async () => {
  installAppBootGlobals();
  const {
    setInviteCopyStatus,
    circleInviteErrorMessage,
  } = await import(pathToFileURL(sharedMjs).href);

  const status = { textContent: "" };
  setInviteCopyStatus(status, true, "soon");
  assert.match(status.textContent, /Copied\. It expires soon/);
  setInviteCopyStatus(null, true, "x");

  assert.equal(circleInviteErrorMessage({ status: 400 }, { error: "nope" }), "nope");
  assert.match(
    circleInviteErrorMessage({ status: 403 }, {}),
    /127\.0\.0\.1/,
  );
  assert.equal(circleInviteErrorMessage({ status: 500 }, {}), "Could not create a connection.");
});

test("runSequential continues after a failing step", async () => {
  const { runSequential } = await import(pathToFileURL(callMjs).href);
  const seen = [];
  await runSequential([1, 2, 3], async (n) => {
    seen.push(n);
    if (n === 2) throw new Error("skip");
  });
  assert.deepEqual(seen, [1, 2, 3]);
});

test("openDefaultSessionsInOrder opens sessions sequentially", async () => {
  const { openDefaultSessionsInOrder } = await import(pathToFileURL(terminalMjs).href);
  const calls = [];
  await openDefaultSessionsInOrder(
    async (id, name, opts) => {
      calls.push({ id, name, focus: opts?.focus });
      if (id === "bad") throw new Error("broken");
    },
    [
      { id: "s1", name: "One" },
      { id: "bad", name: "Broken" },
      { id: "s2", name: "Two" },
    ],
  );
  assert.deepEqual(calls, [
    { id: "s1", name: "One", focus: false },
    { id: "bad", name: "Broken", focus: false },
    { id: "s2", name: "Two", focus: true },
  ]);
});

test("onboarding helpers read the Core's state", async () => {
  const ob = await import(pathToFileURL(onboardingMjs).href);
  assert.equal(ob.setupStepText({ id: "folder", done: false }), "Share a project folder");
  assert.equal(ob.setupStepText({ id: "custom", done: true }), "custom");
  assert.equal(ob.setupComplete({ next: "ready" }), true);
  assert.equal(ob.pickActiveCircle([{ id: "a" }, { id: "b" }], "b").id, "b");
  assert.equal(ob.pickActiveCircle(null, "b"), null);
  assert.equal(ob.formatPairingCode("123456"), "123 456");
  assert.equal(ob.formatPairingCode("12"), "12");
  for (const state of ["connecting", "comparing", "waiting", "joined", "rejected", "expired", "failed"]) {
    for (const role of ["member", "joiner"]) {
      assert.ok(ob.pairingPrompt({ state, role, hostname: "laptop" }).title);
    }
  }
  assert.ok(ob.pairingPrompt({ state: "comparing", confirmed: true }).title.startsWith("Waiting"));
  const sessions = [
    { id: "j", role: "joiner", state: "connecting" },
    { id: "m", role: "member", state: "comparing" },
  ];
  assert.equal(ob.pairingToShow(sessions, new Set()).id, "m");
  assert.equal(ob.pairingToShow(sessions, new Set(["m"])).id, "j");
  assert.equal(ob.pairingToShow([], new Set()), null);
});

test("circle settings helpers explain folders, members and removals", async () => {
  const cs = await import(pathToFileURL(circleSettingsMjs).href);
  const circle = {
    creator_device_id: "dev_a", is_creator: true, key_epoch: 1, key_rotated_at: "2026-10-18T11:00:00Z",
    previous_key_until: "2999-01-01T00:00:00Z",
    members: [
      { device_id: "dev_a", hostname: "studio", self: true, receives_keys: true },
      { device_id: "dev_b", invited_by: "dev_a", receives_keys: false },
      { device_id: "dev_c", invited_by: "dev_b", receives_keys: true },
    ],
  };
  assert.equal(cs.folderLabel("/srv/code/api/"), "api");
  assert.equal(cs.memberNotes(circle.members[1], circle), "Invited by studio · Needs an Overdrive update to get new keys");
  assert.equal(cs.canRemoveMember(circle.members[0], circle), false);
  assert.equal(cs.canRemoveMember(circle.members[1], circle), true);
  assert.match(cs.keyStatusText(circle), /^The key changed 1 time, last on .* keep working until /);
  assert.match(cs.keyStatusText({ key_epoch: 0 }), /first key/);
  assert.equal(cs.keyStatusText({ key_epoch: 3, key_rotated_at: "not a date" }), "The key changed 3 times, last on not a date.");
  const plan = { targets: ["dev_b"], invitees: ["dev_c"], cut_off: ["dev_b", "dev_c"] };
  const alone = cs.revokeImpact(plan, circle, false);
  assert.equal(alone.title, "Remove dev_b?");
  assert.ok(alone.lines.some((line) => line === "dev_b and dev_c run an Overdrive without key rotation and lose access until updated."));
  const cascade = cs.revokeImpact({ ...plan, cut_off: [] }, circle, true);
  assert.match(cascade.lines[0], /^dev_b and the 1 device it invited \(dev_c\) lose access now/);
  assert.equal(cs.revokeImpact({}, null, false).title, "Remove This device?");
});

/**
 * Targeted coverage for dashboard helpers introduced in Sonar new-code period.
 */
import assert from "node:assert/strict";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import test from "node:test";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const appMjs = join(root, "runtime/experience/dashboard/app.mjs");
const callMjs = join(root, "runtime/experience/dashboard/call.mjs");
const terminalMjs = join(root, "runtime/experience/dashboard/terminal.mjs");

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
  } = await import(pathToFileURL(appMjs).href);

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

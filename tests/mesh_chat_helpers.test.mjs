/**
 * Coverage-friendly exercises for exported chat.mjs helpers.
 * Run: node --test tests/mesh_chat_helpers.test.mjs
 */
import assert from "node:assert/strict";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import test from "node:test";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const chatMjs = join(root, "runtime/experience/dashboard/chat.mjs");
const {
  fileKind,
  formatVoiceTime,
  meshSplitPaneWidth,
  MESH_CHAT_MIN_WIDTH,
  MESH_STAGE_MIN_WIDTH,
  MESH_STAGE_MAX_WIDTH,
  recordingLevel,
  sortConversationsByActivity,
  waveformPeaks,
} = await import(pathToFileURL(chatMjs).href);

test("fileKind classifies common attachments", () => {
  assert.equal(fileKind("photo.png", "image/png"), "image");
  assert.equal(fileKind("voice.webm", "audio/webm"), "audio");
  assert.equal(fileKind("clip.webm", "video/webm"), "video");
  assert.equal(fileKind("readme.md", "text/markdown"), "markdown");
});

test("sortConversationsByActivity orders by last timestamp", () => {
  const sorted = sortConversationsByActivity([
    { conversation_id: "a", last_timestamp: "2020-01-01T00:00:00Z" },
    { conversation_id: "b", last_timestamp: "2024-06-01T12:00:00Z" },
    { conversation_id: "c", last_timestamp: "2022-01-01T00:00:00Z" },
  ]);
  assert.deepEqual(sorted.map((c) => c.conversation_id), ["b", "c", "a"]);
});

test("waveformPeaks and recordingLevel return bounded values", () => {
  const peaks = waveformPeaks(new Float32Array([0, 0.5, 1, 0.25]), 4);
  assert.equal(peaks.length, 4);
  assert.ok(peaks.every((p) => p >= 0 && p <= 1));
  const level = recordingLevel(new Uint8Array([128, 140, 116, 128]));
  assert.ok(level >= 0 && level <= 1);
});

test("formatVoiceTime renders mm:ss", () => {
  assert.equal(formatVoiceTime(0), "0:00");
  assert.equal(formatVoiceTime(65), "1:05");
});

test("meshSplitPaneWidth clamps chat and stage widths", () => {
  const w = 900;
  assert.equal(meshSplitPaneWidth("chat", 0, 0, w), MESH_CHAT_MIN_WIDTH);
  assert.equal(meshSplitPaneWidth("chat", 500, 0, w), 500);
  assert.equal(meshSplitPaneWidth("stage", 0, 0, w), MESH_STAGE_MIN_WIDTH);
  assert.equal(meshSplitPaneWidth("stage", 2000, 0, w), MESH_STAGE_MAX_WIDTH);
});

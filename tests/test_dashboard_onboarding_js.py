"""Onboarding and pairing helpers of the mesh dashboard, run in Node."""

from __future__ import annotations

import json
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
ONBOARDING_MJS = ROOT / "runtime" / "experience" / "dashboard" / "onboarding.mjs"


def _node_eval(script: str) -> str:
    node = shutil.which("node")
    if not node:
        pytest.skip("Node.js not available")
    proc = subprocess.run([node, "--input-type=module", "-e", script], cwd=ROOT, capture_output=True, text=True, check=False)
    if proc.returncode != 0:
        raise AssertionError(proc.stderr or proc.stdout)
    return proc.stdout.strip()


def _eval(body: str):
    return json.loads(_node_eval(
        "import * as ob from " + json.dumps(str(ONBOARDING_MJS)) + ";\n"
        "console.log(JSON.stringify((() => {" + body + "})()));"
    ))


def test_setup_steps_read_from_the_core_state():
    out = _eval("""
        return {
          pending: ob.setupStepText({ id: "peer", done: false }),
          done: ob.setupStepText({ id: "peer", done: true }),
          ready: ob.setupComplete({ next: "ready" }),
          notReady: ob.setupComplete({ next: "folder" }),
        };
    """)
    assert out == {
        "pending": "Pair another device",
        "done": "Another device is in your circle",
        "ready": True,
        "notReady": False,
    }


def test_the_active_circle_is_the_one_chosen_not_the_first():
    out = _eval("""
        const circles = [{ id: "c1", name: "first" }, { id: "c2", name: "second" }];
        return {
          chosen: ob.pickActiveCircle(circles, "c2").id,
          gone: ob.pickActiveCircle(circles, "c9").id,
          none: ob.pickActiveCircle([], "c1"),
        };
    """)
    assert out == {"chosen": "c2", "gone": "c1", "none": None}


def test_pairing_code_reads_in_two_groups():
    out = _eval('return [ob.formatPairingCode("042917"), ob.formatPairingCode("12")];')
    assert out == ["042 917", "12"]


def test_pairing_prompts_never_offer_to_add_before_the_code_is_compared():
    out = _eval("""
        const member = ob.pairingPrompt({ role: "member", state: "comparing", hostname: "ada", code: "123456" });
        const joiner = ob.pairingPrompt({ role: "joiner", state: "comparing", hostname: "bob", code: "123456" });
        const connecting = ob.pairingPrompt({ role: "joiner", state: "connecting", hostname: "bob" });
        const confirmed = ob.pairingPrompt({ role: "joiner", state: "comparing", confirmed: true, hostname: "bob", code: "123456" });
        return { member, joiner, connecting, confirmed };
    """)
    assert out["member"]["circle"] is True and "confirm" in out["member"]["actions"] and out["member"]["code"] is True
    assert "same code" in out["member"]["body"]
    assert out["joiner"]["circle"] is False and out["joiner"]["code"] is True
    assert "confirm" not in out["connecting"]["actions"]
    assert "confirm" not in out["confirmed"]["actions"]


def test_a_device_asking_to_join_comes_first():
    out = _eval("""
        const sessions = [
          { id: "j1", role: "joiner", state: "comparing" },
          { id: "m1", role: "member", state: "comparing" },
          { id: "old", role: "member", state: "expired" },
        ];
        return {
          first: ob.pairingToShow(sessions, new Set()).id,
          afterDismiss: ob.pairingToShow(sessions, new Set(["m1"])).id,
          nothing: ob.pairingToShow([{ id: "x", role: "member", state: "joined" }], new Set()),
        };
    """)
    assert out == {"first": "m1", "afterDismiss": "j1", "nothing": None}

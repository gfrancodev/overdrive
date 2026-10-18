"""Connection health helpers of the mesh dashboard (states, peers, fixes, network text), run in Node."""

from __future__ import annotations

import json
from pathlib import Path

from test_dashboard_onboarding_js import _node_eval

ROOT = Path(__file__).resolve().parents[1]
CONNECTION_MJS = ROOT / "runtime" / "experience" / "dashboard" / "connection.mjs"


def _eval(body: str):
    return json.loads(_node_eval(
        "import * as cn from " + json.dumps(str(CONNECTION_MJS)) + ";\n"
        + "console.log(JSON.stringify((() => {" + body + "})()));"
    ))


def test_summary_names_the_capabilities_in_the_overall_state():
    out = _eval("""
        const caps = (states) => Object.entries(states).map(([id, state]) => ({ id, state }));
        return {
          none: cn.connectionSummary(null),
          connected: cn.connectionSummary({ state: "connected", capabilities: caps({ chat: "connected" }),
            peers: [{ reach: "ok" }, { reach: "ok" }, { reach: "timeout" }] }),
          alone: cn.connectionSummary({ state: "connected", capabilities: caps({ chat: "connected" }), peers: [] }),
          one: cn.connectionSummary({ state: "offline", capabilities: caps({ chat: "offline", calls: "reconnecting" }) }),
          two: cn.connectionSummary({ state: "blocked", capabilities: caps({ chat: "connected", memory: "blocked", terminal: "blocked" }) }),
          three: cn.connectionSummary({ state: "degraded", capabilities: caps({ chat: "degraded", memory: "degraded", terminal: "degraded" }) }),
        };
    """)
    assert out == {
        "none": "Checking the connection…",
        "connected": "Connected to 2 devices.",
        "alone": "Everything is connected.",
        "one": "Chat is offline.",
        "two": "Experience and Terminals are blocked.",
        "three": "Chat, Experience and Terminals are partly connected.",
    }


def test_each_fix_is_offered_once_in_capability_order():
    out = _eval("""
        return cn.healthActions({ capabilities: [
          { id: "chat", action: "resume_mesh" }, { id: "memory", action: "allow_network" },
          { id: "terminal", action: "allow_network" }, { id: "calls", action: "unknown" }, { id: "x" },
        ] }).map((a) => [a, cn.healthActionLabel(a)]);
    """)
    assert out == [["resume_mesh", "Resume mesh"], ["allow_network", "Allow devices on this network"]]


def test_peer_rows_say_how_each_device_answered():
    out = _eval("""
        return [
          { reach: "ok", rtt_ms: 4 }, { reach: "ok" }, { reach: "refused" }, { reach: "refused", local_only: true },
          { reach: "timeout" }, { reach: "unreachable" }, { reach: "no_address" }, { reach: "self_address" }, {},
        ].map(cn.reachText);
    """)
    assert out == [
        "Answers in 4 ms", "Answers", "Refuses connections", "Accepts connections only from itself", "Doesn't answer",
        "No route from this device", "No address yet", "Registered an address that only works on its own computer",
        "Not checked",
    ]


def test_network_text_says_who_can_connect():
    out = _eval("""
        return [
          cn.networkText({ lan_access: false, listen: "127.0.0.1:7741" }),
          cn.networkText({ lan_access: true, listen: "0.0.0.0:7741", address: "192.168.1.2:7741" }),
          cn.networkText(null),
          cn.healthLabel("blocked"), cn.healthLabel("weird"), cn.capabilityTitle("memory"), cn.capabilityTitle("other"),
          cn.diagnosticsText({ a: 1 }),
        ];
    """)
    assert out[0] == (
        "Only this computer can pair, sync experience or open terminals with Overdrive here (127.0.0.1:7741). "
        "Devices on the network see it but can't do those. Circle chat, open only to members, still crosses the network."
    )
    assert out[1].startswith("Devices on this network connect to 192.168.1.2:7741. Only circle members get in")
    assert out[2:] == ["", "Blocked", "Checking", "Experience", "other", '{\n  "a": 1\n}']

"""Circle settings helpers of the mesh dashboard (folders, members, removal impact), run in Node."""

from __future__ import annotations

import json
from pathlib import Path

from test_dashboard_onboarding_js import _node_eval

ROOT = Path(__file__).resolve().parents[1]
SETTINGS_MJS = ROOT / "runtime" / "experience" / "dashboard" / "circle_settings.mjs"

CIRCLE = """
const circle = {
  id: "c1", creator_device_id: "dev_a", is_creator: true, key_epoch: 0,
  members: [
    { device_id: "dev_a", hostname: "studio", self: true, receives_keys: true },
    { device_id: "dev_b", hostname: "laptop", invited_by: "dev_a", receives_keys: true },
    { device_id: "dev_c", hostname: "old-mac", invited_by: "dev_b", receives_keys: false },
    { device_id: "dev_d", invited_by: "dev_a", revoked: true, receives_keys: true },
  ],
};
"""


def _eval(body: str):
    return json.loads(_node_eval(
        "import * as cs from " + json.dumps(str(SETTINGS_MJS)) + ";\n"
        + CIRCLE
        + "console.log(JSON.stringify((() => {" + body + "})()));"
    ))


def test_folder_label_is_the_last_path_segment_on_any_os():
    out = _eval("""
        return [cs.folderLabel("/home/me/code/api"), cs.folderLabel("C:\\\\Users\\\\me\\\\web\\\\"), cs.folderLabel("")];
    """)
    assert out == ["api", "web", ""]


def test_member_rows_say_who_invited_whom_and_who_needs_an_update():
    out = _eval("""
        return circle.members.map((m) => cs.memberNotes(m, circle));
    """)
    assert out == [
        "This device · Creator",
        "Invited by studio",
        "Invited by laptop · Needs an Overdrive update to get new keys",
        "Invited by studio · Removed",
    ]


def test_only_the_creator_can_remove_and_never_itself():
    out = _eval("""
        const joined = { ...circle, is_creator: false };
        return {
          creator: circle.members.map((m) => cs.canRemoveMember(m, circle)),
          joined: circle.members.map((m) => cs.canRemoveMember(m, joined)),
        };
    """)
    assert out == {"creator": [False, True, True, False], "joined": [False, False, False, False]}


def test_key_status_says_how_often_the_key_changed_and_the_grace_window():
    out = _eval("""
        const now = Date.parse("2026-10-18T12:00:00Z");
        return {
          first: cs.keyStatusText(circle, now),
          rotated: cs.keyStatusText({ ...circle, key_epoch: 2, key_rotated_at: "2026-10-18T11:00:00Z",
            previous_key_until: "2026-10-19T11:00:00Z" }, now),
          graceOver: cs.keyStatusText({ ...circle, key_epoch: 1, previous_key_until: "2026-10-18T11:00:00Z" }, now),
        };
    """)
    assert out["first"].startswith("The circle still uses its first key")
    assert out["rotated"].startswith("The key changed 2 times, last on ")
    assert "keep working until" in out["rotated"]
    assert out["graceOver"] == "The key changed 1 time."


def test_removal_impact_names_who_loses_access_and_what_cannot_be_undone():
    out = _eval("""
        const plan = { targets: ["dev_b"], invitees: ["dev_c"], cut_off: ["dev_c"], next_epoch: 1 };
        const cascade = { targets: ["dev_b", "dev_c"], invitees: ["dev_c"], cut_off: [], next_epoch: 1 };
        return { alone: cs.revokeImpact(plan, circle, false), cascade: cs.revokeImpact(cascade, circle, true) };
    """)
    alone, cascade = out["alone"], out["cascade"]
    assert alone["title"] == "Remove laptop?" and alone["inviteeCount"] == 1
    assert alone["lines"][0].startswith("laptop loses access now")
    assert "Devices laptop invited stay in the circle: old-mac." in alone["lines"]
    assert any(line.startswith("old-mac runs an Overdrive without key rotation") for line in alone["lines"])
    assert alone["lines"][-1] == "What laptop already received stays on it: removing a device cannot erase copies."
    assert cascade["lines"][0].startswith("laptop and the 1 device it invited (old-mac) lose access now")
    assert not any("stay in the circle" in line for line in cascade["lines"])
    assert not any("without key rotation" in line for line in cascade["lines"])

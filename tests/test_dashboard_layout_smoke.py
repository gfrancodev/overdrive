"""Playwright smokes for layout problems seen on a real dashboard: a Nearby list full of devices long gone with
their Pair buttons under the forget buttons, and a messages window that vanished beside a call once dragged."""

from __future__ import annotations

import json
from datetime import datetime, timedelta, timezone

import pytest

from test_dashboard_call_media_smoke import runtime_bin  # noqa: F401  (pytest fixture)
from test_dashboard_connection_smoke import api, lone_core, tmp_path  # noqa: F401  (pytest fixtures)

pytestmark = pytest.mark.playwright


def stamp(ago: timedelta) -> str:
    return (datetime.now(timezone.utc) - ago).strftime("%Y-%m-%dT%H:%M:%SZ")


def boxes_overlap(a: dict, b: dict) -> bool:
    return a["x"] < b["x"] + b["width"] and b["x"] < a["x"] + a["width"] and \
        a["y"] < b["y"] + b["height"] and b["y"] < a["y"] + a["height"]


def add_member(home, circle_id: str) -> None:
    """A second member on another computer, so the messages window offers calls."""
    path = home / "share" / "circles" / f"{circle_id}.json"
    data = json.loads(path.read_text())
    data["members"].append({"device_id": "dev_00aa11bb22cc33dd", "hostname": "laptop", "endpoint": "192.0.2.10:7741"})
    path.write_text(json.dumps(data))


def test_nearby_shows_devices_still_announcing_in_rows_that_fit(lone_core, playwright):  # noqa: F811
    base, home = lone_core["base"], lone_core["home"]
    api(base, "/api/circles", {"name": "Squad"})
    peers = [
        {"device_id": f"dev_{i:016x}", "friendly_name": name, "os": "linux", "display_name": "Ada",
         "host": "192.0.2.20", "port": 7741, "last_seen": stamp(ago)}
        for i, (name, ago) in enumerate([
            ("studio", timedelta(seconds=5)),
            ("laptop", timedelta(seconds=20)),
            ("old-1", timedelta(days=3)),
            ("old-2", timedelta(hours=2)),
            ("old-3", timedelta(minutes=10)),
        ])
    ]
    (home / "share" / "discovered.json").write_text(json.dumps({"peers": peers}))

    browser = playwright.chromium.launch()
    page = browser.new_context(viewport={"width": 965, "height": 640}).new_page()
    try:
        page.goto(base + "/", wait_until="networkidle", timeout=30_000)
        page.wait_for_function("() => document.getElementById('nearbyCount')?.textContent === '2'", timeout=20_000)
        page.locator("#nearbyApp").click()
        page.wait_for_selector("#nearby:not([hidden])", timeout=5_000)
        assert page.locator("#setup").is_hidden(), "Get started stays under the sheet just opened"
        rows = page.locator("#discoveredPeers .machine-row-shell")
        assert rows.count() == 2, "devices that stopped announcing are gone"
        assert sorted(rows.locator("small").all_inner_texts()) == ["laptop", "studio"]
        for i in range(2):
            row = rows.nth(i)
            pair = row.locator(".row-pair").bounding_box()
            forget = row.locator(".row-forget").bounding_box()
            line = row.locator(".machine-row").bounding_box()
            assert not boxes_overlap(pair, forget), "the forget button covers Pair"
            assert pair["x"] + pair["width"] <= line["x"] + line["width"] + 0.5, "Pair spills out of its row"
        page.locator("#nearbyClose").click()
        details = page.locator(".tag em").all_text_contents()
        mine = [text for text in details if "this device" in text]
        assert mine and not any("ago" in text for text in mine), details
    finally:
        browser.close()


def test_a_messages_window_once_dragged_stays_beside_a_call(lone_core, playwright):  # noqa: F811
    base, home = lone_core["base"], lone_core["home"]
    circle = api(base, "/api/circles", {"name": "Squad"})["circle"]
    add_member(home, circle["id"])

    browser = playwright.chromium.launch(args=["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream"])
    context = browser.new_context(viewport={"width": 965, "height": 680})
    # Where an earlier session left the window after a drag and a resize.
    context.add_init_script(
        "localStorage.setItem('messagesWidgetPos', JSON.stringify({ left: 640, top: 360 }));"
        "localStorage.setItem('messagesWidgetSize', JSON.stringify({ width: '360px', height: '300px' }));"
    )
    page = context.new_page()
    try:
        page.goto(base + "/", wait_until="networkidle", timeout=30_000)
        page.wait_for_function("() => globalThis.__odMessagesReady === true", timeout=30_000)
        page.locator("#msgApp").click()
        page.wait_for_selector("#messages:not([hidden])", timeout=5_000)
        page.locator("#msgVideo").click(force=True, timeout=10_000)
        page.wait_for_selector("#callPanel[data-phase='setup']", timeout=15_000)

        live = page.locator("#meshLive").bounding_box()
        chat = page.locator("#messages").bounding_box()
        call = page.locator("#callPanel").bounding_box()
        assert chat["x"] >= live["x"] - 0.5 and chat["y"] >= live["y"] - 0.5, (live, chat)
        assert chat["x"] + chat["width"] <= call["x"] + 0.5, "the chat sits left of the call"
        assert abs(chat["height"] - live["height"]) <= 3, "the chat fills the docked window, not the saved size"
        assert abs(call["height"] - live["height"]) <= 3, "the call column runs the whole height"
        assert page.locator("#callFocus").is_hidden(), "nothing is on the stage before the call"
        assert page.locator("#callVoiceLabel").inner_text() == "Video call"
        assert page.locator("#callNatHint").is_hidden(), "the relay advice waits for a call that failed"
    finally:
        browser.close()

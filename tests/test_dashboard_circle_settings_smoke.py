"""Playwright smoke: share a folder and remove a paired device from the dashboard's circle settings."""

from __future__ import annotations

import json
import time
import urllib.request

import pytest

from test_dashboard_pairing_smoke import fresh_pair, runtime_bin  # noqa: F401  (pytest fixtures)

pytestmark = pytest.mark.playwright


def api(base: str, path: str, body: dict | None = None) -> dict:
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(base + path, data=data, method="GET" if body is None else "POST",
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.loads(resp.read())


def wait_session(base: str, role: str, state: str, timeout: float = 10.0) -> dict:
    deadline = time.time() + timeout
    while time.time() < deadline:
        for s in api(base, "/api/pairing/sessions")["sessions"]:
            if s["role"] == role and s["state"] == state:
                return s
        time.sleep(0.2)
    pytest.fail(f"no {role} pairing session reached {state}")


def pair(a: dict, b: dict, circle_id: str) -> None:
    api(b["base"], "/api/pairing/sessions", {"device_id": a["device"], "endpoint": a["listen"]})
    member = wait_session(a["base"], "member", "comparing")
    joiner = wait_session(b["base"], "joiner", "comparing")
    assert member["code"] == joiner["code"]
    api(a["base"], f"/api/pairing/sessions/{member['id']}/confirm", {"circle_id": circle_id})
    api(b["base"], f"/api/pairing/sessions/{joiner['id']}/confirm", {})
    wait_session(b["base"], "joiner", "joined", timeout=15.0)


def test_circle_settings_share_a_folder_and_remove_a_device(fresh_pair, playwright, tmp_path):  # noqa: F811
    a, b = fresh_pair
    circle = api(a["base"], "/api/circles", {"name": "Squad"})["circle"]
    pair(a, b, circle["id"])
    project = tmp_path / "projects" / "web"
    project.mkdir(parents=True)

    browser = playwright.chromium.launch()
    page = browser.new_context(viewport={"width": 1440, "height": 900}).new_page()
    try:
        page.goto(a["base"] + "/", wait_until="networkidle", timeout=30_000)
        # Paired but nothing shared: Get started opens at the folder step and leads to Circle settings.
        page.wait_for_selector("#setup:not([hidden]) #setupShareFolder", timeout=20_000)
        assert not page.locator("#setupCreate").is_visible() and not page.locator("#setupPairRow").is_visible()
        page.locator("#setupShareFolder").click()
        page.wait_for_selector("#circleManage:not([hidden])", timeout=10_000)
        assert page.locator("#setup").is_hidden()
        assert not page.locator("#activeCircleField").is_visible(), "one circle needs no circle picker"

        page.locator("#folderPath").fill(str(project))
        page.locator("#folderAddButton").click()
        page.wait_for_selector(f"#circleFolders .row-text small:text-is('{project.resolve()}')", timeout=10_000)
        assert "Sharing web" in page.locator("#folderStatus").inner_text()

        remove = page.locator("#circleMembers button.row-action.danger")
        assert remove.count() == 1
        remove.click()
        page.wait_for_selector("#revoke:not([hidden])", timeout=10_000)
        assert not page.locator("#revokeInviteesField").is_visible(), "a device that invited no one has no cascade"
        assert not page.locator("#settings").is_visible(), "the confirmation replaces the settings sheet"
        impact = page.locator("#revokeImpact li").all_inner_texts()
        assert impact[0].endswith("loses access now: no new chat, calls, terminals or shared experience.")
        assert impact[-1].endswith("removing a device cannot erase copies.")
        page.locator("#revokeConfirm").click()
        page.wait_for_selector("#revoke[hidden]", state="attached", timeout=15_000)
        assert page.locator("#settings").is_visible(), "settings come back after the removal"
        assert page.locator("#folderStatus").inner_text().startswith("Removed ")
        page.wait_for_function(
            "() => document.getElementById('circleKey')?.textContent.startsWith('The key changed 1 time')",
            timeout=10_000,
        )
        assert page.locator("#circleMembers button.row-action.danger").count() == 0
    finally:
        browser.close()

    after = api(a["base"], "/api/circles")["circles"][0]
    assert after["key_epoch"] == 1
    assert [m["revoked"] for m in after["members"] if m["device_id"] == b["device"]] == [True]

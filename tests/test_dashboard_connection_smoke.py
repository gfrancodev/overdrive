"""Playwright smokes for connection health: the dashboard with the WAN off, and unblocking a device from the UI."""

from __future__ import annotations

import json
import shutil
import subprocess
import tempfile
import urllib.request
from pathlib import Path
from urllib.parse import urlparse

import pytest

from test_dashboard_call_media_smoke import dual_dashboard, open_messages, runtime_bin, wait_http  # noqa: F401  (pytest fixtures)
from test_share_runtime import free_port, runtime_env

pytestmark = pytest.mark.playwright

LOCAL_HOSTS = {"127.0.0.1", "localhost", "[::1]", "::1"}


@pytest.fixture
def tmp_path():
    """A short home: terminal sockets live under it and a Unix socket path stops at about 104 bytes."""
    path = Path(tempfile.mkdtemp(prefix="od-"))
    yield path
    shutil.rmtree(path, ignore_errors=True)


def wan_off(context) -> list[str]:
    """Abort every request that leaves this computer, as with the internet unplugged, and record it."""
    blocked: list[str] = []

    def route(r):
        url = r.request.url
        host = urlparse(url).hostname or ""
        if url.startswith(("data:", "blob:")) or host in LOCAL_HOSTS:
            r.continue_()
            return
        blocked.append(url)
        r.abort("internetdisconnected")

    context.route("**/*", route)
    return blocked


def test_dashboard_works_with_the_wan_off(dual_dashboard, playwright):  # noqa: F811
    base_a, base_b = dual_dashboard
    browser = playwright.chromium.launch()
    ctx_a = browser.new_context(viewport={"width": 1440, "height": 900})
    ctx_b = browser.new_context(viewport={"width": 1440, "height": 900})
    ctx_a.grant_permissions(["clipboard-read", "clipboard-write"], origin=base_a)
    blocked = wan_off(ctx_a) + wan_off(ctx_b)
    page_a, page_b = ctx_a.new_page(), ctx_b.new_page()
    try:
        page_a.goto(base_a + "/", wait_until="networkidle", timeout=30_000)
        page_b.goto(base_b + "/", wait_until="networkidle", timeout=30_000)
        page_a.wait_for_function("() => globalThis.__odMessagesReady === true", timeout=30_000)
        boot = page_a.evaluate("() => globalThis.__odBootError || ''")
        assert boot == "", boot
        assert page_a.locator("#stage canvas").count() == 1, "the globe needs Three.js from the Core"

        # Connection: the menu bar icon turns healthy once chat links and the peer answers.
        page_a.wait_for_selector("#barListen[data-health='connected']", timeout=45_000)
        page_a.locator("#barListen").click()
        page_a.wait_for_selector("#health:not([hidden])", timeout=5_000)
        caps = page_a.locator("#healthCaps .row-text b").all_inner_texts()
        assert caps == ["Chat · Connected", "Calls · Connected", "Experience · Connected", "Terminals · Connected"], caps
        assert page_a.locator("#healthPeers .row-text small").first.inner_text().startswith("Answers")
        assert page_a.locator("#healthFixes").is_hidden()
        assert page_a.locator("#networkToggle").inner_text() == "Allow devices on this network"
        page_a.locator("#healthCopy").click()
        page_a.wait_for_function(
            "() => document.getElementById('healthStatus')?.textContent.startsWith('Diagnostics copied')", timeout=10_000
        )
        report = json.loads(page_a.evaluate("() => navigator.clipboard.readText()"))
        assert report["health"]["state"] == "connected" and report["circles"][0]["shared_folders"] == 1
        page_a.keyboard.press("Escape")
        assert page_a.locator("#health").is_hidden()

        # Chat across the two cores.
        open_messages(page_a)
        open_messages(page_b)
        page_a.locator("#msgNew").click(force=True, timeout=10_000)
        page_a.locator("#composeContacts button.compose-contact").first.click(timeout=45_000)
        page_a.locator("#messageText").fill("offline hello")
        page_a.locator("#messageText").press("Enter")
        page_b.wait_for_function(
            "() => document.getElementById('messages')?.textContent.includes('offline hello')", timeout=30_000
        )

        # A local terminal.
        page_a.locator("#termApp").click()
        page_a.wait_for_selector("#liveSessions:not([hidden])", timeout=10_000)
        if not page_a.locator("#termNewSession").is_visible():
            page_a.locator("#termToggleList").click()
        page_a.locator("#termNewSession").click()
        page_a.wait_for_selector(".term-xterm .xterm-rows", timeout=20_000)
        page_a.wait_for_function(
            "() => [...document.querySelectorAll('.term-xterm .xterm-rows')].some((r) => r.textContent.includes('$'))",
            timeout=20_000,
        )
        page_a.locator(".term-xterm .xterm-screen").first.click()
        page_a.keyboard.type("echo overdrive-$((40+2))-offline", delay=10)
        page_a.keyboard.press("Enter")
        page_a.wait_for_function(
            "() => [...document.querySelectorAll('.term-xterm .xterm-rows')].some((r) => r.textContent.includes('overdrive-42-offline'))",
            timeout=20_000,
        )
    finally:
        ctx_a.close()
        ctx_b.close()
        browser.close()
    assert blocked == [], f"the dashboard tried to reach the internet: {blocked}"


def api(base: str, path: str, body: dict | None = None) -> dict:
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(base + path, data=data, method="GET" if body is None else "POST",
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.loads(resp.read())


@pytest.fixture
def lone_core(runtime_bin, tmp_path):  # noqa: F811
    home = tmp_path / "home"
    env = runtime_env(home)
    port = free_port()
    env["OVERDRIVE_SHARE_LISTEN"] = f"127.0.0.1:{port}"
    proc = subprocess.Popen([str(runtime_bin), "share", "listen", "--cwd", str(tmp_path)], env=env,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    base = f"http://127.0.0.1:{port + 1}"
    try:
        wait_http(base + "/api/core/ui")
        yield {"base": base, "home": home, "project": tmp_path}
    finally:
        proc.terminate()
        proc.wait(timeout=5)


def test_connection_sheet_lets_the_network_in(lone_core, playwright):
    base, home = lone_core["base"], lone_core["home"]
    circle = api(base, "/api/circles", {"name": "Squad"})["circle"]
    project = lone_core["project"] / "web"
    project.mkdir()
    api(base, f"/api/circles/{circle['id']}/folders", {"path": str(project)})
    # A member on another computer, at a documentation address that never answers.
    path = home / "share" / "circles" / f"{circle['id']}.json"
    data = json.loads(path.read_text())
    data["members"].append({"device_id": "dev_00aa11bb22cc33dd", "hostname": "laptop", "endpoint": "192.0.2.10:7741"})
    path.write_text(json.dumps(data))

    browser = playwright.chromium.launch()
    page = browser.new_context(viewport={"width": 1440, "height": 900}).new_page()
    try:
        page.goto(base + "/", wait_until="networkidle", timeout=30_000)
        page.wait_for_selector("#barListen[data-health='blocked']", timeout=20_000)
        page.locator("#barListen").click()
        page.wait_for_selector("#health:not([hidden])", timeout=5_000)
        assert page.locator("#healthSummary").inner_text() == "Experience and Terminals are blocked."
        experience = page.locator("#healthCaps .row", has_text="Experience")
        assert "accepts connections only from this computer" in experience.inner_text()
        assert page.locator("#healthNetwork").inner_text().startswith("Only this computer can connect")
        fixes = page.locator("#healthFixes button")
        assert fixes.all_inner_texts() == ["Allow devices on this network"], "one fix, offered once"
        assert page.locator("#networkToggle").is_hidden(), "the fix above already offers it"

        fixes.click()
        page.wait_for_function(
            "() => document.getElementById('healthNetwork')?.textContent.startsWith('Devices on this network connect to')",
            timeout=10_000,
        )
        assert api(base, "/api/network")["lan_access"] is True
        # The Core lets the network in; now the remaining problem is the laptop itself.
        page.wait_for_function(
            "() => [...document.querySelectorAll('#healthCaps .row')].some((r) => r.textContent.includes('Check that laptop is on'))",
            timeout=10_000,
        )
        assert page.locator("#healthFixes").is_hidden()
        assert page.locator("#networkToggle").inner_text() == "Accept connections only from this computer"
        page.locator("#networkToggle").click()
        page.wait_for_function(
            "() => document.getElementById('healthNetwork')?.textContent.startsWith('Only this computer can connect')",
            timeout=10_000,
        )
        assert api(base, "/api/network")["lan_access"] is False
    finally:
        browser.close()

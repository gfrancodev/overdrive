"""Playwright smoke: first run on two fresh devices, from creating a circle to pairing, in the dashboard."""

from __future__ import annotations

import json
import shutil
import subprocess
import time
import urllib.request
from pathlib import Path

import pytest

from test_share_runtime import free_port, runtime_env, run

ROOT = Path(__file__).resolve().parents[1]
RUNTIME_DIR = ROOT / "runtime" / "experience"

pytestmark = pytest.mark.playwright


@pytest.fixture(scope="session")
def runtime_bin(tmp_path_factory: pytest.TempPathFactory) -> Path:
    go = shutil.which("go")
    if not go:
        pytest.skip("Go toolchain not available")
    out = tmp_path_factory.mktemp("runtime-bin-pairing") / "overdrive-runtime"
    subprocess.run([go, "build", "-buildvcs=false", "-o", str(out), "."], cwd=RUNTIME_DIR, check=True,
                   capture_output=True, text=True)
    return out


def wait_http(url: str, timeout: float = 15.0) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=1) as resp:
                if resp.status == 200:
                    return
        except OSError:
            time.sleep(0.15)
    pytest.fail(f"HTTP server did not start: {url}")


@pytest.fixture
def fresh_pair(runtime_bin: Path, tmp_path: Path):
    procs = []
    devices = []
    for name in ("a", "b"):
        home = tmp_path / f"home-{name}"
        env = runtime_env(home)
        port = free_port()
        env["OVERDRIVE_SHARE_LISTEN"] = f"127.0.0.1:{port}"
        device = json.loads(run(str(runtime_bin), "share", "identity", env=env).stdout)["device_id"]
        procs.append(subprocess.Popen([str(runtime_bin), "share", "listen", "--cwd", str(tmp_path)], env=env,
                                      stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL))
        devices.append({"base": f"http://127.0.0.1:{port + 1}", "listen": f"127.0.0.1:{port}", "device": device})
    try:
        for d in devices:
            wait_http(d["base"] + "/api/core/ui")
        yield devices
    finally:
        for proc in procs:
            proc.terminate()
        for proc in procs:
            proc.wait(timeout=5)


def test_first_run_creates_a_circle_and_pairs_from_the_dashboard(fresh_pair, playwright):
    a, b = fresh_pair
    browser = playwright.chromium.launch()
    page_a = browser.new_context(viewport={"width": 1440, "height": 900}).new_page()
    page_b = browser.new_context(viewport={"width": 1440, "height": 900}).new_page()
    try:
        page_a.goto(a["base"] + "/", wait_until="networkidle", timeout=30_000)
        page_a.wait_for_selector("#setup:not([hidden])", timeout=20_000)
        page_a.locator("#setupCircleName").fill("Squad")
        page_a.locator("#setupCreateCircle").click()
        page_a.wait_for_function(
            "() => document.querySelector('#setupSteps .setup-step.next')?.textContent === 'Pair another device'",
            timeout=10_000,
        )

        page_b.goto(b["base"] + "/", wait_until="networkidle", timeout=30_000)
        page_b.wait_for_selector("#setup:not([hidden])", timeout=20_000)
        page_b.locator("#setupClose").click()
        # Discovery may not cross this machine's multicast setup; start from the device's address instead.
        page_b.evaluate(
            """([device, endpoint]) => fetch('/api/pairing/sessions', {
                method: 'POST', headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({device_id: device, endpoint})})""",
            [a["device"], a["listen"]],
        )

        page_b.wait_for_selector("#pairing:not([hidden]) #pairingCode:not([hidden])", timeout=10_000)
        page_a.wait_for_selector("#pairing:not([hidden]) #pairingCode:not([hidden])", timeout=10_000)
        code_a = page_a.locator("#pairingCode").inner_text()
        code_b = page_b.locator("#pairingCode").inner_text()
        assert code_a == code_b and len(code_a.replace(" ", "")) == 6

        assert page_a.locator("#pairingCircleField").is_visible()
        assert not page_b.locator("#pairingCircleField").is_visible(), "only the member chooses the circle"
        for page in (page_a, page_b):
            assert page.locator("#pairingConfirm").is_visible() and page.locator("#pairingCancel").is_visible()
            assert not page.locator("#pairingClose").is_visible(), "a pairing in progress offers no Close"
        page_a.locator("#pairingConfirm").click()
        page_b.locator("#pairingConfirm").click()
        page_b.wait_for_function(
            "() => document.getElementById('pairingTitle')?.textContent === 'This device joined the circle'",
            timeout=15_000,
        )
        assert page_b.locator("#pairingClose").is_visible() and not page_b.locator("#pairingConfirm").is_visible()
        with urllib.request.urlopen(b["base"] + "/api/circles", timeout=5) as resp:
            circles = json.loads(resp.read())["circles"]
        assert [c["name"] for c in circles] == ["Squad"]
    finally:
        browser.close()

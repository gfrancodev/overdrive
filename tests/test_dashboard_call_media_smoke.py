"""Playwright smoke: fake media WebRTC between two share listen dashboards."""

from __future__ import annotations

import json
import os
import shutil
import socket
import subprocess
import time
import urllib.request
from pathlib import Path

import pytest

from test_share_runtime import free_port, make_repo, runtime_env, run

ROOT = Path(__file__).resolve().parents[1]
RUNTIME_DIR = ROOT / "runtime" / "experience"

pytestmark = pytest.mark.playwright


@pytest.fixture(scope="session")
def runtime_bin(tmp_path_factory: pytest.TempPathFactory) -> Path:
    go = shutil.which("go")
    if not go:
        pytest.skip("Go toolchain not available")
    out = tmp_path_factory.mktemp("runtime-bin-media") / "overdrive-runtime"
    subprocess.run(
        [go, "build", "-buildvcs=false", "-o", str(out), "."],
        cwd=RUNTIME_DIR,
        check=True,
        capture_output=True,
        text=True,
    )
    return out


def fetch_peers(base: str) -> dict:
    with urllib.request.urlopen(base + "/api/peers", timeout=3) as resp:
        return json.loads(resp.read().decode("utf-8"))


def wait_peer_ready(base: str, timeout: float = 45.0) -> str:
    """Wait until another circle member is visible (mesh synced)."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            data = fetch_peers(base)
            self_id = str(data.get("device_id") or "")
            for circle in data.get("circles") or []:
                for member in circle.get("members") or []:
                    if member.get("revoked"):
                        continue
                    device_id = str(member.get("device_id") or "")
                    if device_id and device_id != self_id:
                        return device_id
        except OSError:
            pass
        time.sleep(0.25)
    pytest.fail(f"no peer visible on dashboard at {base}")


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
def dual_dashboard(runtime_bin: Path, tmp_path: Path):
    repo = make_repo(tmp_path / "repo")
    home_a = tmp_path / "home-a"
    home_b = tmp_path / "home-b"
    env_a = runtime_env(home_a)
    env_b = runtime_env(home_b)

    circle = json.loads(
        run(str(runtime_bin), "share", "circle", "create", "--name", "call-smoke", env=env_a).stdout
    )
    invite = json.loads(
        run(str(runtime_bin), "share", "circle", "invite", "--circle", circle["id"], env=env_a).stdout
    )
    invite_dir = home_b / "share" / "invites"
    invite_dir.mkdir(parents=True, exist_ok=True)
    (invite_dir / f"{invite['code']}.json").write_text(json.dumps(invite))

    port_a = free_port()
    port_b = free_port()
    env_a["OVERDRIVE_SHARE_LISTEN"] = f"127.0.0.1:{port_a}"
    env_b["OVERDRIVE_SHARE_LISTEN"] = f"127.0.0.1:{port_b}"

    run(
        str(runtime_bin),
        "share",
        "circle",
        "folder-add",
        "--circle",
        circle["id"],
        "--folder",
        str(repo),
        env=env_a,
    )

    proc_a = subprocess.Popen(
        [str(runtime_bin), "share", "listen", "--cwd", str(repo)],
        env=env_a,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    time.sleep(0.5)
    run(
        str(runtime_bin),
        "share",
        "circle",
        "accept",
        "--code",
        invite["code"],
        "--fingerprint",
        invite["fingerprint"],
        "--peer",
        env_a["OVERDRIVE_SHARE_LISTEN"],
        env=env_b,
    )
    run(
        str(runtime_bin),
        "share",
        "circle",
        "folder-add",
        "--circle",
        circle["id"],
        "--folder",
        str(repo),
        env=env_b,
    )
    proc_b = subprocess.Popen(
        [str(runtime_bin), "share", "listen", "--cwd", str(repo)],
        env=env_b,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    base_a = f"http://127.0.0.1:{port_a + 1}"
    base_b = f"http://127.0.0.1:{port_b + 1}"
    try:
        wait_http(base_a + "/")
        wait_http(base_b + "/")
        wait_peer_ready(base_a)
        wait_peer_ready(base_b)
        yield base_a, base_b
    finally:
        proc_a.terminate()
        proc_b.terminate()
        proc_a.wait(timeout=5)
        proc_b.wait(timeout=5)


def open_messages(page) -> None:
    """Wait until the dashboard script bound the launcher, then open the widget once."""
    try:
        page.wait_for_function("() => globalThis.__odMessagesReady === true", timeout=30_000)
    except Exception as exc:
        detail = page.evaluate(
            "() => ({ boot: globalThis.__odBootError || '', ready: globalThis.__odMessagesReady === true })"
        )
        pytest.fail(f"dashboard did not finish booting: {detail} ({exc})")
    page.locator("#msgApp").click()
    page.wait_for_selector("#messages:not([hidden])", timeout=5_000)


def test_voice_call_connects_with_fake_media(dual_dashboard, playwright):
    base_a, base_b = dual_dashboard
    browser = playwright.chromium.launch(
        args=[
            "--use-fake-device-for-media-stream",
            "--use-fake-ui-for-media-stream",
            "--autoplay-policy=no-user-gesture-required",
        ]
    )
    ctx_a = browser.new_context(viewport={"width": 1440, "height": 900})
    ctx_b = browser.new_context(viewport={"width": 1440, "height": 900})
    page_a = ctx_a.new_page()
    page_b = ctx_b.new_page()
    try:
        page_a.goto(base_a + "/", wait_until="networkidle", timeout=30_000)
        page_b.goto(base_b + "/", wait_until="networkidle", timeout=30_000)
        page_a.wait_for_function("() => document.getElementById('callPanel')", timeout=20_000)
        page_b.wait_for_function("() => document.getElementById('callPanel')", timeout=20_000)

        open_messages(page_a)
        page_a.locator("#msgNew").click(force=True, timeout=10_000)
        page_a.locator("#composeContacts button.compose-contact").first.click(timeout=45_000)
        page_a.locator("#msgCall").click(force=True, timeout=10_000)
        page_a.locator("#callSetupContinue").click(timeout=15_000)
        page_b.locator("#callAccept").wait_for(state="visible", timeout=20_000)
        page_b.click("#callAccept", timeout=10_000)
        page_b.locator("#callSetupContinue").click(timeout=15_000)

        page_a.locator("#callPanel[data-phase='active']").wait_for(timeout=25_000)
        page_b.locator("#callPanel[data-phase='active']").wait_for(timeout=25_000)

        has_remote = page_a.evaluate(
            """() => {
              const v = document.getElementById('callRemoteVideo');
              const local = document.getElementById('callLocalVideo');
              const stream = (v && v.srcObject) || (local && local.srcObject);
              return Boolean(stream && stream.getTracks && stream.getTracks().length > 0);
            }"""
        )
        assert has_remote, "caller should have a media stream after connect"
    finally:
        ctx_a.close()
        ctx_b.close()
        browser.close()
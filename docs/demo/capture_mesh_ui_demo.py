#!/usr/bin/env python3
"""Record a short mesh dashboard walkthrough for the README (two local peers)."""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RUNTIME_DIR = ROOT / "runtime" / "experience"
OUT_GIF = ROOT / "docs" / "demo" / "mesh-dashboard.gif"
TESTS = ROOT / "tests"
sys.path.insert(0, str(TESTS))

from test_share_runtime import free_port, make_repo, runtime_env, run  # noqa: E402


def wait_http(url: str, timeout: float = 20.0) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=1) as resp:
                if resp.status == 200:
                    return
        except OSError:
            time.sleep(0.15)
    raise RuntimeError(f"HTTP server did not start: {url}")


def fetch_peers(base: str) -> dict:
    with urllib.request.urlopen(base + "/api/peers", timeout=3) as resp:
        return json.loads(resp.read().decode("utf-8"))


def wait_peer_ready(base: str, timeout: float = 45.0) -> None:
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
                        return
        except OSError:
            pass
        time.sleep(0.25)
    raise RuntimeError(f"no peer visible on dashboard at {base}")


def open_messages(page) -> None:
    page.wait_for_function("() => globalThis.__odMessagesReady === true", timeout=30_000)
    page.locator("#msgApp").click()
    page.wait_for_selector("#messages:not([hidden])", timeout=10_000)


def build_runtime(out: Path) -> None:
    go = shutil.which("go")
    if not go:
        raise SystemExit("go toolchain required")
    subprocess.run(
        [go, "build", "-buildvcs=false", "-o", str(out), "."],
        cwd=RUNTIME_DIR,
        check=True,
    )


def webm_to_gif(webm: Path, gif: Path) -> None:
    ffmpeg = shutil.which("ffmpeg")
    if not ffmpeg:
        raise SystemExit("ffmpeg required to render GIF from Playwright video")
    gif.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        [
            ffmpeg,
            "-y",
            "-i",
            str(webm),
            "-vf",
            "fps=8,scale=640:-1:flags=lanczos,split[s0][s1];[s0]palettegen=stats_mode=diff[p];[s1][p]paletteuse=dither=bayer:bayer_scale=3",
            "-loop",
            "0",
            str(gif),
        ],
        check=True,
        capture_output=True,
    )


def main() -> None:
    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        raise SystemExit("install playwright: pip install playwright && playwright install chromium")

    runtime_bin = RUNTIME_DIR / ".demo-overdrive-runtime"
    build_runtime(runtime_bin)

    with tempfile.TemporaryDirectory(prefix="od-mesh-demo-") as tmp:
        tmp_path = Path(tmp)
        repo = make_repo(tmp_path / "repo")
        home_a = tmp_path / "home-a"
        home_b = tmp_path / "home-b"
        env_a = runtime_env(home_a)
        env_b = runtime_env(home_b)

        circle = json.loads(
            run(str(runtime_bin), "share", "circle", "create", "--name", "readme-demo", env=env_a).stdout
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

            video_dir = tmp_path / "video"
            video_dir.mkdir()

            with sync_playwright() as p:
                browser = p.chromium.launch(
                    args=[
                        "--use-fake-device-for-media-stream",
                        "--use-fake-ui-for-media-stream",
                        "--autoplay-policy=no-user-gesture-required",
                    ]
                )
                ctx_a = browser.new_context(
                    viewport={"width": 1280, "height": 800},
                    record_video_dir=str(video_dir),
                    record_video_size={"width": 1280, "height": 800},
                )
                ctx_b = browser.new_context(viewport={"width": 1280, "height": 800})
                page_a = ctx_a.new_page()
                page_b = ctx_b.new_page()
                try:
                    page_a.goto(base_a + "/", wait_until="networkidle", timeout=60_000)
                    page_b.goto(base_b + "/", wait_until="networkidle", timeout=60_000)
                    page_a.wait_for_function("() => document.getElementById('callPanel')", timeout=30_000)
                    page_b.wait_for_function("() => document.getElementById('callPanel')", timeout=30_000)
                    time.sleep(1.5)

                    open_messages(page_a)
                    page_a.locator("#msgNew").click(force=True, timeout=10_000)
                    page_a.locator("#composeContacts button.compose-contact").first.click(timeout=45_000)
                    page_a.locator("#messageText").fill(
                        "Shared workspace ready. Terminal session attached for harness handoff."
                    )
                    page_a.locator("#messageText").press("Enter")
                    time.sleep(1.2)

                    open_messages(page_b)
                    page_b.locator("#threadPane").get_by_text("Shared workspace", exact=False).wait_for(
                        timeout=25_000
                    )
                    time.sleep(1.0)

                    page_a.locator("#msgClose").click(timeout=5_000)
                    page_a.locator("#termApp").click(timeout=10_000)
                    page_a.wait_for_selector("#liveSessions:not([hidden]) .xterm", timeout=25_000)
                    time.sleep(2.0)

                    open_messages(page_a)
                    page_a.locator("#msgCall").click(force=True, timeout=10_000)
                    page_a.locator("#callSetupContinue").click(timeout=15_000)
                    page_b.locator("#callAccept").wait_for(state="visible", timeout=25_000)
                    page_b.click("#callAccept", timeout=10_000)
                    page_b.locator("#callSetupContinue").click(timeout=15_000)
                    page_a.locator("#callPanel[data-phase='active']").wait_for(timeout=30_000)
                    time.sleep(2.5)
                finally:
                    ctx_a.close()
                    ctx_b.close()
                    browser.close()

            webms = list(video_dir.glob("*.webm"))
            if not webms:
                raise RuntimeError("no Playwright video captured")
            webm = max(webms, key=lambda p: p.stat().st_mtime)
            webm_to_gif(webm, OUT_GIF)
            print(f"Wrote {OUT_GIF}")
        finally:
            proc_a.terminate()
            proc_b.terminate()
            proc_a.wait(timeout=5)
            proc_b.wait(timeout=5)


if __name__ == "__main__":
    main()

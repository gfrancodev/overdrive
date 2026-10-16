"""E2E checks against the live share UI HTTP surface."""

from __future__ import annotations

import json
import shutil
import socket
import subprocess
import time
import uuid
import urllib.error
import urllib.request
from pathlib import Path

import pytest

from test_share_runtime import make_repo, runtime_env, run

ROOT = Path(__file__).resolve().parents[1]
RUNTIME_DIR = ROOT / "runtime" / "experience"


@pytest.fixture(scope="session")
def runtime_bin(tmp_path_factory: pytest.TempPathFactory) -> Path:
    go = shutil.which("go")
    if not go:
        pytest.skip("Go toolchain not available")
    out = tmp_path_factory.mktemp("runtime-bin-e2e") / "overdrive-runtime"
    subprocess.run(
        [go, "build", "-buildvcs=false", "-o", str(out), "."],
        cwd=RUNTIME_DIR,
        check=True,
        capture_output=True,
        text=True,
    )
    return out


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def http_json(method: str, url: str, body: dict | None = None) -> tuple[int, dict]:
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            raw = resp.read().decode("utf-8")
            return resp.status, json.loads(raw) if raw else {}
    except urllib.error.HTTPError as err:
        raw = err.read().decode("utf-8")
        try:
            payload = json.loads(raw) if raw else {}
        except json.JSONDecodeError:
            payload = {"error": raw}
        return err.code, payload


@pytest.fixture()
def share_ui(runtime_bin: Path, tmp_path: Path):
    home = Path("/tmp") / f"od-e2e-{uuid.uuid4().hex[:10]}"
    home.mkdir(parents=True, exist_ok=True)
    env = runtime_env(home)
    repo = make_repo(Path("/tmp") / f"od-repo-{uuid.uuid4().hex[:10]}")
    circle = json.loads(
        run(str(runtime_bin), "share", "circle", "create", "--name", "dash-e2e", env=env).stdout
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
        env=env,
    )
    port = free_port()
    endpoint = f"127.0.0.1:{port}"
    env["OVERDRIVE_SHARE_LISTEN"] = endpoint
    proc = subprocess.Popen(
        [str(runtime_bin), "share", "listen", "--cwd", str(repo)],
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    time.sleep(0.6)
    base = f"http://127.0.0.1:{port + 1}"
    for _ in range(60):
        try:
            with urllib.request.urlopen(base + "/", timeout=1) as resp:
                if resp.status == 200:
                    break
        except OSError:
            time.sleep(0.1)
    else:
        proc.kill()
        pytest.fail("share UI did not start")
    yield base, circle["id"], str(repo)
    proc.terminate()
    proc.wait(timeout=5)


def test_dashboard_bundle_contains_call_chat_terminal(share_ui):
    base, _mesh, _repo = share_ui
    with urllib.request.urlopen(base + "/", timeout=10) as resp:
        html = resp.read().decode("utf-8")
    assert "CALL_EVENT_HANDLERS" in html
    assert "FILE_KIND_STRATEGIES" in html
    assert "friendlyTerminalSessionName" in html
    assert "chatMediaModal" in html


def test_call_events_and_terminal_session_api(share_ui):
    base, mesh_id, repo = share_ui
    status, events = http_json("GET", f"{base}/api/call/events?mesh_id={mesh_id}&since=0")
    assert status == 200
    assert "events" in events and "seq" in events

    status, created = http_json(
        "POST",
        f"{base}/api/v1/terminal/sessions",
        {
            "name": "e2e-shell",
            "cols": 80,
            "rows": 24,
            "circle_id": mesh_id,
            "project_root": repo,
            "working_directory": repo,
        },
    )
    assert status == 201, created
    sess = created.get("session") or created
    session_id = sess.get("id") or sess.get("session_id")
    assert session_id

    status, listed = http_json("GET", f"{base}/api/v1/terminal/sessions")
    assert status == 200
    ids = [s.get("id") for s in listed.get("sessions", [])]
    assert session_id in ids

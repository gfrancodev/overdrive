"""End to end on one machine: three real cores pair, share experience, chat, and remove a member.

Each core is a real `share listen` process with its own home, listener and dashboard API, driven through the same
local API the dashboard uses. A creates the circle, B pairs with A, and C pairs with B, so C's admission is signed by
a member that is not the creator and reaches A through the roster exchange.
"""

from __future__ import annotations

import json
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

import pytest

from test_share_runtime import free_port, make_repo, run, runtime_env

ROOT = Path(__file__).resolve().parents[1]
RUNTIME_DIR = ROOT / "runtime" / "experience"
REMOTE = "https://github.com/acme/three-core.git"


@pytest.fixture(scope="module")
def runtime_bin(tmp_path_factory: pytest.TempPathFactory) -> Path:
    go = shutil.which("go")
    if not go:
        pytest.skip("Go toolchain not available")
    out = tmp_path_factory.mktemp("runtime-bin-e2e") / "overdrive-runtime"
    subprocess.run([go, "build", "-buildvcs=false", "-o", str(out), "."], cwd=RUNTIME_DIR, check=True,
                   capture_output=True, text=True)
    return out


def api(core: dict, path: str, body: dict | None = None, method: str | None = None) -> dict:
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(core["base"] + path, data=data, method=method or ("GET" if body is None else "POST"),
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=15) as resp:
        return json.loads(resp.read() or b"{}")


def eventually(what: str, check, timeout: float = 30.0, every: float = 0.3):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        try:
            last = check()
            if last:
                return last
        except (OSError, urllib.error.URLError, KeyError, IndexError, ValueError) as err:
            last = err
        time.sleep(every)
    pytest.fail(f"{what} did not happen in {timeout}s (last: {last!r})")


@pytest.fixture
def three_cores(runtime_bin: Path):
    # A short root: terminal sockets live under each home and a Unix socket path stops at about 104 bytes.
    root = Path(tempfile.mkdtemp(prefix="od3-"))
    cores, procs = {}, []
    try:
        for name in ("a", "b", "c"):
            home = root / f"home-{name}"
            env = runtime_env(home)
            env["OVERDRIVE_SKIP_EMBED_DOWNLOAD"] = "1"
            port = free_port()
            env["OVERDRIVE_SHARE_LISTEN"] = f"127.0.0.1:{port}"
            repo = make_repo(root / f"repo-{name}", REMOTE)
            device = json.loads(run(str(runtime_bin), "share", "identity", env=env).stdout)["device_id"]
            procs.append(subprocess.Popen([str(runtime_bin), "share", "listen", "--cwd", str(repo)], env=env,
                                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL))
            cores[name] = {"name": name, "env": env, "home": home, "repo": repo, "device": device,
                           "listen": f"127.0.0.1:{port}", "base": f"http://127.0.0.1:{port + 1}"}
        for core in cores.values():
            eventually(f"core {core['name']} up", lambda c=core: api(c, "/api/core/ui"), timeout=20)
        yield cores
    finally:
        for proc in procs:
            proc.terminate()
        for proc in procs:
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
        shutil.rmtree(root, ignore_errors=True)


def session(core: dict, role: str, state: str) -> dict:
    return eventually(
        f"{core['name']} {role} session {state}",
        lambda: next((s for s in api(core, "/api/pairing/sessions")["sessions"]
                      if s["role"] == role and s["state"] == state), None),
        timeout=15,
    )


def pair(member: dict, joiner: dict, circle_id: str) -> None:
    api(joiner, "/api/pairing/sessions", {"device_id": member["device"], "endpoint": member["listen"]})
    m, j = session(member, "member", "comparing"), session(joiner, "joiner", "comparing")
    assert m["code"] == j["code"], "both screens must show the same code"
    api(member, f"/api/pairing/sessions/{m['id']}/confirm", {"circle_id": circle_id})
    api(joiner, f"/api/pairing/sessions/{j['id']}/confirm", {})
    session(joiner, "joiner", "joined")


def circle_view(core: dict, circle_id: str) -> dict:
    return next(c for c in api(core, "/api/circles")["circles"] if c["id"] == circle_id)


def members(core: dict, circle_id: str) -> dict[str, dict]:
    return {m["device_id"]: m for m in circle_view(core, circle_id)["members"]}


def circle_messages(core: dict, circle_id: str) -> list[str]:
    path = f"/api/chat/messages?mesh_id={circle_id}&conversation_id=group:circle"
    return [json.dumps(m) for m in api(core, path).get("messages", [])]


def cli(runtime_bin: Path, core: dict, *args: str) -> str:
    return run(str(runtime_bin), *args, env=core["env"]).stdout


def test_three_cores_pair_share_chat_and_remove_a_member(three_cores, runtime_bin):
    a, b, c = three_cores["a"], three_cores["b"], three_cores["c"]
    circle_id = api(a, "/api/circles", {"name": "Squad"})["circle"]["id"]

    # Pairing: B through A, the creator; C through B, a member that is not the creator.
    pair(a, b, circle_id)
    pair(b, c, circle_id)
    assert members(c, circle_id)[c["device"]]["invited_by"] == b["device"]
    for core in (a, b, c):
        api(core, f"/api/circles/{circle_id}/folders", {"path": str(core["repo"])})

    # A learns of C from B's signed admission when they trade rosters after a sync.
    def a_admits_c():
        cli(runtime_bin, a, "session-start", "--cwd", str(a["repo"]), "--quiet")
        return c["device"] in members(a, circle_id)
    eventually("A learning of C through B", a_admits_c, timeout=40, every=2)

    # Experience: a lesson recorded on A reaches C, who never talked to A before.
    cli(runtime_bin, a, "record", "--cwd", str(a["repo"]), "--kind", "lesson",
        "--subject", "ThreeCoreLedger EPIPE on retry",
        "--content", "Use ThreeCoreLedger.flush before retrying a transfer.",
        "--confidence", "0.95", "--source", "verified_execution",
        "--evidence", "src/ledger/three_core_ledger.go integration tests failed with EPIPE")

    def c_recalls_a():
        cli(runtime_bin, c, "session-start", "--cwd", str(c["repo"]), "--quiet")
        got = json.loads(cli(runtime_bin, c, "recall", "--cwd", str(c["repo"]),
                             "--query", "ThreeCoreLedger EPIPE three_core_ledger.go retry"))
        return any("ThreeCoreLedger.flush" in m.get("content", "") for m in got.get("peer_memories", []))
    eventually("C recalling A's lesson", c_recalls_a, timeout=40, every=2)

    # Health: every core reaches the two others for every capability, on one chat. C could not reach A's chat
    # when it joined (A did not know it yet) and hosted its own; it moves back once A answers.
    for core in (a, b, c):
        def healthy(core=core):
            h = api(core, "/api/connections/health")
            return h if h["state"] == "connected" and len(h["peers"]) == 2 else None
        h = eventually(f"{core['name']} healthy", healthy, timeout=90, every=1)
        assert all(p["reach"] == "ok" for p in h["peers"]), h["peers"]
    hosts = {core["name"]: api(core, "/api/diagnostics")["circles"][0]["chat"]["host_id"] for core in (a, b, c)}
    assert set(hosts.values()) == {a["device"]}, f"the circle's chat is split: {hosts}"

    # Chat: one message to the whole circle reaches the other two.
    api(a, "/api/chat/messages", {"mesh_id": circle_id, "conversation_id": "group:circle", "type": "text",
                                  "text": "three-core hello"})
    for core in (b, c):
        eventually(f"{core['name']} receiving A's message",
                   lambda core=core: any("three-core hello" in m for m in circle_messages(core, circle_id)))

    # Removal: A removes C. The key rotates, B gets the new key, C is out.
    preview = api(a, f"/api/circles/{circle_id}/members/{c['device']}/revoke")
    assert preview["targets"] == [c["device"]] and preview["next_epoch"] == 1 and preview["cut_off"] == []
    done = api(a, f"/api/circles/{circle_id}/members/{c['device']}/revoke", {"with_invitees": False})
    assert done["revoked"] == [c["device"]] and done["circle"]["key_epoch"] == 1
    eventually("B on the new key", lambda: circle_view(b, circle_id)["key_epoch"] == 1)
    eventually("B seeing C removed", lambda: members(b, circle_id)[c["device"]]["revoked"])

    api(b, "/api/chat/messages", {"mesh_id": circle_id, "conversation_id": "group:circle", "type": "text",
                                  "text": "after the removal"})
    eventually("A receiving B's message on the new key",
               lambda: any("after the removal" in m for m in circle_messages(a, circle_id)))
    time.sleep(2)
    assert not any("after the removal" in m for m in circle_messages(c, circle_id)), "a removed device kept reading"

    # Diagnostics: no core hands out a circle key, old or new.
    keys = set()
    for core in (a, b, c):
        for path in (core["home"] / "share" / "circles").glob("*.json"):
            data = json.loads(path.read_text())
            keys.add(data.get("group_key", ""))
            keys.add((data.get("previous_key") or {}).get("key", ""))
    keys.discard("")
    assert keys
    for core in (a, b, c):
        report = json.dumps(api(core, "/api/diagnostics"))
        assert not any(k in report for k in keys), f"{core['name']} diagnostics carry a circle key"

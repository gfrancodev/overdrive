#!/usr/bin/env python3
"""Checks three Overdrive hosts against docs/CROSS_MACHINE_RUNBOOK.md, through the dashboard's local API.

Usage: check.py <host a> <host b> <host c>  (Docker container names; scripts/cross-host/run.sh starts them)

A creates the circle, B pairs with A and C pairs with B. The numbers match the runbook's steps; the ones a script
cannot do (installing by hand, calls, shared terminals, the internet unplugged) are left to real machines.
Prints a table and exits non-zero when a step fails.
"""

from __future__ import annotations

import json
import subprocess
import sys
import time

if len(sys.argv) != 4:
    sys.exit(__doc__)
HOSTS = {"a": sys.argv[1], "b": sys.argv[2], "c": sys.argv[3]}
results: list[tuple[str, str, str, float, str]] = []
state: dict = {}


def sh(c: str, cmd: str) -> str:
    out = subprocess.run(["docker", "exec", HOSTS[c], "su", "-", "dev", "-c", cmd], capture_output=True, text=True)
    if out.returncode != 0:
        raise RuntimeError(f"{c}: {cmd}: {out.stderr.strip()}")
    return out.stdout


def api(c: str, path: str, body: dict | None = None) -> dict:
    method = "GET" if body is None else "POST"
    data = "" if body is None else f" -H 'Content-Type: application/json' -d '{json.dumps(body)}'"
    raw = sh(c, f"curl -s -X {method}{data} 'http://127.0.0.1:7742{path}'")
    try:
        return json.loads(raw or "{}")
    except json.JSONDecodeError:
        return {"raw": raw}


def wait(what: str, check, timeout: float = 60, every: float = 1.0):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        try:
            last = check()
            if last:
                return last
        except Exception as err:  # noqa: BLE001 - a host still starting answers with anything
            last = err
        time.sleep(every)
    raise AssertionError(f"{what}: not within {timeout:.0f}s (last: {str(last)[:200]})")


def step(n: str, title: str):
    def run(fn):
        start = time.time()
        try:
            note = fn() or ""
            results.append((n, title, "pass", time.time() - start, note))
        except Exception as err:  # noqa: BLE001
            results.append((n, title, "FAIL", time.time() - start, str(err)[:300]))
        print(f"[{results[-1][2]}] {n}. {title}", flush=True)
        return fn
    return run


def restart_core(c: str, delay: int = 0) -> None:
    sh(c, "curl -s -X POST http://127.0.0.1:7742/api/core/stop >/dev/null; "
          f"(sleep {delay + 2}; cd ~ && OVERDRIVE_SHARE_LISTEN=127.0.0.1:7741 OVERDRIVE_SHARE_UI=127.0.0.1:7742 "
          "nohup ~/.overdrive/bin/overdrive-runtime share listen --cwd ~ >> ~/core.log 2>&1 &) >/dev/null 2>&1 &")


def circle_texts(c: str) -> str:
    return json.dumps(api(c, f"/api/chat/messages?mesh_id={state['circle']}&conversation_id=group:circle").get("messages", []))


def post(c: str, text: str, **extra) -> dict:
    body = {"mesh_id": state["circle"], "type": "text", "text": text, **extra}
    if "recipient" not in extra:
        body["conversation_id"] = "group:circle"
    return api(c, "/api/chat/messages", body)


for c in HOSTS:
    wait(f"{c} Core up", lambda c=c: api(c, "/api/network").get("listening"), timeout=60)
ids = {c: json.loads(sh(c, "~/.overdrive/bin/overdrive-runtime share identity"))["device_id"] for c in HOSTS}
t0 = time.time()


@step("2a", "Before the network is allowed, pairing names the cause and the fix")
def _():
    wait("B sees A nearby", lambda: any(p["device_id"] == ids["a"] for p in api("b", "/api/discovery/peers").get("peers", [])), timeout=30)
    out = api("b", "/api/pairing/sessions", {"device_id": ids["a"]})
    assert "accepts connections only from itself" in out.get("error", ""), out
    return out["error"]


@step("2", "Allow devices on this network, on every host")
def _():
    for c in HOSTS:
        v = api(c, "/api/network", {"lan_access": True})
        assert v.get("lan_access") and v["listen"] == "0.0.0.0:7741", v
    return "; ".join(f"{c} {api(c, '/api/network')['address']}" for c in HOSTS)


@step("3", "A creates a circle")
def _():
    state["circle"] = api("a", "/api/circles", {"name": "Cross host"})["circle"]["id"]


def session(c: str, role: str, st: str) -> dict:
    return wait(f"{c} {role} {st}", lambda: next((s for s in api(c, "/api/pairing/sessions")["sessions"]
                                               if s["role"] == role and s["state"] == st), None), timeout=20, every=0.3)


def pair(member: str, joiner: str) -> str:
    wait(f"{joiner} sees {member} reachable", lambda: any(
        p["device_id"] == ids[member] and not p.get("local_only")
        for p in api(joiner, "/api/discovery/peers").get("peers", [])), timeout=30)
    started = api(joiner, "/api/pairing/sessions", {"device_id": ids[member]})
    assert "session" in started, started
    m, j = session(member, "member", "comparing"), session(joiner, "joiner", "comparing")
    assert m["code"] == j["code"], (m["code"], j["code"])
    api(member, f"/api/pairing/sessions/{m['id']}/confirm", {"circle_id": state["circle"]})
    api(joiner, f"/api/pairing/sessions/{j['id']}/confirm", {})
    session(joiner, "joiner", "joined")
    return f"both screens showed {m['code']}"


@step("4", "B pairs with A from Nearby")
def _():
    return pair("a", "b")


@step("5", "C pairs with B; A learns of C, invited by B")
def _():
    note = pair("b", "c")
    me = next(m for m in api("c", "/api/circles")["circles"][0]["members"] if m["device_id"] == ids["c"])
    assert me.get("invited_by") == ids["b"], me
    wait("A lists C", lambda: any(m["device_id"] == ids["c"] for m in api("a", "/api/circles")["circles"][0]["members"]), timeout=120, every=3)
    return note


@step("6", "Every host shares the project folder")
def _():
    for c in HOSTS:
        assert len(api(c, f"/api/circles/{state['circle']}/folders", {"path": "~/proj"}).get("folders", [])) == 1


@step("7", "The Connection sheet says connected everywhere")
def _():
    for c in HOSTS:
        wait(f"{c} connected", lambda c=c: (lambda h: h.get("state") == "connected" and len(h.get("peers", [])) == 2)(api(c, "/api/connections/health")), timeout=120, every=2)
    return f"first collaboration ready {time.time() - t0:.0f}s after the network was allowed"


@step("8", "A lesson recorded on A reaches C's recall")
def _():
    sh("a", "cd ~/proj && ~/.overdrive/bin/overdrive-runtime record --kind lesson --subject 'CrossHostLedger EPIPE' "
            "--content 'Use CrossHostLedger.flush before retrying.' --confidence 0.95 --source verified_execution "
            "--evidence 'src/ledger/cross_host_ledger.go failed with EPIPE' >/dev/null")

    def recalled():
        sh("c", "cd ~/proj && ~/.overdrive/bin/overdrive-runtime session-start --cwd . --quiet >/dev/null")
        got = json.loads(sh("c", "cd ~/proj && ~/.overdrive/bin/overdrive-runtime recall --query 'CrossHostLedger EPIPE cross_host_ledger.go'"))
        return any("CrossHostLedger.flush" in m.get("content", "") for m in got.get("peer_memories", []))
    wait("C recalls A's lesson", recalled, timeout=90, every=5)


@step("9", "A circle message reaches everyone; a direct one only its recipient")
def _():
    post("a", "cross-host hello")
    for c in ("b", "c"):
        wait(f"{c} gets A's message", lambda c=c: "cross-host hello" in circle_texts(c), timeout=60)
    post("b", "only for C", recipient=ids["c"])
    conv = "direct:" + ":".join(sorted([ids["b"], ids["c"]]))
    wait("C gets B's direct message", lambda: "only for C" in json.dumps(api("c", f"/api/chat/messages?mesh_id={state['circle']}&conversation_id={conv}")), timeout=60)
    time.sleep(2)
    assert "only for C" not in json.dumps(api("a", f"/api/chat/conversations?mesh_id={state['circle']}")), "A saw B's direct message to C"


@step("13", "Restarting A's Core keeps an open terminal and the network choice")
def _():
    sid = api("a", "/api/v1/terminal/sessions", {"name": "cross-host", "working_directory": "", "cols": 80, "rows": 24}).get("session", {}).get("id")
    assert sid
    wait("terminal running", lambda: api("a", f"/api/v1/terminal/sessions/{sid}").get("session", {}).get("state") == "RUNNING", timeout=20)
    restart_core("a")
    wait("A back", lambda: api("a", "/api/network").get("listening"), timeout=30)
    st = wait("session state", lambda: api("a", f"/api/v1/terminal/sessions/{sid}").get("session", {}).get("state"), timeout=20)
    assert st == "RUNNING", st
    assert api("a", "/api/network")["lan_access"], "the network choice must survive a restart"


@step("13b", "A message sent while A's Core restarts reaches A afterwards")
def _():
    restart_core("a", delay=2)
    time.sleep(2)
    sent = post("b", "while A restarts")
    start = time.time()
    wait("A gets it", lambda: "while A restarts" in circle_texts("a"), timeout=120, every=2)
    return f"queued={sent.get('queued')}, arrived {time.time() - start:.0f}s after sending"


@step("14", "A removes C after reading the preview")
def _():
    preview = api("a", f"/api/circles/{state['circle']}/members/{ids['c']}/revoke")
    assert preview.get("targets") == [ids["c"]] and preview.get("cut_off") == [], preview
    assert api("a", f"/api/circles/{state['circle']}/members/{ids['c']}/revoke", {"with_invitees": False}).get("circle", {}).get("key_epoch") == 1


@step("15", "B gets the new key and sees C removed")
def _():
    start = time.time()
    wait("B updated", lambda: (lambda cv: cv["key_epoch"] == 1 and any(m["device_id"] == ids["c"] and m["revoked"] for m in cv["members"]))(api("b", "/api/circles")["circles"][0]), timeout=30, every=0.5)
    return f"{time.time() - start:.1f}s"


@step("16", "C is no longer heard and no longer reads")
def _():
    post("c", "from removed C")
    post("b", "after removal")
    wait("A gets B's message", lambda: "after removal" in circle_texts("a"), timeout=60)
    time.sleep(3)
    assert "from removed C" not in circle_texts("a") + circle_texts("b"), "a removed device was heard"
    assert "after removal" not in circle_texts("c"), "a removed device kept reading"


@step("17", "B closes its network: A names B and the fix, chat stays up")
def _():
    api("b", "/api/network", {"lan_access": False})

    def explained():
        h = api("a", "/api/connections/health")
        b = next((p for p in h.get("peers", []) if p["device_id"] == ids["b"]), {})
        chat = next(x for x in h["capabilities"] if x["id"] == "chat")
        return (b, chat) if b.get("reach") == "refused" and b.get("local_only") else None
    b, chat = wait("A sees B local-only", explained, timeout=60, every=2)
    api("b", "/api/network", {"lan_access": True})
    return f"B {b['reach']}, accepts only itself; chat {chat['state']}"


@step("18", "No diagnostics carry a circle key or the project path")
def _():
    keys = set()
    for c in HOSTS:
        raw = sh(c, 'for f in ~/.overdrive/share/circles/*.json; do cat "$f"; echo "<<SPLIT>>"; done')
        for chunk in raw.split("<<SPLIT>>"):
            if chunk.strip():
                d = json.loads(chunk)
                keys.update(k for k in (d.get("group_key", ""), (d.get("previous_key") or {}).get("key", "")) if k)
    for c in HOSTS:
        report = json.dumps(api(c, "/api/diagnostics"))
        assert not any(k in report for k in keys) and "/home/dev/proj" not in report, c
    return f"{len(keys)} keys looked for"


print()
print("| Step | Check | Result | Time | Notes |")
print("|---|---|---|---|---|")
for n, title, res, dur, note in results:
    print(f"| {n} | {title} | {res} | {dur:.1f}s | {note.replace('|', '/')} |")
sys.exit(0 if all(r[2] == "pass" for r in results) else 1)

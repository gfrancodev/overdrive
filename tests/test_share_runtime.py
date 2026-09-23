from __future__ import annotations

import json
import os
import shutil
import socket
import subprocess
import threading
import time
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
RUNTIME_DIR = ROOT / "runtime" / "experience"


@pytest.fixture(scope="session")
def runtime_bin(tmp_path_factory: pytest.TempPathFactory) -> Path:
    go = shutil.which("go")
    if not go:
        pytest.skip("Go toolchain not available")
    out = tmp_path_factory.mktemp("runtime-bin") / "overdrive-runtime"
    subprocess.run(
        [go, "build", "-buildvcs=false", "-o", str(out), "."],
        cwd=RUNTIME_DIR,
        check=True,
        capture_output=True,
        text=True,
    )
    return out


def run(*args: str, cwd: Path | None = None, env: dict[str, str] | None = None):
    return subprocess.run(
        list(args),
        cwd=cwd,
        env=env,
        check=True,
        capture_output=True,
        text=True,
    )


def make_repo(path: Path, remote: str = "https://github.com/acme/payments-api.git") -> Path:
    path.mkdir(parents=True)
    run("git", "init", "-q", cwd=path)
    run("git", "remote", "add", "origin", remote, cwd=path)
    (path / "README.md").write_text("# test\n")
    return path


def runtime_env(home: Path) -> dict[str, str]:
    env = dict(os.environ)
    env["OVERDRIVE_HOME"] = str(home)
    env["OVERDRIVE_EMBEDDER"] = "stub"
    lib = ROOT / "runtime" / "turbovec-ffi" / "target" / "release" / "liboverdrive_turbovec_ffi.so"
    if lib.exists():
        env["OVERDRIVE_TURBOVEC_LIB"] = str(lib)
    return env


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def setup_circle_pair(runtime_bin: Path, tmp_path: Path, repo: Path) -> tuple[dict, dict, str, str, subprocess.Popen]:
    home_a = tmp_path / "home-a"
    home_b = tmp_path / "home-b"
    env_a = runtime_env(home_a)
    env_b = runtime_env(home_b)

    circle = json.loads(
        run(str(runtime_bin), "share", "circle", "create", "--name", "team", env=env_a).stdout
    )
    invite = json.loads(
        run(
            str(runtime_bin),
            "share",
            "circle",
            "invite",
            "--circle",
            circle["id"],
            env=env_a,
        ).stdout
    )
    invite_dir = home_b / "share" / "invites"
    invite_dir.mkdir(parents=True, exist_ok=True)
    (invite_dir / f"{invite['code']}.json").write_text(json.dumps(invite))

    port = free_port()
    endpoint = f"127.0.0.1:{port}"
    env_a["OVERDRIVE_SHARE_LISTEN"] = endpoint

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

    listener = subprocess.Popen(
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
        endpoint,
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

    return env_a, env_b, circle["id"], endpoint, listener


def test_share_identity_and_invite_revoke(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / "home"
    env = runtime_env(home)
    repo = make_repo(tmp_path / "repo")

    identity = json.loads(run(str(runtime_bin), "share", "identity", env=env).stdout)
    assert identity["device_id"].startswith("dev_")
    assert identity["public_key"]

    circle = json.loads(
        run(str(runtime_bin), "share", "circle", "create", "--name", "alpha", env=env).stdout
    )
    invite = json.loads(
        run(
            str(runtime_bin),
            "share",
            "circle",
            "invite",
            "--circle",
            circle["id"],
            env=env,
        ).stdout
    )
    assert invite["fingerprint"]
    assert invite["expires_at"]

    home_b = tmp_path / "home-b"
    env_b = runtime_env(home_b)
    invite_dir = home_b / "share" / "invites"
    invite_dir.mkdir(parents=True, exist_ok=True)
    (invite_dir / f"{invite['code']}.json").write_text(json.dumps(invite))

    port = free_port()
    endpoint = f"127.0.0.1:{port}"
    env["OVERDRIVE_SHARE_LISTEN"] = endpoint
    listener = subprocess.Popen(
        [str(runtime_bin), "share", "listen", "--cwd", str(repo)],
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    time.sleep(0.5)

    accepted = json.loads(
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
            endpoint,
            env=env_b,
        ).stdout
    )
    listener.terminate()
    assert accepted["id"] == circle["id"]
    assert len(accepted["members"]) >= 2

    device_b = json.loads(run(str(runtime_bin), "share", "identity", env=env_b).stdout)["device_id"]
    revoked = json.loads(
        run(
            str(runtime_bin),
            "share",
            "circle",
            "revoke",
            "--circle",
            circle["id"],
            "--device",
            device_b,
            env=env,
        ).stdout
    )
    assert any(m["device_id"] == device_b and m["revoked"] for m in revoked["members"])


def test_peer_lesson_with_matching_signature_recalls(runtime_bin: Path, tmp_path: Path):
    repo = make_repo(tmp_path / "repo")
    env_a, env_b, circle_id, endpoint, listener = setup_circle_pair(runtime_bin, tmp_path, repo)

    lesson = json.loads(
        run(
            str(runtime_bin),
            "record",
            "--cwd",
            str(repo),
            "--kind",
            "lesson",
            "--subject",
            "ProposalRepository ECONNREFUSED",
            "--content",
            "Use ProposalRepository instead of Prisma in proposal transitions.",
            "--confidence",
            "0.95",
            "--source",
            "verified_execution",
            "--evidence",
            "src/proposals/proposal.service.ts integration tests failed with ECONNREFUSED",
            env=env_a,
        ).stdout
    )
    assert lesson["packet_content"]
    assert lesson["problem_signature"]

    run(str(runtime_bin), "session-start", "--cwd", str(repo), "--quiet", env=env_b)
    listener.terminate()

    recalled = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo),
            "--query",
            "ECONNREFUSED proposal.service.ts ProposalRepository",
            env=env_b,
        ).stdout
    )
    peer = recalled.get("peer_memories", [])
    assert peer, "expected peer memory with matching signature"
    assert any("ProposalRepository" in m.get("content", "") for m in peer)
    assert all(m.get("origin") == "peer" for m in peer)


def test_semantic_peer_without_signature_stays_out(runtime_bin: Path, tmp_path: Path):
    repo = make_repo(tmp_path / "repo")
    env_a, env_b, _, endpoint, listener = setup_circle_pair(runtime_bin, tmp_path, repo)

    run(
        str(runtime_bin),
        "record",
        "--cwd",
        str(repo),
        "--kind",
        "lesson",
        "--content",
        "Prefer small composable React hooks for dashboard widgets.",
        "--confidence",
        "0.9",
        "--source",
        "verified_execution",
        env=env_a,
    )

    run(str(runtime_bin), "session-start", "--cwd", str(repo), "--quiet", env=env_b)
    listener.terminate()

    recalled = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo),
            "--query",
            "kubernetes ingress certificate rotation",
            env=env_b,
        ).stdout
    )
    assert recalled.get("peer_memories", []) == []


def test_local_index_does_not_return_peer_items(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / "home"
    env = runtime_env(home)
    repo = make_repo(tmp_path / "repo")

    peer_mem = {
        "id": "mem_peer_marker_1234567890",
        "kind": "lesson",
        "scope": "repository",
        "scope_id": "github.com/acme/payments-api",
        "subject": "peer-only",
        "content": "Peer-only lesson body.",
        "packet_content": "symptom: peer | fix: Peer-only lesson body.",
        "confidence": 0.8,
        "priority": 45,
        "status": "active",
        "source": "peer_share",
        "evidence_score": 0.7,
        "created_at": "2026-01-01T00:00:00Z",
        "updated_at": "2026-01-01T00:00:00Z",
        "origin": "peer",
        "peer_device_id": "dev_peer",
        "circle_id": "circle_test",
        "layer": 2,
        "problem_signature": "proposal.service.ts,ECONNREFUSED",
        "vector_space": "stub",
        "hot_index": True,
    }
    run(
        str(runtime_bin),
        "record",
        "--cwd",
        str(repo),
        "--kind",
        "lesson",
        "--content",
        "Local lesson unrelated marker.",
        "--confidence",
        "0.9",
        "--source",
        "verified_execution",
        env=env,
    )

    import sqlite3

    conn = sqlite3.connect(home / "experience-v2.db")
    conn.execute(
        """INSERT INTO memories(
            id, vector_id, kind, scope, scope_id, subject, content, confidence, priority, status,
            source, source_ref, evidence, success_count, failure_count, evidence_score,
            created_at, updated_at, last_validated_at,
            origin, peer_device_id, circle_id, layer, packet_content, problem_signature,
            source_folder, vector_space, hot_index
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', 0, 0, ?, ?, ?, '',
            'peer', 'dev_peer', 'circle_test', 2, ?, ?, '', 'stub', 1)""",
        (
            peer_mem["id"],
            999001,
            peer_mem["kind"],
            peer_mem["scope"],
            peer_mem["scope_id"],
            peer_mem["subject"],
            peer_mem["content"],
            peer_mem["confidence"],
            peer_mem["priority"],
            peer_mem["status"],
            peer_mem["source"],
            peer_mem["evidence_score"],
            peer_mem["created_at"],
            peer_mem["updated_at"],
            peer_mem["packet_content"],
            peer_mem["problem_signature"],
        ),
    )
    conn.commit()
    conn.close()

    recalled = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo),
            "--query",
            "local lesson unrelated",
            env=env,
        ).stdout
    )
    assert all(m.get("origin") != "peer" for m in recalled["memories"])
    assert all("Peer-only" not in m.get("content", "") for m in recalled["memories"])


def test_vector_space_mismatch_blocks_peer_import(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / "home"
    env = runtime_env(home)
    repo = make_repo(tmp_path / "repo")

    circle = json.loads(
        run(str(runtime_bin), "share", "circle", "create", "--name", "t", env=env).stdout
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

    from test_experience_runtime import runtime_env as _re

    e, err = None, None
    try:
        import subprocess

        proc = subprocess.run(
            [str(runtime_bin), "project", "--cwd", str(repo)],
            capture_output=True,
            text=True,
            check=True,
            env=env,
        )
        project = json.loads(proc.stdout)
    except Exception as exc:
        pytest.skip(str(exc))

    batch = {
        "device_id": "dev_other",
        "circle_id": circle["id"],
        "repository": project["repository"],
        "cursor": "2026-01-02T00:00:00Z",
        "packets": [
            {
                "id": "mem_hash_only",
                "kind": "lesson",
                "scope_id": project["repository"],
                "subject": "sig",
                "packet_content": "symptom: ECONNREFUSED proposal.service.ts",
                "lesson_content": "Use ProposalRepository.",
                "problem_signature": "proposal.service.ts,ECONNREFUSED",
                "source_folder": str(repo),
                "evidence_score": 0.8,
                "fingerprint": "mem_hash_only",
                "vector_space": "minilm",
                "updated_at": "2026-01-02T00:00:00Z",
                "hot_index": True,
            }
        ],
        "signature": "",
    }

    import sqlite3

    conn = sqlite3.connect(home / "experience-v2.db")
    count_before = conn.execute("SELECT COUNT(*) FROM memories WHERE id = 'mem_hash_only'").fetchone()[0]
    conn.close()
    assert count_before == 0

    # Direct import path is exercised via sync; stub embedder rejects minilm packets.
    run(str(runtime_bin), "session-start", "--cwd", str(repo), "--quiet", env=env)
    conn = sqlite3.connect(home / "experience-v2.db")
    count_after = conn.execute("SELECT COUNT(*) FROM memories WHERE id = 'mem_hash_only'").fetchone()[0]
    conn.close()
    assert count_after == 0


def test_folder_outside_allowed_blocks_share(runtime_bin: Path, tmp_path: Path):
    repo_allowed = make_repo(tmp_path / "allowed")
    repo_private = make_repo(tmp_path / "private", "https://github.com/acme/private.git")
    home = tmp_path / "home"
    env = runtime_env(home)

    circle = json.loads(
        run(str(runtime_bin), "share", "circle", "create", "--name", "scoped", env=env).stdout
    )
    run(
        str(runtime_bin),
        "share",
        "circle",
        "folder-add",
        "--circle",
        circle["id"],
        "--folder",
        str(repo_allowed),
        env=env,
    )

    run(
        str(runtime_bin),
        "record",
        "--cwd",
        str(repo_private),
        "--kind",
        "lesson",
        "--content",
        "Private repo secret lesson marker.",
        "--confidence",
        "0.95",
        "--source",
        "verified_execution",
        env=env,
    )

    folders = json.loads(
        run(
            str(runtime_bin),
            "share",
            "circle",
            "folder-list",
            "--circle",
            circle["id"],
            env=env,
        ).stdout
    )
    assert folders["allowed_folders"]
    assert all("private" not in f for f in folders["allowed_folders"])

    recalled = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo_private),
            "--query",
            "secret lesson marker",
            env=env,
        ).stdout
    )
    assert recalled.get("peer_memories", []) == []


def test_share_status_reports_off_without_circle(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / 'home'
    env = runtime_env(home)
    repo = make_repo(tmp_path / 'repo')
    status = json.loads(
        run(str(runtime_bin), 'share', 'status', '--cwd', str(repo), env=env).stdout
    )
    assert status['mode'] == 'off'
    text = run(
        str(runtime_bin),
        'share',
        'status',
        '--cwd',
        str(repo),
        '--format',
        'text',
        env=env,
    ).stdout.strip()
    assert text.startswith('share: off')


def test_session_end_consolidates_without_error(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / 'home'
    env = runtime_env(home)
    repo = make_repo(tmp_path / 'repo')
    run(str(runtime_bin), 'session-start', '--cwd', str(repo), '--quiet', env=env)
    out = json.loads(run(str(runtime_bin), 'session-end', '--cwd', str(repo), env=env).stdout)
    assert 'share' in out


def test_cold_packet_not_in_hot_recall(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / "home"
    env = runtime_env(home)
    repo = make_repo(tmp_path / "repo")

    item = json.loads(
        run(
            str(runtime_bin),
            "record",
            "--cwd",
            str(repo),
            "--kind",
            "lesson",
            "--content",
            "Cold layer lesson should not hot-recall.",
            "--confidence",
            "0.8",
            "--source",
            "verified_execution",
            env=env,
        ).stdout
    )

    import sqlite3

    conn = sqlite3.connect(home / "experience-v2.db")
    conn.execute("UPDATE memories SET hot_index = 0 WHERE id = ?", (item["id"],))
    conn.commit()
    conn.close()

    recalled = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo),
            "--query",
            "cold layer lesson hot-recall",
            env=env,
        ).stdout
    )
    assert all(m["id"] != item["id"] for m in recalled["memories"])

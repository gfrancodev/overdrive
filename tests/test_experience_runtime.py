from __future__ import annotations

import json
import os
import platform
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
RUNTIME_DIR = ROOT / "runtime" / "experience"
PLUGIN_VERSION = json.loads((ROOT / ".cursor-plugin" / "plugin.json").read_text())["version"]


def run(*args: str, cwd: Path | None = None, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        list(args),
        cwd=cwd,
        env=env,
        check=True,
        capture_output=True,
        text=True,
    )


@pytest.fixture(scope="session")
def runtime_bin(tmp_path_factory: pytest.TempPathFactory) -> Path:
    go = shutil.which("go")
    if not go:
        pytest.skip("Go toolchain not available")
    out = tmp_path_factory.mktemp("runtime-bin") / "overdrive-runtime"
    run(go, "build", "-buildvcs=false", "-o", str(out), ".", cwd=RUNTIME_DIR)
    return out


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


def test_status_reports_sqlite_turbovec_backend(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / "home"
    env = runtime_env(home)
    data = json.loads(run(str(runtime_bin), "status", env=env).stdout)
    assert data["version"] == PLUGIN_VERSION
    assert data["backend"] == "sqlite-fts5-turbovec-v1"
    assert data["store"].endswith("experience-v2.db")
    assert data["embedder"] == "stub"
    assert data["embedder_dim"] == 256


def test_json_v1_migrates_to_sqlite(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / "home"
    home.mkdir()
    legacy = {
        "version": 1,
        "memories": [
            {
                "id": "mem-legacy-1",
                "kind": "fact",
                "scope": "global",
                "scope_id": "",
                "subject": "",
                "content": "Legacy migration marker fact.",
                "confidence": 0.9,
                "priority": 5,
                "status": "active",
                "source": "user_feedback",
                "evidence": "",
                "created_at": "2026-01-01T00:00:00Z",
                "updated_at": "2026-01-01T00:00:00Z",
            }
        ],
    }
    (home / "experience-v1.json").write_text(json.dumps(legacy))
    repo = make_repo(tmp_path / "repo")
    env = runtime_env(home)
    data = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo),
            "--query",
            "legacy migration marker",
            env=env,
        ).stdout
    )
    assert any("Legacy migration marker" in m["content"] for m in data["memories"])
    assert (home / "experience-v2.db").exists()


def test_ledger_add_and_list(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / "home"
    repo = make_repo(tmp_path / "repo")
    env = runtime_env(home)
    added = json.loads(
        run(
            str(runtime_bin),
            "ledger-add",
            "--cwd",
            str(repo),
            "--run",
            "run-alpha",
            "--decision",
            "Keep repository boundary",
            "--evidence",
            "ADR-1",
            "--reason",
            "Matches architecture",
            "--risk",
            "Low",
            "--reversibility",
            "Single PR revert",
            env=env,
        ).stdout
    )
    assert added["decision"] == "Keep repository boundary"
    listed = json.loads(
        run(
            str(runtime_bin),
            "ledger-list",
            "--cwd",
            str(repo),
            "--run",
            "run-alpha",
            env=env,
        ).stdout
    )
    assert len(listed["entries"]) == 1
    ledger_md = home / "runs" / "run-alpha" / "ledger.md"
    assert ledger_md.exists()
    assert "Keep repository boundary" in ledger_md.read_text()


def test_runtime_uses_embedded_sqlite_without_cgo():
    assert (RUNTIME_DIR / "go.mod").exists()
    text = (RUNTIME_DIR / "go.mod").read_text()
    assert "module" in text
    assert "modernc.org/sqlite" in text


def test_project_identity_prefers_git_remote(runtime_bin: Path, tmp_path: Path):
    repo = make_repo(tmp_path / "repo")
    out = run(str(runtime_bin), "project", "--cwd", str(repo))
    data = json.loads(out.stdout)
    assert data["repository"] == "github.com/acme/payments-api"
    assert data["organization"] == "github.com/acme"
    assert Path(data["root"]) == repo


def test_record_and_recall_repository_memory(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / "home"
    repo = make_repo(tmp_path / "repo")
    env = runtime_env(home)

    recorded = run(
        str(runtime_bin),
        "record",
        "--cwd",
        str(repo),
        "--kind",
        "anti_pattern",
        "--subject",
        "persistence",
        "--content",
        "Do not access Prisma directly in proposal state transitions; use ProposalRepository.",
        "--confidence",
        "0.96",
        "--source",
        "verified_execution",
        "--evidence",
        "integration tests passed after repository boundary was restored",
        env=env,
    )
    saved = json.loads(recorded.stdout)
    assert saved["id"]
    assert saved["scope"] == "repository"

    recalled = run(
        str(runtime_bin),
        "recall",
        "--cwd",
        str(repo),
        "--query",
        "change proposal persistence with prisma",
        "--limit",
        "5",
        env=env,
    )
    data = json.loads(recalled.stdout)
    assert data["project"]["repository"] == "github.com/acme/payments-api"
    assert data["memories"]
    assert "ProposalRepository" in data["memories"][0]["content"]


def test_repository_memories_do_not_leak_to_other_repository(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / "home"
    env = runtime_env(home)
    repo_a = make_repo(tmp_path / "a", "https://github.com/acme/a.git")
    repo_b = make_repo(tmp_path / "b", "https://github.com/acme/b.git")

    run(
        str(runtime_bin),
        "record",
        "--cwd",
        str(repo_a),
        "--kind",
        "rule",
        "--content",
        "Repository A must use AlphaFactory.",
        "--confidence",
        "1.0",
        "--source",
        "user_feedback",
        env=env,
    )

    data = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo_b),
            "--query",
            "factory",
            env=env,
        ).stdout
    )
    assert all("AlphaFactory" not in m["content"] for m in data["memories"])


def test_global_memory_is_available_across_repositories(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / "home"
    env = runtime_env(home)
    repo_a = make_repo(tmp_path / "a", "https://github.com/acme/a.git")
    repo_b = make_repo(tmp_path / "b", "https://github.com/other/b.git")

    run(
        str(runtime_bin),
        "record",
        "--cwd",
        str(repo_a),
        "--kind",
        "preference",
        "--scope",
        "global",
        "--content",
        "Prefer existing abstractions before creating new ones.",
        "--confidence",
        "0.95",
        "--source",
        "user_feedback",
        env=env,
    )

    data = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo_b),
            "--query",
            "create abstraction",
            env=env,
        ).stdout
    )
    assert any("existing abstractions" in m["content"] for m in data["memories"])


def test_contradiction_deprecates_memory(runtime_bin: Path, tmp_path: Path):
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
            "fact",
            "--content",
            "The persistence layer uses TypeORM.",
            "--confidence",
            "0.9",
            "--source",
            "repository_observation",
            env=env,
        ).stdout
    )

    updated = json.loads(
        run(
            str(runtime_bin),
            "validate",
            "--id",
            item["id"],
            "--result",
            "contradiction",
            env=env,
        ).stdout
    )
    assert updated["status"] == "deprecated"

    data = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo),
            "--query",
            "typeorm persistence",
            env=env,
        ).stdout
    )
    assert all(m["id"] != item["id"] for m in data["memories"])


def test_repeated_record_deduplicates_and_strengthens_memory(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / "home"
    env = runtime_env(home)
    repo = make_repo(tmp_path / "repo")
    args = [
        str(runtime_bin), "record", "--cwd", str(repo),
        "--kind", "lesson",
        "--subject", "testing",
        "--content", "Use CustomerFactory for customer integration tests.",
        "--confidence", "0.8",
        "--source", "verified_execution",
    ]
    first = json.loads(run(*args, env=env).stdout)
    second = json.loads(run(*args, env=env).stdout)
    assert first["id"] == second["id"]
    assert second["success_count"] >= first["success_count"]
    assert second["confidence"] >= first["confidence"]


def test_installer_installs_matching_runtime_binary(runtime_bin: Path, tmp_path: Path):
    # The repository ships prebuilt binaries. The installer should place the
    # host one into an internal bin directory without requiring user action.
    skills = tmp_path / "skills"
    bin_dir = tmp_path / "bin"
    home = tmp_path / "home"
    env = dict(os.environ)
    env["OVERDRIVE_SKILLS_DIR"] = str(skills)
    env["OVERDRIVE_BIN_DIR"] = str(bin_dir)
    env["OVERDRIVE_HOME"] = str(home)
    env["OVERDRIVE_TRAY_ENABLE"] = "0"
    run(str(ROOT / "scripts" / "install.sh"), env=env)
    installed = bin_dir / ("overdrive-runtime.exe" if os.name == "nt" else "overdrive-runtime")
    assert installed.exists()
    assert os.access(installed, os.X_OK)
    result = json.loads(run(str(installed), "status", env=env).stdout)
    assert result["version"] == PLUGIN_VERSION
    assert result["backend"] == "sqlite-fts5-turbovec-v1"


def test_installer_upgrade_never_rewrites_a_running_core_in_place(tmp_path: Path):
    # A running Core maps its binary and the TurboVec library. Rewriting either file in place truncates it
    # under the process (SIGBUS); the installer must swap in a new file instead, so the inode changes.
    bin_dir = tmp_path / "bin"
    home = tmp_path / "home"
    env = dict(os.environ)
    env["OVERDRIVE_SKILLS_DIR"] = str(tmp_path / "skills")
    env["OVERDRIVE_BIN_DIR"] = str(bin_dir)
    env["OVERDRIVE_HOME"] = str(home)
    env["OVERDRIVE_TRAY_ENABLE"] = "0"
    run(str(ROOT / "scripts" / "install.sh"), env=env)
    runtime = bin_dir / ("overdrive-runtime.exe" if os.name == "nt" else "overdrive-runtime")
    libs = [p for p in [*bin_dir.glob("*turbovec_ffi*"), *(home / "lib").glob("*turbovec_ffi*")] if p.is_file()]
    targets = [runtime, *libs]
    before = {p: p.stat().st_ino for p in targets}
    run(str(ROOT / "scripts" / "install.sh"), env=env)
    for path, inode in before.items():
        assert path.stat().st_ino != inode, f"{path.name} was rewritten in place"
        assert os.access(path, os.X_OK)
    assert not list(bin_dir.glob("*.tmp*")) and not list(bin_dir.glob("*.new"))


def _host_target() -> str:
    machine = platform.machine().lower()
    arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[machine]
    return f"{platform.system().lower()}-{arch}"


@pytest.mark.skipif(os.name == "nt", reason="bash bundle installer")
def test_release_bundle_installs_without_go_or_rust(tmp_path: Path):
    # The release ships one bundle per platform. Installing it must not need a Go or Rust toolchain:
    # the tray comes prebuilt inside the bundle.
    target = _host_target()
    run("bash", str(ROOT / "scripts" / "build-tray.sh"), target)
    env = dict(os.environ, OVERDRIVE_REQUIRE_TRAY="1")
    run("bash", str(ROOT / "scripts" / "package-platform.sh"), target, env=env)
    bundle = ROOT / "dist" / f"install-{target}"
    tray = bundle / "runtime" / "bin" / f"overdrive-tray-{target}"
    assert tray.is_file()

    no_toolchain = tmp_path / "path"
    no_toolchain.mkdir()
    for tool in ("bash", "sh", "uname", "tr", "cp", "mv", "rm", "mkdir", "mktemp", "chmod", "find", "cat",
                 "dirname", "basename", "grep", "sed", "ls", "head", "awk", "cmp", "readlink", "env", "date"):
        found = shutil.which(tool)
        if found:
            (no_toolchain / tool).symlink_to(found)
    home = tmp_path / "home"
    bin_dir = tmp_path / "bin"
    env = {
        "PATH": str(no_toolchain), "HOME": str(tmp_path), "OVERDRIVE_HOME": str(home),
        "OVERDRIVE_BIN_DIR": str(bin_dir), "OVERDRIVE_SKILLS_DIR": str(tmp_path / "skills"),
        "OVERDRIVE_TRAY_ENABLE": "0", "OVERDRIVE_EMBEDDER": "stub",
    }
    assert shutil.which("go", path=env["PATH"]) is None and shutil.which("cargo", path=env["PATH"]) is None
    run("bash", str(bundle / "scripts" / "install.sh"), env=env)
    assert (bin_dir / "overdrive-tray").read_bytes() == tray.read_bytes()
    result = json.loads(run(str(bin_dir / "overdrive-runtime"), "status", env=env).stdout)
    assert result["version"] == PLUGIN_VERSION


def test_skills_treat_experience_as_advisory_and_automatic():
    using = (ROOT / "skills/using-overdrive/SKILL.md").read_text().lower()
    plan = (ROOT / "skills/plan/SKILL.md").read_text().lower()
    execute = (ROOT / "skills/execute-plan/SKILL.md").read_text().lower()
    auto = (ROOT / "skills/auto-run/SKILL.md").read_text().lower()
    debug = (ROOT / "skills/systematic-debugging/SKILL.md").read_text().lower()
    verify = (ROOT / "skills/verification-before-completion/SKILL.md").read_text().lower()

    assert "experience engine" in using
    assert "no user memory-management" in using
    assert "current repository reality" in using
    assert "experience recall" in plan
    assert "reconcile" in plan
    assert "memory" in plan
    assert "task-scoped experience" in execute
    assert "persistent experience" in auto
    assert "similar incidents" in debug
    assert "experience capture" in verify


def test_minilm_fail_open_falls_back_to_hashed(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / "home"
    repo = make_repo(tmp_path / "repo")
    env = runtime_env(home)
    env["OVERDRIVE_EMBEDDER"] = "minilm"
    env["OVERDRIVE_MODEL_DIR"] = str(tmp_path / "missing-models")
    env["OVERDRIVE_ORT_LIB"] = str(tmp_path / "missing-ort.so")
    env["OVERDRIVE_SKIP_EMBED_DOWNLOAD"] = "1"

    run(
        str(runtime_bin),
        "record",
        "--cwd",
        str(repo),
        "--kind",
        "lesson",
        "--content",
        "Fail-open embedder should still record.",
        "--confidence",
        "0.8",
        "--source",
        "verified_execution",
        env=env,
    )
    run(
        str(runtime_bin),
        "recall",
        "--cwd",
        str(repo),
        "--query",
        "fail-open embedder",
        env=env,
    )
    status = json.loads(run(str(runtime_bin), "status", env=env).stdout)
    assert status["embedder"] == "hashed"
    assert status["embedder_dim"] == 256


def test_gc_deletes_old_deprecated_but_keeps_active_adr(runtime_bin: Path, tmp_path: Path):
    import sqlite3
    from datetime import datetime, timedelta, timezone

    home = tmp_path / "home"
    repo = make_repo(tmp_path / "repo")
    env = runtime_env(home)

    adr = json.loads(
        run(
            str(runtime_bin),
            "record",
            "--cwd",
            str(repo),
            "--kind",
            "rule",
            "--content",
            "ADR: keep repository boundary intact.",
            "--confidence",
            "0.99",
            "--source",
            "adr",
            env=env,
        ).stdout
    )
    stale = json.loads(
        run(
            str(runtime_bin),
            "record",
            "--cwd",
            str(repo),
            "--kind",
            "fact",
            "--content",
            "Deprecated fact to garbage collect.",
            "--confidence",
            "0.5",
            "--source",
            "repository_observation",
            env=env,
        ).stdout
    )
    json.loads(
        run(
            str(runtime_bin),
            "validate",
            "--id",
            stale["id"],
            "--result",
            "contradiction",
            env=env,
        ).stdout
    )

    old = (datetime.now(timezone.utc) - timedelta(days=120)).strftime("%Y-%m-%dT%H:%M:%SZ")
    conn = sqlite3.connect(home / "experience-v2.db")
    conn.execute("UPDATE memories SET updated_at = ? WHERE id = ?", (old, stale["id"]))
    conn.commit()
    conn.close()

    run(str(runtime_bin), "session-start", "--cwd", str(repo), "--quiet", env=env)

    data = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo),
            "--query",
            "repository boundary adr",
            env=env,
        ).stdout
    )
    assert any(m["id"] == adr["id"] for m in data["memories"] + data.get("critical_rules", []))
    assert all("Deprecated fact to garbage collect" not in m["content"] for m in data["memories"])


def test_working_memory_isolated_by_repository_and_session(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / "home"
    env = runtime_env(home)
    repo_a = make_repo(tmp_path / "a", "https://github.com/acme/a.git")
    repo_b = make_repo(tmp_path / "b", "https://github.com/acme/b.git")

    run(str(runtime_bin), "session-start", "--cwd", str(repo_a), "--quiet", env=env)
    run(
        str(runtime_bin),
        "record",
        "--cwd",
        str(repo_a),
        "--kind",
        "lesson",
        "--content",
        "Alpha mission scratch marker for working memory.",
        "--confidence",
        "0.9",
        "--source",
        "verified_execution",
        env=env,
    )
    data_a = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo_a),
            "--query",
            "alpha mission scratch",
            env=env,
        ).stdout
    )
    assert data_a["working_memory"]
    assert any("Alpha mission scratch" in m["content"] for m in data_a["working_memory"])

    data_b = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo_b),
            "--query",
            "alpha mission scratch",
            env=env,
        ).stdout
    )
    assert all("Alpha mission scratch" not in m.get("content", "") for m in data_b.get("working_memory", []))

    run(str(runtime_bin), "session-start", "--cwd", str(repo_a), "--quiet", env=env)
    data_cleared = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo_a),
            "--query",
            "kubernetes ingress certificate rotation",
            env=env,
        ).stdout
    )
    assert data_cleared["working_memory"] == []


def test_turbovec_allowlist_reranks_relevant_memory(runtime_bin: Path, tmp_path: Path):
    lib = ROOT / "runtime" / "turbovec-ffi" / "target" / "release" / "liboverdrive_turbovec_ffi.so"
    if not lib.exists():
        pytest.skip("TurboVec FFI library not built")

    home = tmp_path / "home"
    repo = make_repo(tmp_path / "repo")
    env = runtime_env(home)
    env["OVERDRIVE_TURBOVEC_LIB"] = str(lib)

    run(
        str(runtime_bin),
        "record",
        "--cwd",
        str(repo),
        "--kind",
        "lesson",
        "--content",
        "Use ProposalRepository for proposal persistence changes.",
        "--confidence",
        "0.95",
        "--source",
        "verified_execution",
        env=env,
    )
    run(
        str(runtime_bin),
        "record",
        "--cwd",
        str(repo),
        "--kind",
        "lesson",
        "--content",
        "Use CustomerFactory for customer integration fixtures.",
        "--confidence",
        "0.95",
        "--source",
        "verified_execution",
        env=env,
    )

    status = json.loads(run(str(runtime_bin), "status", env=env).stdout)
    assert status["turbovec_available"] is True

    relevant = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo),
            "--query",
            "proposal persistence repository",
            env=env,
        ).stdout
    )
    assert relevant["memories"]
    assert all("CustomerFactory" not in m["content"] for m in relevant["memories"])
    assert any("ProposalRepository" in m["content"] for m in relevant["memories"])

    irrelevant = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo),
            "--query",
            "kubernetes ingress certificate rotation",
            env=env,
        ).stdout
    )
    assert irrelevant["memories"] == []


def test_recall_filters_irrelevant_repository_memory(runtime_bin: Path, tmp_path: Path):
    home = tmp_path / "home"
    env = runtime_env(home)
    repo = make_repo(tmp_path / "repo")

    run(
        str(runtime_bin),
        "record",
        "--cwd",
        str(repo),
        "--kind",
        "lesson",
        "--content",
        "Use CustomerFactory when building customer integration fixtures.",
        "--confidence",
        "0.9",
        "--source",
        "verified_execution",
        env=env,
    )

    data = json.loads(
        run(
            str(runtime_bin),
            "recall",
            "--cwd",
            str(repo),
            "--query",
            "kubernetes ingress certificate rotation",
            env=env,
        ).stdout
    )
    assert data["memories"] == []


def test_terminal_dashboard_mounts_xterm_after_pane_attach():
    js = (RUNTIME_DIR / "dashboard" / "terminal.mjs").read_text(encoding="utf-8")
    shell = (RUNTIME_DIR / "dashboard" / "shell.html").read_text(encoding="utf-8")
    css = (RUNTIME_DIR / "dashboard" / "dashboard.css").read_text(encoding="utf-8")
    assert "ensureMounted" in js
    assert 'getElementById("termTabsScroll")' in js
    assert "|| tabBar" not in js
    assert 'id="termTabsScroll"' in shell
    assert "term-inspector" in shell
    assert "term-inspector-group" in shell
    assert 'setOption("theme"' in js
    assert "--term-bg-opacity" in css
    assert "term-opacity-slider" in css
    assert "term-inspector-group" in css
    assert "SF Pro Text" in css
    assert "term-layout--inspector" in css
    assert 'id="termBgOpacity"' in shell
    assert "termBgOpacityValue" in shell


def test_terminal_split_panes_attach_stream_on_render():
    js = (RUNTIME_DIR / "dashboard" / "terminal.mjs").read_text(encoding="utf-8")
    assert "function attachVisibleLeaves()" in js
    assert "attachVisibleLeaves();" in js
    assert "preserveSplitRoot" in js
    assert "createSession({ forSplit: true })" in js
    assert "queueTabStreamConnect(tab" in js
    assert "tab.connect().then" in js

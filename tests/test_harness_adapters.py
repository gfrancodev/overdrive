import json
import os
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ADAPTERS = ROOT / "hooks" / "adapters"

REQUIRED = {
    "claude-code",
    "codex",
    "command-code",
    "devin",
    "opencode",
    "opencode2",
    "cursor",
    "gemini-cli",
    "omp",
    "pi",
    "crush",
    "claude-desktop",
    "claude-desktop-code",
    "openclaw",
    "antigravity-cli",
    "grok",
    "swival",
    "zero",
    "zcode",
    "kimi-code",
    "kiro-cli",
    "kiro-cli-v3",
    "pool",
    "copilot-cli",
    "vscode-copilot",
    "zed",
    "muse",
    "hermes",
    "grizzybot",
}

CHANNELS = {"session-stdout", "deferred-event", "stdio-mcp", "context-file", "local-listen"}
SESSION_ENDS = {"native", "finalize-next-start", "none"}


def load_adapters():
    found = {}
    for path in ADAPTERS.glob("*.json"):
        data = json.loads(path.read_text(encoding="utf-8"))
        assert data["id"] == path.stem
        assert data["channel"] in CHANNELS
        assert data["session_end"] in SESSION_ENDS
        assert "stdout" in data
        assert isinstance(data["events"], dict)
        found[data["id"]] = data
    return found


def test_adapter_matrix_covers_every_harness():
    found = load_adapters()
    assert set(found) == REQUIRED


def test_adapter_delivery_classes():
    found = load_adapters()
    assert found["cursor"]["channel"] == "session-stdout"
    assert found["claude-code"]["events"]["SessionStart"] == "session-start"
    assert found["grok"]["stdout"] == "discarded"
    assert found["grok"]["delivery_event"] == "PostToolUse"
    assert found["kimi-code"]["delivery_event"] == "UserPromptSubmit"
    assert found["crush"]["channel"] == "context-file"
    assert found["crush"]["events"] == {}
    for mcp_id in ("zed", "muse", "swival", "vscode-copilot", "claude-desktop"):
        assert found[mcp_id]["channel"] == "stdio-mcp"
    assert found["muse"]["optional"] is True
    for late_id in ("kiro-cli", "kiro-cli-v3", "antigravity-cli", "pool", "zcode", "command-code"):
        assert found[late_id]["session_end"] == "finalize-next-start"
    assert found["grizzybot"]["channel"] == "local-listen"


def test_hook_reports_missing_runtime(tmp_path):
    env = os.environ.copy()
    env["PATH"] = "/usr/bin:/bin"
    env["OVERDRIVE_HOME"] = str(tmp_path)
    env["OVERDRIVE_BIN_DIR"] = str(tmp_path / "missing")
    out = subprocess.run(
        [str(ROOT / "hooks/run-hook.sh"), "session-start"],
        check=True,
        capture_output=True,
        text=True,
        env=env,
    )
    assert "using-overdrive" in out.stdout
    assert "experience: runtime ausente" in out.stdout


def test_before_prompt_hook_exits():
    env = os.environ.copy()
    env["PATH"] = "/usr/bin:/bin"
    env["OVERDRIVE_HOME"] = str(ROOT / "does-not-exist-home")
    env["OVERDRIVE_BIN_DIR"] = str(ROOT / "does-not-exist-bin")
    subprocess.run(
        [str(ROOT / "hooks/run-hook.sh"), "before-prompt"],
        check=True,
        capture_output=True,
        text=True,
        env=env,
        input="",
    )
    subprocess.run(
        [str(ROOT / "hooks/run-hook.sh"), "stop"],
        check=True,
        capture_output=True,
        text=True,
        env=env,
    )

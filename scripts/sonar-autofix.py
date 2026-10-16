#!/usr/bin/env python3
"""Apply mechanical Sonar fixes guided by .overdrive/sonar-issues.json."""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ISSUES_PATH = ROOT / ".overdrive" / "sonar-issues.json"
RULE_GLOBAL_THIS = "javascript:S7764"


def load_issues() -> list[dict]:
    if not ISSUES_PATH.is_file():
        print("missing", ISSUES_PATH, file=sys.stderr)
        sys.exit(1)
    with ISSUES_PATH.open(encoding="utf-8") as fh:
        return json.load(fh)


def path_for(issue: dict) -> Path:
    return ROOT / issue["component"].split(":", 1)[-1]


def apply_global_this(content: str) -> str:
    return re.sub(r"\bwindow\b", "globalThis", content)


def optional_chain_expr(expr: str) -> str | None:
    expr = expr.strip()
    m = re.match(r"^(.+?)\s*&&\s*(.+)$", expr)
    if not m:
        return None
    left, right = m.group(1).strip(), m.group(2).strip()
    if right.startswith(left + "."):
        return left + "?" + right[len(left) :]
    if "?.members && circle.members" in expr:
        return expr.replace("circle?.members && circle.members", "circle?.members")
    base = left.split("?.")[-1].split(".")[-1] if "." in left else left
    if right.startswith(base + "."):
        return left + "?" + right[len(base) :]
    return None


def fix_s6582_line(line: str, tr: dict) -> str:
    start, end = tr["startOffset"], tr["endOffset"]
    segment = line[start:end]
    repl = optional_chain_expr(segment)
    if repl and repl != segment:
        return line[:start] + repl + line[end:]
    return line


def fix_s7721_line(line: str, message: str) -> str:
    m = re.search(r"Move function '(\w+)'", message)
    if not m:
        return line
    name = m.group(1)
    line = re.sub(
        rf"^(\s*)async function {re.escape(name)}\s*\(",
        rf"\1const {name} = async (",
        line,
    )
    line = re.sub(
        rf"^(\s*)function {re.escape(name)}\s*\(",
        rf"\1const {name} = (",
        line,
    )
    return line


def find_catch_line_index(lines: list[str], line_idx: int) -> int:
    i = line_idx
    while i >= 0 and "catch" not in lines[i]:
        i -= 1
    if i < 0 or "catch" not in lines[i]:
        return -1
    return i


def catch_block_end(lines: list[str], body_start: int) -> int:
    depth = 0
    for j in range(body_start, min(len(lines), body_start + 40)):
        if "{" in lines[j]:
            depth += lines[j].count("{")
        if "}" in lines[j]:
            depth -= lines[j].count("}")
            if depth <= 0:
                return j
    return body_start


def fix_s2486_block(lines: list[str], line_idx: int) -> None:
    """Ensure catch binding is used (console.debug)."""
    i = find_catch_line_index(lines, line_idx)
    if i < 0:
        return
    catch_line = lines[i]
    if re.search(r"catch\s*\{", catch_line):
        lines[i] = catch_line.replace("catch {", "catch (error_) { globalThis.console?.debug?.(error_);")
        return
    m = re.search(r"catch\s*\(\s*(\w+)\s*\)", catch_line)
    if not m:
        return
    var = m.group(1)
    if var in ("_", "__"):
        return
    body_start = i + 1
    body_end = catch_block_end(lines, body_start)
    body = "\n".join(lines[body_start : body_end + 1])
    if re.search(rf"\b{re.escape(var)}\b", body):
        return
    indent = re.match(r"^(\s*)", lines[body_start]).group(1) if body_start < len(lines) else "    "
    if var != "error_":
        lines[i] = catch_line.replace(f"({var})", "(error_)")
    insert = f'{indent}globalThis.console?.debug?.(error_);'
    lines.insert(body_start, insert)


def apply_issue_rules(plain: list[str], file_issues: list[dict]) -> None:
    for iss in sorted(file_issues, key=lambda x: (-x["line"], x["rule"])):
        rule = iss["rule"]
        line_no = iss["line"]
        idx = line_no - 1
        if idx < 0 or idx >= len(plain):
            continue
        if rule == "javascript:S6582" and iss.get("textRange"):
            plain[idx] = fix_s6582_line(plain[idx], iss["textRange"])
        elif rule == "javascript:S7721":
            plain[idx] = fix_s7721_line(plain[idx], iss["message"])
        elif rule == "javascript:S2486":
            fix_s2486_block(plain, idx)


def process_js_file(_path: Path, file_issues: list[dict], original: str) -> str | None:
    rules = {i["rule"] for i in file_issues}
    plain = original.splitlines()
    apply_issue_rules(plain, file_issues)
    joined = "\n".join(plain) + ("\n" if original.endswith("\n") else "")
    if RULE_GLOBAL_THIS in rules:
        joined = apply_global_this(joined)
    return joined if joined != original else None


def process_file(path: Path, file_issues: list[dict]) -> bool:
    if not path.is_file():
        return False
    original = path.read_text(encoding="utf-8")
    suffix = path.suffix.lower()
    updated: str | None = None
    if suffix in (".mjs", ".js"):
        updated = process_js_file(path, file_issues, original)
    if updated:
        path.write_text(updated, encoding="utf-8")
        print("updated", path.relative_to(ROOT), f"({len(file_issues)} issues targeted)")
        return True
    return False


def main() -> None:
    issues = load_issues()
    by_file: dict[Path, list[dict]] = {}
    for iss in issues:
        by_file.setdefault(path_for(iss), []).append(iss)
    for path, file_issues in sorted(by_file.items()):
        process_file(path, file_issues)


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Raise contrast on Sonar-flagged CSS rule blocks (css:S7924)."""
from __future__ import annotations

import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ISSUES = ROOT / ".overdrive" / "sonar-issues.json"

DARK_PANEL = "rgba(28, 36, 48, 0.94)"
BG_BLUE = "background: #0066cc"
COLOR_WHITE = "color: #ffffff"
WHITE_LIGHT_BG = re.compile(
    r"background:\s*rgba\(\s*255\s*,\s*255\s*,\s*255\s*,\s*0?\.\d+\s*\)"
)
WHITE_TEXT = re.compile(
    r"color:\s*(?:#fff(?:fff)?|rgba\(\s*255\s*,\s*255\s*,\s*255\s*[^)]*\))",
    re.I,
)


def rule_block(lines: list[str], line_idx: int) -> tuple[int, int]:
    """Return [start, end) line indices for the CSS rule containing line_idx."""
    start = line_idx
    while start >= 0 and "{" not in lines[start]:
        start -= 1
    if start < 0:
        return line_idx, line_idx + 1
    depth = 0
    block_end = len(lines)
    for i in range(start, len(lines)):
        depth += lines[i].count("{") - lines[i].count("}")
        if depth <= 0 and i > start:
            block_end = i + 1
            break
    return start, block_end


def _swap_blue_button(out: str) -> str:
    out = re.sub(r"color:\s*#0a84ff", COLOR_WHITE, out)
    out = re.sub(
        r"background:\s*rgba\(\s*10\s*,\s*132\s*,\s*255\s*,\s*[\d.]+\s*\)",
        BG_BLUE,
        out,
    )
    return out


def _fix_white_on_light(out: str) -> str:
    if WHITE_TEXT.search(out):
        out = WHITE_LIGHT_BG.sub(f"background: {DARK_PANEL}", out)
    return out


def _fix_blue_button_pairs(out: str) -> str:
    if re.search(r"color:\s*#fff\b", out, re.I) and "background: rgba(10, 132, 255" in out:
        out = re.sub(
            r"background:\s*rgba\(\s*10\s*,\s*132\s*,\s*255\s*,\s*[\d.]+\s*\)",
            BG_BLUE,
            out,
        )
    if "color: #0a84ff" in out and "background: rgba(10, 132, 255" in out:
        out = _swap_blue_button(out)
    return out


def _fix_warm_accent(out: str) -> str:
    if not re.search(r"color:\s*rgba\(\s*255\s*,\s*214\s*,\s*10", out):
        return out
    out = re.sub(r"color:\s*rgba\(\s*255\s*,\s*214\s*,\s*10[^)]+\)", COLOR_WHITE, out)
    return re.sub(
        r"background:\s*rgba\(\s*255\s*,\s*159\s*,\s*10\s*,\s*[\d.]+\s*\)",
        "background: #b45309",
        out,
    )


def _fix_cyan_accent(out: str) -> str:
    if not re.search(r"color:\s*rgba\(\s*100\s*,\s*210\s*,\s*255", out):
        return out
    out = re.sub(r"color:\s*rgba\(\s*100\s*,\s*210\s*,\s*255[^)]+\)", COLOR_WHITE, out)
    return re.sub(
        r"background:\s*rgba\(\s*100\s*,\s*210\s*,\s*255\s*,\s*[\d.]+\s*\)",
        "background: #0369a1",
        out,
    )


def _fix_pale_text(out: str) -> str:
    if not re.search(r"color:\s*rgba\(\s*232\s*,\s*246\s*,\s*255", out):
        return out
    out = re.sub(r"color:\s*rgba\(\s*232\s*,\s*246\s*,\s*255[^)]+\)", COLOR_WHITE, out)
    return WHITE_LIGHT_BG.sub(f"background: {DARK_PANEL}", out)


def _fix_named_backgrounds(out: str) -> str:
    out = out.replace("background: rgba(255, 69, 58, 0.63)", "background: #c41e12")
    out = out.replace("background: rgba(255, 80, 80, 0.57)", "background: #c41e12")
    if "background: rgba(255, 159, 10, 0.22)" in out and re.search(r"color:\s*#fff", out, re.I):
        out = out.replace("background: rgba(255, 159, 10, 0.22)", "background: #b45309")
    if "background: rgba(10, 132, 255, 0.2)" in out and re.search(r"color:\s*#fff", out, re.I):
        out = out.replace("background: rgba(10, 132, 255, 0.2)", BG_BLUE)
    out = out.replace("background: #0a84ff", BG_BLUE)
    if "background: #ff3b30" in out:
        out = out.replace("color: #fff", COLOR_WHITE)
    return out


def fix_block_segment(segment: str) -> str:
    out = segment
    out = _fix_white_on_light(out)
    out = _fix_blue_button_pairs(out)
    out = _fix_warm_accent(out)
    out = _fix_cyan_accent(out)
    out = _fix_pale_text(out)
    return _fix_named_backgrounds(out)


def main() -> None:
    issues = json.loads(ISSUES.read_text(encoding="utf-8"))
    flagged: dict[Path, set[int]] = {}
    for iss in issues:
        if iss["rule"] != "css:S7924":
            continue
        path = ROOT / iss["component"].split(":", 1)[-1]
        flagged.setdefault(path, set()).add(iss["line"])

    for path, line_nums in flagged.items():
        text = path.read_text(encoding="utf-8").splitlines()
        blocks: set[tuple[int, int]] = set()
        for ln in line_nums:
            if 1 <= ln <= len(text):
                blocks.add(rule_block(text, ln - 1))
        for start, end in sorted(blocks, reverse=True):
            segment = "\n".join(text[start:end])
            fixed = fix_block_segment(segment)
            if fixed != segment:
                text[start:end] = fixed.splitlines()
        path.write_text("\n".join(text) + "\n", encoding="utf-8")
        print("updated", path.relative_to(ROOT), len(line_nums), "flagged lines", len(blocks), "blocks")


if __name__ == "__main__":
    main()

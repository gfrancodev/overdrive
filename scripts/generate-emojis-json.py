#!/usr/bin/env python3
"""One-shot generator for runtime/experience/dashboard/emojis.json.

Usage: generate-emojis-json.py [path/to/emoji-test.txt]
Without a path, the Unicode emoji-test.txt is downloaded from unicode.org.
"""
import json
import re
import sys
import unicodedata
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "runtime/experience/dashboard/emojis.json"
EMOJI_TEST_URL = "https://unicode.org/Public/emoji/15.1/emoji-test.txt"


def load_fully_qualified(source: str | None) -> dict:
    """Map each emoji (VS16 stripped) to its fully-qualified form.

    Ranges below cover whole Unicode blocks; many code points there are plain
    text symbols (astrology, chess, dingbats) with no color glyph, and emoji
    with default text presentation need U+FE0F or they render as black glyphs.
    """
    if source:
        text = Path(source).read_text(encoding="utf-8")
    else:
        with urllib.request.urlopen(EMOJI_TEST_URL, timeout=30) as resp:
            text = resp.read().decode("utf-8")
    out = {}
    for line in text.splitlines():
        if not line or line.startswith("#") or ";" not in line:
            continue
        cps, rest = line.split(";", 1)
        if rest.split("#", 1)[0].strip() != "fully-qualified":
            continue
        glyph = "".join(chr(int(c, 16)) for c in cps.split())
        out.setdefault(glyph.replace("\uFE0F", ""), glyph)
    return out


def flag_emoji(a: str, b: str) -> str:
    return chr(0x1F1E6 + (ord(a) - ord("A"))) + chr(0x1F1E6 + (ord(b) - ord("A")))


_FLAG_CC = (
    "BR US PT ES AR MX GB FR DE IT JP KR CN IN CA AU RU UA IL SA AE TR NL BE CH AT SE NO DK FI PL CZ GR IE NZ ZA "
    "CL CO PE VE EC UY PY BO CR PA CU DO PR NG EG KE MA TH VN ID PH MY SG HK TW PK BD IR IQ SY LB JO KW QA OM YE "
    "AF KZ UZ GE AM AZ BY RO HU BG RS HR SI SK LT LV EE IS LU MT CY AL MK BA ME XK MD GL FO JE GG IM GI AD MC SM VA LI"
).split()
FLAGS = [flag_emoji(cc[0], cc[1]) for cc in sorted(_FLAG_CC)]

RANGES = {
    "smileys": [(0x1F600, 0x1F64F), (0x1F910, 0x1F92F), (0x1F970, 0x1F9E0)],
    "people": [
        (0x1F385, 0x1F3CC),
        (0x1F466, 0x1F487),
        (0x1F575, 0x1F590),
        (0x1F645, 0x1F647),
        (0x1F64B, 0x1F64E),
        (0x1F6B4, 0x1F6B6),
        (0x1F930, 0x1F93A),
        (0x1F93C, 0x1F93E),
        (0x1F977, 0x1F978),
        (0x1F9B5, 0x1F9BB),
        (0x1F9D1, 0x1F9DF),
        (0x270A, 0x270D),
        (0x261D, 0x261D),
    ],
    "animals": [(0x1F400, 0x1F43F), (0x1F980, 0x1F9AE), (0x1F54A, 0x1F54A)],
    "food": [
        (0x1F32D, 0x1F37F),
        (0x1F950, 0x1F96F),
        (0x1F9C0, 0x1F9C2),
        (0x2615, 0x2615),
        (0x1F375, 0x1F37E),
    ],
    "travel": [(0x1F680, 0x1F6C5), (0x1F3E0, 0x1F3F0), (0x26F0, 0x26F5), (0x1F30D, 0x1F30F)],
    "activities": [(0x1F3A0, 0x1F3FA), (0x26BD, 0x26BE), (0x1F3C0, 0x1F3DF)],
    "objects": [
        (0x1F4A0, 0x1F4FF),
        (0x1F50B, 0x1F56F),
        (0x1F4F0, 0x1F4FD),
        (0x1F9E0, 0x1F9FF),
        (0x231A, 0x231B),
        (0x23F0, 0x23F3),
        (0x1F4B0, 0x1F4B9),
    ],
    "symbols": [
        (0x2600, 0x26FF),
        (0x2700, 0x27BF),
        (0x1F300, 0x1F320),
        (0x1F490, 0x1F4A4),
        (0x2764, 0x2764),
        (0x1F48B, 0x1F48F),
        (0x1F191, 0x1F19A),
        (0x1F201, 0x1F251),
    ],
}

EXTRA = {
    "smileys": [
        "😀", "😃", "😄", "😁", "😆", "😅", "🤣", "😂", "🙂", "🙃", "😉", "😊", "😇", "🥰", "😍", "🤩",
        "😘", "😗", "☺️", "😚", "😙", "🥲", "😋", "😛", "😜", "🤪", "😝", "🤑", "🤗", "🤭", "🤫", "🤔",
        "🤐", "🤨", "😐", "😑", "😶", "😏", "😒", "🙄", "😬", "🤥", "😌", "😔", "😪", "🤤", "😴", "😷",
        "🤒", "🤕", "🤢", "🤮", "🤧", "🥵", "🥶", "🥴", "😵", "🤯", "🤠", "🥳", "🥸", "😎", "🤓", "🧐",
    ],
    "people": [
        "👋", "🤚", "🖐️", "✋", "🖖", "👌", "🤌", "🤏", "✌️", "🤞", "🤟", "🤘", "🤙", "👍", "👎", "👏",
        "🙌", "🤝", "🙏", "💪", "🦵", "🦶", "👀", "👶", "👧", "👦", "🧑", "👨", "👩", "🧔", "👴", "👵",
    ],
}

LABELS = {
    "recent": "Recent",
    "smileys": "Smileys",
    "people": "People",
    "animals": "Animals",
    "food": "Food",
    "travel": "Travel",
    "activities": "Activities",
    "objects": "Objects",
    "symbols": "Symbols",
    "flags": "Flags",
}
ICONS = {
    "recent": "🕒",
    "smileys": "😀",
    "people": "👋",
    "animals": "🐶",
    "food": "🍔",
    "travel": "✈️",
    "activities": "⚽",
    "objects": "💡",
    "symbols": "❤️",
    "flags": "🏳️",
}
CAT_ORDER = [
    "smileys",
    "people",
    "animals",
    "food",
    "travel",
    "activities",
    "objects",
    "symbols",
    "flags",
]


def tags_for(glyph: str) -> str:
    base = glyph.replace("\uFE0F", "")
    if not base:
        return ""
    try:
        name = unicodedata.name(base[0])
    except ValueError:
        return ""
    parts = re.split(r"[\s\-]+", name.lower())
    skip = {"face", "symbol", "sign", "mark", "button", "emoji", "with", "and", "the"}
    words = [p for p in parts if len(p) > 1 and p not in skip]
    return " ".join(words[:5])


def collect_ranges(cat_ranges):
    seen = set()
    out = []
    for start, end in cat_ranges:
        for code in range(start, end + 1):
            if 0x1F3FB <= code <= 0x1F3FF:
                continue
            ch = chr(code)
            if ch in seen:
                continue
            seen.add(ch)
            out.append(ch)
    return out


def category_source_items(cat: str) -> list[str]:
    if cat == "flags":
        return list(FLAGS)
    seen: set[str] = set()
    items: list[str] = []
    for g in EXTRA.get(cat, []):
        if g not in seen:
            seen.add(g)
            items.append(g)
    for ch in collect_ranges(RANGES.get(cat, [])):
        if ch not in seen:
            seen.add(ch)
            items.append(ch)
    return items


def compact_category_emojis(
    cat: str, items: list[str], qualified: dict[str, str], global_seen: set[str]
) -> list[list]:
    compact: list[list] = []
    for raw in items:
        g = qualified.get(raw.replace("\uFE0F", ""))
        if g is None:
            continue
        if g in global_seen and cat != "flags":
            continue
        global_seen.add(g)
        compact.append([g, tags_for(g)])
    return compact


def main():
    qualified = load_fully_qualified(sys.argv[1] if len(sys.argv) > 1 else None)
    global_seen: set[str] = set()
    emojis = {}
    for cat in CAT_ORDER:
        emojis[cat] = compact_category_emojis(
            cat, category_source_items(cat), qualified, global_seen
        )

    data = {
        "tabs": ["recent"] + CAT_ORDER,
        "labels": LABELS,
        "icons": {k: qualified.get(v.replace("\uFE0F", ""), v) for k, v in ICONS.items()},
        "emojis": emojis,
    }
    OUT.write_text(json.dumps(data, ensure_ascii=False, separators=(",", ":")), encoding="utf-8")
    total = sum(len(v) for v in emojis.values())
    print(f"wrote {OUT} ({OUT.stat().st_size} bytes, {total} emojis)")


if __name__ == "__main__":
    main()

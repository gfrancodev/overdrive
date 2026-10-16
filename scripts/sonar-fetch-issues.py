#!/usr/bin/env python3
"""Fetch open Sonar issues for overdrive-local into JSON (requires .env.sonar)."""
import json
import os
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
env = {}
for line in (ROOT / ".env.sonar").read_text().splitlines():
    if "=" in line:
        k, v = line.split("=", 1)
        env[k.strip()] = v.strip()
token = env.get("SONAR_TOKEN", "")
base = "http://localhost:9000/api/issues/search"
issues = []
page = 1
while True:
    qs = urllib.parse.urlencode(
        {
            "componentKeys": "overdrive-local",
            "resolved": "false",
            "ps": "500",
            "p": str(page),
        }
    )
    req = urllib.request.Request(f"{base}?{qs}", headers={"Authorization": f"Bearer {token}"})
    with urllib.request.urlopen(req) as resp:
        data = json.load(resp)
    issues.extend(data.get("issues", []))
    if page * 500 >= data.get("total", 0):
        break
    page += 1
out = ROOT / ".overdrive" / "sonar-issues.json"
out.parent.mkdir(parents=True, exist_ok=True)
out.write_text(json.dumps(issues, indent=2), encoding="utf-8")
print(f"wrote {len(issues)} issues to {out}")

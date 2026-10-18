#!/usr/bin/env python3
"""Mutation testing for the Go runtime (runtime/experience).

Mutates the lines a change touched and, for each mutant, runs only the tests that execute that line. A per-test
coverage map is built once. The runtime is a single package whose suite takes over a minute, so running all of it
per mutant (what gremlins does) does not finish, and its -coverpkg coverage pass hangs on this package.

Usage:
  scripts/mutation.py                      # lines changed since the merge base with origin/main
  scripts/mutation.py --base feat/x        # lines changed since another ref
  scripts/mutation.py --files a.go,b.go    # every line of those files

The gate is efficacy: killed / (killed + lived). Mutants no test reaches are listed but do not count, and neither do
mutants that do not compile.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import json
import os
import queue
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
from collections import Counter, defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MODULE_REL = Path("runtime/experience")
MODULE = ROOT / MODULE_REL
TAGS = "overdrive_fake_ort"
TEST_ENV = {"OVERDRIVE_EMBEDDER": "stub", "OVERDRIVE_SKIP_EMBED_DOWNLOAD": "1"}
HUNK = re.compile(r"^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@")


def git(*args: str) -> str:
    return subprocess.run(["git", *args], cwd=ROOT, check=True, capture_output=True, text=True).stdout


def default_base() -> str:
    for ref in ("origin/main", "main"):
        if subprocess.run(["git", "rev-parse", "--verify", "-q", ref], cwd=ROOT, capture_output=True).returncode == 0:
            return ref
    raise SystemExit("no base ref: pass --base")


def is_target(rel: str) -> bool:
    p = Path(rel)
    return p.parent == MODULE_REL and p.suffix == ".go" and not p.name.endswith("_test.go") and (ROOT / p).exists()


def changed_lines(base: str) -> dict[str, set[int]]:
    """Lines added or changed since the merge base with base, working tree included, per runtime file."""
    merge_base = git("merge-base", base, "HEAD").strip()
    out: dict[str, set[int]] = defaultdict(set)
    current = None
    for line in git("diff", "-U0", "--no-color", "--no-ext-diff", merge_base, "--", str(MODULE_REL)).splitlines():
        if line.startswith("+++ "):
            path = line[4:].removeprefix("b/")
            current = path if is_target(path) else None
        elif current and (m := HUNK.match(line)):
            start, count = int(m.group(1)), int(m.group(2) or "1")
            out[current].update(range(start, start + count))
    for rel in git("ls-files", "--others", "--exclude-standard", "--", str(MODULE_REL)).splitlines():
        if is_target(rel):
            out[rel].update(range(1, len((ROOT / rel).read_text().splitlines()) + 1))
    return {k: v for k, v in out.items() if v}


def whole_files(names: list[str]) -> dict[str, set[int]]:
    out = {}
    for name in names:
        rel = str(MODULE_REL / Path(name).name)
        if not is_target(rel):
            raise SystemExit(f"not a runtime source file: {name}")
        out[rel] = set(range(1, len((ROOT / rel).read_text().splitlines()) + 1))
    return out


def list_mutants(tmp: Path, targets: dict[str, set[int]]) -> list[dict]:
    tool = tmp / "mutants"
    subprocess.run(["go", "build", "-o", str(tool), "mutants.go"], cwd=ROOT / "scripts" / "mutation", check=True)
    out = subprocess.run([str(tool), *sorted(targets)], cwd=ROOT, check=True, capture_output=True, text=True).stdout
    mutants = [json.loads(line) for line in out.splitlines() if line.strip()]
    return [m for m in mutants if m["line"] in targets[m["file"]]]


def make_workspace(dest: Path) -> Path:
    """A copy of the module at the same depth, with the rest of the repository linked, so tests that read
    ../turbovec-ffi or ../../hooks behave as in the checkout."""
    dest.mkdir(parents=True)
    for entry in ROOT.iterdir():
        if entry.name != "runtime":
            os.symlink(entry, dest / entry.name)
    (dest / "runtime").mkdir()
    for entry in (ROOT / "runtime").iterdir():
        if entry.name != "experience":
            os.symlink(entry, dest / "runtime" / entry.name)
    shutil.copytree(MODULE, dest / MODULE_REL, symlinks=True,
                    ignore=shutil.ignore_patterns("*.out", "*.test", "*.lcov"))
    return dest / MODULE_REL


def build(module: Path, out: Path, cover: bool = False) -> bool:
    args = ["go", "test", "-c", "-tags", TAGS, "-o", str(out)]
    if cover:
        args += ["-cover", "-covermode=set"]
    return subprocess.run(args, cwd=module, capture_output=True).returncode == 0


def run_tests(binary: Path, module: Path, names: list[str], timeout: float, profile: Path | None = None) -> tuple[str, float]:
    args = [str(binary), "-test.run", "^(" + "|".join(names) + ")$", "-test.count=1", f"-test.timeout={int(timeout)}s"]
    if profile:
        args.append(f"-test.coverprofile={profile}")
    env = {**os.environ, **TEST_ENV}
    start = time.monotonic()
    try:
        done = subprocess.run(args, cwd=module, env=env, capture_output=True, timeout=timeout + 15)
    except subprocess.TimeoutExpired:
        return "timeout", time.monotonic() - start
    elapsed = time.monotonic() - start
    if done.returncode == 0:
        return "pass", elapsed
    return ("timeout" if b"test timed out" in done.stdout + done.stderr else "fail"), elapsed


def coverage_map(tmp: Path, workspaces: list[Path], targets: dict[str, set[int]], log) -> tuple[dict, set, dict, list[str]]:
    """Runs each test alone with coverage. Returns {(file, line): {tests}}, the lines coverage instruments,
    {test: seconds}, and the tests that fail without any mutation, which are left out of every run."""
    binary = tmp / "cover.test"
    if not build(workspaces[0], binary, cover=True):
        raise SystemExit("the runtime tests do not build")
    names = [n for n in subprocess.run([str(binary), "-test.list", ".*"], cwd=workspaces[0], env={**os.environ, **TEST_ENV},
                                       check=True, capture_output=True, text=True).stdout.split() if n.startswith("Test")]
    wanted = {Path(f).name for f in targets}
    covers: dict[tuple[str, int], set[str]] = defaultdict(set)
    instrumented: set[tuple[str, int]] = set()
    durations: dict[str, float] = {}
    failed: list[str] = []
    profiles = tmp / "profiles"
    profiles.mkdir()
    pool: queue.Queue[Path] = queue.Queue()
    for ws in workspaces:
        pool.put(ws)

    def one(name: str) -> tuple[str, str, float]:
        ws = pool.get()
        try:
            status, elapsed = run_tests(binary, ws, [name], 90, profiles / f"{name}.out")
            if status != "pass" or elapsed > 20:
                log(f"coverage: {name} {status} in {elapsed:.0f}s")
            return name, status, elapsed
        finally:
            pool.put(ws)

    log(f"coverage: running {len(names)} tests one by one")
    with concurrent.futures.ThreadPoolExecutor(len(workspaces)) as ex:
        results = list(ex.map(one, names))
    for name, status, elapsed in results:
        if status != "pass":
            # Tests sharing a port or a global may clash when run side by side: give them one more go alone.
            status, elapsed = run_tests(binary, workspaces[0], [name], 90, profiles / f"{name}.out")
        if status != "pass":
            failed.append(name)
            continue
        durations[name] = elapsed
        for line in (profiles / f"{name}.out").read_text().splitlines()[1:]:
            loc, _, count = line.rpartition(" ")
            path, _, span = loc.rpartition(" ")[0].rpartition(":")
            base = Path(path).name
            if base not in wanted:
                continue
            start, end = span.split(",")
            lines = range(int(start.split(".")[0]), int(end.split(".")[0]) + 1)
            instrumented.update((base, n) for n in lines)
            if count != "0":
                for n in lines:
                    covers[(base, n)].add(name)
    return covers, instrumented, durations, failed


def tests_for(m: dict, covers, instrumented) -> list[str]:
    """The tests that execute the mutant's line. Coverage does not instrument some lines, a switch case's
    expression for one; those take the tests that run any line of the enclosing function."""
    base = Path(m["file"]).name
    if (base, m["line"]) in instrumented or not m.get("func_start"):
        return sorted(covers.get((base, m["line"]), ()))
    found: set[str] = set()
    for n in range(m["func_start"], m["func_end"] + 1):
        found |= covers.get((base, n), set())
    return sorted(found)


def stop_strays(tmp: Path) -> None:
    """Stop processes started from the run's binaries: a test that opens a terminal starts a session host, which
    outlives a test binary killed for running too long."""
    if os.name != "posix":
        return
    listing = subprocess.run(["ps", "-eww", "-o", "pid=,args="], capture_output=True, text=True).stdout
    for line in listing.splitlines():
        pid, _, args = line.strip().partition(" ")
        if args.startswith(str(tmp) + os.sep):
            try:
                os.kill(int(pid), signal.SIGTERM)
            except (ProcessLookupError, PermissionError, ValueError):
                pass


def apply(source: bytes, m: dict) -> bytes:
    start, end = m["offset"], m["offset"] + m["length"]
    if source[start:end] != m["from"].encode():
        raise RuntimeError(f"{m['file']}:{m['line']}: expected {m['from']!r}")
    return source[:start] + m["to"].encode() + source[end:]


def run_mutants(tmp: Path, workspaces: list[Path], mutants: list[dict], covers, instrumented, durations, log) -> list[dict]:
    pool: queue.Queue[tuple[int, Path]] = queue.Queue()
    for i, ws in enumerate(workspaces):
        pool.put((i, ws))
    done = Counter()
    lock = threading.Lock()

    def one(m: dict) -> dict:
        tests = tests_for(m, covers, instrumented)
        if not tests:
            return {**m, "status": "NOT_COVERED", "tests": 0}
        i, ws = pool.get()
        path = ws / Path(m["file"]).name
        original = path.read_bytes()
        try:
            path.write_bytes(apply(original, m))
            binary = tmp / f"w{i}.test"
            if not build(ws, binary):
                status = "NOT_VIABLE"
            else:
                budget = max(30.0, 4 * sum(durations[t] for t in tests) + 10)
                result, _ = run_tests(binary, ws, tests, budget)
                status = {"pass": "LIVED", "fail": "KILLED", "timeout": "TIMED_OUT"}[result]
        finally:
            path.write_bytes(original)
            pool.put((i, ws))
        with lock:
            done[status] += 1
            total = sum(done.values())
            if total % 25 == 0:
                log(f"mutants: {total}/{len(mutants)} " + ", ".join(f"{k.lower()} {v}" for k, v in sorted(done.items())))
        return {**m, "status": status, "tests": len(tests)}

    with concurrent.futures.ThreadPoolExecutor(len(workspaces)) as ex:
        return list(ex.map(one, mutants))


def source_line(rel: str, line: int) -> str:
    return (ROOT / rel).read_text().splitlines()[line - 1].strip()


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--base", help="mutate lines changed since the merge base with this ref (default origin/main)")
    ap.add_argument("--files", help="comma-separated runtime files to mutate whole, instead of a diff")
    ap.add_argument("--workers", type=int, default=max(1, min(4, (os.cpu_count() or 2) // 2)))
    ap.add_argument("--threshold", type=float, default=85.0, help="minimum efficacy percent")
    ap.add_argument("--output", help="write every mutant and its result as JSON")
    args = ap.parse_args()

    log = lambda msg: print(msg, file=sys.stderr, flush=True)  # noqa: E731
    targets = whole_files(args.files.split(",")) if args.files else changed_lines(args.base or default_base())
    if not targets:
        print("Mutation testing: no runtime source lines changed.")
        return 0
    tmp = Path(tempfile.mkdtemp(prefix="od-mut-"))
    try:
        mutants = list_mutants(tmp, targets)
        log(f"{len(mutants)} mutants on {sum(map(len, targets.values()))} lines in {len(targets)} files")
        if not mutants:
            print("Mutation testing: nothing to mutate on the changed lines.")
            return 0
        workspaces = [make_workspace(tmp / f"ws{i}") for i in range(args.workers)]
        covers, instrumented, durations, failed = coverage_map(tmp, workspaces, targets, log)
        stop_strays(tmp)
        if failed:
            log(f"left out, failing without any mutation: {', '.join(failed)}")
        results = run_mutants(tmp, workspaces, mutants, covers, instrumented, durations, log)
    finally:
        stop_strays(tmp)
        shutil.rmtree(tmp, ignore_errors=True)

    count = Counter(r["status"] for r in results)
    killed = count["KILLED"] + count["TIMED_OUT"]
    lived = count["LIVED"]
    efficacy = 100.0 * killed / (killed + lived) if killed + lived else 100.0
    print(f"Mutation testing: {len(results)} mutants on {sum(map(len, targets.values()))} lines in {len(targets)} files")
    print(f"  killed {count['KILLED']}, timed out {count['TIMED_OUT']}, lived {lived}, "
          f"not covered {count['NOT_COVERED']}, not viable {count['NOT_VIABLE']}")
    print(f"  efficacy {efficacy:.1f}% (threshold {args.threshold:.0f}%)")
    for status, title in (("LIVED", "Lived"), ("NOT_COVERED", "Not covered")):
        rows = sorted((r for r in results if r["status"] == status), key=lambda r: (r["file"], r["line"], r["column"]))
        if rows:
            print(f"{title}:")
            for r in rows:
                print(f"  {Path(r['file']).name}:{r['line']}:{r['column']} {r['kind']} {r['from']!r} -> {r['to']!r}  "
                      f"{source_line(r['file'], r['line'])}")
    if args.output:
        Path(args.output).write_text(json.dumps({"efficacy": efficacy, "counts": count, "mutants": results}, indent=2))
    return 0 if efficacy >= args.threshold else 1


if __name__ == "__main__":
    sys.exit(main())

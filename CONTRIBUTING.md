# Contributing to Overdrive

Thank you for your interest in improving Overdrive.

Overdrive is an opinionated agentic skills framework and software development
methodology. Contributions that preserve its core principles (architecture-first
discovery, a single authoritative spec, evidence-based verification, and
transversal local experience learning) are especially welcome.

Please read [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) before participating. We
follow the [Contributor Covenant](https://www.contributor-covenant.org/version/2/0/code_of_conduct.html),
the same family of community standards used by projects such as
[Superpowers](https://github.com/obra/superpowers).

## Ways to contribute

- Report bugs or unclear behavior via GitHub Issues
- Improve skills, docs, runtime behavior, installers, or tests
- Propose methodology refinements with concrete scenarios and validation evidence
- Help maintain prebuilt runtime artifacts for supported OS/arch targets

## Before you open a pull request

1. Search existing issues and pull requests to avoid duplicate work.
2. For substantial methodology changes, open an issue first and describe:
   - the behavior that should change;
   - a scenario that exposes the current gap;
   - why the change fits Overdrive’s architecture-first philosophy.
3. Keep pull requests focused. Prefer several small, reviewable changes over one
   large mixed diff.

## Prose conventions

Do not use the em dash character (U+2014) in skills, docs, commands, or README prose.
Use commas, colons, parentheses, or separate sentences instead.

## Skill changes are behavior changes

Changes to files under `skills/` should be treated as **behavior changes**, not
documentation-only edits.

When modifying or adding a skill:

1. Define the behavior that should change.
2. Create a scenario that exposes the undesired behavior.
3. Observe the baseline failure.
4. Modify the skill.
5. Verify the desired behavior.
6. Close obvious loopholes and rationalizations.
7. Run the project validation suite.

Avoid adding a new public skill when the behavior is better represented as an
internal capability of an existing intent-level skill (`plan`, `execute-plan`,
`auto-run`, etc.).

Preferred direction:

```text
fewer public skills
+
stronger internal orchestration
```

### Skill conventions

- Each skill lives in `skills/<name>/SKILL.md`.
- Frontmatter must include `name:` matching the directory and a `description:`
  that starts with `Use when`.
- Planning references belong under `skills/plan/references/` when they are
  supporting material, not standalone public skills.

## Experience Engine / runtime changes

The Experience Engine is a transversal runtime capability, not a user-facing
skill. Runtime work lives under `runtime/experience/` (Go) and
`runtime/turbovec-ffi/` (Rust FFI to official TurboVec).

Guidelines:

- Preserve the CLI contract documented in
  `skills/using-overdrive/references/experience-engine.md`.
- Keep the user install path **zero-toolchain**: published plugins ship prebuilt
  binaries and TurboVec libraries; only maintainers/CI run
  `scripts/build-runtime.sh`.
- Fail open at runtime: missing embedder or TurboVec must not break normal
  Overdrive workflows.
- Add or extend tests in `tests/test_experience_runtime.py` for behavioral
  contracts (recall, migration, ledger, GC, contradiction, etc.).

Maintainer build (not required for plugin users):

```bash
export PATH="/path/to/zig:$PATH"   # cargo-zigbuild for cross-platform TurboVec FFI
./scripts/build-runtime.sh
```

## Local validation

From the repository root:

```bash
python -m pytest -q
python scripts/validate.py
bash -n scripts/*.sh
```

`validate.py` checks skill frontmatter, plugin/runtime version alignment, and
the presence of all six prebuilt runtime binaries plus TurboVec libraries.

For runtime tests without downloading embedding models:

```bash
OVERDRIVE_EMBEDDER=stub python -m pytest tests/test_experience_runtime.py -q
```

## Pull request checklist

- [ ] Behavior change is intentional and scoped
- [ ] Relevant tests added or updated
- [ ] `python scripts/validate.py` passes
- [ ] Docs updated when user-visible behavior or protocol changes
- [ ] Plugin/runtime versions stay aligned when shipping a release
- [ ] No secrets, credentials, or personal data committed

## Release alignment

When changing shipped runtime behavior, update together:

- `runtime/experience/constants.go` (`version`, `backendName`)
- `.cursor-plugin/plugin.json`, `.claude-plugin/plugin.json`, `.codex-plugin/plugin.json`
- `CHANGELOG.md`

Prebuilt artifacts under `runtime/bin/` and `runtime/lib/` must remain present
for all supported targets before tagging a release (zero-toolchain plugin install).

## Releases

Push a tag on `main` (for example `v1.0.0`) to trigger `.github/workflows/release.yml`.
The workflow builds all six runtime binaries and TurboVec FFI libraries on GitHub
runners (ubuntu, macos, windows) and attaches one zip per OS/arch pair:

- `overdrive-runtime-linux-amd64.zip`
- `overdrive-runtime-linux-arm64.zip`
- `overdrive-runtime-darwin-amd64.zip`
- `overdrive-runtime-darwin-arm64.zip`
- `overdrive-runtime-windows-amd64.zip`
- `overdrive-runtime-windows-arm64.zip`

Each zip contains the runtime binary and matching TurboVec library for that target.

The repository also ships prebuilt `runtime/bin/` and `runtime/lib/` in git so
`./scripts/install.sh` works without downloading release assets. Refresh committed
binaries when runtime code changes so `validate.py` stays green. Local cross-builds
need Go, Rust, Zig, and `cargo-zigbuild`; without Zig, only native targets (such as
`linux-amd64` on Linux) rebuild locally. Other targets can be rebuilt in CI.

**Cutting a release (example `1.0.0`):**

1. Align version in `constants.go`, plugin manifests, and `CHANGELOG.md`.
2. Rebuild and commit `runtime/bin/` + `runtime/lib/` if runtime code changed.
3. Run `python3 scripts/validate.py` and `OVERDRIVE_EMBEDDER=stub python3 -m pytest tests/ -q`.
4. Push to `main`, then `git tag v1.0.0 && git push origin v1.0.0`.
5. Confirm the Release workflow succeeded and assets are attached on GitHub Releases.

## Questions

- GitHub Issues: https://github.com/gfrancodev/overdrive/issues
- Maintainer contact: contact@gfrancodev.com

## License

By contributing, you agree that your contributions will be licensed under the
MIT License in [LICENSE](LICENSE), including the Superpowers attribution notice
where applicable.

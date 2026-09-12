# Contributing to Overdrive

Thank you for your interest in improving Overdrive.

Overdrive is an opinionated agentic skills framework and software development
methodology. Contributions that preserve its core principles — architecture-first
discovery, a single authoritative spec, evidence-based verification, and
transversal local experience learning — are especially welcome.

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

## Releases

Publishing a release is tag-driven. Push a version tag on `main` (for example `v0.3.0`) to trigger `.github/workflows/release.yml`. The workflow builds all six Go runtime binaries and six TurboVec FFI libraries (matching `scripts/build-runtime.sh` targets), then attaches one zip per platform to the GitHub Release. Each zip contains the runtime binary and TurboVec library for that OS/arch pair.

Maintainers must commit refreshed `runtime/bin/` and `runtime/lib/` artifacts before tagging so plugin installs and `validate.py` stay green. Cross-compiling TurboVec FFI locally requires Go, Rust, Zig, and `cargo-zigbuild`; without Zig, native `linux-amd64` builds succeed on Linux while other targets may need CI runners (ubuntu, macos, windows) or a prior successful build tree.

## Release alignment

When changing shipped runtime behavior, update together:

- `runtime/experience/constants.go` (`version`, `backendName`)
- `.cursor-plugin/plugin.json`, `.claude-plugin/plugin.json`, `.codex-plugin/plugin.json`
- `CHANGELOG.md`

Prebuilt artifacts under `runtime/bin/` and `runtime/lib/` must remain present
for all supported targets before tagging a release (zero-toolchain plugin install).

### Releases (GitHub Actions)

Pushing a tag `v*` (for example `v0.3.0`) or running the **Release** workflow manually
triggers `.github/workflows/release.yml`. CI rebuilds all six Go runtime binaries and
TurboVec FFI libraries, verifies them against the same size checks as `validate.py`,
and publishes GitHub Release assets:

- `overdrive-runtime-<version>-<platform>.zip` (one binary + one TurboVec lib per platform)
- `overdrive-runtime-<version>-all-platforms.tar.gz` (full `runtime/bin` and `runtime/lib` tree)

The repository still ships prebuilt `runtime/bin/` and `runtime/lib/` in git for frictionless
plugin installs. Release assets are the canonical rebuild for each tag; refresh committed
binaries on `main` when runtime code changes so `validate.py` and CI stay green.

**Cutting a release (example `0.3.0`):**

1. Align version in `constants.go`, plugin manifests, and `CHANGELOG.md`.
2. Rebuild and commit `runtime/bin/` + `runtime/lib/` (`./scripts/build-runtime.sh`) if runtime code changed.
3. Run `python3 scripts/validate.py` and `OVERDRIVE_EMBEDDER=stub python3 -m pytest tests/ -q`.
4. Commit, push to `main`, then `git tag v0.3.0 && git push origin v0.3.0`.
5. Confirm the Release workflow on GitHub Actions and download assets from GitHub Releases.

## Releases

Tagged releases (`v0.3.0`, `v0.3.1`, etc.) trigger `.github/workflows/release.yml`.
That workflow rebuilds all six runtime binaries and TurboVec libraries on GitHub
runners and attaches platform zip archives to the GitHub Release:

- `overdrive-runtime-linux.zip`
- `overdrive-runtime-macos.zip`
- `overdrive-runtime-windows.zip`

The repository also ships prebuilt artifacts under `runtime/bin/` and
`runtime/lib/` so `./scripts/install.sh` works without downloading release
assets. After cutting a tag, verify CI on `main` and confirm release artifacts
before announcing the version.

Maintainer checklist for a new version:

1. Align version in `constants.go`, plugin manifests, and `CHANGELOG.md`.
2. Run local validation (`pytest`, `validate.py`).
3. Commit and push to `main`.
4. Tag and push: `git tag v0.3.0 && git push origin v0.3.0`.
5. Confirm the Release workflow succeeded and assets are attached.

## Questions

- GitHub Issues: https://github.com/gfrancodev/overdrive/issues
- Maintainer contact: contact@gfrancodev.com

## License

By contributing, you agree that your contributions will be licensed under the
MIT License in [LICENSE](LICENSE), including the Superpowers attribution notice
where applicable.

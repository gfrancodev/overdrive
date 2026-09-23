from pathlib import Path
import json, re, sys

ROOT = Path(__file__).resolve().parents[1]
errors = []

for p in ROOT.glob('skills/*/SKILL.md'):
    text = p.read_text(encoding='utf-8')
    m = re.match(r'^---\nname: ([a-z0-9-]+)\ndescription: (.+?)\n---\n', text, re.S)
    if not m:
        errors.append(f'invalid frontmatter: {p}')
    elif m.group(1) != p.parent.name:
        errors.append(f'name mismatch: {p}')
    elif not m.group(2).strip().startswith('Use when'):
        errors.append(f'description trigger: {p}')

json.loads((ROOT / 'skills/plan/references/visual-spec.schema.json').read_text())
json.loads((ROOT / 'skills/plan/references/design-system.visual-spec.schema.json').read_text())
json.loads((ROOT / 'skills/plan/references/visual-spec.example.json').read_text())
json.loads((ROOT / 'skills/plan/references/design-system.visual-spec.example.json').read_text())

for legacy in ['skills/brainstorming', 'skills/writing-plans', 'skills/experience']:
    if (ROOT / legacy).exists():
        errors.append(f'legacy/invalid core path present: {legacy}')

required = [
    'CODE_OF_CONDUCT.md',
    'CONTRIBUTING.md',
    'docs/EXPERIENCE_ENGINE.md',
    'skills/using-overdrive/references/experience-engine.md',
    'runtime/experience/go.mod',
    'runtime/experience/main.go',
    'runtime/experience/constants.go',
    'runtime/turbovec-ffi/Cargo.toml',
    'scripts/build-runtime.sh',
    'scripts/install.ps1',
    'scripts/install-common.sh',
    'scripts/package-platform.sh',
]
for rel in required:
    if not (ROOT / rel).exists():
        errors.append(f'missing Experience Engine file: {rel}')

runtime_targets = [
    ('linux', 'amd64', 'overdrive-runtime-linux-amd64', 'liboverdrive_turbovec_ffi.so'),
    ('linux', 'arm64', 'overdrive-runtime-linux-arm64', 'liboverdrive_turbovec_ffi.so'),
    ('darwin', 'amd64', 'overdrive-runtime-darwin-amd64', 'liboverdrive_turbovec_ffi.dylib'),
    ('darwin', 'arm64', 'overdrive-runtime-darwin-arm64', 'liboverdrive_turbovec_ffi.dylib'),
    ('windows', 'amd64', 'overdrive-runtime-windows-amd64.exe', 'overdrive_turbovec_ffi.dll'),
    ('windows', 'arm64', 'overdrive-runtime-windows-arm64.exe', 'overdrive_turbovec_ffi.dll'),
]
for goos, goarch, name, lib_name in runtime_targets:
    p = ROOT / 'runtime/bin' / name
    if not p.exists() or p.stat().st_size < 100_000:
        errors.append(f'missing/invalid runtime binary: {name}')
    lib = ROOT / 'runtime/lib' / f'{goos}-{goarch}' / lib_name
    if not lib.exists() or lib.stat().st_size < 10_000:
        errors.append(f'missing/invalid TurboVec library: {goos}-{goarch}/{lib_name}')

versions = set()
for rel in ['.claude-plugin/plugin.json', '.cursor-plugin/plugin.json', '.codex-plugin/plugin.json']:
    data = json.loads((ROOT / rel).read_text())
    versions.add(data.get('version'))
    if data.get('name') != 'overdrive':
        errors.append(f'invalid plugin name: {rel}')
if len(versions) != 1:
    errors.append(f'plugin versions disagree: {sorted(versions)}')

constants_go = (ROOT / 'runtime/experience/constants.go').read_text(encoding='utf-8')
runtime_match = re.search(r'^const \(\s*\n\s*version\s+=\s+"([^"]+)"', constants_go, re.M)
if not runtime_match:
    errors.append('runtime version constant missing in runtime/experience/constants.go')
elif runtime_match.group(1) not in versions:
    errors.append(f'runtime version {runtime_match.group(1)!r} disagrees with plugin versions {sorted(versions)}')

backend_match = re.search(r'backendName\s+=\s+"([^"]+)"', constants_go)
if not backend_match or backend_match.group(1) != 'sqlite-fts5-turbovec-v1':
    errors.append('runtime backend must be sqlite-fts5-turbovec-v1')

if errors:
    print('\n'.join(errors))
    sys.exit(1)

version = next(iter(versions))
print(f'Overdrive validation OK: {len(list(ROOT.glob("skills/*/SKILL.md")))} skills, runtime {version}')

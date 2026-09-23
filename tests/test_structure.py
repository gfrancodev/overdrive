from pathlib import Path
import json, re

ROOT = Path(__file__).resolve().parents[1]

REQUIRED_SKILLS = {
    'using-overdrive',
    'plan',
    'execute-plan',
    'auto-run',
    'subagent-driven-development',
    'systematic-debugging',
    'test-driven-development',
    'verification-before-completion',
    'requesting-code-review',
    'receiving-code-review',
    'using-git-worktrees',
    'finishing-a-development-branch',
    'writing-skills',
}


def test_core_files_exist():
    for rel in [
        'README.md',
        'LICENSE',
        'AGENTS.md',
        'CODE_OF_CONDUCT.md',
        'CONTRIBUTING.md',
        'docs/ARCHITECTURE.md',
    ]:
        assert (ROOT / rel).exists(), rel


def test_skill_set_is_complete():
    found = {p.parent.name for p in (ROOT / 'skills').glob('*/SKILL.md')}
    assert REQUIRED_SKILLS <= found
    assert 'writing-plans' not in found
    assert 'brainstorming' not in found


def test_each_skill_has_valid_frontmatter():
    for skill in (ROOT / 'skills').glob('*/SKILL.md'):
        text = skill.read_text()
        m = re.match(r'^---\nname: ([a-z0-9-]+)\ndescription: (.+?)\n---\n', text, re.S)
        assert m, skill
        assert m.group(1) == skill.parent.name, skill
        assert m.group(2).strip().startswith('Use when'), skill


def test_plan_is_architecture_first_and_pattern_preserving():
    text = (ROOT / 'skills/plan/SKILL.md').read_text().lower()
    for phrase in ['architecture discovery', 'existing architecture', 'existing pattern', 'spec', 'ordered implementation tasks']:
        assert phrase in text
    assert 'writing-plans' not in text


def test_plan_has_visual_reasoning_and_typeui_is_conditional():
    text = (ROOT / 'skills/plan/SKILL.md').read_text().lower()
    ref = (ROOT / 'skills/plan/references/visual-reasoning.md').read_text().lower()
    assert 'visual' in text
    assert 'screenshot' in ref
    assert 'design system' in ref
    assert 'micro-detail-taxonomy' in ref
    assert 'observed' in ref and 'inferred' in ref and 'unknown' in ref


def test_visual_spec_has_fidelity_classification_and_microdetails():
    ref = (ROOT / 'skills/plan/references/visual-reasoning.md').read_text().lower()
    plan = (ROOT / 'skills/plan/SKILL.md').read_text().lower()
    schema = json.loads((ROOT / 'skills/plan/references/visual-spec.schema.json').read_text())
    ds_schema = json.loads((ROOT / 'skills/plan/references/design-system.visual-spec.schema.json').read_text())
    example = json.loads((ROOT / 'skills/plan/references/visual-spec.example.json').read_text())

    assert 'classification gate' in ref
    assert 'reference-exact' in ref
    assert 'project-design-system' in ref
    assert 'wget' in ref
    assert 'microdetails' in ref
    assert 'visual-spec.json' in ref
    assert 'design-system.visual-spec.json' in ref

    assert 'classification gate' in plan or 'frontend classification' in plan
    assert 'reference-exact' in plan
    assert 'microdetails' in plan

    assert 'classification' in schema['properties']['meta']['properties']
    assert 'fidelityMode' in schema['properties']['meta']['properties']
    assert 'microDetails' in schema['properties']
    assert 'microDetails' in schema['required']

    assert ds_schema['title'] == 'Overdrive Design System Visual Specification'
    assert 'microDetails' in ds_schema['properties']

    assert example['meta']['fidelityMode'] == 'reference-exact'
    assert len(example['microDetails']) >= 3


def test_visual_fidelity_asks_user_but_auto_run_does_not():
    ref = (ROOT / 'skills/plan/references/visual-reasoning.md').read_text().lower()
    auto_run = (ROOT / 'skills/auto-run/SKILL.md').read_text().lower()
    plan = (ROOT / 'skills/plan/SKILL.md').read_text().lower()

    assert 'ask' in ref
    assert 'auto-run' in ref
    assert 'decision ledger' in ref
    assert 'no additional user interaction' in auto_run
    assert 'continue without asking' in plan


def test_execute_and_verify_obey_visual_spec_json():
    execute = (ROOT / 'skills/execute-plan/SKILL.md').read_text().lower()
    verify = (ROOT / 'skills/verification-before-completion/SKILL.md').read_text().lower()
    subagent = (ROOT / 'skills/subagent-driven-development/SKILL.md').read_text().lower()

    assert 'visual-spec.json' in execute
    assert 'microdetails' in execute
    assert 'binding' in execute
    assert 'microdetails' in verify
    assert 'reference-exact' in verify
    assert 'visual-spec.json' in subagent
    assert 'microdetails' in subagent


def test_auto_run_turns_questions_into_discovery():
    text = (ROOT / 'skills/auto-run/SKILL.md').read_text().lower()
    assert 'questions become discovery missions' in text
    assert 'decision ledger' in text
    for stop in ['destructive', 'irreversible', 'security-sensitive', 'external side effect']:
        assert stop in text


def test_execute_plan_recommends_mode_from_task_graph():
    text = (ROOT / 'skills/execute-plan/SKILL.md').read_text().lower()
    assert 'subagent-driven' in text
    assert 'normal execution' in text
    assert 'recommend' in text
    assert 'shared files' in text
    assert 'dependencies' in text


def test_commands_exist():
    for name in ['plan', 'execute-plan', 'auto-run']:
        assert (ROOT / 'commands' / f'{name}.md').exists()


def test_visual_spec_schema_is_json():
    path = ROOT / 'skills/plan/references/visual-spec.schema.json'
    schema = json.loads(path.read_text())
    assert schema['title'] == 'Overdrive Visual Specification'
    assert 'observed' in schema['properties']
    assert 'inferred' in schema['properties']
    assert 'layout' in schema['required']
    assert 'elements' in schema['required']


def test_plugin_manifests_are_overdrive():
    for rel in ['.claude-plugin/plugin.json', '.cursor-plugin/plugin.json', '.codex-plugin/plugin.json']:
        data = json.loads((ROOT / rel).read_text())
        assert data['name'] == 'overdrive'


def test_codex_manifest_exposes_skills_and_interface():
    data = json.loads((ROOT / '.codex-plugin/plugin.json').read_text())
    assert data['skills'] == './skills/'
    assert data['interface']['displayName'] == 'Overdrive'
    assert 'defaultPrompt' in data['interface']


def test_installer_copies_all_skills(tmp_path):
    import os, subprocess
    env = dict(os.environ)
    env['OVERDRIVE_SKILLS_DIR'] = str(tmp_path / 'skills')
    subprocess.run([str(ROOT / 'scripts/install.sh')], check=True, env=env, capture_output=True, text=True)
    installed = {p.parent.name for p in (tmp_path / 'skills').glob('*/SKILL.md')}
    assert REQUIRED_SKILLS <= installed


def test_session_hook_routes_to_using_overdrive():
    import subprocess
    out = subprocess.run([str(ROOT / 'hooks/run-hook.sh'), 'session-start'], check=True, capture_output=True, text=True).stdout
    assert 'using-overdrive' in out
    assert 'plan' in out and 'execute-plan' in out


def test_experience_engine_is_transversal_not_a_skill():
    assert not (ROOT / 'skills/experience').exists()
    assert (ROOT / 'docs/EXPERIENCE_ENGINE.md').exists()
    assert (ROOT / 'skills/using-overdrive/references/experience-engine.md').exists()
    assert (ROOT / 'runtime/experience/go.mod').exists()


def test_manifests_are_v030_or_newer():
    for rel in ['.claude-plugin/plugin.json', '.cursor-plugin/plugin.json', '.codex-plugin/plugin.json']:
        data = json.loads((ROOT / rel).read_text())
        major, minor, patch = [int(x) for x in data['version'].split('.')]
        assert (major, minor, patch) >= (0, 3, 0)
        assert 'experience' in data['description'].lower() or 'learn' in data['description'].lower()


def test_auto_run_has_zero_approval_gates():
    text = (ROOT / 'skills/auto-run/SKILL.md').read_text().lower()
    assert 'no additional user interaction' in text
    assert 'autonomous boundaries' in text
    assert 'initial request' in text
    assert 'defer' in text
    assert 'protected stop conditions' not in text
    assert 'stop and request explicit consent' not in text


def test_auto_run_policy_propagates_without_questions():
    plan = (ROOT / 'skills/plan/SKILL.md').read_text().lower()
    execute = (ROOT / 'skills/execute-plan/SKILL.md').read_text().lower()
    finish = (ROOT / 'skills/finishing-a-development-branch/SKILL.md').read_text().lower()

    assert 'protected stop condition' not in plan
    assert 'continue without asking' in plan

    assert 'protected stop condition' not in execute
    assert 'must not ask' in execute
    assert 'defer' in execute

    assert 'auto-run' in finish
    assert 'initial request' in finish
    assert 'keep the branch' in finish
    assert 'do not ask' in finish


def test_auto_run_command_and_docs_describe_uninterrupted_autonomy():
    command = (ROOT / 'commands/auto-run.md').read_text().lower()
    agents = (ROOT / 'AGENTS.md').read_text().lower()
    architecture = (ROOT / 'docs/ARCHITECTURE.md').read_text().lower()
    readme = (ROOT / 'README.md').read_text().lower()

    assert 'protected' not in command
    assert 'without asking' in command
    assert 'no approval' in agents
    assert 'autonomous boundaries' in architecture
    assert 'defer' in architecture
    assert 'no additional user interaction' in readme
    assert 'overdrive still stops when explicit consent' not in readme


def test_micro_detail_taxonomy_and_extreme_schema_sections():
    tax = (ROOT / 'skills/plan/references/micro-detail-taxonomy.md').read_text().lower()
    ref = (ROOT / 'skills/plan/references/visual-reasoning.md').read_text().lower()
    schema = json.loads((ROOT / 'skills/plan/references/visual-spec.schema.json').read_text())
    ds_schema = json.loads((ROOT / 'skills/plan/references/design-system.visual-spec.schema.json').read_text())
    example = json.loads((ROOT / 'skills/plan/references/visual-spec.example.json').read_text())

    assert (ROOT / 'skills/plan/references/micro-detail-taxonomy.md').exists()
    for term in ['motion', 'navigation', 'typography', 'css', 'image', 'icon']:
        assert term in tax

    assert 'step 3b' in ref
    assert 'step 4a' in ref
    assert 'micro-detail-taxonomy' in ref
    assert 'customized' in ref

    assert 'motion' in schema['properties']
    assert 'navigation' in schema['properties']
    assert 'css' in schema['properties']
    md = schema['properties']['microDetails']['items']['properties']
    assert 'category' in md
    assert 'motion' in md['category']['enum']
    assert 'icon' in md['category']['enum']
    assets = schema['properties']['assets']['additionalProperties']['properties']
    assert 'iconKind' in assets
    assert 'acquisition' in assets
    assert 'customized' in assets

    assert 'icons' in ds_schema['properties']
    assert 'navigation' in ds_schema['properties']

    cats = {m['category'] for m in example['microDetails']}
    assert 'motion' in cats and 'navigation' in cats and 'icon' in cats
    assert example['assets']['icon-tab-custom']['customized'] is True
    assert example['assets']['icon-chevron']['iconKind'] == 'library-component'
    assert len(example['microDetails']) >= 15


def test_no_em_dash_in_package_markdown():
    em = '\u2014'
    paths = []
    for pattern in ['skills/**/*.md', 'docs/**/*.md', 'commands/**/*.md']:
        paths.extend(ROOT.glob(pattern))
    for name in ['README.md', 'CHANGELOG.md', 'CONTRIBUTING.md', 'AGENTS.md']:
        p = ROOT / name
        if p.exists():
            paths.append(p)
    offenders = []
    for p in paths:
        if em in p.read_text(encoding='utf-8'):
            offenders.append(str(p.relative_to(ROOT)))
    assert not offenders, f'em dash found in: {offenders}'

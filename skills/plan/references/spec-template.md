# <Feature> Spec

## Intent
What outcome is required and why.

## Scope
### In
### Out

## Existing System
Relevant architecture and established conventions discovered in the repository.

## Decisions
Only load-bearing decisions. Include rationale and constraints.

## Architecture
Components, boundaries, data flow, interfaces, persistence, failure behavior.

## Visual Specification
Include only when visual reasoning is activated (frontend classification gate passed).

- **Classification**: kind, signals, confidence
- **Fidelity mode**: `reference-exact` | `project-design-system`
- **Design system choice**: user answer, auto-run ruling, or no project design system
- **Artifacts** (binding contracts, not prose substitutes):
  - `visual-spec.json` path
  - `design-system.visual-spec.json` path
- **Binding microDetails count** and critical regions to verify
- **Reconciliation** summary when `project-design-system` mode
- **Taxonomy coverage**: motion, navigation, image, icon categories in `microDetails`

## Security / Privacy
Relevant controls only.

## Testing Strategy
Behavior and integration surfaces that prove correctness.

## Acceptance Criteria
Observable outcomes.

## Ordered Implementation Tasks
- [ ] 1. <self-contained deliverable>
- [ ] 2. <self-contained deliverable>

Each task should state objective, relevant decisions, dependencies, and acceptance criteria;
do not pre-script every edit, shell command, or commit.

## Execution Analysis
- task count:
- independent tasks:
- shared-file pressure:
- dependency shape:
- integration risk:
- recommended mode: normal | subagent-driven

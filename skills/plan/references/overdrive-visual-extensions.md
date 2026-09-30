# Overdrive extensions on Visual Spec 1.0

Overdrive uses the canonical contract at `https://visualspec.dev/schema/1.0/schema.json`.
Every `visual-spec.json` must include:

```json
{
  "$schema": "https://visualspec.dev/schema/1.0/schema.json",
  "visualSpec": "1.0",
  "metadata": { "title": "..." },
  "profiles": ["ui"]
}
```

Validate offline with `references/visualspec-1.0.schema.bundle.json` or the published bundle at
`https://visualspec.dev/schema/1.0/schema.bundle.json`.

## Fidelity and classification (extensions)

Record Overdrive-only planning decisions under `extensions` using namespaced keys:

| Key | Purpose |
|-----|---------|
| `overdrive.classification` | Frontend classification gate (`kind`, `signals`, `confidence`) |
| `overdrive.fidelity` | `mode`: `reference-exact` or `project-design-system`; `designSystemChoice`; optional `designSystemSpecPath` |
| `overdrive.checklist` | Taxonomy coverage summary (`microDetailCategories`, `bindingRuleCount`) |
| `overdrive.icon` | On `assets[]` entries: `iconKind`, `customized`, `library`, `acquisition` |

High-fidelity UI work must populate Visual Spec core sections (`scenes`, `components`, `tokens`,
`motion`, `assets`, `semantics`, `validation`, `provenance`, `rendering`) rather than prose summaries.

## Companion design-system artifact

When the project has a separate token/component snapshot, still emit
`design-system.visual-spec.json` using `design-system.visual-spec.schema.json`. Prefer `tokens` and
`visualLanguage` in the main Visual Spec when a single artifact is enough.

## Legacy surface schema

`overdrive-surface-visual-spec.legacy.schema.json` is retained for migration reference only. New work
must not target it.

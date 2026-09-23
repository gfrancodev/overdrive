# Visual Reasoning

Activate this reference when the request includes **any** visual input: screenshot, image, video, URL, HTML file,
Figma link, or instruction like "make it look like this site."

A reference is an **input**. The **Visual Spec JSON** is the **binding implementation contract**. Any competent
agent implementing frontend work must follow `microDetails` and binding token/element values. Do not reinterpret
the reference from prose summaries.

Read `visual-spec.schema.json`, `design-system.visual-spec.schema.json`, `micro-detail-taxonomy.md`, and
`visual-spec.example.json` before producing specs.

## Step 0: Mandatory frontend classification gate

Before treating a reference as generic context, classify it:

| Signal | Likely classification |
|--------|----------------------|
| Layout regions, navigation, forms, buttons, typography scale, cards, dashboards | `frontend-ui` |
| Token sheets, component libraries, Storybook-style grids | `design-system` |
| Marketing/landing pages with hero, CTA, sections | `marketing-page` |
| Architecture diagrams, logs, terminal output, photos without UI chrome | `not-frontend` |
| Ambiguous crop or partial frame | `uncertain` |

Record in `meta.classification`:

```json
{
  "kind": "frontend-ui",
  "signals": ["form inputs", "button", "card shadow"],
  "confidence": "high"
}
```

- **`frontend-ui`**, **`design-system`**, **`marketing-page`**: activate full visual extraction (Level 3+).
- **`not-frontend`**: do **not** produce a Visual Spec; record classification only and continue non-visual planning.
- **`uncertain`**: perform minimal inspection; if UI evidence appears, upgrade classification; otherwise ask once
  in normal mode whether the reference is meant as a UI target.

## Step 1: Design system discovery

Inspect the repository for an existing design system before choosing fidelity mode:

- design tokens, CSS variables, Tailwind/theme config;
- shared UI primitives and component libraries;
- Storybook or design docs;
- typography, spacing, radius, elevation, and state patterns.

Set `designSystem.detected` and `designSystem.source` when found.

## Step 2: Fidelity mode (ask once in normal mode)

When a project design system **exists** and classification is frontend-related, ask the user **once** (in the
user's language):

> Reproduzir com fidelidade de microdetalhe da referência, ou adaptar ao design system do projeto?

Map answers to:

- **`reference-exact`**: binding values come from the reference; do not round 13px to 12px project token unless
  documented in `reconciliation` (which stays empty in this mode).
- **`project-design-system`**: map reference intent to project tokens/components; every substitution goes in
  `reconciliation` with `referenceValue`, `projectToken`, `path`, and `rationale`.

When **no** project design system exists: set `fidelityMode` to `reference-exact` and
`designSystemChoice.method` to `no-project-design-system`. Do not ask.

### Auto-run policy

Auto-run must not ask this question. Resolve autonomously:

1. If the initial request explicitly says use the project design system: `project-design-system`.
2. Otherwise: `reference-exact` (default for pixel-faithful "make it look like X" requests).
3. Record the ruling in the Decision Ledger with evidence and rationale.

## Step 3: Acquire reference material

### Screenshots and local images

Save or reference the file path. Measure layout, typography, color, spacing, shadows, radii, and assets visible
in the frame.

### URLs and live sites

Mirror static assets when tooling permits:

```bash
mkdir -p .overdrive/visual-sources/<slug>
wget --page-requisites --convert-links --adjust-extension --no-parent \
  --directory-prefix=.overdrive/visual-sources/<slug> "<url>"
```

Rules:

- Shallow depth: HTML, CSS, images, and fonts referenced by the page, not the whole domain.
- **Never execute** downloaded JavaScript; use HTML/CSS and visual measurement only.
- Record paths in `meta.sources` (`type: wget-mirror`, `localPath`, `assetDir`).
- Parse CSS for colors, fonts, spacing, radii, shadows, transitions, keyframes; cross-check against screenshot/render when available.
- If the page is a JS-only shell with no meaningful static markup, record in `unknown` and fall back to
  browser render + screenshot for extraction.

### Video

Extract key frames at meaningful timestamps. Record observed motion only when measurable (duration, easing from
devtools or stated spec). Do not invent hover/transition behavior not shown.

### Figma

Export frames and note component/token names when accessible; treat exports like screenshots plus metadata.

## Step 3B: Asset and icon acquisition

### Images

Three strategies, in order of preference:

1. **download**: from wget mirror, `<img src>`, CSS `background-image`, or CDP extraction. Save under
   `.overdrive/visual-sources/<slug>/assets/` with `localPath`.
2. **consult**: reuse a project asset when hash, dimensions, and crop match the reference (`source: project-existing`).
3. **generate**: only when download/consult are impossible **and** the result fits the reference perfectly
   (dimensions, proportion, style, layout position). Record `generatedFrom`, `fidelityCheck`, and comparison evidence.

Generation does not replace download when the original asset is accessible. In `reference-exact`, the final image
must be visually indistinguishable from the reference in layout context.

### Icons (maximum fidelity)

Classify each icon before implementing:

| `iconKind` | Description | Action |
|------------|-------------|--------|
| `custom-svg` | Site-specific SVG | Download literal SVG or copy markup; bind paths/strokes/fills |
| `icon-font` | Font Awesome, Material Icons, etc. | Identify family + glyph name + weight/style |
| `library-component` | Lucide, Heroicons, Phosphor, Radix in project | Map to exact component/name |
| `sprite` | SVG sprite / symbol ref | Record `spriteUrl` + `symbolId` |
| `raster` | PNG/WebP icon | Treat as `image` with exact dimensions |
| `unknown` | Not identifiable | Mark in `unknown`; do not substitute a similar generic icon |

Required `assets` fields for icons: `iconKind`, `customized`, `library`, `iconName`/`glyphName`/`symbolId`,
`viewBox`, `strokeWidth`, `fill`, `color`, `acquisition`, `localPath` or `inlineSvg`.

**Never** replace a `customized: true` icon with a similar library glyph.

## Step 4: Extract micro-detail into structured JSON

Produce **two artifacts** when classification is frontend-related:

1. **`visual-spec.json`** (surface): follows `visual-spec.schema.json`
2. **`design-system.visual-spec.json`**: follows `design-system.visual-spec.schema.json`
   - `meta.origin: reference` when extracting tokens from the reference (`reference-exact`)
   - `meta.origin: project` when snapshotting the repo design system (`project-design-system`)

Default location: `.overdrive/visual-sources/<slug>/` or a path recorded in the main spec.

Use `micro-detail-taxonomy.md` as the mandatory checklist before closing the spec.

### Depth levels

- **Level 0**: text/copy-only, no visual analysis.
- **Level 1**: small existing component/style tweak, local inspection + targeted `microDetails`.
- **Level 2**: layout/component design, full layout tree + design-system reconciliation when applicable.
- **Level 3**: screenshot/Figma/URL reconstruction, **full** extraction with exhaustive `microDetails`.
- **Level 4**: multi-form-factor / cross-platform, per-platform layout + responsive sections.

Choose the minimum depth that satisfies fidelity; Level 3+ for any new surface built from a reference.

### Step 4A: Exhaustive micro-detail extraction

1. **CSS extremo**: after wget, parse all rules affecting visible selectors; for SPAs use browser/CDP
   `getComputedStyle` per element and record in `css.computedStyles` (do not execute downloaded JS; render in agent browser).
2. **Tipografia**: download font files; register `@font-face` weight/style; capture OpenType features when visible.
3. **Microanimações**: from video, frames at timestamps (ms); from CSS, literal `@keyframes`; from interaction,
   before/after sequence. Each step goes to `motion` + `microDetails` with `category: motion` and optional `atTime`.
4. **Navegação animada**: identify pattern; capture default + post-interaction state; specify enter/exit in
   `navigation.transitions` linked to `motion`.
5. **Imagens e ícones**: follow Step 3B; classify icons before choosing library components.
6. **Política observed/inferred**: motion and assets only in `observed` with evidence; otherwise `inferred` with
   confidence or `unknown`.

### Evidence buckets

Maintain strict separation:

```json
{
  "observed": {},
  "inferred": {},
  "unknown": {}
}
```

Never present inferred mobile behavior, hover states, or interactions as observed facts.

### Layout and elements

Build a **semantic layout tree** (`layout.root`). For each element capture typography, color, borders, shadows,
motion-related properties, asset refs, and neighbor relations.

### microDetails checklist

Every load-bearing visual property must appear in `microDetails` with `category` from the taxonomy.
`binding: true` means implementers **must not** silently change the value.

## Step 5: Reconcile with project design system (conditional)

Only when `fidelityMode` is `project-design-system`. In **`reference-exact`** mode, project design system is
**context only** and does not override binding values.

## Step 6: Behavior beyond the static frame

Define in `states`, `responsive`, `interactions`, `accessibility` where relevant. Mark unshown behavior in
`unknown` or `inferred`, not in `observed`.

## Step 7: Embed in the main spec

Include paths to JSON artifacts, `fidelityMode`, binding `microDetails` count, and taxonomy coverage summary.
Do **not** replace JSON with prose-only description.

## Step 8: Verification contract

1. Render at required viewports.
2. Compare each `microDetails` entry with `binding: true` by category (see taxonomy).
3. For motion/navigation: replay interactions; duration/easing within ±16ms tolerance when specified.
4. For image/icon: verify dimensions, color, stroke, position; custom icons must match reference asset.
5. In `reference-exact`, reject token rounding. In `project-design-system`, verify `reconciliation`.

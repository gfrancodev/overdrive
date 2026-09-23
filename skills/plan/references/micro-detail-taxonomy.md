# Micro-detail Taxonomy

Mandatory checklist before closing any Visual Spec. Every applicable category must appear in the
structured JSON (`elements`, `css`, `motion`, `navigation`, `assets`) **and** in `microDetails` with
`binding: true` when the value is load-bearing.

Read alongside `visual-spec.schema.json` and `visual-spec.example.json`.

## Categories

### layout

- display, flex/grid, gap, padding, margin, position, z-index, overflow, aspect-ratio
- width, max-width, min-height, align-items, justify-content, grid-template
- neighbor spacing rhythm and container proportions

### typography

- font family and fallbacks
- weight, style, stretch
- font size, line-height, letter-spacing, word-spacing
- text-align, text-transform, text-decoration, text-indent
- font-feature-settings, font-variant, -webkit-font-smoothing
- text-shadow when present

### color

- fill, stroke, solid colors, gradient stops
- opacity, mix-blend-mode, backdrop-filter
- semantic role when mapped (primary, surface, text, border, accent)

### border

- width, style, color per side
- border-radius per corner when asymmetric
- outline

### shadow

- box-shadow layers (full literal value)
- text-shadow, filter

### spacing

- rhythm between siblings and parent/child
- inset, scroll-padding, safe-area when visible

### css

- any relevant computed property not fully covered above
- store per element id in `css.computedStyles` with `source`
- pseudo-states (`:hover`, `:focus-visible`, `:active`) when measured

### motion

- transition: property, duration, delay, easing, binding
- animation: name, keyframes (steps with %/offset and properties)
- duration, delay, easing, iteration-count, direction, fill-mode
- micro-interactions: trigger (hover, scroll, load, route-change), from/to states
- video evidence: `atTime` or `frameIndex` on `microDetails`

### navigation

- pattern type: tabs, sidebar, mega-menu, breadcrumb, mobile-drawer, stepper
- items: label, icon asset ref, active state, href/route
- transitions: enter/exit (fade, slide, scale), duration, easing, stagger
- link to `motion` for animated indicators (underline slide, etc.)

### interaction

- hover, focus, active, pressed: measurable visual delta
- only in `observed` when captured; otherwise `inferred` or `unknown`

### image

- photo, illustration, hero, background, raster logo
- uri, localPath, dimensions, object-fit, object-position, crop, alt
- acquisition: `download`, `consult`, `generate`
- `fidelityCheck` when generated

### icon

- classify before implementing: `custom-svg`, `icon-font`, `library-component`, `sprite`, `raster`, `unknown`
- `customized`: true when site-specific art, not a standard library glyph
- library name, iconName/glyphName/symbolId when applicable
- viewBox, strokeWidth, fill, color (computed), size, alignment
- acquisition: `download`, `consult`, `generate`, `inline-copy`
- never substitute a similar library icon for `customized: true`

## microDetails entry shape

```json
{
  "id": "md-001",
  "category": "motion",
  "path": "motion.transitions[0]",
  "property": "duration",
  "value": "200ms",
  "binding": true,
  "source": "keyframes-css",
  "evidence": "wget CSS .nav-tab::after { transition: width 200ms cubic-bezier(0.4, 0, 0.2, 1) }"
}
```

Optional fields:

- `atTime` / `frameIndex`: video or interaction recording evidence
- `trigger`: hover, route-change, scroll, load

## Rule

If a category applies to an element or region and evidence exists, capture it in structured JSON and
duplicate load-bearing values in `microDetails`. Do not leave binding properties only in prose.

## Verification

After implementation, verify each `microDetails` entry with `binding: true` by category:

- **layout/typography/color/border/shadow/spacing/css**: render comparison at required viewports
- **motion/navigation**: static render plus interaction replay; duration/easing within documented tolerance (e.g. ±16ms)
- **image/icon**: dimensions, color, stroke, position; custom icons must match reference asset exactly

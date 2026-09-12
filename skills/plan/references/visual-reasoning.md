# Visual Reasoning

Activate this reference only when the requested outcome has meaningful visual/UI content.
A screenshot or Figma frame is an input; the **Visual Spec is the implementation contract**.

## Depth

- Level 0: text/copy-only change - no visual analysis.
- Level 1: small existing component/style change - inspect local component and tokens.
- Level 2: layout/component design - design-system reconciliation + states/responsiveness.
- Level 3: screenshot/Figma reconstruction - full structured extraction.
- Level 4: multi-form-factor/cross-platform UI - platform-specific responsive/native behavior.

Choose the minimum depth that can satisfy fidelity.

## Step 1 - Extract evidence before implementation

For screenshots/references, extract micro-detail into structured data, but prioritize semantic
relationships over pixel coordinates:
- viewport/form factor;
- regions and hierarchy;
- grids, rows, columns, alignment, proportions;
- spacing rhythm and density;
- typography scale/weight/line-height;
- colors, borders, radii, elevation;
- assets/icons/imagery;
- component anatomy;
- content hierarchy;
- visible interaction/state clues.

Maintain three buckets:

```json
{
  "observed": {},
  "inferred": {},
  "unknown": {}
}
```

`observed` is visible evidence. `inferred` is a design decision supported by evidence and must carry
confidence/rationale when consequential. `unknown` contains behavior that the reference cannot show.
Never present inferred mobile behavior, hover states, or interactions as observed facts.

## Step 2 - Discover and preserve the project's design system

Inspect existing tokens, CSS variables, Tailwind config, theme files, component libraries, Storybook,
shared primitives, typography, icon strategy, spacing/radius conventions, and repeated composition patterns.

Priority:
1. explicit user requirement;
2. existing project design system/components/tokens;
3. reference's visual intent;
4. selected external design guidance;
5. accessibility/platform best practices;
6. agent defaults.

Map the reference to existing components rather than recreating them. Prefer semantic tokens over raw
values. If the reference suggests radius ≈13px and the project standard is 12px, use the established token
unless fidelity genuinely requires a documented exception.

## Step 3 - External design guidance is conditional

If the project already has sufficient design direction, do not import another aesthetic.
If the project lacks a coherent design system or the user asks for a professional visual direction,
TypeUI may be used as a **design guidance provider**, not as authority over the project.

TypeUI skills can be pulled with its CLI (for example `npx typeui.sh pull <skill>`) when tooling permits.
Select a profile by product intent (enterprise/dashboard, premium, agentic, corporate, etc.) and adapt it.
Never silently replace an established brand/design system.

## Step 4 - Specify behavior beyond the static frame

Where relevant define:
- desktop/tablet/mobile layout transformations;
- min/max widths and overflow behavior;
- loading, empty, error, disabled, active, selected, hover, focus-visible states;
- keyboard navigation and focus management;
- touch targets and safe areas for mobile/native;
- labels, semantic roles, contrast, announcements, reduced motion;
- modal/drawer focus trap and focus restoration;
- platform conventions for web, desktop, iOS/Android.

Use best practices for unknown behavior only when it is low-risk and consistent with project conventions.
Otherwise mark it as a decision/ruling, not visual evidence.

## Step 5 - Embed the Visual Spec in the main spec

The final representation should follow `visual-spec.schema.json` where practical. Record the source of
load-bearing values (`project-design-system`, `visual-reference`, `typeui:<profile>`, `platform-best-practice`,
`accessibility-best-practice`, or explicit user requirement).

After implementation, verification should render the UI and compare it against both the Visual Spec and
reference at required viewports. Correct deviations in hierarchy, proportion, wrapping, spacing, states,
and accessibility-not merely superficial color differences.

---
name: writing-skills
description: Use when creating, editing, or validating reusable Overdrive skills and process guidance
---
# Writing Skills

Skills are reusable judgment/process guides, not narratives or project-specific conventions.

Use a test-driven approach to skill authoring:
1. define pressure scenarios or structural tests that expose the unwanted behavior;
2. observe the baseline failure when possible;
3. write the smallest skill guidance that closes the failure;
4. re-run scenarios/validators;
5. tighten loopholes without bloating frequently loaded skills.

Frontmatter must include `name` and a discovery-oriented `description` beginning with `Use when...`.
Descriptions should describe triggering conditions, not summarize the entire workflow.

Prefer intent-level public skills that own complete outcomes. Put heavy references and reusable schemas in
`references/`; do not create a new public skill for every reasoning micro-step.

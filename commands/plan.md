---
description: Architecture-aware planning that produces one authoritative spec and ordered implementation tasks
---
Use the `plan` skill for the user's request. Inspect the repository before proposing architecture. Automatically
activate visual reasoning when the request contains any visual input (screenshot, image, video, URL, HTML,
Figma, or site reference). Run the frontend classification gate; when frontend-related, produce binding
`visual-spec.json` and `design-system.visual-spec.json` per the schemas. Ask once about fidelity mode when a
project design system exists (normal mode). Produce one spec and end with an execution-mode recommendation.
Do not invoke a separate writing-plans phase.

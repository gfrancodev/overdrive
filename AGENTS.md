# Overdrive agent instructions

Before software development work, read `skills/using-overdrive/SKILL.md`.

Core workflow:

1. Use `plan` before non-trivial implementation.
2. Treat explicit project instructions and established current architecture as authority.
3. Use Experience Engine recall automatically when available, but treat historical memory as advisory.
4. Current repository reality always wins over recalled experience; retire contradictions instead of forcing stale patterns.
5. Use the resulting spec as the single source of truth for the current change.
6. Use `execute-plan` for implementation with task-scoped experience.
7. Use `auto-run` only when explicitly requested. In auto-run there are no approval gates: resolve decisions autonomously;
   defer unrequested consequential actions instead of asking the user, and continue all safe work.
8. Apply TDD during behavior changes.
9. Verify with fresh evidence before claiming completion; verified evidence is the gate for durable positive learning.

Do not ask users to manage memory. Do not introduce a new architecture, design system, dependency, or abstraction merely
because it is preferred in general. First prove the existing pattern cannot meet the requirement.

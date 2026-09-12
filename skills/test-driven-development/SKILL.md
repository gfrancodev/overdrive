---
name: test-driven-development
description: Use when implementing a feature, bugfix, refactor, or behavior change before production code is written
---
# Test-Driven Development

Core loop: **RED → GREEN → REFACTOR**.

1. Write the smallest test that expresses one required behavior.
2. Run it and confirm it fails for the expected missing behavior, not for a setup mistake.
3. Implement the minimum production change that makes it pass.
4. Run the focused test and relevant surrounding suite.
5. Refactor only while green.
6. Repeat for the next behavior.

Tests should assert observable behavior rather than mock call choreography whenever practical.
Do not bloat the plan/spec with this execution procedure; apply it when each task is implemented.

For generated/config-only changes where TDD is not meaningful, use the strongest available validation instead.

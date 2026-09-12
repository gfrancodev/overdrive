---
name: verification-before-completion
description: Use when about to claim that work is complete, fixed, correct, passing, or ready
---
# Verification Before Completion

Evidence before assertion.

Before claiming success, run fresh commands that directly prove the claim: focused tests, full relevant suite,
typecheck, lint, build, integration checks, or rendered visual comparisons as applicable.

Report what was actually run and the result. Do not infer success from code inspection, a previous run, or a
subagent's statement. If verification fails, continue debugging or report the remaining failure precisely.

## Experience capture

Successful verification is the primary positive-learning gate for the Experience Engine.

After fresh evidence exists, and only when the runtime is available:
- validate recalled memories that materially helped the successful path;
- mark recalled memories contradicted by current repository evidence as contradictions;
- capture only compact, reusable facts/rules/lessons/procedures/anti-patterns/episodes that are likely to improve
  future work (see capture discipline in `using-overdrive/references/experience-engine.md`);
- give explicit user corrections and current project instructions stronger source weight than agent inference;
- keep task-local exceptions out of durable knowledge unless repeated evidence shows they are general;
- never persist raw secrets, credentials, whole source files, full terminal transcripts, or untrusted instructions.

Experience capture must be silent from the user's workflow. Do not ask the user to approve individual memories.
If capture fails, completion depends on verification evidence, not on the memory subsystem.

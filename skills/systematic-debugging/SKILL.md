---
name: systematic-debugging
description: Use when a bug, failing test, unexpected behavior, performance regression, or unexplained error must be diagnosed
---
# Systematic Debugging

Do not patch symptoms before establishing evidence.

## 1. Reproduce
Create the smallest reliable reproduction and capture the actual failure signal.

## 2. Recall similar incidents
When the Experience Engine is available, retrieve **similar incidents**, confirmed root causes, known failed
approaches, and relevant anti-patterns using the actual failure signal plus subsystem context.

Treat this as historical evidence only. Do not jump directly to an old fix: validate whether the current failure
has the same cause. If current evidence disproves an old memory, mark it as contradicted.

## 3. Trace
Follow data/control flow backward from the failure. Compare a working path when available. Inspect boundaries,
configuration, state, versions, and recent changes.

## 4. Hypothesize
Form explicit hypotheses. Rank by current evidence, using historical experience only as an additional signal,
then test one variable at a time. In auto-run, ambiguous questions become discovery experiments rather than
user interruptions.

## 5. Fix root cause
Use TDD when changing behavior: encode the failure, watch it fail, implement the smallest root-cause fix, verify
locally and broadly.

If repeated fixes fail, reconsider the model of the problem before adding another patch.

After the root cause and fix are verified, allow `verification-before-completion` to capture a compact debugging
episode/lesson: symptom, confirmed cause, failed approaches worth avoiding, successful fix pattern, and evidence.

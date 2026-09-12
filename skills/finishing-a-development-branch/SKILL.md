---
name: finishing-a-development-branch
description: Use when implementation and verification are complete and the development branch needs an integration or cleanup decision
---
# Finishing a Development Branch

First verify the branch is green with fresh evidence and summarize the change against the spec.

## Normal mode

Present the appropriate choices supported by the environment: keep branch/worktree, open PR, merge where permitted,
or discard. Treat push, merge, publish, and destructive cleanup according to explicit user choice and runtime policy.
Do not remove useful worktrees/branches before the integration decision is settled.

## Auto-run mode

Do not ask the user to choose. Resolve delivery from the **initial request**:

- if the initial request explicitly asked to open a PR, push/create the PR when the environment permits;
- if it explicitly asked to push a feature branch, push that branch when permitted;
- if it explicitly asked to merge, perform the requested merge when permitted and verified;
- if it explicitly asked to publish/deploy, perform only that named action and scope when permitted;
- if no delivery/integration action was requested, **keep the branch and worktree** and report their location/state;
- never infer destructive cleanup or discard from silence; defer cleanup instead.

If a host/platform blocks an authorized operation, record the blocked action and keep the work intact. Auto-run must
not turn that runtime limitation into a second approval request.

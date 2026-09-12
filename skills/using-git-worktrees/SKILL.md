---
name: using-git-worktrees
description: Use when implementation should be isolated from the current working tree or when parallel development needs separate branches
---
# Using Git Worktrees

Prefer an isolated worktree for non-trivial implementation. Reuse an established project worktree directory if one
exists; otherwise use a clearly ignored location such as `.worktrees/` after verifying it is ignored.

Create a feature branch, install dependencies as required, and confirm the baseline tests/build relevant to the task
before changing code. Never begin non-trivial work directly on main/master without explicit consent in normal mode.
When auto-run is active, do not ask: create/reuse an isolated worktree/feature branch automatically. If the environment
prevents isolation, defer non-trivial writes rather than modifying main/master implicitly.

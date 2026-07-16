---
name: satelle-cross-repo-containment
type: principle
tags: [type:principle, principles:session]
applies_to: ["*"]
description: Create stories anywhere; action them only in a session opened in THAT repo. Mutating another tree needs [gate] allow_outside_tree_edits. Reminder, not a wall.
---

# Cross-repo containment

**Create anywhere; action only at home.** You may create stories in other satelle repos. Do **not** progress, engage, edit, or mutate another tree from this session — open a session in THAT repo.

**Fence.** Mutations outside the session home (env pin / repo root, not live CWD) are denied by default.

**Escape.** Multi-repo installs may set `[gate] allow_outside_tree_edits = true` (containment only).

**Honest posture.** A bypassed hook is expected under bypass permissions — reminder, not a sandbox.

See [[satelle-edits-require-a-story]].

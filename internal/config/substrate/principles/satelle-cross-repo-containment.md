---
name: satelle-cross-repo-containment
type: principle
tags: [type:principle, principles:session]
applies_to: ["*"]
description: Create stories anywhere; action them only in a session opened in THAT repo. Mutating another tree needs [gate] allow_outside_tree_edits. Reminder, not a wall.
---

# Cross-repo containment

**Create anywhere; action only at home.** You may create stories in other satelle repos. Do **not** progress, engage, edit, or mutate another tree from this session — open a session in THAT repo.

**Fence.** Mutations in another git working tree (root ≠ session home) are denied. Temp, scratchpads, non-repo paths are not fenced.

**Escape.** Multi-repo installs: `[gate] allow_outside_tree_edits = true`.

**Honest posture.** Bypassed hook expected under bypass — reminder, not a sandbox.

See [[satelle-edits-require-a-story]].

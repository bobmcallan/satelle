---
name: satelle-substrate-pushed-check
scope: system
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: Functional-check for a story's close — rejects while the authored substrate the story changed exists only on this machine, i.e. a touched synced area is unpushed, never synced, diverged, unmerged or unavailable against the hosted copy. Accepts a story that touched no synced substrate. Deterministic and self-contained; the verdict is `satelle sync status --story`'s exit code.
---

# Substrate-pushed check (close gate)

**Functional-check** gate for a story whose change touched the developer's
authored substrate. Done means the outcome is durable; a substrate change that
lives on one disk is not done (where a repo keeps its substrate out of git, the
hosted copy is its only off-machine home). On the edge it is
named on, the check runs `satelle sync status --story <id>` and **accepts only on
exit 0**:

- every synced area the story touched is **in step** with the hosted copy, or
  **behind** it (the hosted copy holds the story's change in a snapshot) —
  accepted;
- a touched area that is **unpushed**, **never synced**, **diverged**,
  **unmerged** or **unavailable** — rejected, with the area, its state, the
  touched paths and the command that moves it on printed (`satelle sync push`,
  `satelle sync reconcile`, `satelle login`, …; a **behind** area is brought in
  by `satelle sync pull`);
- a story that touched **no synced substrate** (only non-substrate paths, only
  `local`-scope areas, or a repo with no `[sync]` areas) — accepted without
  contacting the server.

Which paths the story touched, which area owns each, and whether that area is on
the hosted copy are all answered by the binary's mechanism (`satelle story diff`,
the sync area map, `satelle sync status`); this skill only names the gate. It
ships as a canonical default and is **not wired** into the embedded route: a repo
opts in by adding it to the step's `reviewers = [...]` in `step.toml` for the
step that closes a story. Its
pass/fail rule stays configuration — a repo that wants a different one overrides
this skill under `.satelle/skills/`.

The check is the embedded ```check script below — **self-contained**, no
external file (see [[satelle-reviewer-self-contained]]). Exit 0 accepts;
non-zero rejects with notes. **Mechanism, not judgment.** See
[[satelle-agent-model]].

```check
#!/usr/bin/env bash
set -uo pipefail
payload=$(cat)
sid=$(printf '%s' "$payload" | grep -oE '"id":"sty_[a-f0-9]+"' | head -1 | grep -oE 'sty_[a-f0-9]+')
if [ -z "$sid" ]; then
  echo "could not read the story id from the transition payload"
  exit 1
fi

out=$(satelle sync status --story "$sid" 2>&1)
code=$?
printf '%s\n' "$out"
if [ "$code" -ne 0 ]; then
  echo "$sid changed substrate that is not on the hosted copy — run the command named above, then present the edge again"
fi
exit "$code"
```

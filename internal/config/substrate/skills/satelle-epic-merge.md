---
name: satelle-epic-merge
scope: system
type: skill
tags: [type:skill, type:executor]
evidence_commands: satelle trunk sync, satelle trunk publish
description: In-loop executor skill for a container's `merged` step on the epic-branch lane. Syncs trunk, merges every branch-lane member's `epic/<child-id>` branch with `--no-ff`, and puts trunk on origin once through `satelle trunk publish`. Skips `lane:trunk` and cancelled members, and never advances status. A repo using pull requests overrides it by name.
---

# Epic merge (in-loop executor step)

You are the **executor** for the container's `merged` step. Every member has
closed (`after_children` held the step back until then). A member on the **branch
lane** — one with no `lane:trunk` tag — closed with its slice on a pushed
`epic/<child-id>` branch and **nothing on trunk**. This step is what puts those
branches on trunk, **once**, for the whole container. You do not advance status:
the workflow's gate does, and `satelle-epic-merged-check` refuses the close while a
branch is not on trunk.

## What to do

Work in the tree where **trunk is checked out**: `satelle trunk publish` refuses
any other branch.

0. **Start from trunk as it is on origin.** Run
   `satelle trunk sync --fast-forward --strict`. It brings local trunk level with
   the remote's and prints what came in. A non-zero exit means trunk is dirty,
   diverged, ahead (unpushed commits), cannot be moved, is unreachable or
   unresolved: stop, report its line verbatim, and do not merge onto a stale base.
   `satelle trunk sync --json` names the trunk and the remote (`trunk`, `remote`).
1. **List the members.** `satelle story route <container-id>` prints a `## Members`
   section of `- <id> — <status>` lines: that is the container's set, the same one
   the close gate judges. Drop every `cancelled` member. Read each remaining
   member's tags with `satelle story get <id> --json` and drop every one tagged
   `lane:trunk`: it releases itself.
2. **Fetch each remaining member's branch**, `epic/<child-id>`:
   `git fetch <remote> epic/<child-id>`. If a member has no such branch, stop and
   say which id. Do not invent a branch from trunk.
3. **Merge in dependency order.** A member whose `depends-on:` target is also
   merging already contains that target's commits if it was cut from that branch:
   merge the tip of each chain, not both a dependency and a dependent that holds
   it. For each branch:
   `git merge --no-ff -m "merge epic/<child-id> (<child-id>)" <remote>/epic/<child-id>`.
4. **Resolve conflicts** only when they are overlapping edits of the same lines.
   Do not redesign a member's slice.
5. **Put trunk on origin once:** `satelle trunk publish --story <container-id>`.
   It integrates a trunk that moved, runs the repo's `[trunk]` stamp and prove
   commands on the combined head, and pushes with a plain push, never forced. It
   refuses when the repo declares no `[trunk] prove`: stop and report that line,
   since what proves a repo is the repo's decision. A conflict or a failed proof
   leaves trunk where it was and origin untouched; report it and stop.
6. Report the ids you merged and the published head.

## What you must not do

- Do not push trunk any way but `satelle trunk publish`; a bare `git push` skips
  the proof and the moved-trunk handling.
- Do not bump `.version` or edit a changelog: a repo that stamps a release
  declares it in `[trunk] stamp`, which publish runs.
- Do not `satelle story set` the container or a member.
- Do not cancel a member to make the merge smaller.

## A repo that merges through a pull request

This skill merges locally and publishes with `satelle trunk publish`. A repo that
lands an epic through a pull request overrides it by name: author
`.satelle/skills/satelle-epic-merge.md` and keep step 0 and step 1, then open the
request with the forge's own tool (for example `gh pr create`) in place of steps
3 to 5. The step's key in `step.toml` stays `merged`; only the rubric changes.

See [[satelle-agent-model]].

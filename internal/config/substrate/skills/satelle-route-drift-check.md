---
name: satelle-route-drift-check
scope: system
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: Functional-check that refuses a transition whose story walked a lane its category no longer derives — route drift. Ships UNWIRED; a repo names it on the step it wants guarded, best the engagement step, where reclassification is still legal. Deterministic and self-contained.
---

# Route-drift check (name it on the step you want guarded)

**Functional-check** gate. It rejects a transition when the story has walked
states that are **not** states of the lane its category derives **now**.

## The hazard

A story's `workflow:` stamp records the LIFECYCLE. Inside a derived route the
**lane** is re-derived from the story's **category** at every transition. So
adding a category table — or altering one — **re-lanes every in-flight story of
that category**, with no re-stamp, no ledger entry, and no notice. Work already
past a step can find itself on a lane that never had that step.

## Where to name it

On the **engagement step** — entry to the first performing state — because
`category` is a frozen definition field once a story leaves its entry state.
That edge is the last one where the cheap fix (`story set --category`) is still
legal; afterwards the category can only be corrected under the amend gate
(`satelle story amend <id> --category … --reason …`, when the repo declares an
`amend_review` hook and the new lane declares the story's current state), or the
story is cancelled and re-raised. A repo that wants the check later as well names
it on later steps too.

It ships **unwired**: which step it guards is the repo's decision, authored in
`step.toml`, not the binary's.

## What it reads

The transition payload carries `route_drift` **only when drift exists** — the
binary enumerates and attaches, and decides nothing:

```
route_drift: { walked: [...], derived: [...], off_route: [...], status_on_route: bool }
```

No block means no drift, and the check accepts immediately. A status that is not
on the derived lane is refused by the binary before any gate — no legal edge
exists — so what reaches this gate is the softer case: **movement is still
legal, but the history is off-lane.** Whether that is tolerable is the judgment
this gate owns.

Self-contained (see [[satelle-reviewer-self-contained]]). Exit 0 accepts;
non-zero rejects with notes. **Mechanism, not judgment** about the workflow
itself. See [[satelle-agent-model]].

```check
#!/usr/bin/env bash
set -uo pipefail
payload=$(cat)

case "$payload" in
  *'"route_drift"'*) ;;
  *) echo "no route drift: the story's walked lane is the lane its category derives"; exit 0 ;;
esac

if command -v python3 >/dev/null 2>&1; then
  printf '%s' "$payload" | python3 -c "import json,sys
try:
 d=json.load(sys.stdin).get('route_drift') or {}
except Exception:
 sys.exit(0)
if not d:
 sys.exit(0)
off=', '.join(d.get('off_route') or [])
walked=' -> '.join(d.get('walked') or [])
derived=' -> '.join(d.get('derived') or [])
print('route drift: this story walked %s, but its category derives %s now.' % (walked, derived))
print('Off-lane states: %s' % (off or '(none)'))
print('The lane changed under work already in flight. Reclassify while the category is still mutable, or re-raise the work under the right category carrying supersedes:<id>.')
sys.exit(1)"
  exit $?
fi

echo "route drift: the story walked states its category's lane does not declare — the lane changed under work already in flight. Reclassify while the category is still mutable, or re-raise under the right category carrying supersedes:<id>."
exit 1
```

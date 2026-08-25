---
name: satelle-estimate-actual-review
scope: system
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: CODED gate judging PRESENCE of self-reported cost tags — an estimate entering in_progress and an actual entering done. Scoped reviewer node (on="in_progress,done"); self-contained functional check, no agent. Presence is judged, not accuracy.
---

# Estimate / actual presence gate (coded functional check)

Scoped gate (`on="in_progress,done"`); the check below IS the decision —
deterministic code, no LLM. Reads `{story, from, to, review_skill}` on stdin.
Estimate/actual are story tags set by `satelle story estimate` /
`satelle story actual`:

- `estimate-minutes:<n>` and/or `estimate-tokens:<n>` — plan estimate.
- `actual-minutes:<n>` and/or `actual-tokens:<n>` — actual cost.

Rule: entering `in_progress` requires an estimate tag; entering `done`
requires an actual tag; other edges pass. Presence only — any non-empty value
satisfies; accuracy is never judged.

```check
# Coded estimate/actual presence gate. Reads {story, from, to, review_skill}
# on stdin; exit 0 accepts, non-zero rejects with the reason on stdout.
IN=$(cat)
rest=${IN##*\"to\":\"}; to=${rest%%\"*}
case "$to" in
 in_progress)
 case "$IN" in *'"estimate-minutes:'*|*'"estimate-tokens:'*) exit 0;; esac
 echo "no plan estimate recorded — run: satelle story estimate <id> --time <dur> --tokens <n> [--basis <note>], then retry the edge"
 exit 1;;
 done)
 case "$IN" in *'"actual-minutes:'*|*'"actual-tokens:'*) exit 0;; esac
 echo "no actual recorded — run: satelle story actual <id> --tokens <n> [--time <dur>], then retry the edge"
 exit 1;;
esac
exit 0
```

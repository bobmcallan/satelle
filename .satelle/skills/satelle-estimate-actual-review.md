---
name: satelle-estimate-actual-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: CODED gate that judges PRESENCE of the agent's self-reported cost — a plan estimate at begin-work and the actual at close. The workflow DECLARES it as a scoped reviewer node (on="in_progress,done"), and the skill carries a self-contained functional check (no agent involved) that reads the transition payload on stdin: a transition INTO in_progress is rejected with no estimate-minutes/estimate-tokens tag, and a transition INTO done is rejected with no actual-minutes/actual-tokens tag. Presence is judged, not accuracy. This repo's copy of the embedded default (a presence check needs no agent run).
---

# Estimate / actual presence gate (coded functional check)

The workflow declares this skill as a scoped gate (`on="in_progress,done"`), and
the check below IS the decision — deterministic code, no LLM. It receives the
transition payload `{story, from, to, review_skill}` on stdin; the estimate and
actual are recorded as story tags by `satelle story estimate` /
`satelle story actual`:

- `estimate-minutes:<n>` and/or `estimate-tokens:<n>` — the plan estimate.
- `actual-minutes:<n>` and/or `actual-tokens:<n>` — the actual cost.

The rule: entering `in_progress` requires an estimate tag; entering `done`
requires an actual tag; any other edge passes. Presence only — any non-empty
value satisfies; accuracy is never judged.

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

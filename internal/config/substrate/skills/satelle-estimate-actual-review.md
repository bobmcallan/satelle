---
name: satelle-estimate-actual-review
scope: system
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: CODED gate. Entering in_progress requires an estimate tag in fresh-input, output, or legacy tokens or time. A dollar tag is not an estimate. Entering done prints that estimate beside ledger token figures, never a dollar figure, and never rejects for overrun.
---

# Estimate / actual presence gate (coded functional check)

Scoped gate (workflow declares it on `in_progress` and `done`); the check
below IS the decision — deterministic code, no LLM. Reads `{story, from, to,
review_skill, measured_actual}` on stdin.

The estimate is a story TAG a driver writes with `satelle story estimate`, in
whichever unit it chose:

- `estimate-fresh-input:<n>` / `estimate-output:<n>` — tokens.
- `estimate-tokens:<n>` — a legacy bare token count. Never treated as fresh input.
- `estimate-minutes:<n>` — a legacy duration.

A stored `estimate-usd` tag is not an estimate. The check ignores it.

The actual is never a tag this gate requires: it is `measured_actual`,
computed from the ledger by the binary (`ComputeStoryActual`) and attached to
EVERY gate payload, generically — enumeration, not something this skill
triggers by name. The check prints `fresh_input`, `output`, `cache_read` and
`cache_write` from `measured_actual.total`. It does not print a dollar figure,
it does not compute a cost band, and it contains no boundary table.
`Figures.CostBand` owns that table. `recordActual` writes the `actual-cost`
tag from `CostBand` when the result is non-empty, and writes no tag when it
is empty. Display uses `FormatCostBand`, which maps an empty band to the word
"unavailable".

Rule: entering `in_progress` requires an estimate tag in ANY of the units
above — presence only, accuracy is never judged. Entering `done`, the check
never rejects: it prints the estimate (in whichever unit was recorded, or
"no estimate recorded" when none was) beside the measured token figures, and
exits 0 regardless of the size of any overrun. When the payload carries no
`measured_actual` block at all (a computation failure), it prints "measured
actual unavailable" rather than inventing a fresh-0 figure nothing measured.
No ratio or comparison is computed between a token estimate and another
figure. A repo that wants a tolerance gate authors its own check, in its own
unit, as a separate skill.

```check
# Coded estimate/actual gate. Reads {story, from, to, review_skill,
# measured_actual} on stdin; exit 0 accepts, non-zero rejects with the reason
# on stdout. Prints token figures only — no dollar figure, no cost band.
IN=$(cat)
rest=${IN##*\"to\":\"}; to=${rest%%\"*}

# tagval <tag-name> — the value after "<tag-name>:" up to the closing quote,
# read from the story's own tags array; empty + failure when absent.
tagval() {
  local hit
  hit=$(printf '%s' "$IN" | grep -o "\"$1:[^\"]*\"" | head -1)
  [ -n "$hit" ] || return 1
  hit=${hit#\"$1:}
  printf '%s' "${hit%\"}"
}

# ledgerval <json-key> — the LAST "<key>":<number> in the payload. Own,
# each child, and Total all carry the same figure names inside
# measured_actual; Total is marshalled last (StoryActual{Own,Children,Total}),
# so the last occurrence in the whole payload is always Total's.
ledgerval() {
  printf '%s' "$IN" | grep -o "\"$1\":[0-9.eE+-]*" | tail -1 | cut -d: -f2
}

case "$to" in
 in_progress)
 for tag in estimate-fresh-input estimate-output estimate-tokens estimate-minutes; do
 if tagval "$tag" >/dev/null; then exit 0; fi
 done
 echo "no plan estimate recorded — run: satelle story estimate <id> --fresh-input <n> --output <n> (or the legacy --tokens <n> / --time <dur>), then retry the edge"
 exit 1;;
 done)
 if ! printf '%s' "$IN" | grep -q '"measured_actual"'; then
 # No computed actual on this payload (an unwired store, a missing item) —
 # say so rather than inventing a fresh-0 figure nothing measured.
 actual="measured actual unavailable"
 else
 fresh=$(ledgerval fresh_input); out=$(ledgerval output)
 cread=$(ledgerval cache_read); cwrite=$(ledgerval cache_write)
 actual="actual (ledger-computed): fresh ${fresh:-0}, output ${out:-0}, cache read ${cread:-0}, cache write ${cwrite:-0}"
 fi
 if v=$(tagval estimate-fresh-input); then
 o=$(tagval estimate-output) || o=0
 echo "estimate: $v fresh input + $o output tokens | $actual"; exit 0
 fi
 if v=$(tagval estimate-tokens); then
 echo "estimate: $v tokens (legacy unit) | $actual"; exit 0
 fi
 if v=$(tagval estimate-minutes); then
 echo "estimate: $v minutes (legacy unit) | $actual"; exit 0
 fi
 echo "no estimate recorded on this story | $actual"
 exit 0;;
esac
exit 0
```

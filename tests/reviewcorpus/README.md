# reviewcorpus

THE frozen corpus named by epic sty_1e037210's close criterion (token
accountability for reviewer runs): a fixture of known-defect and known-valid
changes for every proof rubric this epic's shipped process alters — ready,
start, integration, implementation, coverage, and done. Later reviewer-process
changes are judged against this corpus; it is not regenerated per change.

Consumers reuse `reviewcorpus.Dir()` and `reviewcorpus.Load` rather than
inventing another set:

- bundling — sty_23e10d92
- isolation — sty_9ded2605
- audit — sty_9ed88e1e
- warm-resume — sty_91d44f06

## Layout

`testdata/corpus/<rubric>/<case-id>/` holds one `case.json` and one frozen
`change.diff` per case. Each rubric has at least one `defect` case (an
expected `reject`) and one `valid` case (an expected `accept`).

`case.json`:

```json
{
  "id": "...",
  "rubric": "ready|start|integration|implementation|coverage|done",
  "skill": "satelle-...-review",
  "label": "defect|valid",
  "story_id": "sty_...",
  "expected_verdict": "reject|accept",
  "source": {
    "kind": "ledger_review_note|captured_note",
    "story_id": "sty_...",
    "skill": "satelle-...-review",
    "ledger_id": "evt_...",
    "note": "the recorded reject note, verbatim"
  },
  "summary": "..."
}
```

`source` is required on every `defect` case (AC2) and cites a real
`satelle ledger list` row: the story it was rejected on, the skill that
rejected it, and the note text. A `valid` case does not require `source`.

## Rubric → gate skill

The mapping is this repo's `.satelle/workflows/step.toml`, not a guess:

| rubric | skill | transition |
| --- | --- | --- |
| ready | `satelle-story-ready-review` | backlog → ready |
| start | `satelle-story-plan-review` | plan → in_progress |
| integration | `satelle-integration-review` | integration → release |
| implementation | `satelle-code-ac-review` | in_progress → integration |
| coverage | `satelle-story-integration-coverage-review` | plan → in_progress |
| done | `satelle-story-done-review` | ready → done (epic-parent close) |

## Provenance

Every `defect` case's `change.diff` is anchored to the story and skill its
`source` cites; where the historical diff the reviewer actually saw was not
itself preserved (this repo's reviewers judge a working-tree diff or a story
document, not a stored artifact), the diff is a minimal, clearly-labeled
reconstruction of the defect the cited note describes — trimmed to the hunks
the note names, per each case's `summary`. `valid` cases use real accepted
code or the real accepted document where available. This does not change how
a reviewer judges (AC4): the corpus is read-only fixture data.

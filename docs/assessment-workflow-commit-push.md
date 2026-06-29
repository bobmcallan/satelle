# Assessment — project workflow with the `commit_push` step (as-is)

Spike `sty_af798983`. Drive a story through the active project workflow as it
stands and record where (if anywhere) it breaks, the root cause, and whether the
break is caught up front or only after the executor has done the work. Findings
feed `sty_318708ed` (add workflow validation to the pre-`in_progress` gate) and
`sty_30edfebc` (fix the default workflow).

## Method

Inspected the active project workflow
(`.satelle/workflows/satelle-project-workflow.md`), the gate dispatch
(`internal/reviewer/reviewer.go`, `internal/verb/workitem.go`), the authored
skills under `.satelle/skills`, and the embedded substrate under
`internal/config/substrate`. Cross-checked skill presence with `git log`.

## AC1 — Failure point when driving toward `done`

As of HEAD there is **no failure point**. The `commit_push` executor node's
prompt `@skill:commit-push` resolves: the skill exists at
`.satelle/skills/commit-push.md` (scope `project`), authored in commit `0122542`
(*"feat(skills): add the commit-push executor skill"*) **after this story was
written**. A story can traverse `backlog → in_progress → integration →
commit_push → committed → done` with every referenced skill resolving.

Historically — when the **global** commit-push skill was removed and before it
was re-homed as a project skill — the break was at the `commit_push` step: the
executor reaching that node had no skill body to read. It was **not** surfaced at
engagement.

## AC2 — Root cause / resolution scope

`@skill:commit-push` on the `commit_push` node is an **executor** prompt
(`actor=executor`), not a reviewer gate. It resolves against authored skills via
the docindex store, **project scope first** (`.satelle/skills`) then embedded
**system** scope. The artifact that was historically missing was the
global/system commit-push skill; the fix was to author it as a **project** skill
in `.satelle/skills`.

Squaring this with the present tree: **`.satelle/skills/commit-push.md` DOES
exist** (project scope). So today nothing is missing and `@skill:commit-push`
resolves via the project skill. The story's original premise ("the global
commit-push skill has been removed") is therefore **stale** — it was true when
written, and has since been resolved by re-homing the skill to project scope.

Separately: `reviewer_skill` **edge** gates and reviewer **node** prompts are
gated, but `reviewer.go` (the "named-but-absent rubric is advisory" rule) treats
a referenced-but-**absent** reviewer rubric as **advisory** (`Gated=false`,
auto-accept) to keep fresh repos working — so a missing **reviewer** skill
silently *un-gates* rather than blocking.

## AC3 — When detected (wasted-work exposure)

A missing workflow skill is **not** detected at engagement (`backlog →
in_progress`). Two distinct exposures:

- **Executor `@skill:` prompts** (e.g. `commit_push`) are only looked up when the
  executor **reaches** that step — i.e. *after* `in_progress` + `integration`
  work is already done. The wasted-work exposure is real: a full slice can be
  implemented before a missing `commit_push` skill bites.
- **Missing `reviewer_skill` rubrics** don't bite at all — they degrade to
  advisory auto-accept, silently dropping a gate.

Neither is caught up front. The `in_progress` gate runs only
`satelle-story-intent-review` (+ the system estimate review); it does not resolve
the story's downstream workflow skills.

## AC4 — What the validation step and the fix must address

**`sty_318708ed` (validation):** add an up-front workflow-skill-resolution check
to the `backlog → in_progress` gate that resolves **every** skill the active
workflow references — both executor `@skill:` node prompts **and**
`reviewer_skill` edges — and **rejects** engagement naming any unresolved skill.
Reuse the embedded `satelle-workflow-review` skill
(`internal/config/substrate/skills/satelle-workflow-review.md`) rather than
re-implementing parsing. This closes both exposures above by moving detection to
engagement.

**`sty_30edfebc` (fix):** the broken default workflow is **already repaired** by
re-homing commit-push as a project skill (`.satelle/skills/commit-push.md`).
Remaining work: (a) confirm no dangling `@skill:`/`reviewer_skill` ref in the
project workflow (all resolve today); (b) document the resolution scope (project
`.satelle/skills` over embedded system) in the workflow description; (c) keep the
embedded baseline
(`internal/config/substrate/workflows/satelle-baseline-workflow.md`) completable
as the order-zero fallback — it must not reference commit-push (it closes
`in_progress → done` directly).

## Process finding — no spike/assessment lane

The project workflow `applies_to: ["*"]` and has no spike/assessment lane, so a
findings-only story must still pass `satelle-code-ac-review` entering
`integration`. That gate is **test-exempt** for docs/substrate-only changes (the
deliverable is the change itself), but it reads the **working tree** — a DB-only
ledger note or story attachment is invisible to it. Lesson recorded here by
landing the findings as this tracked file. A future story could add a spike lane
so assessments don't traverse the code/commit/CI path.

# Creating a story — the path from draft to done

A story is a unit of work. In satelle it travels a gated lifecycle: each edge is
judged by an isolated reviewer before it is enacted, so quality is managed at the
boundary rather than self-asserted by the executor.

## 1. Draft and create

CLI:

    satelle story create \
      --title "Ship the thing" \
      --body "What done looks like / the outcome sought" \
      --acceptance "1. first testable criterion
    2. second testable criterion" \
      --priority high --tags mvp,web

A well-formed draft needs three things (the required structure):

1. a specific **title** (names the change, not just a noun),
2. a **body** stating the goal / what done looks like, and
3. numbered, **testable acceptance criteria**.

`satelle init` seeds **`[review] gate_create = true`** (opt out with
`false`). Creation always runs the **deterministic** structure check (title,
goal body, numbered ACs, non-empty category). When the active workflow declares
`create_review` (the embedded default is `satelle-story-create-review`), an
isolated reviewer also judges **content/alignment** and **classification**
against [[satelle-story-classification]] — e.g. reject an epic draft filed as
`category: feature` (use `epic-parent`). A reject pushes back with notes;
nothing is persisted until the draft is sound. With `gate_create = false`, only
the structure of an unguarded create path remains — the standard is the same,
the enforcement is not.

## 2. Begin work (backlog → in_progress)

Move the story into work:

    satelle story set <id> --status in_progress

On the seeded default workflow this edge carries one CODED gate: the
estimate/actual check rejects begin-work until a plan estimate is recorded
(`satelle story estimate <id> --time <dur> --tokens <n>`). A repo that authors
richer gates (e.g. `satelle-story-intent-review`, judging the story is
well-formed enough to start) adds them to its workflow; a reject keeps the story
in backlog with notes on what to clarify.

On **first entry into a performing/engaging state** (e.g. `backlog → plan` or
`backlog → in_progress`), satelle records an **engagement baseline** ledger row
(`engagement_baseline`) with the current git HEAD (and whether the worktree was
dirty). Re-entry after park/blocked does **not** overwrite it. Gates that judge
slice scope consume the baseline via enumeration only:

```
satelle story diff <id>           # changed files + diffstat since baseline
satelle story diff <id> --patch   # plus full unified diff
```

The command never decides pass/fail — it only lists. A story with no baseline
(never engaged, or created before this feature) errors clearly. Gate authors
invoke it from functional checks or reviewer prompts (`Bash(satelle:*)`). The
embedded **`satelle-story-scope-review`** gate (implementation exit / close)
consumes this enumeration to reject bundled sibling work.

## 3. Reach done through the workflow's gates

The exact path to `done` is whatever the active workflow declares — `done` is
always the terminal state, and every gate on the path runs before it (see
`satelle help reviewer-checks`). The seeded default project workflow is the most
basic lifecycle — it closes directly (`in_progress → done`) with **no LLM
reviewers**; only the coded estimate/actual check (the actual cost must be
recorded before the close) and the per-transition step summary run. Reviewer
gates like `satelle-story-done-review` — an isolated, **read-only** reviewer
that reads the repository and works through the numbered acceptance criteria one
by one — are authored substrate a repo layers into its own workflow (the parent
workflow and this repo's workflow both declare it).

A repo may layer extra steps onto the path before `done` — this repo's workflow
adds one in-loop **release** step: the executor bumps the version, commits the
slice, pushes to `main`, and — rather than block watching CI — refreshes the
service during the CI window and records the `test` + version-gated `release` run
URLs, their conclusions, and the published tag as evidence. The
`satelle-story-release-review` gate is the authority on CI-green, judging that
recorded evidence before close:

    satelle story set <id> --status release     # executor: bump .version, commit, push, record CI evidence
    satelle story set <id> --status done        # gate: release evidence (CI green) + acceptance review

Drive each transition and let its gate judge it; a reject blocks the move and
records why. You never self-enact a gated edge.

## 4. Cancel (any → cancelled)

To abandon an item, record why:

    satelle story set <id> --status cancelled

## What you see

Every transition writes evidence to the **ledger** (visible on the story detail
page and timeline). A per-transition summariser (`satelle-step-summary`) records
a short prose recap of each step. The web project page shows a **Progress**
column of numbered stage lights folded from the ledger: green = accepted,
red = rejected, slate = ungated checkpoint, amber pulsing = current stage.

See also: `satelle help reviewer-checks`.

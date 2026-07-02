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

If the repo enables create-gating (`[review] gate_create = true` in
`.satelle/satelle.toml`), the `satelle-story-review` reviewer judges
the draft against that structure before it is persisted. A reject pushes back
with notes; nothing is created until the structure is sound. With gating off,
the same structure is still the standard — the gate is advisory.

## 2. Begin work (backlog → in_progress)

Move the story into work:

    satelle story set <id> --status in_progress

On the seeded default workflow this edge carries one CODED gate: the
estimate/actual check rejects begin-work until a plan estimate is recorded
(`satelle story estimate <id> --time <dur> --tokens <n>`). A repo that authors
richer gates (e.g. `satelle-story-intent-review`, judging the story is
well-formed enough to start) adds them to its workflow; a reject keeps the story
in backlog with notes on what to clarify.

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
adds sequential **commit** and **push** steps: the `commit` executor bumps the
version and commits the slice, the `push` executor pushes and watches CI + the
version-gated release, and a functional gate confirms the bump, CI, and the
published release before close:

    satelle story set <id> --status commit      # executor: bump .version, commit
    satelle story set <id> --status push        # executor: push, watch test + release
    satelle story set <id> --status committed   # gate: bump + CI + release verified, summary doc
    satelle story set <id> --status done        # gate: acceptance review

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

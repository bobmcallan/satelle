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
`.satelle/satelle.toml`), the `satelle-story-structure-review` reviewer judges
the draft against that structure before it is persisted. A reject pushes back
with notes; nothing is created until the structure is sound. With gating off,
the same structure is still the standard — the gate is advisory.

## 2. Begin work (backlog → in_progress)

Move the story into work:

    satelle story set <id> --status in_progress

This edge is gated by `satelle-intent-plan-review`: the story must be well-formed
enough to start — a clear goal and numbered, testable acceptance criteria. A
reject keeps it in backlog with notes on what to clarify.

## 3. Close (in_progress → done)

When the work satisfies the acceptance criteria:

    satelle story set <id> --status done

This edge is gated by `satelle-story-done-review`: an isolated, **read-only**
reviewer reads the repository (Read/Grep/Glob) and works through the numbered
acceptance criteria one by one, looking for concrete evidence each is satisfied.
A reject pushes back with the specific unmet criteria; the story stays
in_progress.

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

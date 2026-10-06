# Epic wave: which children may start now

`satelle story wave <epic-parent-id>` prints the children of an epic that may
start now, one id per line. It is an **assessment only**: it writes no status,
lease or tag, and it does not engage, dispatch, open a worktree or spawn an
agent. `--json` prints the full response (`runnable`, `omitted`, `schedule`).

## What it reads

- **Membership** is the epic set — every story carrying the epic's `epic:<theme>`
  tag, the same set close uses. Not `parent_id`, not a second list.
- **Schedule** is declared on the container's route: `schedule = "parallel"` or
  `"sequential"` on its `waits_on_children` step in `step.toml`.
- **Edges** are the `depends-on:<story id>` tags on the children.

## The wave

A child is runnable when it is not terminal (not done, not cancelled) and every
`depends-on` target is **done by that story's own route**. A child with no
`depends-on` is eligible. A dependency that is in progress, blocked, missing or
**cancelled** does not satisfy the edge — a cancelled story's commits are not
there. The child stays out of the wave and a `# omitted <id>: waiting on <dep>`
line names the dependency (a cancelled one is called out as cancelled).

- **parallel** returns every runnable child.
- **sequential** returns the runnable set only when it holds exactly one id. With
  more than one it exits non-zero, names those ids, and says to add `depends-on`
  edges. It never picks one by `order:`, `created_at` or title.

## Refusals (non-zero exit, no id printed as runnable)

- the container's route declares no schedule (or two different ones);
- two epic-parents carry the epic tag;
- the container has no `epic:<theme>` tag (or more than one);
- the story is not an epic-parent.

There is no fallback to `order:` or to every child — the declaration is the
authority.

## Engagement honours the wave

Entering an engaging status (`satelle story set <child> --status plan`) is
refused for a child of a **scheduled** epic that the wave would not return, and
the refusal quotes the wave's reason: the dependency that is not done, the
sequential wave that is wider than one (add `depends-on`), or that the story is
not in the epic set. A child the wave returns still meets the seat mode and the
one-engagement-per-worktree rule, so two eligible siblings co-engage only from
distinct worktrees. Only a *new* engagement is checked; a child already
mid-flight keeps moving.

A container with no schedule is unchanged: the "no schedule" refusal above
belongs to `story wave`, not to engagement. Stories that are not children of an
epic are unaffected. See `satelle help workflow-convert` for `schedule` and `depends-on`.

## A container step that waits on a child obligation

A container step may declare `after_children = "<obligation>"` in `step.toml`.
Entering it is refused until every child has discharged that obligation on its
own route; the refusal names each child that has not, with its status. A child
has discharged it when it is done or cancelled, or sits at or past the step of
its own route that provides the obligation. A child whose route has no such step
holds the container back, and the refusal says so.

The step may name a performer. Entering it takes a seat for the container, under
the epic key its children already share, so a child that has discharged the
obligation can still engage its later steps from another worktree while the
container holds it — still one worktree per lease. That needs
`[engagement] parallel = "epic"`: in `none` mode the container and a child hold
different keys and conflict. The container's `waits_on_children` step still
holds no seat. Without `after_children`, container engagement is unchanged, and
`children-resolved` still requires every child terminal.

The binary only gates the entry and the seat. It runs no git command; a merge is
a skill the step names.

## Where a child runs

A child's step runs locally unless its step declares otherwise. A performer step
in `step.toml` may declare `remote_agent = "<binding>"` (a `role = "agent"`,
`interface = "cloud"` binding in `agents.toml`) and `local_tags = ["<tag>", …]`.
The step is then performed by `remote_agent`, in a cloud session, when **all** of
these hold:

- the child belongs to an epic whose container declares `schedule = "parallel"`
  (the wave's own membership and schedule — a sequential epic, or a story that is
  not an epic child, keeps the step's own `agent =`);
- the child carries none of the step's `local_tags` (a tag the repo uses to pin a
  child local, such as the children that edit shared substrate);
- the session is **signed in**. A local-only session performs the step with its
  own `agent =` and records the ledger note
  `placement: remote declared, local used — not signed in`. A git email is not
  a sign-in.

A step with no `remote_agent` is unchanged, and the epic's merge never moves: a
container step (`waits_on_children` or `after_children`) cannot declare
`remote_agent`, so integration and release stay local. Which children run remote
and which run local is therefore declared configuration, never a decision of the
agent driving the epic.

**The driver pushes first.** A cloud session is based on the child's pushed
branch, and satelle never pushes for you. Before presenting a remote child's
performer step, push its worktree branch: `git push -u <remote> <branch>`. If you
forget, the dispatch is refused before anything launches, naming the child, the
remote placement and that command.

**Rework is refused for a remote child.** A cloud session is one-shot and cannot
be a relay partner, so `satelle story rework` on a child placed remote is refused
and ledgered (`rework_refused`). Re-present the edge instead: that launches a
fresh cloud session carrying the reviewers' findings in its payload. A remote
child that keeps being rejected parks to `blocked` after the step's declared
gate-rejection count, with the last objection quoted — nothing is retried
locally. A local child (a local tag, a sequential epic, or the not-signed-in
fallback) still opens the relay.

**Failure.** A remote child whose cloud dispatch fails stays at its from-state,
with the failure and the session URL on the ledger (`cloud_dispatch`, which also
records `placement: remote`). The base agent is not dispatched in its place. The
container's `after_children` step keeps refusing entry, naming that child, until
it has discharged the obligation.

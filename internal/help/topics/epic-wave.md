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

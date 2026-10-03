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
authority. See `satelle help workflow-convert` for `schedule` and `depends-on`.

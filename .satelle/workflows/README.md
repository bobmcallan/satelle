# workflows

Authored lifecycles in the DOT standard (the agent model): each node is a step
with an `agent` (executor|reviewer), each edge a transition, the edge into a
reviewer node its gate. Frontmatter needs `type: workflow`, `scope`, `applies_to`.
The lifecycle must start at `backlog`; `done` is terminal.

Binding form: gate-specific reviewers bind as edge CSV (`prompt="@skill:…"` on the
edge); scoped `on=` nodes are for multi-state/always-on only (estimate, step).
See `satelle help workflows` — edge CSV vs scoped on=, the over-fire trap, and
list-order / short-circuit semantics.

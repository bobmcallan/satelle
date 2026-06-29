# workflows

Authored lifecycles in the DOT standard (the actor model): each node is a step
with an `actor` (executor|reviewer), each edge a transition, the edge into a
reviewer node its gate. Frontmatter needs `type: workflow`, `scope`, `applies_to`.
The lifecycle must start at `backlog`; `done` is terminal.

# workflows

Authored lifecycles in the DOT standard (the agent model): each node is a step
with an `agent` (executor|reviewer), each edge a transition, the edge into a
reviewer node its gate. Frontmatter needs `type: workflow`, `scope`, `applies_to`.
The lifecycle must start at `backlog`; `done` is terminal.

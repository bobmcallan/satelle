---
name: satelle-dot-standard
type: principle
tags: [type:principle]
applies_to: ["*"]
description: Canonical latest workflow DOT format — node-consistent edge gates ([agent=reviewer, prompt="@skill:NAME"]), performing-node prompt convention, required graph/shape/frontmatter fields. THE single reference other workflow-review tools cite. Pointer to Graphviz for raw grammar; satelle-specific conventions only.
---

# Workflows use the DOT standard (canonical form)

satelle workflows are authored and stored as **Graphviz DOT** graphs — the
node-centric form of the agent model. This principle is the **single source of
truth** for the **canonical latest** authored form. Format-drift detection and
assisted refresh target this form; the parser still accepts older input for
back-compat.

For the raw DOT language grammar see Graphviz:
<https://graphviz.org/doc/info/lang.html>. This document states only
satelle-specific conventions.

## Canonical edge-gate form (node-consistent)

A gated edge uses the **same vocabulary as a reviewer node**:

```dot
from -> to [agent=reviewer, prompt="@skill:NAME"]
```

Multi-skill gates join skill names in the prompt (CSV), still node-consistent:

```dot
from -> to [agent=reviewer, prompt="@skill:a,b"]
```

**Legacy (parse-only):** the edge attribute `reviewer_skill="NAME"` (or CSV)
still parses. When both forms are present on one edge, `reviewer_skill` wins.
Legacy is **not** the canonical target — refresh rewrites it to the
node-consistent form. Emitters and serializers **must write** the node-consistent
form only.

## Performing-node prompt convention

A performing node (executor, planner, or other non-reviewer agent on the path of
work) carries an explicit rubric:

```dot
in_progress [agent=executor, prompt="@skill:code"]
```

Reviewer nodes name their gate the same way (`prompt="@skill:…"`). Nodes with no
skill omit `prompt`. The emitter writes `prompt="@skill:…"` whenever a state's
Skill is set.

## Per-gate / per-node model override

A gated **edge** or a **node** may set `model="…"` to override only the model of
the allocated agents.toml binding for that gate or step — without duplicating the
harness into a second binding:

```dot
release -> done [agent=reviewer, prompt="@skill:satelle-story-release-review", model="opus"]
estimate [agent=reviewer, prompt="@skill:satelle-estimate-actual-review", on="in_progress,done", model="sonnet"]
plan [agent=planner, prompt="@skill:plan", model="opus"]
```

- The binding remains the source of **command template and tools**.
- Empty / absent `model=` inherits the binding's model (unchanged behaviour).
- CSV multi-skill edges share one `model=` for all skills on that edge.
- `satelle agent validate` and `satelle workflow validate` print each gate's
  effective model (with an `(override)` marker when DOT `model=` is set).

## Step-level `applies_to` on scoped reviewer nodes

An **edge-less scoped reviewer** (a node with `on=…`) may also carry
`applies_to="surface:ui"` (CSV list) so the gate is enqueued only for stories that
hold a matching tag (sty_c6d093c8 / epic:surface-scoped-steps):

```dot
design [agent=reviewer, prompt="@skill:satelle-design-review", on="integration", applies_to="surface:ui"]
```

| Rule | Behaviour |
| --- | --- |
| Absent `applies_to` | Matches every story (equivalent to `["*"]`) — today's behaviour |
| Matching | EqualFold ANY-match against the story's **tags** only — not category, not kind |
| Multi-surface | A story with both `surface:ui` and `surface:cli` picks up **both** matching nodes (plain filter, no override, no tie-break) |
| On an **edge** | Rejected (the edge IS the transition — skipping it is ambiguous) |
| On a **performing** node | Rejected here; surface-scoped executor rubrics are sty_8225d8a5 |
| Unknown attribute | Rejected with a named error (fail closed — no silent drop) |

Workflow-frontmatter `applies_to` (which workflow governs a category) is a different
altitude and is unchanged. Step-level `applies_to` only filters **whether a scoped
gate is enqueued**, not which workflow is stamped.



## Park node `from=` (sty_f75286dc)

A **park node** (agent=reviewer, non-start) may declare inbound sources without
drawing an N×2 edge explosion:

```dot
blocked [agent=reviewer, prompt="@skill:satelle-story-blocked-review", from="*"]
// optional explicit exits:
blocked -> cancelled
```

| Form | Meaning |
| --- | --- |
| `from="*"` | Every spine **performing** state may park into this node |
| `from="plan,integration"` | Only the named sources may park |

**Resume is not an edge.** The engine stores the origin status on the work item
when entering the park node and enforces resume **only to that origin** — so
parking from `integration` resumes to `integration` without re-running gates
already passed, and cannot wormhole to `release`.

**Wildcards live in attribute values, never as edge endpoints.** `* -> blocked`
is rejected by Validate with a named error (it would register a phantom node,
corrupt Start(), and pollute the diagram). Use `from="*"` on the park node.

Existing explicit `X -> blocked` / `blocked -> X` edges still parse (migration).
When `park_origin` is set, resume to any performing state other than the origin
is refused even if a legacy resume edge is drawn.

## Executor augmentation (`on=` overload, sty_8225d8a5)

An **edge-less performing node** with `on="<state>"` **augments** that spine state's
executor rubrics additively — design knowledge at CODE time, not only at review:

```dot
in_progress [agent=executor, prompt="@skill:code"]
codeui [agent=executor, prompt="@skill:code-ui", on="in_progress", applies_to="surface:ui"]
```

| Story tags | Rubrics for `in_progress` |
| --- | --- |
| (none) | `code` |
| `surface:ui` | `code` + `code-ui` |
| `surface:ui` + `surface:cli` | `code` + `code-ui` (+ `code-cli` if declared) |

**Why reuse `on=` rather than `augments=`.** For a reviewer, `on=X` means “attach to
transitions into X”. For an executor, the same attribute means “attach to the
performing of X”. Both read as attach-to-state; the meaning is keyed by `agent=`.
One concept, documented here — a second name would double the surface without
removing the agent-keyed branch.

Rules:

- Additive, declaration order after the spine skill — no override, no tie-break.
- Augmentation nodes are **annotations**, not lifecycle states (web diagram and
  engagement predicates exclude them).
- Matching uses the same `applies_to` / `tagsMatchAppliesTo` path as scoped
  reviewers (one implementation).
- A missing augmentation skill hard-blocks engagement only for stories whose tags
  match it (the surface-aware wasted-work trap).

## Required graph / shape / frontmatter shape

Documented as the canonical shape (enforcement of lag is format-drift detection,
not a parse failure):

| Layer | Canonical |
| --- | --- |
| Graph attrs | `graph [goal="…", vars="…"]` and `rankdir=LR` |
| Start / terminal | `shape=Mdiamond` (start), `shape=Msquare` (terminal) |
| Frontmatter | `name`, `scope`, `type: workflow`, `applies_to`, `description`; optional `create_review` |

Inline-YAML lifecycle blocks (`states:` / `transitions:`) are **legacy-compat
input**, converted to DOT on ingest (`ToDOT`). Authored workflows should be DOT.

## What is not format drift

Repo-specific topology — extra states, recovery edges, scoped always-on
reviewers (`on=…`), guardrails prose — is **not** format lag. Only superseded
syntax (legacy edge attributes, missing performing-node prompts where the
convention expects them, missing required fields above) is format drift.

See [[satelle-agent-model]].

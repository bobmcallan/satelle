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

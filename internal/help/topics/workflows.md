# Workflows — how a story's lifecycle is chosen

A **workflow** is a story's lifecycle: the states it moves through and the
reviewer that gates each edge, authored in DOT under `.satelle/workflows`. satelle
does not hardcode a lifecycle — the operator authors it as substrate, and satelle
enforces it.

## The ideal: one governing workflow per story type

A workflow declares, in its frontmatter, **what it governs** and **its purpose**:

```yaml
applies_to: ["feature"]      # the story categories this workflow governs ("*" = any)
description: ...             # what this lifecycle is for — read by the agent
create_review: my-rubric    # (optional) the content/alignment reviewer for `story create`
```

The ideal is **one workflow per category**. Selection is then unambiguous and
deterministic.

## Multiple candidates → the agent chooses by content

A repo MAY have more than one workflow that could apply (e.g. a category-specific
one and a wildcard). satelle resolves a single **active** workflow by precedence —
a category-specific match beats a wildcard, and a repo workflow beats the embedded
default. To see the candidates and their purpose for a story's category:

```
satelle workflow list --category <category>
```

The head of that list is the active choice. When several genuinely fit, the agent
picks by the story's content and **records the choice** by adding a
`workflow:<name>` tag at create (otherwise satelle stamps the resolved active one
automatically).

## The choice is stamped and stable

At create, the governing workflow is **stamped** on the story — a `workflow:<name>`
tag plus a `workflow_stamped` ledger entry. Every gate thereafter reads the
**stamped** workflow, not a freshly re-derived one, so a story's lifecycle is
fixed once it begins (deterministic after create).

## Avoiding misconfiguration

Flexibility is not a licence to over-configure. `satelle validate` flags
inconsistencies the operator should fix, and the agent should advise on them:

- **Ambiguous `applies_to`** — two repo workflows that claim the same category (or
  the wildcard) at the same precedence, so the tiebreak is arbitrary.
- **An unresolved reviewer skill** — a workflow names a gate (`reviewer_skill=` or
  an `@skill:` node) that does not resolve in the substrate.

Run `satelle validate workflows` to surface these before they bite.

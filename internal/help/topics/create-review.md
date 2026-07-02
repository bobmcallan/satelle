# Adding a create-review (worked example)

A **create-review** is an optional content/alignment reviewer that judges a
story draft at `satelle story create`, after the deterministic structure check
(clear goal, numbered ACs, category). It is **opt-in twice over**: the repo
enables create-gating (`[review] gate_create = true` in `.satelle/satelle.toml`),
and the governing workflow **declares** the reviewer via its `create_review`
frontmatter. Absent either — or if the declared skill does not resolve —
creation stays deterministic-only. This guide is the end-to-end recipe.

## 1. Author the reviewer rubric skill

Create `.satelle/skills/my-create-review.md`:

```markdown
---
name: my-create-review
scope: project
type: skill
tags: [type:skill, type:reviewer]
description: Create gate — judges a story draft is aligned with this repo's conventions before it is persisted.
---

# Story create review

You are an isolated reviewer judging whether a story DRAFT should be created.
You receive the draft (title, body, acceptance_criteria, category, tags) on
stdin. Judge alignment — the structural basics (goal, numbered ACs) have
already passed deterministically.

## Accept when

1. The story is one shippable slice (not several bundled mechanisms).
2. The body says what done looks like in this repo's terms.

## Reject when

Scope is bundled or the intent conflicts with an existing open story. On
reject, give a short, actionable list so the author can fix and resubmit.

## Verdict

Reply with exactly one JSON object, nothing else of that shape:

```json
{"decision": "accept", "notes": ""}
```

`decision` is `"accept"` or `"reject"`; `notes` is a brief actionable string
(may be empty on accept).
```

The three parts every reviewer skill needs: the frontmatter (`name` matching
the filename, `type: skill`, a `description`), the rubric body (accept/reject
criteria), and the verdict contract (the JSON object above — prose verdicts
like `Verdict: reject` are tolerated, but the JSON block is the contract).

## 2. Declare it on the governing workflow

Add one frontmatter key to the workflow that governs the story's category
(`.satelle/workflows/<your-workflow>.md`):

```yaml
---
name: my-project-workflow
scope: project
type: workflow
applies_to: ["*"]
create_review: my-create-review   # <- the binding
---
```

The binding lives on the **workflow**, not in code or config-by-filename: the
draft's category selects the workflow, and that workflow names the reviewer.
Different categories can carry different create-reviews (or none).

## 3. Enable create-gating

In `.satelle/satelle.toml`:

```toml
[review]
gate_create = true
```

## Confirm it is wired

- Read the workflow frontmatter: `satelle doc get workflows my-project-workflow`
  should show `create_review: my-create-review`.
- Validate the skill: `satelle skill validate my-create-review` passes.
- Validate the binding: `satelle workflow validate` flags a workflow that
  declares a `create_review` which does not resolve in the substrate — a clean
  pass means the binding is live.
- Try it: `satelle story create --title … --body … --acceptance "1. …" --category …`
  now runs your reviewer after the structure check; a reject blocks creation
  and prints the reviewer's notes.

## What happens when it is NOT wired

This is a graceful degradation, not an error: no `gate_create`, no
`create_review` declaration, or an unresolved skill each mean `story create`
runs the deterministic structure check only. The `workflow validate` warning
above is how an *intended-but-broken* binding is surfaced.

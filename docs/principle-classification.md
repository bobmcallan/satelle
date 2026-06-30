# Principle classification — embedded (operating) vs project (authoring/dev)

Story `sty_807ae744`. Which principles ship **embedded** in the binary
(`scope: system`, travel to every repo) versus live in this repo's
`.satelle/principles` (`scope: project`)? The criterion is the **agent's
operating perspective**: *what does an agent need to OPERATE satelle — to drive a
story through its gated workflow?*

## Embedded (required to operate)

| Principle | Why it stays embedded |
|---|---|
| `satelle-agent-goals` | It *is* the operating discipline — drive to the terminal state, status is the sole proof of done, never route around a gate, one story at a time. |
| `satelle-agent-model` | The model the agent operates within — executor mutates, reviewer judges, status advances only on a reviewer accept. |

These two are what an agent must know to operate; they are repo-agnostic and ship
to every satelle repo.

## Relocated to `.satelle/principles` (authoring / development, not operating)

| Principle | Why it moved |
|---|---|
| `satelle-dot-standard` | Needed to *author* a workflow, not to drive one. |
| `satelle-configuration-over-code` | satelle's *build/design* philosophy (keep process out of the binary) — guidance for developing satelle. |
| `satelle-reviewer-self-contained` | A rule for *authoring* a reviewer skill. |
| `satelle-done-is-last` | A workflow-*authoring* invariant, already enforced by the binary (the mandatory done gate on the spine); `agent-goals` already tells the agent to drive to the terminal state. |

The binary still enforces the mechanisms these describe (DOT parsing, the done
spine, self-contained reviewers); the **principle prose** is satelle's own
development/authoring substrate and belongs with this repo's other development
principles (`agile-increments`, `broken-windows`, `yagni`, `constitution`,
`repo-agnostic`).

## Residency is preserved

Relocating does not change what the agent sees each session: each moved file keeps
its residency tag, so the `principles:always` ones (`configuration-over-code`,
`done-is-last`, `reviewer-self-contained`) are still injected at SessionStart from
`.satelle/principles`. The change is **scope** (`system → project`) and **home**
(embedded → repo substrate), not residency.

## Note on the reviewer rubric

`satelle-principle-review` rejects a `scope: system` principle that carries
opinion/repo-specifics; it does not object to mechanism-describing prose living at
`scope: project`. Moving these out of the embedded set keeps the embedded
`scope: system` set free of anything beyond the operating essentials.

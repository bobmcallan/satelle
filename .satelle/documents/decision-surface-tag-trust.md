---
name: decision-surface-tag-trust
type: document
tags: [type:document, epic:surface-scoped-steps]
description: Decision for sty_dcce86d5 — whether a self-declared surface: tag is trustworthy enough to skip a gate. Option (c) accepted risk for v1 dogfood, with skip visibility and a named revisit trigger.
---

# Decision: surface: tag trust for gate skipping

**Story:** sty_dcce86d5  
**Epic:** epic:surface-scoped-steps  
**Date:** 2026-07-14  
**Status:** decided

## Decision

**Option (c) — v1 trusts tags (accepted risk), with a visible skip artifact.**

A `surface:` tag remains self-declared and mutable. Step-level `applies_to` on
scoped reviewers (sty_c6d093c8) and executor augmentations (sty_8225d8a5) filter
from tags alone. We **do not** ship a deterministic path→surface rubric in v1.

## Why not (a) yet

Option (a) — mirror `satelle-substrate-only-check` with a path→surface rubric —
is the right long-term pattern. That check keeps `category: substrate` honest by
inspecting the committed diff from **inside a rubric**, because `workitem.Item`
carries no diff and the engine has no diff seam. The same constraint applies to
surface: any honest path check must live in a reviewer rubric, not in
`applies_to`.

We defer (a) because:

1. The first consumer is dogfood only (sty_e4359efe design-system gate).
2. UI path inventory is repo-specific config (constitution: no path rule in the binary).
3. The check can only run once a diff exists (code/integration time), not at plan.

## Residual hole (explicit)

**A dropped or omitted `surface:ui` tag silently fails OPEN:** the design gate
and any `code-ui` augmentation never enqueue. That is **indistinguishable from a
story that legitimately has no UI surface** at the tag layer.

**Mitigations in v1:**

1. **Skip visibility (AC4):** when a scoped reviewer with non-empty `applies_to`
   matches `on=` but not the story's tags, the engine records a
   `scoped-gate-skipped` telemetry event (skill, to-status, tags). A filtered
   gate is no longer invisible.
2. **Create-time classification:** `satelle-story-create-review` already classifies
   controlled namespaces; agents should tag interfaces at create (sty_034d843c).
3. **Named revisit trigger:** implement option (a) **before any surface-scoped
   gate becomes load-bearing for a release** (or before it gates production
   ship criteria). Owner: the operator of this repo / epic:surface-scoped-steps
   follow-up.

## Precedent

`satelle-substrate-only-check` is the template for (a): deterministic diff
inspection inside a rubric keeps a self-declared discriminator honest. When we
revisit, reuse that shape with UI paths from satelle.toml config (not hardcoded
Go), analogous to `[tags.vocabulary]`.

## Constitution

No repo-specific path or surface rule is compiled into the binary. Vocabulary
and (when built) path lists stay in satelle.toml.

## See also

- sty_034d843c — surface vocabulary  
- sty_c6d093c8 — applies_to gate filtering  
- sty_8225d8a5 — executor augmentation  
- satelle-substrate-only-check — honesty precedent  
- satelle-story-classification — controlled namespaces  

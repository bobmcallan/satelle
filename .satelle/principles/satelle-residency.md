---
name: satelle-residency
type: principle
tags: [type:principle, principles:session]
applies_to: ["*"]
description: Residency is the single injection axis — system (principles:session marker, always injected) vs ondemand (no marker, pull on reference). Ownership (embedded_sha) is orthogonal.
embedded_sha: c946d8dc1a30222a0fe8b52e66777e35c0d116b33fd231e151daa739ab9b8931
---

# Principle residency

Residency is the **only** axis that controls whether a principle is auto-injected
into session context. There is no second classifier.

## The two tiers

| Tier | Meaning | Marker |
| --- | --- | --- |
| **system** | Always injected at SessionStart | carries the `principles:session` tag |
| **ondemand** | Discoverable; pulled on reference | absence of that tag (the default) |

- **system** — `satelle hook context` injects the body every session (bounded by
  the SessionStart byte ceiling). Keep this set minimal.
- **ondemand** — pull with `satelle doc get principles <name>` when a skill,
  workflow, or the constitution references it. Do not preload.

The on-disk **system-residency marker is the `principles:session` tag**. One
classifier — not a `scope:` field, not a second frontmatter key.

## Ownership is orthogonal

`embedded_sha` (file-local provenance from init) marks **binary-seeded ownership**,
not residency:

| | system (session-tagged) | ondemand (untagged) |
| --- | --- | --- |
| **embedded_sha present** | binary-owned always-injected default | binary-owned reference principle |
| **no stamp** | operator-authored always-injected | operator-authored on-demand |

A repo may author its own always-injected principle with no stamp. An embedded
default may be on-demand (e.g. `satelle-agent-model`). Do not conflate the two
axes.

See [[satelle-agent-goals]], [[satelle-edits-require-a-story]],
[[satelle-constitution]].

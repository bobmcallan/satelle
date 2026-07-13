---
name: principle-residency-audit
type: document
tags: [type:document, context, principles, epic:channel-alignment, order:1]
description: Living audit of principle residency — system vs ondemand under the single SessionStart ceiling (alwaysContextCeiling = 16384 bytes). Updated by sty_da2abd5c (channel-alignment order:1 — promote satelle-repo-agnostic to resident).
---

# Principle residency audit

Residency is the **only** injection axis for principles. One marker:
`principles:session` in frontmatter tags.

| Tier | Marker | Behaviour |
| --- | --- | --- |
| **system** | carries `principles:session` | injected at every SessionStart by `satelle hook context` |
| **ondemand** | no marker (default) | pull with `satelle doc get principles <name>` when referenced |

There is no `scope:` field on principles. Ownership (`embedded_sha`) is
**orthogonal** to residency. See [[satelle-residency]].

## The SessionStart ceiling

**`alwaysContextCeiling = 16384` bytes** (`internal/cli/cmd_hook.go`) **IS the
single SessionStart budget**. It bounds constitution + system-resident principle
bodies + the on-demand pointer. There is no second budget. Overflow truncates
with a stderr note; the hook still fails open.

Measure: `satelle hook context 2>/dev/null | wc -c`

## Channel-alignment order:1 (sty_da2abd5c)

Promote **`satelle-repo-agnostic`** to system residency so the constitution's
declared order-zero guard rides the **push** channel. It is **repo-local by
nature** (governs developing satelle itself; not product-canon for other repos)
and therefore stays unstamped (no `embedded_sha`).

| | Bytes (`hook context`) | Headroom under 16384 |
| --- | ---: | ---: |
| **BEFORE (triad only)** | ~13060 | ~3324 |
| **AFTER (+repo-agnostic)** | 15531 | 853 |

**Trade:** none demoted. Headroom after the context diet (sty_cd5e341c) absorbed
the ~2.5 KB body without cutting the operating triad. Preference when a future
promotion would overflow: favour identity/altitude rules
(`satelle-repo-agnostic`) over restating what gates already enforce.

**AFTER resident set (4):**

| Principle | Tier | Ownership | Why |
| --- | --- | --- | --- |
| `satelle-repo-agnostic` | **system** | repo-local (no stamp) | order-zero product-vs-dogfood guard on every code change |
| `satelle-agent-goals` | **system** | embedded | drive-to-terminal / status-is-proof / one-story |
| `satelle-edits-require-a-story` | **system** | embedded | engage-before-edit/commit gate discipline |
| `satelle-recognise-blockage` | **system** | embedded | park-reason-resume |

Prior diet demotions remain ondemand: `satelle-residency`,
`satelle-agent-telemetry`, `satelle-generated-readonly`.

## Per-principle table (current)

| Principle | Tier | Notes |
| --- | --- | --- |
| satelle-repo-agnostic | **system** | Product vs dogfood; repo-local identity rule |
| satelle-agent-goals | **system** | Operating discipline |
| satelle-edits-require-a-story | **system** | Edit/commit gate rule |
| satelle-recognise-blockage | **system** | Blockage park (not missing engagement) |
| satelle-residency | ondemand | Defines system\|ondemand; embedded reference |
| satelle-agent-model | ondemand | Execution model; embedded |
| satelle-agent-telemetry | ondemand | Prompted telemetry channel |
| satelle-generated-readonly | ondemand | Generated OKF views are 0o444 |
| satelle-skill-naming | ondemand | Skill naming convention |
| satelle-agile-increments | ondemand | Delivery paradigm |
| satelle-broken-windows | ondemand | Working discipline |
| satelle-configuration-over-code | ondemand | Harness design (overlaps constitution) |
| satelle-done-is-last | ondemand | Workflow invariant |
| satelle-dot-standard | ondemand | DOT grammar pointer |
| satelle-enable-then-operate | ondemand | Init vs operate phases |
| satelle-reviewer-self-contained | ondemand | Reviewer authoring rule |
| satelle-story-classification | ondemand | Epic / sprint / order |
| satelle-yagni | ondemand | Coding paradigm |

Constitution (`.satelle/constitution.md`) is injected first every session and is
**not** a principle.

## Consistency

The resident set is now identity + operating triad: know what product you are
building, engage a story, drive it through gates, park on real blockage.

## History

- **sty_cd5e341c** (context diet): resident set reduced 6 → 3; ceiling headroom restored.
- **sty_da2abd5c** (this story): `satelle-repo-agnostic` promoted system; no demotion.

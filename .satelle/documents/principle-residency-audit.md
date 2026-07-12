---
name: principle-residency-audit
type: document
tags: [type:document, context, principles, epic:substrate-convergence, order:4]
description: Living audit of principle residency — system vs ondemand under the single SessionStart ceiling (alwaysContextCeiling = 16384 bytes). Updated by sty_cd5e341c (context diet).
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

## Context diet (sty_cd5e341c, order:4 of epic:substrate-convergence)

| | Bytes (`hook context`) | Headroom under 16384 |
| --- | ---: | ---: |
| **BEFORE** | 16061 | 323 (near overflow; stderr truncation risk) |
| **AFTER** | 13060 | 3324 |

**BEFORE resident set (6):** `satelle-agent-goals`, `satelle-edits-require-a-story`,
`satelle-recognise-blockage`, `satelle-residency`, `satelle-agent-telemetry`,
`satelle-generated-readonly` (+ order-zero constitution).

**AFTER resident set (3) — the operating triad:**

| Principle | Tier | Ownership | Why |
| --- | --- | --- | --- |
| `satelle-agent-goals` | **system** | embedded | drive-to-terminal / status-is-proof / one-story |
| `satelle-edits-require-a-story` | **system** | embedded | engage-before-edit/commit gate discipline |
| `satelle-recognise-blockage` | **system** | embedded | park-reason-resume (consistent with edits-require after order:2) |

**Demoted to ondemand (remove `principles:session`):**

| Principle | Ownership | Why demote |
| --- | --- | --- |
| `satelle-residency` | embedded | taxonomy *definition* is authoring reference, not per-session operating guidance; lands in embedded source + converges via `satelle init` / `embedded_sha` |
| `satelle-agent-telemetry` | repo-authored | prompted self-report channel; supplementary quality logging |
| `satelle-generated-readonly` | repo-authored | `0o444` mode self-enforces; discoverable on reference |

**Already ondemand (no action):** `satelle-repo-agnostic`, `satelle-skill-naming`,
`satelle-agent-model`, and every other principle under `.satelle/principles/`.

## Per-principle table (current)

| Principle | Tier | Notes |
| --- | --- | --- |
| satelle-agent-goals | **system** | Operating discipline |
| satelle-edits-require-a-story | **system** | Edit/commit gate rule |
| satelle-recognise-blockage | **system** | Blockage park (not missing engagement) |
| satelle-residency | ondemand | Defines system\|ondemand; embedded reference |
| satelle-agent-model | ondemand | Execution model; embedded; length reserved for order:5 rewrite |
| satelle-agent-telemetry | ondemand | Prompted telemetry channel |
| satelle-generated-readonly | ondemand | Generated OKF views are 0o444 |
| satelle-repo-agnostic | ondemand | Product vs dogfood guard |
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

Constitution (`.satelle/constitution.md`, ~5.3 KB) is injected first every
session and is **not** a principle — not trimmed by this diet.

## Consistency

The three system principles form a consistent operating triad: engage a story,
drive it through gates, park on real blockage — never treat missing engagement
as blockage (order:2). Demotion removes reference docs; it does not reintroduce
conflicts.

## Flagged out of scope (not fixed here)

Disk copies of `satelle-agent-goals` and `satelle-edits-require-a-story` may be
body-ahead of embedded sources without an `embedded_sha` stamp (order:2
convergence tail). A future rebase could drop those enrichments — track as a
follow-up convergence story, not this diet.

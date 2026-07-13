---
name: principle-residency-audit
type: document
tags: [type:document, context, principles, epic:channel-alignment, order:2]
description: Living audit of principle classification (canon/repo-local/dead), residency, and carrying channel. Decision-of-record for sty_ceb1a3ef (channel-alignment order:2).
---

# Principle channel audit (decision of record)

Residency is the **only** injection axis for principles. One marker:
`principles:session` in frontmatter tags.

| Tier | Marker | Behaviour |
| --- | --- | --- |
| **system** | carries `principles:session` | injected at every SessionStart by `satelle hook context` |
| **ondemand** | no marker (default) | pull with `satelle doc get principles <name>` when referenced |

Ownership (`embedded_sha`) is **orthogonal** to residency. See [[satelle-residency]].

## The SessionStart ceiling

**`alwaysContextCeiling = 16384` bytes** (`internal/cli/cmd_hook.go`).
Measure: `satelle hook context 2>/dev/null | wc -c`

| | Bytes | Headroom under 16384 |
| --- | ---: | ---: |
| After order:1 (+repo-agnostic) | 15531 | 853 |

Resident set is unchanged by order:2 (embed promotions do not add session tags).

## Classification framework (sty_ceb1a3ef)

| Verdict | Meaning | Action |
| --- | --- | --- |
| **canon** | Any repo needs it to operate or author in the harness | Body under `internal/config/substrate/principles/`; stamped materialization |
| **repo-local** | Governs developing satelle / this dogfood repo | Unstamped under `.satelle/principles/`; self-declares "Repo-local" |
| **dead** | Redundant or channel-less orphan | Merge load-bearing sentence into surviving home, then delete |

## Count arithmetic (AC4)

| | Count |
| --- | ---: |
| Start (README excluded) | 18 |
| Deleted (`satelle-configuration-over-code`) | −1 |
| Added | 0 |
| **End** | **17** (≤18 ✓) |

## Per-principle table (AC1 + AC3)

| Principle | Classification | Tier | Channel | Verify |
| --- | --- | --- | --- | --- |
| satelle-agent-goals | **canon** (embedded) | system | resident tag | `hook context` contains it |
| satelle-edits-require-a-story | **canon** (embedded) | system | resident tag | `hook context` contains it |
| satelle-recognise-blockage | **canon** (embedded) | system | resident tag | `hook context` contains it |
| satelle-repo-agnostic | **repo-local** | system | resident tag | `hook context` contains it; unstamped |
| satelle-agent-model | **canon** (embedded) | ondemand | referencing skills/workflows | `grep -rl satelle-agent-model` substrate |
| satelle-residency | **canon** (embedded) | ondemand | defining reference | embedded body names `principles:session` |
| satelle-done-is-last | **canon** (**promoted**) | ondemand | referencing workflows/skills | parent/substrate/task workflows cite it |
| satelle-reviewer-self-contained | **canon** (**promoted**) | ondemand | referencing skills | integration-check, substrate-only-check cite it |
| satelle-dot-standard | **canon** (**promoted**) | ondemand | referencing skill | `satelle-workflow-advisor` cites it |
| satelle-story-classification | **canon** (**promoted**) | ondemand | referencing workflow | parent workflow + create-review (order:3) |
| satelle-agent-telemetry | **repo-local** | ondemand | referencing skill | `satelle-step-summary` cites it |
| satelle-generated-readonly | **repo-local** | ondemand | referencing skill | `build.md` cites it |
| satelle-yagni | **repo-local** | ondemand | referencing skill | `build.md` cites it |
| satelle-broken-windows | **repo-local** | ondemand | referencing skill | `build.md` cites it |
| satelle-agile-increments | **repo-local** | ondemand | referencing skill | `build.md` cites it |
| satelle-skill-naming | **repo-local** | ondemand | referencing skill | `build.md` cites it |
| satelle-enable-then-operate | **repo-local** | ondemand | referencing skill | `build.md` cites it |
| satelle-configuration-over-code | **dead** (deleted) | — | merged into constitution "Configuration over code" | file gone; agent-model See-links repointed |

## Embedded set after order:2 (AC2)

`internal/config/substrate/principles/`:

1. satelle-agent-goals
2. satelle-agent-model
3. satelle-edits-require-a-story
4. satelle-recognise-blockage
5. satelle-residency
6. satelle-done-is-last *(new)*
7. satelle-reviewer-self-contained *(new)*
8. satelle-dot-standard *(new)*
9. satelle-story-classification *(new)*

Materialization: `materializePrinciples` on init/rebase seeds every embedded
principle into `.satelle/principles/<name>.md` with `embedded_sha`. Asserted by
`TestEmbeddedOperatingPrinciples` in `internal/config/embed_principle_test.go`.

## Workflow-embedded rules surfaced (AC5)

| Source | Rule that lived only there | Disposition |
| --- | --- | --- |
| `satelle-parent-workflow` description | File containers as `category: epic-parent` / `parent`; category-specific `applies_to` beats wildcard | Folded into **satelle-story-classification** (Category section); parent workflow now wikilinks that principle |
| (scan) other workflows | Lifecycle/guardrails prose only — no orphan normative class rules | No additional principle created |

## History

- **sty_cd5e341c** — context diet: resident 6 → 3.
- **sty_da2abd5c** (order:1) — promote `satelle-repo-agnostic` to resident (4).
- **sty_ceb1a3ef** (order:2) — channel audit: 4 promotions, 1 deletion, 8 repo-local self-declarations; count 18 → 17.

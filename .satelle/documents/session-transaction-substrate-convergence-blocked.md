---
type: document
title: Session transaction — epic:substrate-convergence (superseded framing + restructure)
description: Historical block note for the heal-era epic framing; updated after epic review option 2 restructure. Current critical path is order:2 contradiction fix (sty_0334d12b).
tags: [document, session-trace, session-transaction, epic:substrate-convergence, gate, superseded-in-part]
timestamp: '2026-07-12T13:00:00Z'
---

# Session transaction: epic:substrate-convergence

**Status of this document.** The **block narrative below is historical** (why an
early implement session stopped). The **child list and Option-A/Option-B framing
are SUPERSEDED** by the 2026-07-12 redesign and the later **epic review option 2
restructure**. Do not resume from the heal-era orders in § children-old.

## Current epic shape (authoritative)

| Order | Id | Status | Title (short) |
| --- | --- | --- | --- |
| 1 | `sty_304ee454` | **done** | `embedded_sha` provenance (stamp + re-init converge) |
| 2 | `sty_0334d12b` | critical path | Fix recognise-blockage vs edits-require-a-story |
| 3 | `sty_1278fdd9` | backlog | Residency taxonomy system\|ondemand |
| 4 | `sty_cd5e341c` | backlog | Context diet (SessionStart ceiling) |
| 5 | `sty_704bfb8b` | backlog | agent-model rewrite short+correct |
| 6 | `sty_ed1443eb` | backlog | Placement validator (deterministic) |
| 7 | `sty_4afc458d` | backlog | agents.toml format-migration (deprioritised) |
| 8 | `sty_77abd78b` | backlog | Context-audit task |
| 9 | `sty_1019dc3c` | backlog | Post-release lessons step |

Parent: `sty_0d2c5472` · tag `epic:substrate-convergence`.

**Provenance is in scope** (order:1 shipped). Merge skill for diverged files is
**out of epic** — order:1 surfaces + backs up only.

**Next work:** engage **order:2** `sty_0334d12b` (contradiction fix), not
agents.toml migration.

---

## Historical: why an early implement session stopped

**Session objective (then).** `implement epic:substrate-convergence` under the
old Option-A *heal, no provenance* four-child plan.

**Hook / gate.** `satelle hook gate` denied first product-code write: no
performing story engaged (all children `backlog`). Enforcement worked as
designed (`satelle-edits-require-a-story`).

**Additional finding (drives order:2).** The same session surface showed two
SESSION-injected principles contradicting each other: `satelle-recognise-blockage`
listed “nothing engaged” as blockage while `satelle-edits-require-a-story`
prescribes engage-and-proceed. That content bug is the critical path after
order:1, not config format migration.

**Children-old (DO NOT USE):** order:1 config reconciler · order:2 init heal ·
order:3 `satelle heal` · order:4 help spectrum — replaced by the table above.

**Unblock then (still true):** engage a performing project story through the
workflow (`plan` / gates / `in_progress`) before editing non-exempt paths.
`.satelle/documents/` remains edit-exempt for notes like this one.

---

## Explicit non-claims (updated)

- The heal-era child list is **not** current backlog.
- Option B / provenance is **not** out of scope — order:1 is **done**.
- Implementation of order:2+ is **not** complete until those stories close.

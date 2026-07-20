---
type: execution
status: done
category: process
parent: tsk_backlog-groom
created: 2026-07-16T10:30:00Z
updated: 2026-07-16T10:30:00Z
---

# Full backlog groom — condense to one epic, cull the rest

OPERATOR DIRECTIVE: too many backlog items; condense core structural stories
into a single epic actioned AFTER the re-arch (epic:substrate-planes); cull
everything "potential" without fear of loss.

## Result: 22 backlog → 10 (5 re-arch + 1 epic-parent + 4 keepers)

## Created

- sty_ec034eca `epic-parent` — epic:post-rearch-core. Holds the four keepers;
  notes sty_ca64d0cb is calendar-coupled to satelle-server and may pull forward.

## Kept (parented to sty_ec034eca, tag epic:post-rearch-core)

| order | id | why core structural |
|---|---|---|
| 1 | sty_ca64d0cb | breaking server API coordination (team-workspaces) — time-sensitive |
| 2 | sty_1d069587 | live sync deadlock bug (relocation sty_4660bbe1 may dissolve it — verify then) |
| 3 | sty_c21490cc | step-scoped command block; real push-at-in_progress slip; shares machinery with sty_aadd4d6c |
| 4 | sty_f75286dc | park-from-any-step workflow mechanism |

Tag hygiene: sty_1d069587 dropped epic:sync-publish/order:7; sty_f75286dc
dropped epic:park-from-any-step/order:1 (single-epic membership).
Cross-links: sty_ebd3d666 +supersedes:sty_07eb0f22; sty_aadd4d6c
+relates:sty_c21490cc.

## Cancelled (13 — each body carries a one-line audited reason)

- sty_dabeeeba tag-hygiene chore
- sty_dde5aa70 vocabulary layering reshaped by virtual defaults (sty_29e5a9a5)
- sty_da9bbc18 drift/refresh surface shrinks under virtual defaults
- sty_4d69e1e7 low tech-debt consolidation
- sty_3819b378 covered by satelle-substrate-audit skill + sty_29e5a9a5 audit AC
- sty_fdc2850a cosmetic (tag chips)
- sty_ecb1af58 cosmetic (muted skipped-step circle)
- sty_62688b42 summariser nicety
- sty_7d352a92 /workspace refinement — resurfaces on own evidence if it bites
- sty_952d6ff2 CONTRADICTS the re-arch (materializing embedded help into repos)
- sty_a123ec36 absorbed by runtime relocation (sty_4660bbe1)
- sty_07eb0f22 superseded by sty_ebd3d666 (declared-optional is the skip path)
- sty_390e6b45 covered by satelle-workflow-drift skill + virtual defaults

## Notes

- Cancel transitions passed satelle-story-cancel-review only after a reason
  was appended to each body (first attempt rejected: "no cancel reason on
  record") — the gate worked as designed.
- Resurrect candidates if evidence returns: sty_7d352a92 (UI availability),
  sty_dde5aa70 (category vocabulary).

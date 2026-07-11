---
type: document
title: Epic closed — workflow-review-followups
description: Close record for epic:workflow-review-followups (sty_4603db29). Children shipped; residual substrate landed.
tags: [document, epic:workflow-review-followups]
---

# Epic closed: workflow-review-followups

Parent: `sty_4603db29` (restamped `epic-parent` / `satelle-parent-workflow` for close).

Children (terminal):

| Order | Story | Status | Ship |
| --- | --- | --- | --- |
| 1 | sty_e3687ec4 | done | 26a4781 — hybrid decision (A) |
| 2 | sty_e433dee4 | done | deb0037 — prose pass (code/release triad/estimate/browser) |
| 3 | sty_64ffe668 | done | abf2f6e — format lag cleared (substrate/task-run prompts) |
| 4 | sty_ca97c680 | done | 3a5322f — plan-fidelity hard gate in code-ac-review |
| 5 | sty_577d292f | done | ed9be8e / e8156df — commitgate deny semantics (v0.0.191) |
| 6 | sty_6572de21 | done | 6e19dfc — engage-before-commit session principle |
| folded | sty_dfbbf9ad, sty_5c325147, sty_3e65beeb | cancelled | residual into sty_e433dee4 |

**Outcomes:**

- Hybrid agent-model decision recorded; `code.md` matches in-loop executor allocation.
- Dogfood triad named (`check_cli_version`, `check_live_footer`, `check_persistent_supervisor`).
- Substrate + task workflows format-drift CLEAN (performing-node prompts).
- Plan fidelity is a hard gate in `satelle-code-ac-review` when a plan exists.
- commitgate deny text + always-loaded engage-before-commit rule ship so fused engage+commit fails with a fixable message.

Closed via parent-workflow children-resolved gate after orders 2–4 residual commits landed post hold.

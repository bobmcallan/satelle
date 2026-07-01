---
name: principle-residency-audit
type: document
tags: [type:document, context, principles, epic:session-context]
description: Dogfood audit (tsk_fa292e14) of every .satelle/principles/*.md — token length, session/on-demand residency tier, and relevance. Confirms the injected session set is minimal (operating principle only, alongside the order-zero constitution) and within the 16384-byte SessionStart ceiling.
---

# Principle residency audit (tsk_fa292e14)

Reviews every principle for relevance and token length and classifies each into
the two residency tiers the code actually honours (`internal/cli/cmd_hook.go`):

- **session** — carries the `principles:session` marker in `tags:`; injected at
  every SessionStart by `selectAlwaysDocs`, alongside the order-zero constitution.
- **on-demand** — the default (no marker); pulled with `satelle doc get principles
  <name>` when a skill, workflow, or the constitution references it.

`principles:global` was a **stale, no-op tag** — no code path reads it (only
`principles:session` is live). It was removed from the three docs that carried it
so the taxonomy is exactly two tiers: session-marked, or nothing.

Token counts are estimated at ~4 chars/token from the on-disk file size.

## Per-principle table

| Principle | ~tokens | Tier | Relevance / why this tier |
|---|--:|---|---|
| satelle-agent-goals | 427 | **session** | The operating principle. Every session needs drive-to-terminal, status-is-proof, one-story-at-a-time, and surface-don't-work-around. The only principle that belongs in every context. |
| satelle-agent-model | 2137 | on-demand | The full executor/reviewer execution model. Deep and detailed — pulled when authoring or driving a workflow. Longest principle, but the length is substantive, not bloat; not session because it is reference, not per-session guidance. |
| satelle-agile-increments | 482 | on-demand | Delivery paradigm (small stories, one commit each). Pulled when decomposing a request. |
| satelle-broken-windows | 387 | on-demand | Working discipline (a failure you meet is yours; never add a new red). Pulled at commit / debt decisions. |
| satelle-configuration-over-code | 663 | on-demand | Harness design (process is substrate, not code). **Redundant with the constitution's "Configuration over code" section** (near-verbatim); its own description says it "explains how satelle is built, not a rule an agent needs to drive a story." Kept on-demand; flagged for dedup below. |
| satelle-done-is-last | 417 | on-demand | Workflow invariant (done is terminal; gates precede it). The gist already rides in the session set via agent-goals; the full statement is pulled when reasoning about workflow shape. |
| satelle-dot-standard | 227 | on-demand | Pointer to the DOT grammar for workflow authoring. Situational. |
| satelle-enable-then-operate | 728 | on-demand | Two-phase structure (init scaffolds `.satelle/`; every command operates from it). Pulled when touching init / root resolution. |
| satelle-repo-agnostic | 734 | on-demand | The order-zero guard on every code change (product vs dogfood repo). The constitution names it, so a code change pulls it on demand — it does not need to ride every session pre-emptively. |
| satelle-reviewer-self-contained | 547 | on-demand | Reviewer-authoring rule (rubric + check live in the skill). Pulled when writing a gate. |
| satelle-story-classification | 674 | on-demand | Epic / sprint / order taxonomy. Pulled when classifying stories. |
| satelle-yagni | 502 | on-demand | Coding paradigm (build for a real need, not a foreseen one). Pulled when designing a change. |

Session tier total: **~427 tokens / 1710 bytes** — one principle. Well under the
16384-byte SessionStart ceiling (`alwaysContextCeiling`), even added to the
constitution which rides first.

## Findings

1. **Session set is already minimal and correct.** Only `satelle-agent-goals`
   carries `principles:session`; combined with the order-zero constitution that is
   exactly the "operating principle + constitution" target. No principle needed
   promoting to session, and none needed demoting out of it.
2. **No principle is over-long for its tier.** The only budget-constrained tier is
   session (the 16384-byte ceiling); it holds one 1710-byte doc, so nothing
   over-long is injected. On-demand principles are each tight and single-purpose;
   the largest (agent-model, ~2137 tok) is substantive reference pulled
   deliberately, not session bloat. No content trim was required.
3. **Stale `principles:global` removed** from `satelle-agent-model`,
   `satelle-dot-standard`, and `satelle-story-classification` — a no-op tag no code
   reads, which misleadingly implied a third residency tier. Now two tiers only.

## Follow-up (not actioned here)

`satelle-configuration-over-code` substantially duplicates the constitution's
"Configuration over code" section. Deduping it (fold the unique bits into the
constitution or repo-agnostic, then retire the standalone principle and fix the
inbound `[[satelle-configuration-over-code]]` links in agent-model,
enable-then-operate, and repo-agnostic) is a structural substrate change with link
fan-out — it belongs in its own reviewed story, not this residency audit.

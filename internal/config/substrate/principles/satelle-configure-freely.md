---
name: satelle-configure-freely
type: principle
tags: [type:principle]
applies_to: ["*"]
description: When a change would refuse, block or reject an operator's configuration, or apply a rule the operator cannot read in the docs: configure freely, warn on what is unsupported or sub-standard, never block it, and never keep a rule undocumented.
---

# Configure freely; warn, never block

The operator decides what runs. satelle's default is to let the agent or
developer configure what they choose, and to **warn** when the configuration is
unsupported or below standard. It does not block a configuration, and it does not
apply a rule that is undocumented or buried.

1. **Warn, do not refuse.** A configuration satelle cannot fully support — a
   reviewer whose tools it cannot hold to the grant, an adapter that reports no
   usage, an unrecognised harness — still runs. The gap is stated, never silently
   accepted and never turned into an error that stops the loop. A refusal that
   leaves the operator unable to reach the fix (a seat that cannot file the story
   to change the seat) is the failure this principle exists to prevent.
2. **The warning is never silent.** Show it at the point of use (one line on
   stderr naming the binding, the adapter, the specific gap and the fix), record it
   on the ledger, report it in `satelle doctor`, and list it in `satelle help`.
3. **An acknowledgement downgrades, it does not hide.** An operator who accepts a
   known gap acknowledges it in configuration (`isolation = "operator-attested"`).
   The dispatch warning becomes an info note in doctor; the ledger still records
   the limitation.
4. **Observed behaviour is recorded and warned, never aborts.** A tool that ran
   outside its grant, or a peer that never asked, is recorded and warned. It does
   not cancel the run the operator chose to make.
5. **No undocumented or buried rule.** Every warned configuration, its gap and its
   fix is listed in help. A rule an operator can only discover by hitting it is a
   defect.

This does not touch **gates**: a reviewer verdict still decides whether a story
advances ([[satelle-agent-goals]], [[satelle-constitution]]). It governs how
satelle treats the operator's *configuration*, not how a gate judges the work.
Provider-specific gap text stays in that provider's adapter, never in the engine.

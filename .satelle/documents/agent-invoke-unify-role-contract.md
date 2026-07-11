---
story: sty_69fd4e20
type: design
name: design
---

# Design: role contract in agents.toml — reviewer as a declared role on the one sub-process path

**Story:** sty_69fd4e20 (order:1 of epic sty_fc670c9b / epic:agent-invoke-unify)  
**Status:** authoritative for implementers of sty_ba860c8a and sty_e21cbc08  
**Date:** 2026-07-11  
**Non-goals:** session-subagent transport (dead); changing gate semantics; policing user-owned binding attributes (grant/model/command/read-only).

---

## 1. Problem

Reviewer and named dispatch already share the invoke seam (`buildRequest` + `runOnce` in `internal/agentstep/invoke.go`). What remains reviewer-special is the **contract**: an isolated sub-process that returns a verdict `{decision, notes, reasoning}`. That specialness is correct.

The defect is that the contract hangs off the **magic binding NAME** `"reviewer"` (special-cased across the tree), so:

- the judge-vs-perform model is invisible in configuration,
- a binding named something else cannot be a reviewer without code changes,
- inject context (principles, constitution) is not declared per binding,
- accept verdicts hide notes/reasoning from session output.

## 2. Decisions (operator-locked — do not re-open)

| Decision | Choice |
|---|---|
| Where is role declared? | Per binding in `agents.toml`: `role = "reviewer" \| "agent"` — **not** type markers on DOT nodes/edges |
| Sole hard determination | Verdict contract `{decision, notes, reasoning}` at runtime + reviewer-skill check that the skill **specifies** that contract |
| Everything else (tools, read-only ceiling, model, command) | **User configuration** — seeded correct, user-breakable, never policed by code |
| Transport | Sub-process exec only (`agentcli.Runner`); session-subagent is dead |
| Injected context ownership | Workflow passes the **skill**; binding declares **which principles** ride |

---

## 3. `agents.toml` binding shape

### 3.1 Role

```toml
[reviewer]
role    = "reviewer"   # "reviewer" | "agent"
command = "..."
tools   = "..."
model   = "..."
principles = "session" # see §5

[planner]
role    = "agent"
command = "..."
# ...
```

**Semantics**

| `role` | Expect | Meaning |
|---|---|---|
| `reviewer` | `verdict` | Isolated judge. Output must parse to a gate decision. Charter = `reviewerCharter()`. |
| `agent` | `perform` | Performer. In-loop or sub-process per `command`. Charter = `executorCharter(...)` when isolated. |

**Back-compat inference (absent `role`)**

1. Binding section name is `reviewer` → `role = "reviewer"`.
2. Else → `role = "agent"`.
3. `satelle agent validate` / `agent show` emit a **warning** to declare `role` explicitly.
4. No hard fail on inference — green path for existing repos.

**Built-in sections:** `[executor]` and `[reviewer]` remain first-class table keys in `AgentsConfig` for load stability. Named agents remain flat `[name]` tables. Role is orthogonal to section name: a future `[strict-reviewer] role = "reviewer"` is eligible as a reviewer binding if the gate resolver selects it (YAGNI for multi-reviewer binding selection in this epic — default remains the `[reviewer]` section; see §8).

**Resolved API (implementers)**

```go
// config.AgentBinding additions
Role       string `toml:"role"`        // "reviewer" | "agent" | ""
Principles string `toml:"principles"`  // selector; empty → "session"

// ResolvedRole returns Role when set; else infers from section name.
func ResolvedRole(section string, b AgentBinding) string // "reviewer" | "agent"

// ResolvedPrinciples returns the principles selector after alias expansion.
func (b AgentBinding) ResolvedPrinciples() PrinciplesSelector

// InjectsPrinciples remains: true when ResolvedPrinciples() != none
// (deprecated inject_principles still wins as alias — see §5).
```

`LoadAgents` / `ReviewerBinding` / `NamedBinding` / `ExecutorBinding` continue to resolve command/tools defaults. They do **not** validate role against command (no policing). Role validation that matters at gate time is §7 (in-loop reviewer fails loud as mechanism).

### 3.2 What is NOT enforced by code

- Tool grant content (read-only vs write)
- Model id
- Command template (except gate-time mechanism: in-loop cannot produce an isolated verdict)
- Read-only ceiling flags in the command

Seeded defaults in `DefaultReviewer*` and the init scaffold remain correct read-only reviewer defaults. Users may break them; that is intentional.

---

## 4. Invoke contract

### 4.1 Surface

Formalize over the **existing** seam. Do not invent a second runner path.

```go
// Expect is derived from binding role (not a free parameter at call sites
// that already know the binding). Orchestration may pass an override only
// for the summariser (verdict-less perform-style recap) — see §4.3.
type Expect int
const (
    ExpectVerdict Expect = iota // role=reviewer
    ExpectPerform               // role=agent
)

type InvokeRequest struct {
    Binding  config.AgentBinding // resolved binding (command/tools/model/env/settings/role/principles)
    Section  string              // binding section name (for logging, ledger actor, inference)
    Rubric   string              // skill body from workflow node/edge (may be empty for perform)
    Payload  any                 // marshalled to JSON stdin
    Charter  string              // optional override; empty → charter from role
    Timeout  time.Duration       // ≤0 → engine default / binding timeout
    Runner   agentcli.Runner     // optional; empty → newRunner(binding.Command)
    // Attempts: only meaningful for ExpectVerdict (retry loop). 0 → engine default.
    Attempts int
}

type InvokeResult struct {
    Stdout   []byte
    Usage    agentcli.UsageResult
    Command  string // resolved harness command string for ledger
    Decision *verb.GateDecision // non-nil only when ExpectVerdict and parse succeeded
    Err      error              // timeout, runner error, no-verdict after retries, in-loop reviewer, etc.
}
```

### 4.2 What moves into `Invoke` vs stays orchestration

| Concern | Location |
|---|---|
| Resolve tools/model/env/settings/principles from binding | **Invoke** (or a thin pre-step shared by Invoke) |
| Charter selection by role (`reviewerCharter` / `executorCharter`) | **Invoke** when `Charter` empty |
| `buildRequest` prompt assembly | **Invoke** (already shared) |
| `runOnce` / `agentcli.Runner.Run` | **Invoke** — **only** path that calls `Runner.Run` for LLM steps |
| Verdict parse + prose fallback + retry loop | **Invoke** when `ExpectVerdict` |
| Functional-check short-circuit (```check / `check:`) | **Stays in `runReviewer` / Gate orchestration** — no LLM, no Invoke |
| Edge skill list, scoped `on=`, multi-judge short-circuit | **Stays in `Gate()`** |
| Missing rubric → advisory (`Gated: false`) | **Stays in `runReviewer`** before Invoke |
| Broken skill structure → refuse | **Stays in `runReviewer`** before Invoke |
| Named-binding hard-fail (missing `[name]`) | **Stays in `DispatchExecutor`** before Invoke |
| Code-edit engagement lock / satelle-CLI grant check | **Stays in `DispatchExecutor`** before Invoke |
| Ledger rows, CLI session output, step summary | **Stays in `verb/workitem.go`** |

### 4.3 Call-site thinning

```
runReviewer:
  resolve skill body → structure check → functional-check branch OR
  resolve [reviewer] binding (role must resolve to reviewer; §8) →
  refuse if command is in-loop (§7) →
  Invoke(ExpectVerdict) → map Decision

DispatchExecutor:
  resolve named binding → engagement / CLI grant checks →
  if command in-loop: return no-dispatch (orchestrator performs) →
  Invoke(ExpectPerform) → DispatchResult

Summarise / Retrospect:
  Prefer Invoke(ExpectPerform) with summariser/retrospect rubric for a single
  Runner.Run path (in scope for sty_ba860c8a if cheap; otherwise fold in a
  follow-up without blocking the epic). Until folded, they remain on buildRequest+runOnce
  with a comment that Invoke is the target.
```

**Behaviour freeze for dogfood:** same CLI templates, same retry counts, same gate semantics. Tests stay green without public CLI contract changes.

### 4.4 Expect derivation

```go
func ExpectFromRole(role string) Expect {
    if role == "reviewer" { return ExpectVerdict }
    return ExpectPerform
}
```

Gates always invoke a binding whose resolved role is `reviewer`. Named dispatch always uses a binding whose resolved role is `agent` (or in-loop executor). A mis-declared `role = "agent"` on `[reviewer]` is user error: the gate will still call Invoke with ExpectVerdict **because the gate path selects ExpectVerdict from the gate contract**, while validate warns if `[reviewer]`'s resolved role is not `reviewer`. Conversely, a named binding with `role = "reviewer"` used as a performing node is user error; perform path does not parse a verdict.

**Clarification (implementer rule):**  
- Gate LLM path: `expect = ExpectVerdict` always (mechanism). Binding role should be `reviewer`; validate warns if not.  
- Named dispatch path: `expect = ExpectPerform` always. Binding role should be `agent`; validate warns if not.  
- Role is the **declared identity** for transparency and for future multi-reviewer binding selection; expect is selected by **call path** in this epic so gate semantics cannot be accidentally demoted by a typo. Order:3 may optionally refuse a gate Invoke when resolved role ≠ reviewer (hard fail) — recommended for loud failure; design chooses **refuse at gate time** if `ResolvedRole(section, binding) != "reviewer"` for the binding the gate uses.

---

## 5. Principles selector + constitution order-zero

### 5.1 Selector

```toml
principles = "session"   # default when absent
# | "all" | "system" | "project" | "none"
# | comma-list of those selectors, e.g. "system,project"
```

| Value | Principles injected |
|---|---|
| `session` | `principles:session`-tagged set (today's `alwaysPrinciples` behaviour) + operating-principle guarantee |
| `system` | principles with scope/system classification (embedded/system substrate) |
| `project` | project-scoped principles only |
| `all` | every principle body in the index |
| `none` | nothing |
| comma-list | union of the named selectors (stable name-sorted) |

**Scope classification for system/project:** use existing docindex / frontmatter `scope` (and embedded vs project origin) the same way substrate listing already distinguishes them. If a principle has no scope, treat as project when on-disk under the data dir, system when embedded-only. Document the exact helper in implementation plan; do not invent a second taxonomy.

### 5.2 Deprecated alias

- `inject_principles = true`  → `principles = "session"`
- `inject_principles = false` → `principles = "none"`
- If both set: `principles` wins; validate warns about the deprecated key.
- Absent both → `session`.

`AgentBinding.InjectsPrinciples()` becomes: `ResolvedPrinciples() != none`.

### 5.3 Constitution order-zero

Whenever the resolved principles selector is **not** `none`, the injected block is:

```
# Project constitution          ← ORDER ZERO (when constitution file non-empty)
<constitution body>

# Always-resident principles (satelle)   ← or a clearer heading listing the selector
<selected principle bodies, name-sorted>
```

This **mirrors** `renderAlwaysContent` in `internal/cli/cmd_hook.go` (SessionStart). It **fixes** the current gap: `alwaysPrinciples` omits the constitution even though comments claim SessionStart parity and the constitution text expects gates to read it.

When `principles = "none"`: inject neither constitution nor principles (explicit opt-out).

### 5.4 Prompt assembly contract (canonical order)

Every isolated LLM invocation via `buildRequest` / Invoke:

1. **Constitution + principles block** (when selector ≠ none) — §5.3  
2. **Role charter** (reviewer / executor; empty for summariser)  
3. **Pull-context call-to-action** (`pullContextCallToAction` — always)  
4. **Skill rubric** (workflow-supplied; may be empty)  
5. **Stdin payload** JSON: gates use `{story, from, to, review_skill, children?}`; perform uses the existing dispatch payload shape.

No other ordering. Charter is never the home of pull-context (summariser has no charter).

---

## 6. Verdict contract `{decision, notes, reasoning}`

### 6.1 Parsed schema

```go
type rawDecision struct {
    Decision  string `json:"decision"`  // accept | reject
    Notes     string `json:"notes"`
    Reasoning string `json:"reasoning"` // NEW; optional for back-compat
}
```

- `decision` required (same as today).  
- `notes` optional.  
- `reasoning` optional: absent → empty string; **notes-only output remains valid** (back-compat).  
- Prose fallback (`parseProseDecision`) unchanged for accept/reject extraction; reasoning stays empty on prose path unless a later enhancement extracts it (out of scope).

### 6.2 Propagation

- `verb.GateDecision` and `verb.ReviewerVerdict` gain `Reasoning string`.  
- Ledger payloads include `reasoning` when non-empty (and prefer always including the field for accept+reject).  
- **Session transparency (epic AC):** on both accept and reject, CLI/`story set` output surfaces `decision`, `notes`, **and** `reasoning` (not only reject notes as today). Implement in `verb/workitem.go` where reject messages are formatted; extend accept path similarly (today accepts are quieter).

### 6.3 Reviewer skill check verb

**What:** a deterministic check that a **reviewer skill** document specifies the verdict contract — i.e. the skill body instructs the agent to return JSON `{decision, notes, reasoning}` (or documents the contract clearly enough that a structure/content check can pass).

**Where:** extend existing skill validation rather than a brand-new CLI surface if possible:

- Preferred: `structure.Doc("skills", …)` / reviewer-skill structure rules, or a dedicated helper invoked from `satelle skill validate` and from gate pre-flight when the skill is classified as a reviewer skill (prompt on a reviewer node/edge, or skill used by Gate).
- Name in docs: "reviewer skill contract check".  
- Failure mode: same class as today's broken-skill refuse — gate refused with actionable message (`satelle skill validate <name>`).  
- **Does not** re-parse live agent output (that is runtime parse). This is substrate validation.

**Heuristic for "specifies the contract"** (implementers pick the minimal reliable one and test it):

1. Skill body mentions `decision` and (`notes` or `reasoning`) in a JSON example or explicit "return JSON" contract section, **or**
2. Skill carries a frontmatter/tag marker such as `contract: verdict` (optional future; do not require a mass migration of skills in this epic if body heuristic covers substrate).

Baseline substrate skills already document `{decision, notes}`; extend seeded skill templates to mention `reasoning` as part of order:3 / dogfood, without failing every existing skill that only says `{decision, notes}` — **accept skills that document at least `decision` + `notes`; treat `reasoning` as recommended**. Runtime already accepts missing reasoning.

### 6.4 Gate-time mechanism: in-loop reviewer

If the binding used for a gate LLM path has `CommandTemplate()` resolving to in-loop (runner == nil / command `in-loop`):

```
gate refused: reviewer binding %q is command=in-loop and cannot produce an isolated verdict —
set [reviewer] command to an isolated agent CLI (claude|grok|codex or a full template)
```

- This is **mechanism** (a gate needs an isolated verdict), not config policing of tools/model.  
- Validate may **warn** earlier; gate **fails loud** at engage/transition time.  
- Tests: unit test with in-loop reviewer binding → clear error, status not enacted.

---

## 7. Transparency surfaces

### 7.1 `satelle agent show` / `agent validate`

Each binding grant line gains:

```
GRANT [reviewer] role=reviewer principles=session constitution=yes backend=isolated:grok ...
GRANT [planner]  role=agent    principles=session constitution=yes backend=isolated:claude ...
GRANT [executor] role=agent    principles=session constitution=n/a (in-loop; session injects) ...
```

- `role=` shows **resolved** role (declared or inferred).  
- `principles=` shows resolved selector.  
- `constitution=yes|no` reflects whether order-zero constitution would inject for that binding (yes when principles ≠ none and constitution file non-empty; in-loop notes that SessionStart injects).  
- Warning when `role` was inferred.  
- Warning when `inject_principles` is used instead of `principles`.  
- Warning when `[reviewer]` resolved role ≠ `reviewer`, or a performing named binding has role `reviewer`.

### 7.2 Session output on gate verdicts

See §6.2 — accept and reject both print decision + notes + reasoning.

---

## 8. Gate binding resolution (order:3)

**Default:** gates use the `[reviewer]` binding (`AgentsConfig.ReviewerBinding()`), same as today, resolved through Invoke.

**YAGNI this epic:** per-edge `agent=` on a reviewer edge to pick an alternate reviewer binding. If a future need appears, role=reviewer bindings become the eligibility set; not required now.

**Eligibility check:** when resolving the gate binding, if `ResolvedRole("reviewer", binding) != "reviewer"`, refuse at gate time with a clear error (misconfiguration).

**Engine fields simplification (order:3):** prefer reading tools/model/env/inject from the binding inside Invoke rather than parallel `g.tools` / `g.model` / `g.injectPrinciples` / `g.reviewerEnv` set at bootstrap — either delete the parallel fields or document them as a cache of the reviewer binding. Goal: one resolution path.

---

## 9. Magic-name retirement map

Disposition codes:

- **(a)** Role lookup / binding resolution — stop keying on the literal name for judge-vs-perform when agents.toml is available.  
- **(b)** DOT vocabulary — topology classification in pure workflow DOT where agents.toml may be absent; keep the string `agent=reviewer` as the **workflow DSL** token for "this node/edge is a gate judge".  
- **(c)** Delete or reword if redundant after (a)/(b).  
- **(d)** Ledger/telemetry **actor label** — stable string for audit trail, not binding identity; keep `"reviewer"` as actor unless a follow-up renames actors globally (out of scope).

| Site | Disposition | Notes |
|---|---|---|
| `wfdot.go:134` `ScopedReviewers` (`st.Agent != "reviewer"`) | **(b)** | DOT-only topology: edge-less always-on gates are declared `agent=reviewer` in the graph language. agents.toml is not loaded in `wfdot`. |
| `wfdot.go:192` `IsPerforming` (`st.Agent != "reviewer"`) | **(b)** | DOT vocabulary: performing = non-empty agent that is not the reviewer DSL token. |
| `wfdot.go:239` (park/cancel classification) | **(b)** | Same: reviewer nodes with no outgoing edges are cancel/park sinks in DOT. |
| `wfdot.go:350` edge parse `attrs["agent"] == "reviewer"` | **(b)** | Parser maps DSL → edge skills. |
| `wfdot.go:398` transition inherit from reviewer target node | **(b)** | Parser: entry into a reviewer node is gated by that node's skill. |
| `wfdot/format_drift.go:69` performing-node detection | **(b)** | Format lint over DOT text only. |
| `wfdot/refresh.go:132` performing prompt rewrite | **(b)** | Assisted refresh rewrites DOT; no agents.toml. |
| `agentstep/engine.go:578` skip dispatch for executor/reviewer | **(a)** | Become: skip dispatch when agent is empty, `executor`, **or** the binding for `target.Agent` resolves role=reviewer (and keep hard-coded skip for literal `agent=reviewer` DOT token so pure-gate nodes never dispatch). Practical rule: `if !isNamedPerformer(target.Agent) { return }`. |
| `agentstep/engine.go` telemetry `"reviewer"` actor | **(d)** | Keep actor string. |
| `agentvalidate` built-in section list `{"reviewer", ...}` | **(a)** partial | Still load `[reviewer]` section; display **resolved role**. Named-agent skip (`st.Agent == "reviewer"`) stays **(b)** for DOT tokens. Add role/principles to grant output. |
| `verb/engage_validate.go:109` cancel/park exemption | **(b)** | Uses DOT agent=reviewer + no outgoing edges. |
| `verb/single_story.go` park/engaging | **(b)** via `NonTerminalEngagingStates` | Already shape-derived; comments mention agent=reviewer — leave. |
| `verb/workitem.go` ledger actor `"reviewer"` | **(d)** | Stable ledger actor. |
| `verb/workitem.go` payload `Agent: "reviewer"` | **(d)** / display | Prefer binding section name when available; default `"reviewer"`. |
| `config/agents.go` `Reviewer` field / case `"reviewer"` | **keep** | Section key for the default reviewer binding — not a magic behaviour switch; role field is the behaviour switch. |
| `config/vars.go` resolveBinding `"reviewer"` | **keep** | Section name for env resolution. |
| `web/workflow.go` / `web/web.go` agent=="reviewer" UI | **(b)** | Workflow diagram coloring from DOT agent attribute. |
| `ledger/ledger.go` comment executor/reviewer | **(d)** | Documentation of actor vocabulary. |

**Justify every (b):** The workflow DOT is a portable graph language that must classify gate vs perform **without** requiring agents.toml (embedded workflows, validate-before-agents, structure checks). The token `agent=reviewer` in DOT means "this node is a gate judge in the lifecycle graph." The **binding role** in agents.toml means "this process contract returns a verdict." They align by convention (`[reviewer]` seeds role=reviewer; nodes use agent=reviewer) but live at different layers. This epic does **not** rename the DOT token.

**Sites that become (a) in practice for judge-vs-perform outside DOT:** engine dispatch skip list (named performer check), agentvalidate grant display, gate binding role check, any future code that currently branches on `name == "reviewer"` to choose verdict vs perform when a binding is in hand.

---

## 10. Gate semantics preserved (non-negotiable)

1. Edge judges run in declared order; first reject short-circuits.  
2. Scoped `on=` reviewers append after edge judges (existing order).  
3. Functional-check skills short-circuit without LLM.  
4. Missing reviewer rubric remains advisory (`Gated: false`).  
5. Broken skill structure refuses the gate.  
6. Named-binding hard-fail when workflow allocates a missing `[name]`.  
7. Multi-reviewer edges unchanged.  
8. No policing of user-owned grant/model/command beyond §6.4 mechanism.

---

## 11. Implementation split

| Story | Scope |
|---|---|
| **sty_69fd4e20** (this doc) | Design only; attach as type `design`. No product code. |
| **sty_ba860c8a** | Formalize `Invoke` over `buildRequest`+`runOnce`; thin `runReviewer` / `DispatchExecutor`; role field parse + inference + principles selector plumbing + constitution in inject; `{decision,notes,reasoning}` parse fields; tests green. Prefer minimal public API churn. |
| **sty_e21cbc08** | Gate uses role-checked reviewer binding only; in-loop reviewer fails loud; magic-name map applied; agent show/validate transparency; accept+reject session surfacing of reasoning; reviewer skill contract check; simplify engine reviewer fields; dogfood on this repo's agents.toml (`role` + `principles` declared). |

### Suggested file touch list (order:2)

- `internal/config/agents.go` — Role, Principles, ResolvedRole, ResolvedPrinciples, inject alias  
- `internal/agentstep/invoke.go` — Invoke, principles+constitution inject, buildRequest selector  
- `internal/agentstep/engine.go` — thin runReviewer/DispatchExecutor; parseDecision reasoning  
- `internal/verb/review.go` — Reasoning fields  
- tests under `internal/agentstep`, `internal/config`

### Suggested file touch list (order:3)

- `internal/agentvalidate` — show role/principles/constitution  
- `internal/cli/cmd_agent.go` — surface same  
- `internal/verb/workitem.go` — accept/reject output with reasoning  
- `internal/agentstep/engine.go` — in-loop refuse; binding-only resolution  
- `internal/structure` or skill validate — reviewer contract check  
- `.satelle/agents.toml` — declare `role` and `principles` on bindings (dogfood)  
- seeded defaults / init scaffold if needed

---

## 12. Acceptance mapping

| AC (sty_69fd4e20) | Section |
|---|---|
| 1. Design defines role, Invoke expect, verdict+skill check, prompt assembly, principles selector, constitution, back-compat, errors | §§3–7 |
| 2. Preserves multi-judge, on=, functional-check, advisory missing rubric, named hard-fail | §10 |
| 3. No policing of user-owned attributes; verdict is sole hard determination | §§2–3, §6.4 |
| 4. Injected context declared + inspectable; constitution gap closed | §§5, 7 |
| 5. Magic-name map with dispositions + (b) justifications | §9 |
| 6. No code beyond this design | this attach |

---

## 13. Dogfood notes

After order:3 lands on this repo:

1. `.satelle/agents.toml` declares `role` and `principles` on every binding.  
2. `satelle agent show` displays role + principles + constitution.  
3. Drive a real gate (e.g. design-story transitions) and confirm accept output includes reasoning when the model supplies it.  
4. Confirm an intentional `command = "in-loop"` on a temp reviewer binding fails the gate with the mechanism error (local experiment; do not leave broken).

# Named-agent dispatch — how an isolated step receives its instructions

A route's step can allocate its work to a **named agent** instead of the in-loop
session. When a step carries `agent: <name>` (any name other than `executor` or
`reviewer`), satelle **dispatches** that step: it spawns the agent CLI configured
in `.satelle/workflows/agents.toml` under `[<name>]`, hands it the work, and folds the
result back in. The agent runs with a **fresh context** — it never sees the
conversation — so the contract below is how it learns what to do and how it gets
the rest of the story.

## The dispatch contract

Dispatch fires **on entry** to the step, after that step's entry gate accepts,
and ONLY because the route allocated that step to a named agent.

**Flat dispatch.** The orchestrator is the sole scheduler: `orch → step → orch`.
Steps never call steps. A reviewer returns a verdict and dispatches nothing.
An agent-less, `agent: executor` or `agent: reviewer` step dispatches nothing —
entering a step never fires an agent of its own.

An **advisor** is a named agent the route says the orchestrator MAY consult —
park triage, a post-close retrospective. It is a declaration, never a dispatch:
`satelle story route <id>` names it, the orchestrator decides when to consult it,
and the orchestrator records the advice on the story. (The earlier
on-enter entry dispatch is retired: a step that fires an agent at itself hides
work from the one place accountable for the route.)

The agent receives:

- **System prompt**, assembled in this order:
  1. the session-resident principles (unless the binding sets
     `inject_principles = false`; default on),
  2. an **executor charter** — you are performing this step of this workflow;
     do the step's work, but **never change the item's status** (the workflow's
     gates govern every advance),
  3. the **pull-context call-to-action** (see below),
  4. the step's `skills:` **rubric** — the instructions for the step.
- **Payload (dual delivery):** the work item as JSON —
  `{story, from, to, review_skill}`. `story` carries the id, title, body, and
  acceptance criteria; `from`/`to` are the transition being performed. Delivery
  is **always on stdin**, and the same bytes substitute into the command
  placeholder **`{payload}`** when the template includes it (one argv token).
  Stdin-first CLIs (e.g. Claude) leave `{payload}` out of the template so the
  prompt is not double-fed; argv-first CLIs (e.g. `grok -p {payload} …`) opt in.
  Empty `{model}`/`{settings}` drop their flag; empty `{payload}` does not.
- **Capabilities**: the binding's `tools` grant, and its `model` unless the

### Multi-reviewer steps (`parallel:`)

A step with several `reviewers:` runs them **concurrently** by default (cap 4):
all verdicts are collected with no short-circuit, ledger order stays list order,
and any reject still refuses the transition (the error names every rejecting
reviewer). Trade-off: a rejected parallel round spends tokens on every reviewer.
Set `parallel: 0` on the step for byte-for-byte sequential execution with
first-reject short-circuit, or `parallel: N` to bound the fan-out. See
`satelle help workflows`.

### Gate binding by agent name

A step's `reviewer_agent:` (or a `## gate` section's `agent:`) may name any
`role = "reviewer"` binding in `.satelle/workflows/agents.toml`. Omitted, the gate uses
`[reviewer]`. The agents layer owns harness, tools, and model — the route names
*who*. See the satelle-route-standard principle.


### What each role needs

The two roles get their context by opposite routes, so they need opposite grants.

- **Performers** — a spine step with `agent: <name>` —
  are **dispatched** as a child process with **no conversation history**. They
  reconstruct context by *pulling* the story, its documents, and the ledger, so
  the grant must carry a **context channel**: either `Bash(satelle:*)` (a broad
  `Bash`, `Bash(*)` or `*` also qualifies) for the read-only satelle CLI, **or**
  `read_file` for disk reads under `~/.satelle/<repo-key>/stories/<id>/`.
  Claude-only `Read` does **not** qualify — the Claude pull path is the CLI, not
  a disk-first rubric.
- **Reviewers** (`role = "reviewer"`, named by a step's `reviewers:` or by a
  `## gate` section) need **no channel**: satelle *pushes* the attachments into the transition payload's
  `docs` array, and reviewer bindings never reach the dispatch path that
  consults a grant. A shell grant on a reviewer is capability that is never
  exercised — it only widens the ceiling.

`satelle agent validate` judges both **before** you engage anything: a performer
with no channel is an **error** (non-zero exit — dispatch would refuse it later
anyway), and an unused reviewer shell grant is a **warning** (exit 0 — keeping
it is the repo's call). One predicate decides the channel question for both the
runtime refusal and validate, so they cannot disagree.

**Refusals (fail loud, never silent):**

- A step names `agent: <name>` but `.satelle/workflows/agents.toml` defines no `[<name>]`
  binding → the transition is **refused** (there is no silent in-loop fallback).
- A dispatched binding's `tools` grant carries no context channel (see *What
  each role needs* above) → the dispatch is **refused**, because the agent could
  not pull its context.
- A binding whose `command` is `in-loop` keeps the step with the orchestrating
  session — not dispatched, and so exempt from the channel requirement.

## Control plane in vs agent I/O out

- **In** (orchestrator / any agent → satelle): **satelle CLI verbs** only
  (`satelle story set`, `story get`, …). Small local surface — not an MCP tool
  dump of every verb. Status and engagement change only through satelle.
- **Out** (satelle → isolated worker): subprocess configured in
  `.satelle/workflows/agents.toml`. Two **transports**, one binding shape
  (epic:agent-dispatch-transport):

| `interface` | Meaning |
|-------------|---------|
| **`command`** (default; omit = command) | Full multi-token argv template; any CLI (Claude Code, `grok -p`, wrappers, custom). |
| **`acp`** | Agent Client Protocol over stdio; `command` is the **spawn line only** (e.g. `grok agent stdio`). System/payload ride the session, not `{placeholders}`. |

Shared fields on both: `role`, `tools`, `model`, `effort`, `secondary`,
`principles`, `env`, `timeout`, `settings`. Claude Code does **not** support ACP
— keep Claude on `interface = command`. An ACP-capable CLI is usable when it
implements ACP agent stdio **and** the binding sets `interface = "acp"`. Workers
never advance story status; they return text/verdicts that satelle enacts after
gates.

### Progressive execution diagnostics

Command JSONL and ACP `session/update` traffic are normalized into the same
provider-neutral execution events: start, heartbeat, safe message, tool start
and completion, artifact candidate, usage, completion, and failure. Interactive
CLI progress is written to stderr, while the command's structured result remains
on stdout and the final agent response remains authoritative.

Named dispatches write a sanitized normalized event log under the repository's
Satelle runtime `logs/dispatch/` directory. Set
`SATELLE_AGENT_TRACE_RAW=1` only for short-lived transport debugging to create a
sibling `-raw.log`; raw traces are opt-in because provider traffic may be
sensitive. Satelle filters hidden reasoning and redacts obvious credential
shapes from both surfaces, but operators should still protect and remove raw
traces after diagnosis.

### Structured step artifacts

A skill can ask Satelle to own a dispatched step's final artifact by declaring a
generic contract in its frontmatter:

```yaml
output_name: plan
output_type: plan
output_required: true
output_schema: body
output_ac_coverage: true
```

The isolated agent returns one canonical final object:

```json
{"artifact":{"name":"plan","type":"plan","body":"# Plan\n\n## AC1\n..."}}
```

Satelle decodes command and ACP results through the same seam, validates the
declared fields and optional acceptance-criterion coverage, and attaches the
typed document before committing the workflow transition. A decode, validation,
or attachment failure refuses the transition and releases its in-flight lease.
Because Satelle owns the write, a contracted planner can use only read-only
repository tools.

An output-contract skill can opt into a bounded validate–repair–escalate policy:

```yaml
attempt_repair_max: 1
attempt_escalate_max: 1
attempt_max_total: 3
attempt_token_budget: 12000
attempt_time_budget: 8m
attempt_on_exhaust: fail
attempt_initial_effort: low
attempt_repair_effort: medium
attempt_escalate_effort: high
attempt_escalate_binding: stronger-planner
```

All keys are optional. With no `attempt_*` keys, dispatch remains a single
validate-or-fail call. Binding names refer to ordinary provider-neutral
`agents.toml` sections; effort overrides use the same command/ACP effort seam as
the binding itself. Quality escalation is deliberately separate from
`secondary=`, which remains reserved for rate-limit or service-unavailable
failover.

After each candidate, Satelle runs the declared deterministic output validators.
A valid initial result attaches immediately. An invalid result can receive a
targeted repair containing the prior draft and all validator findings; only
after configured repair attempts fail can the stronger binding or effort run.
Attempt count, token (when reported), and wall-time budgets bound the loop.
Cancellation and invocation timeouts stop immediately. Exhaustion always fails
the transition: no policy can attach an invalid artifact or bypass a workflow
gate.

Each policy attempt records an `agent-attempt` telemetry event with phase,
binding, model, effort, elapsed time, validator findings, escalation reason, and
`usage_available`. Token fields are omitted when the transport did not report
usage, so unavailable cost is never represented as measured zero.

Legacy self-attaching steps remain supported. To migrate one:

1. Add the `output_*` contract to its skill.
2. Change its rubric to return the JSON artifact instead of running
   `satelle story attach`.
3. Remove `Bash(satelle:*)` from its binding when no other Satelle verb is
   needed.
4. Keep the workflow's exit review responsible for semantic artifact quality.

### Planner transport evidence

For this repository the default `[planner]` remains Claude's non-interactive
command transport. The comparison is reproducible with `make planner-bench`,
which runs the same planning fixtures through that binding shape and Grok ACP
and writes versioned per-run records plus redacted raw and attached-artifact
sidecars under `tests/plannerbench/out/`. The schema and interpretation guide is
`tests/plannerbench/EVIDENCE.md`.

Artifact quality failures remain inspectable benchmark outcomes with explicit
per-criterion reasons. Infrastructure failures or an under-sampled selected
cell fail the target. Usage that a transport does not report is `n/a` with
provenance, never numeric zero.

Changing the binding requires ACP to preserve 100% artifact correctness and
policy fidelity, introduce no reliability regression, and win at least two of:
20% lower median wall time, lower median tokens, or strictly better failure
diagnostics. An ambiguous or under-powered result retains Claude command.
Operators can use Grok ACP as a temporary planner fallback by copying its
benchmark binding into `[planner]`; the documented default is not rewritten
automatically.

### Reasoning effort (`effort=`)

Optional per-binding thinking/reasoning level (e.g. `low` | `medium` | `high`).
Empty means the peer default.

| Transport | How effort is applied |
| --- | --- |
| **command** | Substitutes into `{effort}` (flag dropped when empty, like `{model}`). Also supports fused forms such as `model_reasoning_effort="{effort}"` (empty drops the whole token and a preceding `-`flag). Default Claude/Grok templates include the flag; DefaultCodexExecCommand uses `-c model_reasoning_effort="{effort}"` (TOML-quoted string for Codex `-c`). |
| **ACP** | Session path: `session/set_config_option` for `reasoning_effort` / `effort` (failure-tolerant). **Grok-shaped** ACP spawns also receive argv `--reasoning-effort` (Grok CLI flag). **Codex ACP and other non-Grok peers never get that argv flag** — it is not ACP (sty_aa726901). |

### Rate-limit secondary (`secondary=` / `[defaults]`)

When an isolated dispatch fails with a **classified rate-limit or unavailability**
error (429, overloaded, quota, 503, …), satelle retries **once** on a secondary
binding — without rewriting agents.toml mid-incident:

```toml
[defaults]
secondary = "fallback-grok"   # used when a binding omits secondary=

[planner]
# secondary = "fallback-grok"  # per-binding override of [defaults]
effort = "high"

[fallback-grok]
interface = "acp"
command   = "grok agent stdio"
tools     = "read_file,grep,list_dir"
model     = "grok-4.5"
```

Non-rate-limit failures still refuse the transition (no silent swallow). Unconfigured
secondary preserves single-binding behaviour. Failover is per-dispatch only (not a
sticky rewrite of the primary binding).

Example ACP binding (optional; defaults stay command):

```toml
[reviewer-acp]
role      = "reviewer"
interface = "acp"
command   = "grok agent stdio"
tools     = "read_file,grep,list_dir"
model     = "grok-4.5"
principles = "session"
```

### Codex — preferred ACP, secondary command (sty_3b4909bb)

Codex is a first-class agent on the **same two transports** as everyone else.
There is no third satelle interface. App Server is an implementation detail of
the ACP adapter, not a satelle protocol.

| Preference | Transport | Binding shape |
| --- | --- | --- |
| **1. Preferred** | ACP | `interface = "acp"` + `command = "npx -y @agentclientprotocol/codex-acp"` (`DefaultCodexACPCommand`) |
| **2. Secondary** | command | `interface = "command"` (default) + full `codex exec -s read-only -m {model} -c model_reasoning_effort="{effort}" {system}` (`DefaultCodexExecCommand`) |

Bare `command = "codex"` is rejected by validate (like bare claude/grok);
`satelle init` / migrate expands it to `DefaultCodexExecCommand`. Global
`NewRunner("codex")` resolves to that **command** template — for ACP, set
`interface = "acp"` explicitly.

**`satelle agents` vs `satelle agent`:** `satelle agents install|remove` provisions
two satelle-owned surfaces per target (`claude` / `grok` / `codex` / `all`):

1. **Launchers** under `$SATELLE_HOME/agents/bin/` (e.g. `satelle-codex` →
   `npx -y @agentclientprotocol/codex-acp` — **no** `stdio` subcommand; that is
   not part of the adapter contract). Generated ACP bindings use
   `command = "sh <launcher>"` (multi-token) so `interface=acp` accepts them.
2. **Harness compliance scaffolds** in the repo: `.claude/settings.json`,
   `.grok/hooks/satelle.json`, `.codex/hooks.json` — blocking PreToolUse hooks
   that deny governed code-changing actions unless a satelle story is engaged.

Ownership: only marker-bearing launchers and satelle hook entries are written or
removed; user harness keys/hooks are preserved. Install/remove are idempotent.
Neither path changes the default reviewer or `[agent] cli`.

`satelle agent` (singular) selects and validates the headless CLI / agents.toml.

#### PreToolUse deny channels

The installed `satelle-hook.sh` uses one coherent structured-deny contract:

| Harness | Structured deny emitted by Satelle | Handler exit |
| --- | --- | --- |
| Claude | `hookSpecificOutput.permissionDecision=deny` with a non-empty `permissionDecisionReason` | `0` |
| Grok | top-level `decision=deny` with a non-empty `reason` | `0` |
| Codex | `hookSpecificOutput.permissionDecision=deny` with a non-empty `permissionDecisionReason` | `0` |

Exit `0` means the hook handler ran successfully; the JSON decision still blocks
the tool. Claude and Codex use a separate fallback contract for exit `2`: the
blocking reason must be non-empty on stderr and structured stdout is not the
authoritative channel. Do not mix exit `2` with JSON-only stdout and empty
stderr. Satelle's wrapper prefers the structured path for policy and
infrastructure denials, emits a static safe infrastructure reason when the
binary is absent or unusable, and keeps irrelevant/read-only Bash fail-open so
the operator can diagnose the installation.

#### Install compliance + ACP adapter (dogfood)

```bash
# Launchers + .claude/.grok/.codex blocking-hook scaffolds (repo cwd)
satelle agents install all
# Or a single harness:
satelle agents install codex
# Manual ACP fallback (or use npx each run):
#   npm install -g @agentclientprotocol/codex-acp
# Authenticate through the Codex CLI (for example, `codex login`). Satelle does
# not require CODEX_API_KEY, OPENAI_API_KEY, or a Satelle-specific environment flag.
# Optional: point at a specific codex binary
export CODEX_PATH=$(which codex)
# Reviewer dogfood: keep the adapter in read-only agent mode when possible
export INITIAL_AGENT_MODE=read-only
# Codex will prompt to trust .codex/hooks.json on first run (/hooks).
# Automation: codex exec --dangerously-bypass-hook-trust …
```

**Local hook smoke (never CI):** run `go test -tags codexlive ./tests/codexlive/`
(or `make codex-smoke` when present). It uses existing Codex CLI login/configuration
and reports a clear prerequisite when Codex is absent or unauthenticated. Hermetic
unit tests cover the install path without Codex, npm, or API keys.

#### Sample agents.toml — Codex ACP for low-cost / park roles

```toml
# Preferred: reuse satelle's ACP client (same as Grok).
[reviewer-summary]
role       = "reviewer"
effort     = "low"
interface  = "acp"
command    = "npx -y @agentclientprotocol/codex-acp"
tools      = "read_file,grep,list_dir"
model      = "o4-mini"
principles = "session"

[retrospective]
role       = "agent"
effort     = "high"
interface  = "acp"
command    = "npx -y @agentclientprotocol/codex-acp"
tools      = "read_file,grep,list_dir,Bash(satelle:*)"
principles = "session"

[blocked-triage]
role       = "agent"
effort     = "high"
interface  = "acp"
command    = "npx -y @agentclientprotocol/codex-acp"
tools      = "read_file,grep,list_dir,Bash(satelle:*)"
principles = "session"
```

#### Sample — Codex exec command transport

```toml
[reviewer-codex-exec]
role      = "reviewer"
command   = "codex exec -s read-only -m {model} -c model_reasoning_effort=\"{effort}\" {system}"
# payload is always on stdin; do not add {payload} to argv
effort    = "high"
model     = "o4-mini"
principles = "session"
```

`satelle agent validate` treats `-s read-only` as reviewer ceiling evidence and
**hard-rejects** `role=reviewer` Codex **command** templates whose effective
sandbox is not read-only — including `workspace-write`, an omitted sandbox, and
`danger-full-access` / `--dangerously-bypass-approvals-and-sandbox`. Codex ACP
reviewers are not required to carry `-s` (ceiling = tools grant + permission
policy).

#### Live dogfood vs CI

- **Hermetic tests** (in `make integration` / unit suites) use fake ACP peers and
  validate/buildArgs only — they never require `codex`, npm, or API keys.
- **Live dogfood** is optional and uses the same model as Claude and Grok: the
  operator authenticates the agent CLI itself (`codex login`, Claude login, Grok
  session). Satelle never stores or injects agent API keys. Install the adapter
  when using ACP, point a named binding at Codex ACP or exec, run
  `satelle agent validate`, then drive a cheap gate (e.g. step-summary). Optional
  live probes must never be required by CI.

Claude remains the default init `[reviewer]` preset until an operator opts in.

## The command template — full templates and placeholders

Each binding's **`command`** says *how* the agent runs. With **`interface =
"command"`** (the default), an **isolated** binding requires a **full multi-token command template** — the real argv is literal in the file so the operator can
read exactly what will run. Bare single-token CLI names (`claude` / `grok` /
`codex`) are **rejected** by `satelle agent validate` (and refuse engage); run
`satelle init` to expand a legacy bare preset, or write the full template
yourself. The only bare single-token value that remains valid is:

- `command = "in-loop"` — no subprocess; the driving session performs the step.

An omitted `[reviewer] command` resolves to the default full claude template
(read-only denylist on Write/Edit/NotebookEdit/Bash). Example full templates
seeded by init and the canonical consts:

- Claude (stdin-first): `claude -p --output-format json --disallowedTools Write,Edit,NotebookEdit,Bash --append-system-prompt {system} --allowedTools {tools} --model {model}`
- Grok (argv-first): `grok -p {payload} --system-prompt-override {system} --tools read_file,grep,list_dir -m {model} --deny Write --deny Edit …`

The first token is the binary; the rest are argv tokens carrying the placeholders
(each one argv token): **`{system}`** (the rubric), **`{tools}`** (the grant),
**`{model}`**, **`{settings}`**, **`{payload}`**. Empty `{model}`/`{settings}` drop
that flag; empty `{payload}` does not. The work item is **always also on stdin**
(dual delivery), so stdin-first CLIs (claude) omit `{payload}` and argv-first
CLIs (grok) include it.

With **`interface = "acp"`**, `command` must **not** contain those placeholders —
satelle sends system/payload over the ACP session instead.

> The field is **`command`**; the older key **`harness`** is a **deprecated alias**
> — a pre-rename `agents.toml` still parses (`command` wins when both are set).

The agent's output is captured to the executor log (and, for a task execution, a
run-output document), so an isolated step's work stays reviewable.

## The pull contract — reconstruct context by id, don't wait to be told

Because a dispatched agent starts fresh, satelle does **not** cram documents or
history into the payload. The payload is a **handle** — the item and its id — and
the agent **pulls** everything else itself, by id, with the read-only satelle CLI:

- `satelle story get <id>` — the full current record.
- `satelle story docs <id>`, then `satelle story doc <id> <name>` — the attached
  documents: the implementation **plan** and every prior **step summary** (each
  gated transition deposits one), which narrate the work so far.
- `satelle ledger list --story <id>` — the evidence ledger (transitions, review
  verdicts, summaries).

A read-only reviewer whose grant excludes Bash judges attachments from the
transition payload's `docs` array (injected by the engine, sty_58fa970e) — no
disk path required. Shell-granted agents may also pull more via the satelle CLI.
Do **not** use in-repo `.satelle/stories/` — that path is obsolete
post-relocation. **Fetch before concluding a document or a prior step is
missing** (payload first, then CLI when available).

## What makes a step safe to dispatch (sufficiency)

- **Give the step a rubric.** A dispatched step needs `skills: <name>`.
  A rubric-less dispatched step (`agent: <name>` with no `skills:`) receives only
  the charter and the item — rarely enough to perform a real step.
- **Make the item self-sufficient.** An isolated agent never sees the
  conversation, so the story's body, acceptance criteria, and attached docs must
  **stand alone**. Anything the step needs that lives only in the chat is lost.
  The plan and step-summary documents (pulled by id) are the sanctioned channel
  for carrying context forward — not the conversation.

## Gate/dispatch sequencing — judge the EXIT edge

Dispatch fires **on entry to the target state, after the entry gate accepts** —
so a dispatched step's work must be judged by its **exit edge's** gates. An
entry-gated state followed by an ungated commit/push ships the dispatched agent's
mutations **unjudged**. When you allocate a step to a named agent, make sure the
edge *out* of that state carries the review that vets what the agent did.

## Custom agents — a worked example

To add a custom agent (say an `architect` that runs on a stronger model), you
define it as a **binding** and **allocate a step to it** — both in satelle's own
substrate, never in a harness's agent directory.

1. **Define the binding** in `.satelle/workflows/agents.toml`:

   ```toml
   [architect]
   command = "claude -p --append-system-prompt {system} --allowedTools {tools} --model {model}"
   tools   = "Read,Grep,Glob,Bash(satelle:*)"   # read-only + the pull-context CLI
   model   = "opus"                              # per-step model selection is the model key
   ```

   Argv-first CLI example (payload on `-p` **and** still on stdin). Grok's
   single-turn flag is `-p`/`--single` — put **`{payload}`** there (not only on
   stdin). Prefer `--output-format plain` so the model's decision JSON is on
   stdout for gate parsing; `--output-format json` also works because satelle
   unwraps Grok's `{ "text": "…" }` envelope the same way it unwraps Claude's
   `{ "result": "…" }`. Use enough `--max-turns` for tool-using gates (1 is
   often too low).

   ```toml
   [architect]
   command = "grok -p {payload} --system-prompt-override {system} --tools {tools} --always-approve --output-format plain --max-turns 8 --no-subagents"
   tools   = "read_file,grep,list_dir,run_terminal_command"  # Grok-native tool ids
   # `read_file` IS a context channel (disk reads under ~/.satelle/<repo-key>/
   # stories/<id>/), so a Grok-native grant needs no Bash(satelle:*) to dispatch.
   ```

2. **Allocate a route step** to it in `step.md`:

   ```
   ## design
   agent: architect
   skills: architect
   ```

3. **satelle dispatches it** on entry to `design`: the item on stdin, the
   `architect` rubric (+ charter + pull-context call-to-action) as the system
   prompt, the binding's tools/model as the grant — on whatever CLI the harness
   names.

**Anti-pattern:** defining that agent in a harness-specific agent directory (e.g.
`.claude/agents/architect.md`) works *for that one harness*, but hides the process
configuration from satelle — it cannot see, validate, dispatch, or carry it
repo-agnostically, and it silently pins the repo to one CLI vendor. Keep process
agents in `.satelle/workflows/agents.toml` + the route's two halves.

## Mixing model backends — per-binding env + `${VAR}` (sty_001558ce)

`model` selects the model *within* the harness's CLI; to point one step at a
*different backend* (a non-default API), a binding may also set **`env`** —
environment variables layered onto that dispatched agent's process, binding keys
winning. Each value may reference the **`[vars]` KV** via `${NAME}`, resolved at
load; an unknown `${VAR}` refuses the command (naming the binding + var) rather
than dispatching with a blank credential. `${...}` appears only in env values, so
it never collides with the `{system}/{tools}/{model}` argv placeholders.

Put the KV in `[vars]`: NON-secret values may sit in the committed `satelle.toml`;
**secrets go in the gitignored `satelle.local.toml`**, whose keys win per-key. The
KV is file-only (no DB) and `satelle.local.toml` is excluded from the substrate
push, so a key never leaves the machine.

Worked example — run one step on **GLM** through z.ai's Anthropic-compatible
endpoint (the *same* `claude` CLI, no wrapper binary), while the in-loop session
stays on its own model:

```toml
# .satelle/workflows/agents.toml
[planner]
command = "claude -p --append-system-prompt {system} --allowedTools {tools} --model {model}"
tools   = "Read,Grep,Glob,Bash(satelle:*)"
model   = "glm-4.6"   # the model id the endpoint expects (glm-5.2 is the newest)
env     = { ANTHROPIC_BASE_URL = "https://api.z.ai/api/anthropic", ANTHROPIC_AUTH_TOKEN = "${GLM_API_KEY}" }
```
```toml
# satelle.local.toml  (gitignored — never commit the key)
[vars]
GLM_API_KEY = "sk-…"
```

Keep the **committed default** on your always-available backend so a clone with no
key still runs; make the alternate backend an **opt-in** the operator switches on.
An exit gate that re-judges the step (e.g. `plan → in_progress`) on the default
backend keeps a weak alternate-model output from reaching the build.

See also: `satelle help workflows` (choosing a lifecycle) and
`satelle help reviewer-checks` (gate skills).


A step names the binding that gates it with `reviewer_agent:` (default `[reviewer]`). Models live in agents.toml only.

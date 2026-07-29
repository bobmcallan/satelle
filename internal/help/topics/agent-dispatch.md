# Named-agent dispatch — how an isolated step receives its instructions

A workflow node can allocate a step to a **named agent** instead of the in-loop
session. When a state carries `agent=<name>` (any name other than `executor` or
`reviewer`), satelle **dispatches** that step: it spawns the agent CLI configured
in `.satelle/agents.toml` under `[<name>]`, hands it the work, and folds the
result back in. The agent runs with a **fresh context** — it never sees the
conversation — so the contract below is how it learns what to do and how it gets
the rest of the story.

## The dispatch contract

Dispatch fires **on entry** to the state, after that state's entry gate accepts.

A node may also carry **`on_enter_agent=<name>`** with optional
`on_enter_prompt="@skill:…"` — a one-shot performer dispatched on entry while
the node's **`agent=`** remains the engagement role (typically `agent=reviewer`
for a park state). That keeps the parked status non-engaging for edit/commit
gates while still running triage once on entry. When both a named `agent=`
performer and `on_enter_agent` are set, the named `agent=` performer wins.

The agent receives:

- **System prompt**, assembled in this order:
  1. the session-resident principles (unless the binding sets
     `inject_principles = false`; default on),
  2. an **executor charter** — you are performing this step of this workflow;
     do the step's work, but **never change the item's status** (the workflow's
     gates govern every advance),
  3. the **pull-context call-to-action** (see below),
  4. the node's `@skill:<name>` **rubric** — the instructions for the step.
- **Payload (dual delivery):** the work item as JSON —
  `{story, from, to, review_skill}`. `story` carries the id, title, body, and
  acceptance criteria; `from`/`to` are the transition being performed. Delivery
  is **always on stdin**, and the same bytes substitute into the command
  placeholder **`{payload}`** when the template includes it (one argv token).
  Stdin-first CLIs (e.g. Claude) leave `{payload}` out of the template so the
  prompt is not double-fed; argv-first CLIs (e.g. `grok -p {payload} …`) opt in.
  Empty `{model}`/`{settings}` drop their flag; empty `{payload}` does not.
- **Capabilities**: the binding's `tools` grant, and its `model` unless the

### Parallel multi-reviewer edges (`parallel=`)

An edge with multiple CSV reviewers (`prompt="@skill:a,@skill:b"`) runs them
**sequentially** with first-reject short-circuit by default. Set `parallel=true`
(default cap 4) or `parallel=N` on the **edge** to run that list concurrently:
all verdicts are collected (no short-circuit), ledger order stays list order,
and any reject still refuses the transition (the error names every rejecting
reviewer). Trade-off: a rejected parallel round spends tokens on every reviewer
— keep parallel opt-in only on gates that need multi-axis judgment. Absent
`parallel=` is byte-for-byte sequential. See `satelle help workflows`.

### Gate binding by agent name

A gated edge or reviewer node may name any `role = "reviewer"` binding in
`.satelle/agents.toml` via `agent=<name>`. Omitted or `agent=reviewer` uses
`[reviewer]`. The agents layer owns harness, tools, and model — the workflow
names *who*. Legacy DOT `model=` is superseded (warning + strip on refresh).
See the satelle-dot-standard principle.


**Refusals (fail loud, never silent):**

- A node names `agent=<name>` but `.satelle/agents.toml` defines no `[<name>]`
  binding → the transition is **refused** (there is no silent in-loop fallback).
- A binding's `tools` grant does not include the read-only satelle CLI
  (`Bash(satelle:*)`, or a broad `Bash` / `*`) → the dispatch is **refused**,
  because the agent could not pull its context (below).
- A binding whose `command` is `in-loop` keeps the step with the orchestrating
  session — not dispatched.

## Control plane in vs agent I/O out

- **In** (orchestrator / any agent → satelle): **satelle CLI verbs** only
  (`satelle story set`, `story get`, …). Small local surface — not an MCP tool
  dump of every verb. Status and engagement change only through satelle.
- **Out** (satelle → isolated worker): subprocess configured in
  `.satelle/agents.toml`. Two **transports**, one binding shape
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
satelle-owned launcher scripts under `$SATELLE_HOME/agents/bin/` (does not change
the default reviewer or `[agent] cli`). `satelle agent` (singular) selects and
validates the headless CLI / agents.toml.

#### Install the ACP adapter (dogfood)

```bash
# Preferred: satelle-owned launcher (does not change default reviewer)
satelle agents install codex
# Manual fallback (or use npx each run):
#   npm install -g @agentclientprotocol/codex-acp
# Auth: CODEX_API_KEY or OPENAI_API_KEY (ChatGPT login also works for interactive)
export CODEX_API_KEY=…                          # or OPENAI_API_KEY
# Optional: point at a specific codex binary
export CODEX_PATH=$(which codex)
# Reviewer dogfood: keep the adapter in read-only agent mode when possible
export INITIAL_AGENT_MODE=read-only
```

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
- **Live dogfood** is optional: set credentials, install the adapter, point a
  named binding at Codex ACP or exec, run `satelle agent validate`, then drive a
  cheap gate (e.g. step-summary) or a one-off story transition. Env gate
  `SATELLE_CODEX_DOGFOOD=1` is the operator convention for optional live
  probes — unset must never fail CI.

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

- **Give the node a rubric.** A dispatched node needs `prompt="@skill:<name>"`.
  A rubric-less dispatched node (`agent=<name>` with no `@skill:`) receives only
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

1. **Define the binding** in `.satelle/agents.toml`:

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
   # note: satelle's Bash(satelle:*) grant check still expects Claude-shaped tools for dispatch
   ```

2. **Allocate a workflow node** to it in the DOT:

   ```dot
   design [agent=architect, prompt="@skill:architect"]
   ```

3. **satelle dispatches it** on entry to `design`: the item on stdin, the
   `architect` rubric (+ charter + pull-context call-to-action) as the system
   prompt, the binding's tools/model as the grant — on whatever CLI the harness
   names.

**Anti-pattern:** defining that agent in a harness-specific agent directory (e.g.
`.claude/agents/architect.md`) works *for that one harness*, but hides the process
configuration from satelle — it cannot see, validate, dispatch, or carry it
repo-agnostically, and it silently pins the repo to one CLI vendor. Keep process
agents in `.satelle/agents.toml` + the workflow DOT.

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
# .satelle/agents.toml
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


A gated edge names its binding with `agent=<name>` (default `[reviewer]`). Models live in agents.toml only; DOT `model=` is superseded.

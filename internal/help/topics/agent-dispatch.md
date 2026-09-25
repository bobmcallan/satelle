# Named-agent dispatch — how an isolated step receives its instructions

A route's step can allocate its work to a **named agent** instead of the in-loop
session. When a step carries `agent: <name>` (any name other than `executor` or
`reviewer`), satelle **dispatches** that step: it spawns the agent CLI configured
in `.satelle/workflows/agents.toml` under `[<name>]`, hands it the work, and folds the
result back in. The agent runs with a **fresh context** — it never sees the
conversation — so the contract below is how it learns what to do and how it gets
the rest of the story.

The repo agents file is **committed substrate by product default** (role set +
bindings a clone needs to run gated steps); execution detail and secrets live
in the machine catalog and `satelle.local.toml`. See
`satelle help global-agents` for that posture and the fresh-clone checklist.

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

A step's `reviewer_agent:` (or a `[[gate]]` entry's `agent`) may name any
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
  `[[gate]]` entry) need **no channel**: satelle *pushes* the attachments into the transition payload's
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
  `.satelle/workflows/agents.toml`. Three **transports**, one binding shape
  (epic:agent-dispatch-transport):

| `interface` | Meaning |
|-------------|---------|
| **`command`** (default for a one-shot use) | Full multi-token argv template; any CLI (Claude Code, `grok -p`, wrappers, custom). One-shot stdin/argv; reviewers stay here. |
| **`acp`** | Agent Client Protocol over stdio; `command` is the **spawn line only** (e.g. `grok agent stdio`). System/payload ride the session, not `{placeholders}`. |
| **`stream`** | Stream-json live session (Claude preset, opt-in: `DefaultClaudeStreamCommand`). `{system}`/`{payload}` are rejected — they ride the first user message; `{tools}`/`{model}`/`{effort}` remain argv. For the orchestrator binding (live turns), not reviewers. |

Shared fields on all three: `role`, `tools`, `model`, `effort`, `secondary`,
`principles`, `env`, `timeout`, `idle_timeout`, `settings`. Reviewers keep Claude on
`interface = command` (cold one-shot, verdict contract). `stream` is the
live-session option for an orchestrator binding. An ACP-capable CLI is usable
when it implements ACP agent stdio **and** the binding sets `interface = "acp"`.
Workers never advance story status; they return text/verdicts that satelle
enacts after gates.

### Default interface (epic:model-selection child 2)

Omitting `interface` no longer means "always command" — the resolved
transport now depends on **how the binding is used**:

- **Live** (a rework relay's coder seat or a `rework.consult` binding) resolves to the first entry in
  `[defaults] live_interfaces` that can actually open the binding's AUTHORED
  command — two questions, both must hold: MECHANISM (which CLI is this? only
  `stream` for a Claude command, `ExecutableToken` == `claude`; `acp` for any
  other spawn line) and SHAPE (does the argv actually parse for that
  transport? checked by the same construction the runtime opens with, not
  guessed — an authored ONE-SHOT command, e.g. the `[reviewer]` shape's
  `--append-system-prompt {system}`, fails both `stream` and `acp`
  construction, since `{system}`/`{payload}` ride the live protocol, not
  argv, so it is correctly excluded rather than waved through on "first token
  is claude"). A binding with **no `command` and no `profile=`** is refused, never
  resolved to a provider: satelle compiles no live default command. It reports
  `live use: no command` (`satelle agent validate` warns; opening the session
  refuses) with a message naming both live options — `stream` (a stream-json
  CLI spawn line) and `acp` (an ACP agent spawn line). Any default comes from
  authored configuration: a `profile=` in the machine catalog or the `[roles]`
  opt-in. When no candidate can open an authored command, it falls back to
  `command` and names the gap:
  `live use: in-loop` when the command is the in-loop preset (no live
  transport exists for it at all), or `live use: not live-capable` otherwise
  — the existing not-live-capable WARN/refusal then fires with that reason.
- **One-shot** (a gate reviewer, a planner, an edge advisor) always resolves
  to `command`, exactly as before.
- **Explicit** always wins, on either path — an `interface=` that names a
  transport its CLI cannot serve live still produces the not-live-capable WARN
  from `satelle agent validate` and is refused at session open, unchanged.

**Baseline seats.** The logical seats (executor, reviewer, planner, coder,
orchestrator, and the rest the shipped route names) ship embedded. A repo
`agents.toml` names only the seats it changes: a named seat overrides just the
fields it writes, an unnamed seat stays the baseline's, and with no repo file the
baseline runs. A baseline seat carries role and principles only; its command,
tools and model come from a `profile=` the seat names, or from the catalog's
`[roles]` when the repo sets `[defaults] use_global_roles`. Without either, the
embedded fallback stands. A catalog never retargets a repo that did not ask.

The preference order between `stream` and `acp` for a live binding is
configuration, not a compiled opinion — `[defaults] live_interfaces` in
`.satelle/workflows/agents.toml`:

```toml
[defaults]
live_interfaces = ["stream", "acp"]   # shipped default — list only "acp" (or
                                       # only "stream") to exclude a transport;
                                       # mechanism, not list order, decides
                                       # which one an authored command can
                                       # serve, since a Claude command can
                                       # never be acp-capable or vice versa
```

`satelle agent validate` prints each binding's resolved interface and why:
`interface=stream (live use)`, `interface=command (one-shot default)`,
`interface=acp (explicit)`, `interface=command (live use: in-loop)`,
`interface=command (live use: not live-capable)`,
`interface=command (live use: no command)`.

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

### Idle-stall detection vs a hard timeout (`idle_timeout` / `timeout`, sty_752c4ef2)

A dispatch runs for as long as it keeps making progress. Satelle stops it only
when it has genuinely **stalled** — no REAL event (a tool start/end, a
message, or a usage report) for `idle_timeout`. A heartbeat alone does not
reset the clock: a heartbeat proves the transport is still alive, not that the
agent is doing anything, so a dispatch that heartbeats forever with no real
event is still judged stalled.

- **`idle_timeout`** is a Go duration string (`"5m"`), set per binding or once
  for every binding under `[defaults]`:

  ```toml
  [defaults]
  idle_timeout = "5m"   # shipped default when neither level sets one

  [coder]
  idle_timeout = "10m"  # this binding's own override wins over [defaults]
  ```

  There is **no compiled wall-clock cap** — a binding that authors no
  `idle_timeout` and whose `[defaults]` is also silent still gets the shipped
  default (currently 5 minutes of no real event), not an unbounded run.
- **`timeout`** is now an *optional hard ceiling* on one dispatch's total
  wall-clock time. It is **unset by default** — a progressing agent is never
  cut off by elapsed time alone. An operator who genuinely wants an upper
  bound (e.g. a cost or CI-slot limit) still sets `timeout = "20m"` on the
  binding; when set, it wins over the stall detector even for an agent that is
  still emitting real events.
- A stall is recorded as its own outcome, **`stalled`** — distinct from
  `timeout` (the hard ceiling fired) or `error` (the process failed) — carrying
  the idle duration and the last real event's label. It is ledgered as
  `agent-stalled` telemetry, and the refusal text names the stall (`"stalled:
  no activity for 5m0s (last event: tool: Bash)"`), not a generic deadline.
- Applies to every transport (`command`, `stream`, `acp`) and to a live
  session's own turn (a rework relay coder/consult
  round) — not only the one-shot dispatch path.

### Silent one-shot command bindings (`busy_timeout`, sty_db62a3b9)

A `command`-transport binding whose CLI prints one envelope at exit (for
example `claude -p --output-format json`, `grok -p … plain`, `codex exec`)
emits nothing while it works, so output alone cannot tell a thinking run from a
hung one. For the command transport only, satelle also samples the CPU time of
the child's **process tree** (Linux: `/proc`); each time it advances, that
counts as liveness and resets the `idle_timeout` clock. A process that makes no
CPU progress (a hang, a sleep) still stalls at `idle_timeout` exactly as before.

- **`busy_timeout`** caps that exemption, measured from the last *real* output
  event (the start event when the CLI printed nothing). Past it, CPU progress no
  longer keeps the run alive and the stall message ends `(busy cap exceeded)`.
  A Go duration string set per binding or under `[defaults]`; binding wins over
  `[defaults]`, which wins over the shipped default (60 minutes). `"0"` or
  `"off"` disables CPU liveness and restores strict `idle_timeout` behaviour.

  ```toml
  [defaults]
  busy_timeout = "60m"   # shipped default when neither level sets one

  [planner]
  busy_timeout = "off"   # strict idle_timeout for this binding
  ```
- Stream and ACP transports are unchanged — they never use the probe.
- On a platform where process CPU time cannot be read (anything but Linux) the
  probe reports that it is unavailable and the run keeps the strict behaviour.
- An explicit `timeout` (hard ceiling) still wins over both.

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

### Stream-json (Claude live session, sty_d244fe1b)

`interface = "stream"` speaks Claude Code's native bidirectional NDJSON
(`--input-format stream-json --output-format stream-json`). Satelle writes the
system prompt and payload as the first user message, answers `control_request`
permission asks with the same mutator policy as ACP, and unwraps the terminal
`{"type":"result","result":"…"}` so `parseDecision` sees the verdict JSON.

Opt-in preset for Claude (never an implicit default), `DefaultClaudeStreamCommand`:

```
claude -p --input-format stream-json --output-format stream-json --verbose --disallowedTools Write,Edit,NotebookEdit,Bash --allowedTools {tools} --model {model} --effort {effort}
```

`{system}` and `{payload}` are rejected on the spawn line; `{tools}` /
`{model}` / `{effort}` stay argv (empty drops the preceding flag). **Reviewers
stay `command`** — a live channel does not help a cold one-shot verdict.
`stream` exists for live seats such as a rework relay's coder or consult binding.

#### Who performs a step

- The **driving session is the in-loop executor**. The shipped `[coded]` step is
  `agent = "executor"`, so the session that engaged the story performs
  `in_progress`, integration, release and the close itself.
- A **one-shot coder** (`interface = "command"`, a `claude -p` style line with
  no `interface = "stream"`) runs only when the route names it on a step's
  `agent =`. It is optional; nothing requires one.
- **`satelle story rework` is the only live relay.** It is off unless the step
  declares `rework = { consult, rounds }`, and its consult binding must be
  live-capable (`acp` or `stream`). A consulting reviewer is
  **consulting, not judging** — its reply is context, not a verdict; the gate
  that judges the edge still runs cold and one-shot over the payload satelle
  builds. See
  `satelle doc get principles satelle-agent-consultation`.

A live session's permission channel is the same policy as PreToolUse (see
**PreToolUse deny channels** below): a mutator tool ask is denied by satelle
without prompting the human when the story is not in an executor-owned
performing state (or a transition is in flight). Otherwise the human is asked
allow/deny.

### Converge then gate

Two loops, contrasted. The **expensive** loop is present-edge → gate rejects →
re-present until the gate accepts. The **cheap** loop is warm convergence
between a coder session and a consulting reviewer (the rework relay, when the
step sets `rework`), then **one** cold gate. The
second does not replace the first — it precedes it. Authority stays with the
gate: a consultant's `READY` is a signal to the orchestrator, never a verdict
(see the READY contract under **Rework relay** below).

**Blocked fallback** (authored in the orchestrator skill, not compiled): after
the round budget without `READY`, or after a declared number of gate rejections
on the same edge, the orchestrator parks to `blocked` quoting the last objection
and messages the developer. The binary carries only the relay and its
termination rule. See `satelle doc get principles satelle-agent-consultation`.

#### Rework relay — `satelle story rework` (sty_8e0b29a0)

The reject → re-present cycle is the expensive loop. The cheaper shape is **warm
convergence then a cold gate**: a coder session and a *consulting* reviewer
session converse about the slice until the consultant says ready or a round
budget is spent; then the orchestrator presents the edge and the edge's reviewer
runs cold and one-shot with the transcript in its payload. Neither live session
moves status. Authority stays with the gate.

**Declared as configuration, on the step.** A performing step opts in; a step
without the key has no loop and behaves exactly as before.

```toml
[coded]
status = "in_progress"
agent  = "coder"                                  # the performer — the CODER side
rework = { consult = "reviewer", rounds = 3 }     # who to converse with, for how long
```

`satelle story route <id>` and `satelle workflow show <category>` print the
line; `satelle agent validate` **warns** when `consult` names a missing or
non-live-capable binding (warn, never a refusal — a repo may author the loop
before wiring the binding, and the relay is opened by hand). Both `agent` and
`consult` must be live-capable bindings: a `command = "in-loop"` performer
cannot be relayed, so a step that opts in must allocate a live-capable one.

**Who opens it.** The orchestrator or the in-repo agent runs
`satelle story rework <id>`, at the story's *current* status. Nothing dispatches
it — entering a state fires no agent (flat dispatch), and satelle relays rather
than anyone monitoring. `--rounds N` may only **lower** the authored budget:
the budget is configuration.

**Turn protocol.** The consultant speaks first (it reviews the slice as it
stands against the ACs). Then each round is consultant → coder → consultant.
Every turn is ledgered as an `agent_message` with its real `from`/`to` roles —
the binding names — plus `cc = "*"`, so `satelle story messages <id>` reads as
the conversation *and* the transcript reaches whoever later judges the edge
through the payload's `messages[]` (within the existing message budget). `cc` is
an additional address a row is readable under; `to` stays who it is for.

**Termination is a rule, not a decision.** The consultant's reply must end with
a FINAL non-empty line that is exactly:

```
READY
NOT READY: <the single most important thing still wrong>
```

`READY` ends the relay with `converged=true`. Anything else — including a reply
that ignores the contract — counts as **NOT READY** with the objection captured
and **consumes a round**, so a consultant that cannot follow the contract cannot
hang the loop. When the budget is spent the relay ends with `converged=false`
and the last objection.

The relay prints its result and records the same as a ledger row:

```json
{"converged": true, "rounds": 2}
{"converged": false, "rounds": 3, "last_objection": "AC4 has no test"}
```

It **never** sets status. `READY` is a signal to the orchestrator, never a
verdict: the orchestrator presents the edge, or — after the budget without
ready, or after a declared number of gate rejections on the same edge — parks to
`blocked` quoting the last objection. It never lowers an AC to converge. See
`satelle doc get principles satelle-agent-consultation`.

**Permission policy, per side.** The two sessions are *not* symmetric:

| Side | Charter | Mutator tools |
| --- | --- | --- |
| coder (step's `agent`) | executor — it is driving its own edits | allowed **iff** its own binding's `tools` grant admits mutators **and** the seat is live, not mid-transition, and its committed status is a performing state the route allocates to **that binding** |
| consultant (`consult`) | consulting — reply is context, not a verdict | **denied by policy**, whatever the seat says |

The coder's rule is the route's own allocation, read from the seat — not a state
name compiled into the binary. The consultant's grant is already read-only; the
policy is the second, non-negotiable refusal, because a consultant that can edit
is a reviewer marking its own work. PreToolUse `gate` / `commitgate` honour the
same allocated-binding rule when the rework verb marks the coder spawn with
`SATELLE_RELAY_BINDING` / `SATELLE_RELAY_ITEM` (and exports the lease's session
id so the hook binds that seat); unmarked sessions stay on the driving-session
and in-flight dispatch branches.

### Workspace bindings layer — `satelle sync bindings` (sty_01949949)

The agents layer can be decided centrally and executed locally. Two verbs and
one file:

- `satelle sync bindings push [--dry-run]` publishes this repo's
  `.satelle/workflows/agents.toml` into the bound TEAM workspace's publish
  catalog (kind `agents`), **redacted**.
- `satelle sync bindings pull` applies that catalog entry as
  `.satelle/workflows/agents.workspace.toml` — a separate file, so sync never
  rewrites authored bytes. The bare `satelle sync` aggregate pulls it too when
  the `agents` area is opted in.
- The workspace file is a **layer under** the repo's `agents.toml` in the
  precedence ladder: `repo → workspace → profile / global-role → embedded`. A
  repo field wins; a workspace field fills a repo blank; a workspace-only table
  applies whole. Role is identity on this tier as on a profile — a disagreement
  is refused. `satelle agent validate` prints the layer's presence and names
  the source of every effective field (`source: tools = "…" (workspace)`).

**Redaction is a property of the agents kind, not of a verb.** Every transport
of an agents layer — `sync bindings push`, `publish push` of
`workflows/agents.toml` (whatever `--kind` was typed), the `agents` area of
`sync config push`, and `sync bindings pull` on **ingest** — goes through the
same function: literal `env` values are blanked (keys survive: "this binding
needs `ANTHROPIC_AUTH_TOKEN`"; a pure `${VAR}` reference survives too — a
variable name is not a secret, and it is what lets the receiving machine's
`[vars]` fail-fast fire), secret-shaped `settings` values and everything under
`settings.env` are blanked the same way, absolute path strings anywhere in
`command` or `settings` are reduced to their base name, `profile=` is dropped.
A body that does not parse is an error, never shipped raw. Ingest redacts again
so a catalog entry an older path left unredacted cannot land absolute paths or
env values as live bindings.

**A blank left by redaction is "declared, unsatisfied", never a value.** The
workspace tier drops blank env keys and blank secret settings before it is
merged, so a pulled layer can never lay `KEY=` over the machine's real
environment or a blank secret over `settings.local.json`. And when a redacted
store copy is written back over the authored `agents.toml` — `sync config
deploy`, `publish adopt`, `publish check --update` — the file is either left
byte-for-byte alone (the store matches it after redaction) or the store's
layout is deployed with this machine's env values, command paths and
`profile=` re-applied from the local file.

**Executables and `${VAR}` resolve on the machine that runs the binding.** The
workspace says `claude -p …`; this machine's PATH decides whether `claude`
exists. A binding whose program is missing is a hard refusal at dispatch — the
same posture as a missing binding, never a silent in-loop fallback — and a
`WARN` in `satelle agent validate`. `${VAR}` references expand from the local
`[vars]` at wiring time, as they always have.

A repo with no hosted server or no team workspace does nothing on either verb
and contacts nothing; a repo that never pulled a layer resolves byte-identically
to before.

### Codex — preferred ACP, secondary command (sty_3b4909bb)

Codex is a first-class agent on the **same command and acp transports** as
everyone else. Codex needs no third interface. App Server is an implementation
detail of the ACP adapter, not a satelle protocol. `stream` is Claude's live
channel, not a Codex transport.

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
- `satelle story messages <id>` — directed agent messages (kind `agent_message`),
  oldest first; `--to` keeps that role plus `*`; `--since` is RFC3339. Read-only.
  Write with `satelle story message <id> --from <role> [--to <role>|*] --body <text>`.

A read-only reviewer whose grant excludes Bash judges attachments from the
transition payload's `docs` array (injected by the engine, sty_58fa970e) and
the engagement slice from the payload's `diff` object (files, stat, patch;
sty_a125b440) — no disk path required. Both are enumeration, not verdict.
The payload also carries `messages[]` (sty_2db624d0): engagement-windowed
`agent_message` rows whose `to` is the dispatched binding name, the protocol
role (`reviewer` on a gate, `executor` on a named performer), or `*`, oldest
first, at most 20, each body capped at 2 KiB. A message is **context**, never
a verdict input a reviewer must obey. Absent when none qualify (`omitempty`).
Shell-granted agents may also pull more via the satelle CLI.
Do **not** use in-repo `.satelle/stories/` — that path is obsolete
post-relocation. **Fetch before concluding a document or a prior step is
missing** (payload first, then CLI when available).

`story docs`, `story messages`, and `ledger list` may render a compact
`[N]{col:kind,...}` table instead of a JSON array — this repo's `[output]`
config defaults compact mode on for a dispatched or in-loop caller (see
`satelle help compact-output`); parse it the same way, or pass `--json` for
the plain form. `story diff --patch` similarly loses its `index` lines and
offloads noisy/whitespace-only hunks behind a `<<ccr:HASH,KIND,SIZE>>` marker
that `satelle retrieve <hash>` resolves exactly.

## Scratch directory, attaching without a file, and leftover files (sty_e7aaf8b1)

A dispatched or live agent session never has to be told where scratch work
goes, and never has to write evidence into the repo tree to hand it to
satelle:

- **Every dispatch gets a scratch directory by mechanism.** A one-shot
  `Invoke` (reviewer, named executor, retrospective), the step-summary
  dispatch (`Summarise`, its own `buildRequest`/`runOnce` call outside
  `Invoke`), and every live session (the rework relay's
  coder and consult seats) each get their own
  `<tmp>/satelle/<repo-key>/<story>/<dispatch-id>/`, mode `0700`.
  It is exported as both `TMPDIR` and `SATELLE_SCRATCH` — RESERVED keys that
  override any binding-authored `[env]` value of the same name — and named in
  the generated charter every binding receives, so no skill or principle file
  carries the instruction.
- **Success removes it; failure keeps it.** A dispatch or live session that
  ends cleanly has its scratch directory removed. One that errors (a failed
  run, a session whose `Close()` returns an error) keeps the directory and
  records a `scratch_kept` ledger row naming its path, so a failure stays
  inspectable.
- **Attach without a file.** `satelle story attach <id> --name <n> --type <t>
  --body "..."` takes the document body inline — no pipe, no file, works
  under a `Bash(satelle:*)`-only grant. `--body`/`--file` are mutually
  exclusive; `--file -` reads stdin, and `--file $SATELLE_SCRATCH/<f>` is the
  alternative for a very large document (the scratch path sits outside the
  repo tree and is already edit-gate exempt under `/tmp/`). `satelle story
  log <id> --kind <k> --data key=value` takes typed telemetry the same way —
  a `--data` value may itself be a multi-line string.
- **Leftover files are swept before the next gate.** A DRIVING-role session
  (a named coder's one-shot perform dispatch, or a live coder/orchestrator
  session — never a read-only consult/reviewer session) that leaves an
  untracked file matching `[dispatch.leftovers]` is caught at the dispatch's
  end, before the next gate sees the diff:

  ```toml
  [dispatch.leftovers]
  patterns = [".ac-evidence*", "*_debug_test.go"]  # globs, path or basename
  content_regex = '^\s*package \w+\s*$'            # optional: full-content match
  max_bytes = 64                                    # bounds the content_regex read
  action = "move"                                   # "move" (default) | "flag"
  ```

  Only files **this session created** count — a snapshot of untracked files is
  taken before the session runs, so a pre-existing untracked file is never
  touched. A match is moved to `<scratch>/leftovers/<relpath>` (kept there,
  inspectable, even though the run itself succeeded) or, with `action =
  "flag"`, left in place and only reported. Either way a `leftovers` ledger
  row names the files and the scratch directory. Empty `patterns` and empty
  `content_regex` (the default — the binary ships no opinion, not even a Go
  test-file rule) disable the sweep entirely, with no extra `git` cost.

- **Clearing debris the sweep did not catch (sty_d74e9b1b).** A coder can create
  files but has no grant to delete them, and the scope gate rejects untracked
  debris before anyone may delete. `satelle story tidy <id> <path>...` MOVES
  such paths into a story-level tidy area (`<tmp>/satelle/<repo-key>/<story>/tidy/`,
  a sibling of the per-dispatch scratch dirs, so it outlives them) and writes one
  `tidy` ledger row per path; `satelle story untidy <id> [<path>...|--all]` moves
  them back and writes `tidy_restore` rows. It works at any performing step, from
  the driver or a dispatched session, and is all-or-nothing: a path that is
  tracked, exists in HEAD or at the engagement baseline, predates the engagement
  baseline, is missing, or lies outside the worktree refuses the whole call with
  a reason per path, and untidy never overwrites. The scope-review skill
  ends a debris rejection with the ready-to-run command.

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

2. **Allocate a route step** to it in `step.toml` (`agent: architect`):

   ```toml
   [designed]
   status = "design"
   agent = "architect"
   skills = ["architect"]
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

## Model selection (sty_7069bced)

`model` is optional in agents.toml. When a binding leaves it unset, satelle
selects one deliberately instead of silently taking the CLI's own default, and
records WHY next to the alias and the resolved id (`satelle story cost`, the
web timeline). Precedence, first match wins:

1. **binding** — the binding's own `model =` in `.satelle/workflows/agents.toml`.
2. **step** or **agent** — a model named for THIS ONE dispatch: a workflow
   step's `model =` in `step.toml` (a spine performer node only — a gate/edge
   has no step tier of its own), or a dispatching agent's `--model` flag
   (`satelle story rework --model`, `satelle
   story retrospect --model`). An agent flag wins over a step default when
   both would apply to the same dispatch.
3. **inherited-orchestrator** / **inherited-in-loop** — the orchestrator
   live-session model on this story, else the in-loop
   engaging session's. The orchestrator wins whenever both are eligible (it
   is the live driving session). Both are guarded so a Claude session's
   model id can never reach a Codex/Grok dispatch — see "cross-provider
   guard" below.
4. **creator** — the model of the session that created the story, same guard.
5. **order** — the first entry of the `[model_order]` list for THIS binding's
   executable (see "The model order" below). Applied only when the binding can
   accept a model, and never from another executable's list.
6. **cli-default** — none of the above resolved: the `{model}` placeholder is
   dropped and the CLI's own default runs. Recorded as `cli-default`, not left
   silent.

Every dispatch — a gate, a named-agent step, a live chat/rework session, a
retrospective, quality escalation — records BOTH the resolved model and which
of these eight values (`binding`, `step`, `agent`, `inherited-orchestrator`,
`inherited-in-loop`, `creator`, `order`, `cli-default`) picked it, on the same
`agent_invocation` / `review_accept` / `review_reject` row that already
carries `model_resolved`. `satelle agent validate` reports a step's
`model =` as the node's effective model (source `step`) whenever the
allocated binding itself has no `model =` (source `binding` wins outright).

### The model order

An empty `model =` stays a real choice: it lets a dispatch follow the driving
session of the same CLI. When no same-CLI session model applies, the order tier
uses `[model_order]` in agents.toml — one list per executable, first entry
first. An entry is a name, or an array of names that are one rank (an alias and
its resolved id); the first name is applied. `satelle init` writes these lists
into a fresh agents.toml and appends them to an existing one that has no
`[model_order]` table:

| executable | order (first applies) |
| --- | --- |
| claude | opus, sonnet, haiku |
| grok | grok-4.7 |
| codex | gpt-5-codex |

The codex id comes from a captured `codex exec --json` fixture and the grok id
from a captured ACP `modelId`; claude uses the CLI's own aliases. An executable
with no list falls through to `cli-default`, recorded as such.

### The cross-provider guard

An inherited or creator model is applied only when the dispatch binding's own
command template carries a `{model}` placeholder (a slot to fill) AND the
captured session's executable matches the binding's own command executable
(e.g. both `claude`). A Grok-native or Codex session's model id never rides
into a Claude dispatch, or the reverse — a guard failure simply falls through
to the next precedence tier instead of erroring.

### Per-adapter capabilities

What each adapter gives satelle. An unavailable is recorded explicitly — an
adapter-named reason on the ledger row, never a silent zero or a Claude default
(`satelle-agent-agnostic`). `internal/agentcli/capabilities.go` is the source of
this table and a test checks every cell against the code that produces it.

| adapter | usage | cache split | resolved model | model inheritance | live session |
| --- | --- | --- | --- | --- | --- |
| claude command | yes | yes | yes | yes | unavailable: interface=command is one-shot only |
| claude stream | yes | yes | yes | yes | yes |
| grok command | yes | yes | yes | yes | unavailable: interface=command is one-shot only |
| grok acp | yes | yes | yes | yes | yes |
| codex command | yes | yes | unavailable: codex exec --json names no model | yes | unavailable: interface=command is one-shot only |
| codex acp | unavailable: no captured usage report from the peer | unavailable: no captured usage report from the peer | unavailable: codex acp reports no model | yes | yes |

### What each harness reports

- **Claude Code** — the in-loop hook payload's `model` field (or the
  transcript's last assistant entry) captures the in-loop tier; a `stream`
  session's own `system`/`init` record captures the orchestrator tier
  (a live orchestrator session).
- **Codex** — its hook payload carries `model`, so the in-loop tier is
  captured; if a payload omits it the session is recorded `unknown` and the
  rule falls through.
- **Grok** — its hook payload carries no model, so the in-loop tier is recorded
  `unknown`.
- **ACP (Grok, Codex)** — a live session records the
  model it opened with (the peer's own id, else the model the handshake
  applied) as the orchestrator tier, under the binding's own executable. That
  tier applies to any dispatch whose binding has the same executable and can
  take a model, so grok command inherits from a `grok agent stdio`
  orchestrator. When no model is known the session is recorded `unknown`.

Model inheritance in the capability table is "available" when either tier can
apply; it needs a live session or a hook that carries a model, so with neither
the dispatch falls through to `cli-default`, recorded explicitly.

An explicit `model =` on a binding is unaffected by any of this — it wins
outright, unchanged from before this story, and no session capture or ranking
lookup ever runs for that dispatch.

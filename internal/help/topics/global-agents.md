# Machine-wide agent profiles — one provider definition, many repos

An operator with several satelle repositories used to restate the same Claude
or Grok binding in each one. The **profile catalog** at
`~/.satelle/agents.toml` holds those definitions once; each repository then
**explicitly** points a binding at a profile.

The split is the constitution's line, applied to configuration:

| Where | What lives there |
| --- | --- |
| `~/.satelle/agents.toml` (machine-wide) | **Execution** configuration — how an agent runs: command, transport, model, effort, tools, timeout, env, settings. |
| `.satelle/workflows/agents.toml` (repo) | Which logical roles this repo has, and which profile (or inline binding) each one uses. |
| `.satelle/workflows/*.md` (repo) | **Process** — which step runs which skill, which gate judges which edge. Never machine-wide. |

## Repo agents.toml posture (committed substrate)

**`.satelle/workflows/agents.toml` is committed substrate by product default.**
It declares the logical roles your workflows name (`agent=<name>`) and which
profile or inline binding each one uses. A clone that tracks process under
`.satelle` needs this file to run any gated step. Secrets and machine-specific
execution detail never belong in it — put those in the catalog
(`~/.satelle/agents.toml`) or the gitignored `satelle.local.toml`.

`.satelle` as a whole stays **git-optional** (continuity is local disk or
personal rehydrate). This is recommended posture when a team tracks process,
not an enforcement: a repo that ignores all of `.satelle/` still runs.

**Fresh clone checklist**

1. If the repo commits `.satelle/workflows/agents.toml` and bindings are fully
   inline (no `profile=`), gated steps run after a normal init of any other
   missing scaffold.
2. If bindings reference profiles, seed or restore the catalog
   (`satelle agent migrate`, or restore a backed-up `~/.satelle/agents.toml`),
   then run `satelle agent validate` so every binding resolves.
3. Put secrets under `[vars]` in `satelle.local.toml` or the catalog — never in
   the committed agents file.

**Why committed.** Workflows name roles with `agent=<name>`; a clone whose
agents file never declares those roles cannot run the gated steps that name
them — the role set travels with the process it serves. The product already
behaves this way: `satelle init` scaffolds a runnable agents file whose seeded
body says secrets never belong in it, load is fail-closed on a missing or
malformed agents.toml, and the managed gitignore block ignores
`satelle.local.toml`, `workflows/agents.local.toml` and `.satelle/satelle`,
never `workflows/agents.toml`.
Operator-local pressure is relieved elsewhere: `profile=` keeps command, model,
and effort machine-side; `[vars]` plus `satelle.local.toml` keep secrets out of
the tree; repo-inline values still win per field. None of this forces git
tracking of `.satelle` as a whole — continuity stays local disk or personal
rehydrate; "committed substrate" means *if* you track process, track the agents
layer with it.

A profile that tries to carry process — `applies_to`, `skill`, `prompt`, `on`,
`output_*`, a workflow name — is **refused at load**. That refusal is the whole
point: a machine-wide file must not be able to change what any repo's process
*is*.

## The catalog

```toml
# ~/.satelle/agents.toml

[vars]
# Machine-wide KV for ${NAME} in a profile's env/settings. Secrets live here, on
# this machine — expanded in memory at dispatch, never written into a repo.
GLM_API_KEY = "sk-…"

[profiles.claude-opus]
role       = "reviewer"
interface  = "command"
command    = "claude -p --disallowedTools Write,Edit,NotebookEdit,Bash --append-system-prompt {system} --allowedTools {tools} --model {model}"
tools      = "Read,Grep,Glob"
model      = "opus"
effort     = "high"
timeout    = "45m"
principles = "session"
secondary  = "grok-acp"

[profiles.grok-acp]
role      = "reviewer"
interface = "acp"
command   = "grok agent stdio"
tools     = "read_file,grep,list_dir"
model     = "grok-4.5"

[roles]
# OPT-IN per-role defaults — see tier 3 below. Reaches only a repo that asks.
# reviewer = "claude-opus"
```

A profile carries exactly the `AgentBinding` execution keys: `role`,
`interface`, `command`, `tools`, `model`, `effort`, `timeout`, `principles`,
`env`, `settings`, `secondary`, and `profile` (to extend another profile).
Anything else — a policy key or a typo — fails the load by name.

## Consuming a profile from a repo

```toml
# .satelle/workflows/agents.toml
[reviewer]
profile = "claude-opus"   # explicit reference
effort  = "low"           # …and this still wins over the profile's "high"
```

Scalar fields: the repo's non-empty value wins. `env` and `settings` merge
key-wise, with the repo's key winning; a repo cannot delete a profile's key.
**`role` is identity, not an override** — a repo declaring a role that
contradicts the profile's is refused rather than silently resolved either way.
Restating the same role is fine.

A profile may extend another with `profile = "<name>"`. The outermost wins field
by field; a reference cycle is refused, naming the loop.

## Precedence

Highest first:

0. **local** — an inline value in `.satelle/workflows/agents.local.toml`, this
   machine's override of the shared file. Same schema, field by field, and it
   may name the `profile=` and set `[defaults] use_global_roles` for this machine
   alone. It is gitignored and **never syncs**; `agent validate` and `doctor`
   warn on machine-specific content left in the shared file (`profile=`,
   `use_global_roles`, a literal env value, an absolute path), and a push of it
   is refused (`satelle help agent-dispatch`). Only agents.toml has a local twin.
1. **repo** — an inline value on the binding in `.satelle/workflows/agents.toml`
2. **profile** — the profile the binding explicitly names via `profile=`
   (and, transitively, whatever that profile extends)
3. **global-role** — the catalog's `[roles]` default for the binding's role, and
   **only** when the repo opts in with `[defaults] use_global_roles = true`
4. **embedded** — satelle's compiled fallback (`in-loop` for the executor, the
   default Claude template and read-only grant for the reviewer)

**There is no implicit same-name merge.** A profile called `reviewer` and a repo
`[reviewer]` that never mentions it do *not* combine — the repo resolves
byte-identically whether or not the catalog exists. A repo with no `profile=`
anywhere is untouched by anything the operator adds to the catalog later. That
guarantee is what makes a shared catalog safe on a machine holding pinned
repositories.

## Seeing what resolved, and from where

```bash
satelle agent profiles     # the catalog: every profile and role default
satelle agent validate     # every binding's effective fields + their source
```

`agent validate` renders each grant and then, per field, the tier that supplied
it:

```
GRANT [reviewer] role=reviewer … model="opus" effort="low" …
       source: command = "claude -p …" ← profile:claude-opus (~/.satelle/agents.toml)
       source: effort = "low" ← repo (.satelle/workflows/agents.toml)
       source: model = "opus" ← local (.satelle/workflows/agents.local.toml)
       source: tools = "Read,Grep,Glob" ← embedded
```

`env` and `settings` lines name the field and its source only — values may be
secrets and are never printed.

The same run refuses, with the offender named: a missing profile, a reference
cycle, a repo/profile role conflict, an invalid `interface`, a reviewer whose
merged binding escapes its read-only ceiling, and an unresolved `${VAR}`. The
ceiling is judged on the **merged** binding, so a profile cannot smuggle a
capability past a check by supplying it machine-wide.

## Variables

The catalog's `[vars]` is the base; a repo's own `[vars]` (and its gitignored
`satelle.local.toml` overlay) win per key. Expansion happens in memory at
dispatch wiring, so a machine-wide secret referenced as `${NAME}` reaches the
agent process without ever being written into a repository.

## Launch steps

A profile may declare what a session needs before its harness starts: commands
to run, environment to set and environment to clear. A session started through a
launcher from `satelle agents install claude|grok` runs the in-loop profile's
steps, in order, then starts the harness; one started any other way skips them,
and the session-start hook says so. Nothing is configured by default, and
satelle knows no tool a step runs.

```toml
[profiles.<name>.launch]
set   = { KEY = "literal or ${SATELLE_PROFILE_DIR}/x" }   # the session's environment
unset = ["KEY"]                                           # removed from the session

[[profiles.<name>.launch.steps]]
run     = ["cmd", "arg"]                  # argv; literals or ${SATELLE_PROFILE_DIR}
env     = { KEY = "…" }                   # this step's environment; same rule
secrets = { KEY = "${NAME}" }             # exactly one [vars] reference; this step only
```

`${SATELLE_PROFILE_DIR}` is the directory satelle keeps for the profile
(`~/.satelle/profiles/<name>`). A `${NAME}` that names a `[vars]` secret is
allowed in exactly one place, a step's `secrets` value, and it reaches that
step's process environment and nothing else — not the session's environment, not
any argv, not the repo, not a log. A step that needs the secret on its command
line expands it from its own environment (`sh -c`, below). A failing step stops
the launch with that step's exit status and a message naming only its index and
command.

Two examples, written as configuration only. Each step signs in to a config
directory under `${SATELLE_PROFILE_DIR}`, and `set` points the session at the same
directory. Azure:

```toml
[profiles.ops-az.launch]
set = { AZURE_CONFIG_DIR = "${SATELLE_PROFILE_DIR}/az" }

[[profiles.ops-az.launch.steps]]
run     = ["sh", "-c", 'az login --service-principal -u "$AZ_CLIENT_ID" -p "$AZ_SECRET" --tenant "$AZ_TENANT"']
env     = { AZURE_CONFIG_DIR = "${SATELLE_PROFILE_DIR}/az" }
secrets = { AZ_CLIENT_ID = "${AZ_CLIENT_ID}", AZ_SECRET = "${AZ_SECRET}", AZ_TENANT = "${AZ_TENANT}" }
```

AWS:

```toml
[profiles.ops-aws.launch]
set = { AWS_CONFIG_FILE = "${SATELLE_PROFILE_DIR}/aws/config", AWS_SHARED_CREDENTIALS_FILE = "${SATELLE_PROFILE_DIR}/aws/credentials", AWS_PROFILE = "default" }
unset = ["AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"]

[[profiles.ops-aws.launch.steps]]
run     = ["sh", "-c", 'aws configure set aws_access_key_id "$K" && aws configure set aws_secret_access_key "$S"']
env     = { AWS_CONFIG_FILE = "${SATELLE_PROFILE_DIR}/aws/config", AWS_SHARED_CREDENTIALS_FILE = "${SATELLE_PROFILE_DIR}/aws/credentials" }
secrets = { K = "${AWS_KEY_ID}", S = "${AWS_SECRET}" }
```

Select the profile on this machine in `.satelle/workflows/agents.local.toml` (a
catalog name means nothing on another machine, so it is not synced):

```toml
[executor]
command = "in-loop"
profile = "ops-az"
```

References resolve from the catalog `[vars]` and the repo's gitignored
`satelle.local.toml` only — never from the committed `satelle.toml`.
`satelle agent validate` rejects a malformed table, a literal secret, a `[vars]`
reference anywhere but a step's `secrets`, and a reference nothing defines. It
warns when the in-loop profile has an `env`: the in-loop session execs no child,
so that `env` is not applied — use `launch.set`. pi and cursor have no launcher,
so launch steps are recorded as unavailable for them. A catalog still carrying
the 0.0.672 `[profiles.<name>.cloud]` table fails to load and points here.

**What satelle does and does not do.** satelle runs what the profile declares; it
does not enforce rights or isolate secrets. The cloud's roles and policy, plus a
separate OS user for the agent, do the enforcing: an agent running as the same OS
user can read anything that user can. A tool a step runs may persist credentials
in its own cache, such as the config directories above, and that cache is
readable by the agent too. A step's own tool may also hold a secret briefly in
its argv while the step runs, before the agent exists.

## Migrating

Nothing is required. With no catalog present, every existing repo — including
one relying on `~/.satelle/config.toml [agent] cli` — resolves exactly as
before.

```bash
satelle agent migrate      # seed ~/.satelle/agents.toml from the selected CLI
```

`migrate` is opt-in and non-destructive: it never overwrites an existing catalog
and never writes into a repository. The catalog it seeds leaves `[roles]`
commented out, so it changes nothing until a repo writes `profile = "…"`.

## Personal backup (operator runtime, not project sync)

The catalog is machine-wide operator runtime. Project `satelle sync` never
includes or requires it. To rehydrate a tuned catalog onto a new machine:

```bash
satelle login
satelle agent profiles push      # upload (requires login); [vars] is NEVER uploaded
satelle agent profiles restore   # download onto a clean home; refuses overwrite
satelle agent profiles restore --force   # replace existing (keeps agents.toml.bak)
```

**`[vars]` is never uploaded** — it is this machine's secret KV. After restore,
re-enter secrets under `[vars]` (or in `satelle.local.toml`). Unauthenticated
push/restore fail closed and name `satelle login` — they never silently no-op.

The personal store path is `PUT|GET /api/v1/me/files/agents.toml` (not project
config, not documents, not team publish). The hosted server must implement that
route; the CLI ships the client and the OpenAPI contract.

See also: `satelle help agent-dispatch` (how a dispatched step runs) and
`satelle help workflows` (where process lives).

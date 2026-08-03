# Converting a DOT workflow to done.toml + step.toml

If satelle refused a transition saying **"workflow *X* declares no route"**, this
repo still authors its lifecycle as a DOT graph. That front end is retired: a
lifecycle is now a **derived route** in two files under `.satelle/workflows/`.

Nothing is lost and nothing is automatic. `satelle migrate` REMOVES the
superseded graphs once a working route is on disk, but it will never author one:
deriving obligations from a graph is interpretation, and interpretation is
authored and reviewed, not generated. That is what this page is for — you are the
agent doing the conversion.

Until it is done, the repo refuses transitions rather than running them ungated.
That refusal is deliberate: silently falling back to some other lifecycle would
drop every gate the repo authored.

## The two files

Both halves are **TOML**. The format carries no meaning of its own: a `[table]`
is a record, a `#` line is a comment, and there is nothing else to learn.

**`done.toml` — what DONE means.** One table per story category, keyed by the
category name, each listing its obligations in order plus the role states the
binary synthesises. `["*"]` governs any category with no table of its own (it is
quoted because `*` is not a bare TOML key).

```toml
[meta]
name = "done"
type = "workflow"
scope = "project"
description = "What done means in this repo."

["*"]
obligations = ["raised", "coded", "closed"]
park    = { state = "blocked", gate = "satelle-story-blocked-review", advisor = "blocked-triage", advisor_skill = "satelle-story-blocked-triage" }
cancel  = { state = "cancelled", gate = "satelle-story-cancel-review" }
recover = { step = "in_progress", from = ["integration"] }

[["*".tag_obligation]]
tag = "surface:ui"
obligation = "design-reviewed"
```

- `park` / `cancel` are inline tables with a `state` and a `gate`; the `gate` is
  optional but omitting it means that exit is ungated.
- `advisor` / `advisor_skill` on `park` name an advisor the ORCHESTRATOR consults
  on that state. It is a declaration, never a dispatch.
- `recover` allows backward movement to `step`. Name only steps the route
  actually declares — `from` emits edges verbatim, so a stale name becomes an
  edge from a state that does not exist.
- `[[<category>.tag_obligation]]` appends an obligation when the story carries
  the tag.

**`step.toml` — what discharges each obligation.** One table per step, **keyed by
the obligation it discharges**, plus a `[[gate]]` entry per always-on gate.

```toml
[meta]
name = "step"
type = "workflow"
scope = "project"
description = "The steps and always-on gates this repo's routes select from."

[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
skills = ["code"]
reviewers = ["satelle-story-plan-review", "satelle-story-architecture-review"]
reviewer_agent = "reviewer"
parallel = 0
requires = ["raised"]

[closed]
status = "done"
reviewers = ["satelle-story-done-review"]
terminal = true
requires = ["coded"]

[[gate]]
skill = "satelle-estimate-actual-review"
on = ["in_progress", "done"]
for = ["*"]
```

The table KEY is the obligation — that is the step's identity, and it is what a
category's `obligations` list names. The `status` is the stage name an item holds
while in the step, and stage names REPEAT by design: several route families reach
`done` by discharging different obligations. **This is the one rule the format
does not make obvious, and the one every conversion gets wrong.**

Step keys: `status`, `agent`, `skills`, `reviewers`, `reviewer_agent`,
`parallel`, `requires`, `applies_to`, `advise`, `start`, `terminal`.
Gate keys: `skill`, `agent`, `on`, `applies_to`, `for`, `mandatory`.

An unknown key is an ERROR, never a silent drop — a typo'd `reviewrs =` that
parsed as "no reviewers" would lose a gate.

Both files carry an ordinary `[meta]` header (`name`, `type = "workflow"`,
`scope`, `description`) and **must not carry `applies_to`** — done.toml's tables
are the selector, and a second one would be a second precedence rule.

## Translating the graph you have

Read the retired `.satelle/workflows/*.md` graphs. For each one:

| In the DOT | In the route |
| --- | --- |
| a node with `agent=executor` (or a named agent) | a step table whose `status` is the node name, with that `agent = "…"` and its `prompt="@skill:x"` as `skills = ["x"]` |
| the gate on the edge **into** a node | that step's `reviewers = [...]` — gates belong to the step they admit, not to an edge |
| `reviewer_skill="a,b"` on an edge | the legacy spelling of the row above: the **target** step's `reviewers = ["a", "b"]`. It lands on the step the edge ADMITS, never the one it leaves — putting it on the source moves the gate a step early |
| `agent=` on a gated edge | the step's `reviewer_agent = "…"` |
| `parallel=N` on an edge | `parallel = N` on the target step |
| an edge-less node with `on="a,b"` | a `[[gate]]` with `skill = "…"` and `on = ["a", "b"]` |
| `applies_to="surface:ui"` on a scoped node | `applies_to = ["surface:ui"]` on the gate |
| `mandatory=true` (e.g. the step-summary node) | `mandatory = true` on the gate |
| `shape=Mdiamond` | `start = true` |
| `shape=Msquare` on the success terminal | `terminal = true` |
| a park node (`from="*"`) | done.toml's `park = { … }` |
| a cancel sink | done.toml's `cancel = { … }` |
| `create_review:` / `hooks:` frontmatter | the same declaration in **done.toml**'s `[meta]` — `create_review = "…"`, or a `[[meta.hooks]]` table |
| `rankdir=` | drop it — DOT layout, no meaning in a derived route |
| `on_enter_agent=` / `on_enter_prompt=` | **RETIRED mechanism** — re-home as `advise <agent> @<skill>` on the step. Read the callout below before you touch one |
| `goal=` / `vars=` on the `graph [...]` line | **not the route's** — move to the repo's constitution |
| the `guardrails:` YAML block after the graph | same — move to the constitution |

Then key every spine step by the obligation it discharges, give it a `requires`
naming the previous one, and list those obligations in done.toml in order.

### `on_enter_agent=` is retired, not renamed

It was a live one-shot **entry dispatch**: arriving at a state fired an agent
with `on_enter_prompt`'s skill. Flat dispatch removed it — a state may not
dispatch an agent of its own — so there is no key to rename it to.

Its advisor re-homes as `advise = { agent = "…", skill = "…" }` on the step (or
as `advisor` / `advisor_skill` on done.toml's `park`, for a park node's triage).
That is a **declaration the ORCHESTRATOR consults**; entry to a state never fires
it.

Both ways of getting this wrong lose something:

- **Transcribing it literally** writes a key the grammar rejects — noisy, but it
  fails at parse time, which is the good outcome.
- **Dropping it silently** is the bad one: the state still exists, the route
  still validates, and behaviour the repo relied on is simply gone.

If an entry action genuinely cannot be expressed as an advisor, removing it is a
**decision to record** — in the constitution, or in the story that converts —
not something to leave unsaid.

### `goal=`, `vars=` and `guardrails:` belong in the constitution

They hold real operator intent — a binding constraint on where a service may
listen, a never-do rule about destroying data, a "prove the deploy this specific
way" instruction — and the route grammar has **no home for any of it**. The
route describes states, obligations and gates; it never described intent, and
satelle never enforced these.

That is what makes them the quiet loss: **nothing will warn you when they
vanish.** The converted route parses, validates green, and runs, with the intent
gone. Copy them into the repo's constitution document before you delete the
graph, where a gate that reads the constitution can still act on them.

**Do not author topology.** The binary owns ORDER (a topological sort of
`requires`/`provides`) and the shape (cancel from every non-terminal step, park
from anywhere, backward movement, park→cancel). Every `-> cancelled`,
`-> blocked` and back-edge in the old graph is synthesised — writing them as
steps is the most common conversion mistake.

**A category-specific workflow becomes a SECTION, not a second file.** A graph
declaring `applies_to: ["epic-parent","parent"]` becomes `[epic-parent]` and
`[parent]` tables in the one done.toml, selecting from the one shared catalogue.
Every graph in `.satelle/workflows/` collapses this way — you finish with two
files, however many you started with. Give each lane its own obligation names
where its steps differ, or two lanes will select the same step and collide.

Gates in a shared catalogue need `for` — the categories whose route they belong
to. Without it a deployment gate on `done` fires on every lane, including the
ones with no release to verify.

## The two decisions only you can make

Everything above is a mapping — the same answer in every repo. These two are
not. No document can supply them, because they are choices about what this
repo's process should be. Make them deliberately, then prove them with the
verify loop below.

### 1. Which categories get a `done.toml` table

The binary ships a route, and a repo with no `.satelle/workflows/done.toml`
inherits it. **An authored done.toml overrides the shipped one WHOLLY** — not
table by table. So the override re-declares every category it wants, and the ones
easiest to lose are the ones nobody converted by hand:

- `[execution]` and `[task]` — a repo with tasks in its store loses task-run
  routing without them.
- `[substrate]` — there is **no shipped substrate lane at all**, so a
  markdown-only lane has to be authored, not inherited.

`satelle substrate edit workflows done` materialises the shipped halves into the
repo so you can edit from them rather than starting blank. That is also what
`satelle rebase` redeploys.

The retired graph's `applies_to` sets tell you which categories that repo
actually claimed. They are the input to this decision, not the answer to it.

### 2. Whether the close step stays ungated

Check what admitted the terminal state in the old graph, in **both** places it
could have been declared: a `reviewer_skill=` on the final edge, and a
`agent=reviewer, prompt="@skill:…"` on the terminal NODE itself. A graph often
has an ungated final edge and puts the close gate on the node — read only the
edge and you convert a gated close into an ungated one.

If neither carries a gate, converting literally gives you an ungated close. That
is legal and it is probably not what anyone intended. Decide: name a close
reviewer in `reviewers` on the terminal step, or record that ungated was chosen
on purpose.

## Verify, then retire the graphs

Do this **before** `satelle migrate --yes`. Migrate deletes the graphs, and the
graph is the only thing you have to diff against.

```bash
satelle workflow validate done          # and: satelle workflow validate step
satelle workflow list --category <cat>  # heads with the derived route, active
satelle workflow show <category>        # the DERIVED route for that category
satelle story route <story-id>          # the same route for a real story, with outcomes
satelle migrate                         # dry-run: names the graphs it will retire
satelle migrate --yes                   # LAST — removes them, now that a route resolves
satelle validate                        # green
```

`satelle workflow show <category>` is the check that matters, and `satelle story
route <id>` is the same view for a story that already exists. Read either one
**against the retired graph, gate by gate**: for every gate the graph declared,
find its counterpart in the route, and account for any that has none. Do it for
each category the old graphs claimed, not just one — a shared step catalogue
means a gate can be right for one lane and missing from another.

A gate that quietly disappeared is the one failure this representation must not
have, and this diff is the only thing that catches it.

## Already on `done.md` + `step.md`? The md-to-toml mapping

A repo that converted from DOT earlier has a working route in the retired
MARKDOWN spelling. satelle refuses it by name — *"the route source is TOML now;
rename to done.toml and step.toml"* — because feeding markdown to a TOML decoder
produces a parse error, and a route that does not parse is a repo with no gates.

This conversion is mechanical: the same records, the same keys, a different
syntax. Rename both files to `.toml` and translate:

| In the markdown | In TOML |
| --- | --- |
| `---` frontmatter block | a `[meta]` table (no closing marker) |
| `name: done` | `name = "done"` |
| `create_review: <skill>` | `create_review = "<skill>"`, in `[meta]` |
| a `hooks:` block list | one `[[meta.hooks]]` table per hook, same keys |
| `## <category>` in done.md | `[<category>]` — quote it if it is `*` or has a dot |
| the `- <obligation>` list under it | `obligations = ["a", "b", "c"]`, same order |
| `park: <state> @<gate>` | `park = { state = "<state>", gate = "<gate>" }` |
| `advise <agent> @<skill>` on `park:` | `advisor = "<agent>", advisor_skill = "<skill>"` in the same inline table |
| `cancel: <state> @<gate>` | `cancel = { state = "<state>", gate = "<gate>" }` |
| `recover: <step> from a, b` | `recover = { step = "<step>", from = ["a", "b"] }` |
| `+ <tag> <obligation>` | a `[[<category>.tag_obligation]]` with `tag` and `obligation` |
| `## <name>` in step.md | `[<obligation>]` — **the KEY is what the step `provides:`**, and the old heading becomes `status = "<name>"` |
| `provides: <obligation>` | gone — it is the table key now |
| `requires: a, b` | `requires = ["a", "b"]` |
| `skills: a, b` / `reviewers: a, b` | `skills = ["a", "b"]` / `reviewers = ["a", "b"]` |
| `parallel: 0` | `parallel = 0` (a number, not a string) |
| `start: true` / `terminal: true` | `start = true` / `terminal = true` |
| `advise <agent> @<skill>` on a step | `advise = { agent = "<agent>", skill = "<skill>" }` |
| `## gate <skill>` | a `[[gate]]` table with `skill = "<skill>"` |
| `on: a, b` / `for: *` on a gate | `on = ["a", "b"]` / `for = ["*"]` |
| `<!-- comment -->` | `# comment` — and it may now span lines freely |

The step table's key is the only part that is not a straight transcription, and
it is the part worth checking twice: markdown keyed a step by its HEADING (the
stage name) and named its obligation in `provides:`. TOML keys it by the
OBLIGATION, because that is unique — stage names repeat, and two steps sharing
one heading is why the old spelling needed a separate `provides:` at all.

The prose the old format needed is not part of the conversion. Both halves
carried a "HOW TO READ THIS FILE" preamble teaching a grammar that existed
nowhere else, plus a one-line-comments-only convention a real parser makes
unnecessary. Delete them. Keep only the comments that say why THIS repo's route
is shaped the way it is.

Verify exactly as above — `satelle workflow show <category>` for every category,
read against the markdown you started from — before deleting the old files.
Nothing else changed: the derived route the binary builds from the TOML must be
identical, gate for gate.

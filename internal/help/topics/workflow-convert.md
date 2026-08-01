# Converting a DOT workflow to done.md + step.md

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

**`done.md` — what DONE means.** One `## <category>` section per story category,
each an ordered list of obligations, plus the role states the binary synthesises.
`## *` governs any category with no section of its own.

```
## *
- raised
- coded
- closed
park: blocked @satelle-story-blocked-review advise blocked-triage @satelle-story-blocked-triage
cancel: cancelled @satelle-story-cancel-review
recover: in_progress
+ surface:ui design-reviewed
```

- `park:` / `cancel:` are `<state> @<gate-skill>`; the `@gate` is optional but
  omitting it means that exit is ungated.
- `advise <agent> @<skill>` names an advisor the ORCHESTRATOR consults on that
  state. It is a declaration, never a dispatch.
- `recover: <step>` allows backward movement to `<step>`. Name only steps the
  route actually declares — `recover: x from a, b` emits edges from `a` and `b`
  verbatim, so a stale name becomes an edge from a state that does not exist.
- `+ <tag> <obligation>` appends an obligation when the story carries the tag.

**`step.md` — what discharges each obligation.** One `## <name>` section per step
and one `## gate <skill>` per always-on gate.

```
## backlog
start: true
provides: raised

## in_progress
agent: executor
skills: code
reviewers: satelle-story-plan-review, satelle-story-architecture-review
reviewer_agent: reviewer
parallel: 0
provides: coded
requires: raised

## done
reviewers: satelle-story-done-review
terminal: true
provides: closed
requires: coded

## gate satelle-estimate-actual-review
on: in_progress, done
for: *
```

Step keys: `agent`, `skills`, `reviewers`, `reviewer_agent`, `parallel`,
`provides`, `requires`, `applies_to`, `advise`, `start`, `terminal`.
Gate keys: `agent`, `on`, `applies_to`, `for`, `mandatory`.

Both files carry ordinary frontmatter (`name`, `type: workflow`, `scope`,
`description`) and **must not carry `applies_to`** — done.md's sections are the
selector, and a second one would be a second precedence rule.

## Translating the graph you have

Read the retired `.satelle/workflows/*.md` graphs. For each one:

| In the DOT | In the route |
| --- | --- |
| a node with `agent=executor` (or a named agent) | a `## <name>` step with that `agent:` and its `prompt="@skill:x"` as `skills: x` |
| the gate on the edge **into** a node | that step's `reviewers:` — gates belong to the step they admit, not to an edge |
| `agent=` on a gated edge | the step's `reviewer_agent:` |
| `parallel=N` on an edge | `parallel: N` on the target step |
| an edge-less node with `on="a,b"` | `## gate <skill>` with `on: a, b` |
| `applies_to="surface:ui"` on a scoped node | `applies_to: surface:ui` on the gate |
| `mandatory=true` (e.g. the step-summary node) | `mandatory: true` on the gate |
| `shape=Mdiamond` | `start: true` |
| `shape=Msquare` on the success terminal | `terminal: true` |
| a park node (`from="*"`) | done.md's `park:` line |
| a cancel sink | done.md's `cancel:` line |
| `create_review:` / `hooks:` frontmatter | the same frontmatter, on **done.md** |

Then give every spine step a `provides:` obligation and a `requires:` naming the
previous one, and list those obligations in done.md in order.

**Do not author topology.** The binary owns ORDER (a topological sort of
`requires`/`provides`) and the shape (cancel from every non-terminal step, park
from anywhere, backward movement, park→cancel). Every `-> cancelled`,
`-> blocked` and back-edge in the old graph is synthesised — writing them as
steps is the most common conversion mistake.

**A category-specific workflow becomes a SECTION, not a second file.** If the
repo had `satelle-parent-workflow.md` with `applies_to: ["epic-parent","parent"]`,
that becomes `## epic-parent` and `## parent` sections in the one done.md,
selecting from the one shared catalogue. Give each lane its own obligation names
where its steps differ, or two lanes will select the same step and collide.

Gates in a shared catalogue need `for:` — the categories whose route they belong
to. Without it a deployment gate on `done` fires on every lane, including the
ones with no release to verify.

## Verify, then retire the graphs

```bash
satelle workflow validate done          # and: satelle workflow validate step
satelle workflow list --category <cat>  # heads with done.md+step.md, active
satelle story route <story-id>          # the ordered steps, gates and outcomes
satelle migrate                         # dry-run: names the graphs it will retire
satelle migrate --yes                   # removes them, now that a route resolves
satelle validate                        # green
```

`satelle story route` is the check that matters: read it against the graph you
converted and confirm every gate survived. A gate that quietly disappeared is the
one failure this representation must not have.

## The shipped default

The binary ships a route (`backlog → in_progress → done`, plus container and
task-run sections). A repo with no `.satelle/workflows/done.md` inherits it; a
repo that authors one overrides it wholly, so an override must re-declare every
section it wants — including `execution`/`task` if the repo runs tasks.

`satelle substrate edit workflows done` materialises the shipped halves into the
repo as a starting point. Editing from them is easier than starting blank, and it
is what `satelle rebase` redeploys.

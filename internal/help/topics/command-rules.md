# Command rules — a repo limits the shell commands its agents run

A repo can deny or warn on an agent's shell command by pattern, and lift that
for chosen workflow steps. satelle supplies the mechanism and ships **no rule
and no default list**: with nothing authored, nothing is refused or warned.

```toml
[[gate.command_rule]]
pattern = "TOOL delete"          # an argv prefix; a token of * matches any one token
action  = "deny"                 # "deny" refuses before it runs; "warn" lets it run
allow   = ["in_progress"]        # story statuses or step names where it passes
message = "delete changes shared state"
```

The first rule that matches any simple command in the line, in file order, wins.
`satelle validate` rejects a malformed rule, and an `allow` entry that names no
state of the route.

## What happens

- **deny** — refused before it runs, with the rule's message and the statuses to
  engage at. A deny rule with no `allow` list refuses in every status, release
  included, whatever story is engaged.
- **warn** — the command runs, and the agent is told the message where its
  harness can carry that. `satelle help agent-dispatch` has the per-harness
  table (the PreToolUse warn column); a harness that cannot deliver it is named
  there as unavailable, and the command still runs.
- **allowed** — a story engaged at a listed status or step passes the command.
- A command a rule matched and allowed, or warned on, is a `command_rule` row on
  the engaged story's ledger: the command, the rule and the story's status.

Rules apply in every session that runs satelle's command hook: the driving
session, dispatched agents and rework relays, and a fresh clone with this
repo's synced `satelle.toml`.

If the rules cannot be loaded (a malformed rule, or a config that does not
parse), every shell command is refused with the file, rule and field, rather
than silently dropping the denies.

## What it is not

satelle judges only the command line it sees, with the same best-effort
classifier the commit and containment gates use: wrappers, `eval`, aliases and
scripts bypass a rule. It does not know what a command does, which account it
runs as, or what that account may do. **satelle manages no identity or rights**:
the tool's own permissions and the account it runs as enforce what a command may
do.

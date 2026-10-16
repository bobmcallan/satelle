# Command rules — a repo limits the shell commands its agents run

A repo can deny or warn on an agent's shell command by pattern, and lift that
for chosen workflow steps. satelle supplies the mechanism and ships **no rule
and no default list**: with nothing authored, nothing is refused or warned.

```toml
[[gate.command_rule]]
pattern = "TOOL delete"          # an argv prefix; see Pattern syntax below
action  = "deny"                 # "deny" refuses before it runs; "warn" lets it run
allow   = ["in_progress"]        # story statuses or step names where it passes
message = "delete changes shared state"
```

The first rule that matches any simple command in the line, in file order, wins.
`satelle validate` rejects a malformed rule, and an `allow` entry that names no
state of the route.

## Pattern syntax

A pattern is whitespace-separated tokens compared with the command's argv. It
matches when its tokens are a **prefix** of the argv: arguments after the last
token are ignored. Examples use the fictional tools `orbctl` and `fetchy`.

| Token | Matches |
| --- | --- |
| `word` | a token equal to `word` |
| `*` | any one token |
| `**` | any run of tokens, none included |
| `**/NAME` | first token only: a program whose last path element is `NAME`, however it is invoked |
| `*TEXT*` | one token containing `TEXT`, such as a host inside a URL or `--url=...` |

```toml
pattern = "orbctl scale * --force"           # exact words, * for one token
pattern = "**/orbctl"                        # orbctl, /opt/x/orbctl, ./orbctl, ~/bin/orbctl, $HOME/bin/orbctl
pattern = "**/fetchy ** *api.example.test*"  # fetchy with that host at any argument position
```

`**/NAME` compares text only: no PATH lookup, no symlink resolution, no variable
expansion. `$HOME/bin/orbctl` matches because its text ends in `/orbctl`; a
variable that holds the whole program (`$TOOL`) does not. A token holding `*`
that is none of these forms (`a*`, `*a`, `***`, `*a*b*`), or `**/NAME` anywhere
but first, is a malformed pattern; `satelle validate` names the rule and the
field `pattern`, never the value.

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

## Subscribing to a shared list

Rather than write every rule, a repo can subscribe to a list published on the
satelle.dev catalog or by a third party at a URL. satelle publishes no list: a
repo that does not subscribe has none, and nothing is fetched.

```toml
[[gate.command_policy]]
name      = "cloud"
workspace = "acme"                 # a team workspace's publish catalog ...
path      = "policy/cloud.toml"    # ... and the path published in it
max_age   = "24h"                  # optional; default 24h

[[gate.command_policy]]
name = "vendor"
url  = "https://example.com/commands.toml"
```

A list holds `[[command_rule]]` entries under the grammar above. Its rules apply
**after** the repo's own, so a repo rule that matches the same command wins, and a
subscribed rule still judges every command no repo rule matches.

Session start reads only the local cache, kept under satelle's per-repo state
directory. When a list is missing or older than its `max_age` it starts one
detached `satelle policy refresh` and does not wait; the command path makes no
network call. A list older than its `max_age` is named unverified in the
session-start warning, with the time it was last fetched. A refresh that fails
(source unreachable, a list that does not parse, a catalog body that does not
match the catalog's checksum) keeps the last good list, and the next session start
names the subscription and the reason. `satelle policy status` shows the cache.

**A URL list is unverified.** A URL gives no checksum or signature, so satelle
cannot check what it serves, and the session-start warning says the list is from
an unverified source. Subscribe to a URL only if you trust whoever serves it. A
catalog list is checked against the sha256 the catalog returns for it.

## What it is not

satelle judges only the command line it sees, with the same best-effort
classifier the commit and containment gates use: wrappers, `eval`, aliases and
scripts bypass a rule. It does not know what a command does, which account it
runs as, or what that account may do. **satelle manages no identity or rights**:
the tool's own permissions and the account it runs as enforce what a command may
do.

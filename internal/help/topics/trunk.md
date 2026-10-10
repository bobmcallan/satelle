# Trunk: checking main against its remote before work starts

satelle repos are usually developed trunk-based, with several machines pushing
to the same branch. When a story or an epic first enters a performing state,
satelle compares local trunk with the remote's, so work begins from the trunk
other machines have already published rather than from a stale copy.

## What the engage prints

One line, to stderr, starting `satelle: trunk`, for every state but a level one.
When the engage runs as an agent session's gate, the line is also part of the
verdict the session is handed:

| State | Line |
| --- | --- |
| behind, moved | `fast-forwarded <trunk> by N commit(s) <old>..<new>` |
| behind, not moved | `behind <remote>/<trunk> by N, not moved: <reason>` |
| ahead | `N unpushed commit(s)` |
| diverged | `diverged: A ahead, B behind` |
| dirty | `dirty tree on <trunk>` |
| offline | `fetch from <remote> failed (proceeding): <git error>` |

An existing story's engage also writes a `trunk_check` row with the same text to
its ledger (`satelle ledger list --story <id>`). A level trunk, and a directory
with no remote to compare, print and write nothing.

The trunk is the branch the remote's HEAD ref names. The check never merges,
rebases, resets, stashes or commits: a behind trunk is advanced with
`merge --ff-only`, and only when the invoking working tree has trunk checked out
and clean. From a linked worktree it reports and does not move trunk.

## What stops an engage

A dirty or diverged trunk would only surface later as a rejected push, so by
default those two refuse the engage after the line is printed; the story stays
where it was. The repo declares otherwise in `satelle.toml`:

```toml
[trunk]
check  = true                  # false switches the check off
refuse = ["dirty", "diverged"] # any of dirty, diverged, behind, ahead, offline
```

An absent table means the check is on with that refuse list. A behind trunk that
was fast-forwarded counts as resolved, so `behind` refuses only when it could not
be moved. Offline proceeds unless it is listed.

## Publishing onto a trunk other machines have moved

```
satelle trunk publish [--story id] [--prove cmd] [--stamp cmd] [--rounds n] [--json]
```

is the mechanism a release invokes in place of a bare `git push`. Each round it
fetches the remote's trunk and merges in what other machines pushed (a merge, not
a rebase, so no release commit is rewritten and an epic's child merges stay in
the pushed history), runs the `stamp` command, runs the `prove` command on the
combined head, and pushes that head with a plain push. A push refused because the
trunk moved again puts the tree back at the release head and starts another round,
up to `publish_rounds`; the bound spent, it stops with
`<remote>/<trunk> moved N times; not pushed (bound N)`. No push carries a force
option or a forced refspec.

```toml
[trunk]
prove          = "go test ./..."   # required: runs on the combined head, no default
stamp          = "./bump.sh"       # optional: commits the version bump, computed from the integrated tree
publish_rounds = 5                 # most rounds a refused push is answered with
```

`stamp` runs after the remote's commits are in, so a bump never conflicts with
another machine's; a refused push drops it and the next round computes it again.
With nothing new on the remote the publish pushes once, adds no commit, runs the
proof once, and the pushed head is the local release head.

A merge conflict, a failed `stamp` or `prove`, or a spent bound exits non-zero with
local trunk back at the release head and the remote untouched. On success the line
`satelle: trunk published <sha> (combined <sha>, rounds N, incoming K, proved by
<cmd>)` is printed, and with `--story` also written to the story's ledger as a
`trunk_publish` row carrying the pushed and combined heads.

## Running it by hand

```
satelle trunk sync [--fast-forward] [--json] [--remote r] [--trunk-branch b] [--strict] [--refuse s,...]
```

reports the same state for the invoking tree and exits 0 once a state is
reported. It is the operator's entry point to the same unit the engage runs.

## Cutting a worktree and merging an epic from main as it is on the remote

An epic's children are cut from the trunk and run for hours; the container then
merges them. Both points start from the trunk brought level with the remote, so
neither works from a stale local copy:

- `satelle story worktree <id> --base <trunk>` runs this check with fast-forward
  first (see `satelle help worktree`) and cuts from the updated local trunk.
- `satelle trunk sync --fast-forward --strict` is what the container's merge step
  runs before it merges a child. It prints what came in and exits non-zero when
  the trunk is in a state the stop set names.

Level: the cut prints nothing and `--strict` prints the level line and exits 0.

**The stop set** is `[trunk] base_refuse`, or `--refuse` on `trunk sync`. Its
default is `dirty, diverged, ahead, behind, offline, unresolved`, where `behind`
means behind and not fast-forwarded (main is checked out in another worktree, so
it was not moved). In a stopping state both points stop before changing anything:
no branch or worktree is created and local main is unchanged. The message names
the state, the ahead/behind counts where relevant, and the way out (for ahead,
push first). It is wider than the engage-time `[trunk] refuse` on purpose: an
engage only reads the trunk, so an offline remote proceeds there; a cut or merge
writes new history from its base, so a base not shown to be level stops.

```toml
[trunk]
base_refuse = ["dirty", "diverged"]  # any of dirty, diverged, ahead, behind, offline, unresolved
branch      = "main"                 # the trunk's name when the remote's HEAD ref does not give one
```

**When the remote's HEAD ref is missing,** both points first run
`git remote set-head <remote> --auto`, which writes only the remote-tracking
symref. If the trunk then resolves it is checked as above; if the remote cannot
be reached the state is `offline`. If it still does not resolve and no hint is
given, satelle cannot tell the trunk from a dependency branch, so the state is
`unresolved`: `story worktree` stops for every base with

```
satelle: trunk unresolved — cannot tell whether <ref> is the trunk; pass --trunk-branch <name> or run git remote set-head <remote> --auto
```

and `trunk sync --strict` stops the same way. `--trunk-branch` (or `[trunk] branch`)
is the hint: the hinted branch, in any of its four spellings (`main`,
`refs/heads/main`, `origin/main`, `refs/remotes/origin/main`), is then the trunk
and is synced, and any other base is cut as named. On `story worktree` the
`--branch` flag names the new worktree branch, never the trunk. `trunk sync`
keeps `--branch` as a deprecated alias of `--trunk-branch`.

Listing a state out of the stop set lets it through with the line printed. A
machine already level, a base that is not the trunk, `--existing`,
`[trunk] check = false` and a repo with no remote behave as they did before.

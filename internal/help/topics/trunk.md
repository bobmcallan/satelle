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

## Running it by hand

```
satelle trunk sync [--fast-forward] [--json] [--remote r] [--branch b]
```

reports the same state for the invoking tree and exits 0 once a state is
reported. It is the operator's entry point to the same unit the engage runs.

# satelle session — naming an agent session

Every interactive agent session is named from what satelle knows about where it
runs, so the operator can tell sessions apart in the harness's own list (Claude
Code's `claude agents` / Remote Control) and in the hold records other machines
see.

```
satelle session name [--harness <h>]
```

prints the one identity:

```
<short host> · <repo name>[ · <worktree dir>][ · <story id>]
```

- the short host is the hostname up to its first dot;
- the repo name is the same for a worktree and its main tree;
- the worktree directory appears only for a linked worktree;
- the story id appears only when this tree holds a live engagement seat, and
  once: a worktree directory already named after the engaged story carries
  the id as its worktree segment alone.

The same host · repo (· worktree) form is the label a checkout registers as its
location with a hosted server, so a hold owned by another machine names it.

## Naming at launch — the satelle launcher

The name is set at launch, by the launcher that `satelle agents install` writes:

```
satelle agents install claude      # writes $SATELLE_HOME/agents/bin/satelle-claude
```

`$SATELLE_HOME` is `~/.satelle` unless set, so the launcher is
`~/.satelle/agents/bin/satelle-claude`. Put its directory on `PATH` and start
`satelle-claude` where you would start `claude`:

```
export PATH="$HOME/.satelle/agents/bin:$PATH"
satelle-claude
```

The launcher passes `--name "$(satelle session name)"` to claude. It adds nothing
when you name the session yourself (`--name`, `--name=…`, `-n`), when the name is
empty, or when satelle fails — it then runs the bare harness. `satelle init`
rewrites an installed, satelle-marked launcher that is out of date, and leaves
one you wrote yourself alone.

The name is fixed at launch: a session does not gain its story afterwards.

## Harnesses without a launch name flag

A harness whose CLI has no session-name flag (grok, cursor) keeps its launcher
unchanged, and `satelle session name --harness <h>` prints `title: unavailable`
naming it. pi has a name flag (`--name`, `-n`) but no satelle launcher, so name a
pi session by passing `--name "$(satelle session name)"` yourself. The flags each
harness declares live in `internal/agentcli` (`launcherNameFlags`).

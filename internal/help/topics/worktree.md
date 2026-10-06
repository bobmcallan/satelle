# Worktrees: carrying the gitignored files a tree needs

`git worktree add` gives a new tree only tracked content. A repo's gitignored
files — harness wiring, local tool config, env files — are missing from it, and
every repo ignores something different. `satelle story worktree <id> --base <ref>`
opens the worktree a story is engaged from and carries those files into it.

## The declaration

The repo declares what a worktree needs in the `[worktree]` table of
`satelle.toml`. satelle ships no value for any key.

```toml
[worktree]
include = [".env", ".tool-config"]   # repo-relative paths, files or directories
branch  = "work/{id}"                # branch template; {id} is the story id
path    = "../trees/{id}"            # location template; relative = from the main tree
```

- `include` names **paths**, never a tool. Each must be gitignored in the main
  tree. A path the main tree does not have is skipped and reported by name.
- `branch` and `path` are templates; `{id}` is the only placeholder and each must
  contain it. With no template, the matching `--branch` / `--path` flag is
  required. A flag always overrides its template.
- Changing the declaration changes the next worktree, with no code change.

`satelle validate` and every command that loads the config refuse a malformed
declaration, naming the entry: an empty or absolute entry, one that points
outside the repository, one that names (or contains) the data dir or `.git`, a
pattern, a duplicate, or a branch template that is not a valid branch name.
Nothing is rewritten.

## How a path is carried

Each declared path is **linked** from the worktree to the main tree's path — one
source, so a carried path cannot drift and a secret is never duplicated. git
treats a symlink as a file, so a directory-only ignore pattern (`.tool/`) does
not match it; satelle appends an anchored line for each carried path to the
repository's common `info/exclude` (git has no per-worktree exclude file) and
then checks that the worktree ignores it. A path it cannot prove ignored is
reported and the command exits non-zero, so nothing is staged by accident. The
data dir is never carried, and nothing outside the repository is.

## The base

`--base <ref>` is required; satelle never defaults to HEAD. For an independent
child of an epic it is the epic's base branch. For a child whose `depends-on`
target is done and already on trunk, or done with no branch of its own, it is
trunk. For a child whose done target is not yet on trunk it is that target's
branch, so the dependent tree holds the change it was written against. The branch
is new: an existing branch is refused, never reused or reset.

## An existing worktree

`satelle story worktree <id> --existing <path>` applies the declaration to a
worktree that already exists (it must belong to the same repository and is
refused if it is the main tree). It creates no branch and is safe to repeat: an
already-linked path is reported as `already carried`, and a real file or
directory found where a link belongs is left as is and reported. To replace a
hand-copied folder, delete the copy yourself and run the command again.

## A performer in a worktree without its gate wiring

A performer dispatched into a linked worktree runs there, so the harness's edit
and commit gates must be wired into that tree. A worktree satelle did not
prepare (a manual `git worktree add`, a tree from before the declaration) may
lack them, and a performer would then run ungoverned. satelle checks first. The
check covers a named step performer and a driving-role live session (the
`satelle story rework` coder); reviewer verdicts are read-only and not covered,
and the main tree is left to drift detection.

```toml
[worktree]
absent_wiring = "fail-open"          # refuse (default) | fail-open

[harness.mybot]
gate_wiring = [".mybot/hooks.json"]  # tree-relative paths this harness needs
```

- A dispatch whose harness wiring is absent is **refused** before it starts,
  naming the harness and each missing path. This is the default.
- `absent_wiring = "fail-open"` lets it run instead. satelle then says so on every
  dispatch it permits: an `UNGATED` warning naming the policy, and an
  `ungated_dispatch` ledger row. An ungated run is visible even when allowed.
- `gate_wiring` is declared per harness. Embedded defaults cover claude
  (`.claude/settings.json`), grok (`.grok/hooks/satelle.json`) and pi
  (`.pi/extensions/satelle.ts`); a repo's table overrides them. A harness with
  none declared is treated as having absent wiring.
- A present wiring file is also scanned for the hook wrapper script it calls
  (`.satelle/hooks/satelle-hook.sh`, comment lines ignored). That script must
  exist where the call points; a missing one counts as missing wiring.
- The harness comes from the binding's command: claude, grok, pi, otherwise
  `unknown`. An unrecognised command is `unknown`, which declares no wiring, so it
  is refused (naming the executable) unless the repo declares
  `[harness.unknown] gate_wiring`. pi is recognised here but stays unrecognised
  for reviewer tool isolation.
- Both keys are read from the **main tree's** configuration. A policy or wiring
  declared only in a worktree's own `satelle.toml` changes nothing, so a worktree
  cannot exempt itself.

Loading refuses an unknown `absent_wiring` value, an unknown key under
`[worktree]` or `[harness.<name>]`, and a malformed `gate_wiring` entry (the same
path rules as `include`), naming the field. Nothing is rewritten.

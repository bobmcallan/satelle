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

`--base <ref>` is required; satelle never defaults to HEAD. For a child of an
epic it is the epic's base branch; for a child whose `depends-on` target is done
it is that target's branch, so the dependent tree holds the change it was written
against. The branch is new: an existing branch is refused, never reused or reset.

## An existing worktree

`satelle story worktree <id> --existing <path>` applies the declaration to a
worktree that already exists (it must belong to the same repository and is
refused if it is the main tree). It creates no branch and is safe to repeat: an
already-linked path is reported as `already carried`, and a real file or
directory found where a link belongs is left as is and reported. To replace a
hand-copied folder, delete the copy yourself and run the command again.

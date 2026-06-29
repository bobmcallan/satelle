# Legacy `.satellites/` directory — cause and prevention

Story `sty_54df7b31`. The pre-rebrand `.satellites/` state directory kept
reappearing in the satelle repo. This records why and how it is now prevented.

## Root cause

The pre-rebrand (V5) CLI binary `satellites` is still installed on this machine
at `/home/bobmc/.local/bin/satellites`. When that binary is invoked with the
satelle repo as the working directory, it auto-initializes a workspace state dir
at `./.satellites/` (a `state.db`, and historically `index.db`/`logs/`) — the old
satellites local state, written into the V6 repo.

Observed: `.satellites/state.db` reappeared with an mtime matching the start of a
session in which the legacy `satellites` CLI was invoked manually (while locating
the backlog, before realising satelle uses its own `./satelle` binary and
`.satelle/` store).

### Trigger enumeration

- **Manual invocation of the legacy `satellites` CLI in this repo** — the actual
  trigger. The binary creates `./.satellites/` on use.
- **satelle automation — ruled out.** No satelle code, hook, or skill shells out
  to the legacy `satellites` CLI. The `.claude/settings.json` hooks all invoke
  `satelle` (`satelle index`, `satelle hook context|gate|commitgate`). A
  word-boundary grep across the repo (`*.go|*.sh|*.md|*.toml|*.json|*.yml`) finds
  only **documentation** references to `satellites` (the rebrand/porting history)
  — none invoke the binary.
- **claude.ai Satellites MCP — unrelated.** That is a remote MCP server, not this
  local binary; it does not write `./.satellites/`.

## Prevention (in effect)

1. **Ignored.** `/.satellites/` is in `.gitignore`, so a stray legacy dir can
   never be staged or committed (`git check-ignore .satellites` → matches).
2. **Removed.** The stray `./.satellites/` working-tree dir is deleted and is not
   tracked by git.
3. **No automation reintroduces it.** Confirmed above — only a manual legacy-CLI
   invocation can recreate it, and it would land in the ignored path.

## The legacy binary (operator action)

satelle (V6) fully replaces the V5 `satellites` CLI for this repo, so the legacy
binary is not needed here. It is intentionally **not** auto-removed by this story:
deleting a machine-wide binary the operator installed is outside this repo's scope
and may affect other repos/tooling. To retire it, the operator can run:

```sh
rm -f ~/.local/bin/satellites
```

Until then, the `.gitignore` rule keeps any stray `.satellites/` harmless — it
cannot enter version control. Do not run the legacy `satellites` CLI inside this
repo; use `./satelle` (or the PATH `satelle`).

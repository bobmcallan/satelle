# Reviewer capability — scoped CLI access + principle injection

Story `sty_e15c15a4`. satelle's isolated reviewers were too restrictive to do
their job well: they ran read-only over the working **tree** only, with no way to
see the substrate they reason about.

## What was wrong

The reviewer harness was:

```
claude -p --disallowedTools Write,Edit,Bash,NotebookEdit \
  --append-system-prompt {system} --allowedTools Read,Grep,Glob --model {model}
```

and a reviewer received **only** its rubric (`{system}`) + the work item on stdin.
Two consequences:

- **Embedded substrate was invisible.** Canonical skills and principles ship
  *embedded in the binary* — they are not files on disk. With `Bash` denied, a
  reviewer could neither `Grep` them nor run `satelle` to resolve them. So a
  reviewer asked "does `@skill:commit-push` resolve?" could not actually tell.
- **Principle-blindness.** Nothing injected principles into the reviewer. Yet
  reviewer rubrics cite principles by name (`satelle-repo-agnostic`,
  `satelle-reviewer-self-contained`) the isolated reviewer had no way to read —
  defeating much of those rubrics' reasoning.

## The change — treat the reviewer like a read-only executor

1. **Scoped, read-only `satelle` CLI access.** `Bash` is dropped from the deny
   ceiling and the reviewer's tool grant is `Read,Grep,Glob,Bash(satelle:*)` —
   `Bash` scoped to the `satelle` binary. A reviewer can now resolve any skill or
   principle via the CLI (`satelle doc get skills <name>`, `satelle doc get
   principles <name>`, `satelle doc list`), **including embedded defaults**. The
   work-tree mutators (`Write`, `Edit`, `NotebookEdit`) stay denied, so the
   read-only invariant holds: a reviewer judges, it never modifies the repo.
2. **Principle injection.** Every reviewer's system prompt is assembled by
   `reviewerSystemPrompt`: the always-resident principles (`principles:always`,
   the same set the executor receives at SessionStart) + a read-only
   call-to-action + the reviewer's own rubric.
3. **Call-to-action.** The injected preamble tells the reviewer it has read-only
   `satelle` CLI access and must resolve referenced substrate via the CLI rather
   than assuming absence — an embedded default resolves even when no file exists
   under `.satelle/`.

## Why this matters

It is the foundation for `satelle-workflow-review` to verify that every gate a
workflow references is *actionable* (resolvable) before a story is engaged — the
reviewer can now check, via the CLI, against the full substrate (embedded ∪
project), not just the files on disk.

## Security note

`Bash(satelle:*)` scopes the shell to the `satelle` binary; it is not a general
shell, and the mutator denylist is a ceiling (deny wins over allow). Prefix
scoping is not airtight against crafted command chains, so `satelle` subcommands
invoked by a reviewer must themselves stay read-only (the `doc`/`principle`/
`workflow list` surfaces are).

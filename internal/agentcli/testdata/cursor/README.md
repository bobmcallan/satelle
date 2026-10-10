# cursor-agent captures

Real runs of `cursor-agent` 2026.10.01-e373342, captured 2026-10-10 on linux by
the driving session of sty_383ff068 (probe scripts and raw output were kept in
that session's scratchpad). They are evidence for the cursor stories under epic
sty_ba302c1b and are read by `cursor_fixtures_test.go`; no adapter code uses them
yet.

Every run has a `<run>.meta.json` recording the cursor-agent version, the exact
argv and the capture date. A run's `.out` is its stdout, `.err` its stderr plus
a final `exit=N` line, `.fs` the post-run presence of `written.txt` and
`shell.txt` in the sandbox.

## Normalisation applied to every file

Everything is REAL verbatim except:

- `user_email` and any email address -> `redacted@example.invalid`.
- The scratchpad sandbox path -> `/SCRATCH` (hook log lines are truncated to 600
  characters by the hook script, so a path can end mid-way; the remainder is also
  `/SCRATCH`). Cursor's dash-encoded form of it in `transcript_path` ->
  `SCRATCH-ENCODED`. The user's home directory -> `/HOME`.
- No credential value appears in any capture (`apiKeySource` is only the string
  `login`); nothing needed `REDACTED`.

## Other deviations from the raw capture

- `3-abs.meta.json`, `3-user.meta.json`, `6.meta.json`, `clean-6.meta.json`: the
  raw meta had no `argv`; it was added from the probe script that ran the command.
- `5-acp.meta.json`: lifted from the `cursor_agent_version`, `date` and `argv`
  header of `5-acp.json` (the ACP probe wrote them there, not to a meta file).
- `8b-stdin-plus-arg.out`: the re-run (exit 0). The file ends in a stray second
  line left by the earlier, longer run; read only its first line.
- `1c`, `8a`, `8b` metas record `argv` as the piped shell command line (a
  string or a one-element array), as the raw probe wrote it.
- `clean-6*` are the raw `out-clean/6*` files (run with the parent's Claude Code
  environment removed); `6*` are the raw `out/6*` files (inherited environment).

## Captures

| Question | Run | Files |
|---|---|---|
| json output | 1a | 1a-json.out, 1a-json.err, 1a-json.meta.json |
| stream-json output | 1b | 1b-stream.out, 1b-stream.err, 1b-stream.meta.json |
| prompt on stdin | 1c | 1c-stdin.out, 1c-stdin.err, 1c-stdin.meta.json |
| instructions via AGENTS.md | 1d | 1d-agentsmd.out, 1d-agentsmd.err, 1d-agentsmd.meta.json |
| instructions via .cursor/rules | 1e | 1e-rules.out, 1e-rules.err, 1e-rules.meta.json |
| failed run (unknown model) | 1g | 1g-fail.out, 1g-fail.err, 1g-fail.meta.json |
| `--mode plan` write attempt | 2a | 2a-plan.out, 2a-plan.err, 2a-plan.fs, 2a-plan.meta.json |
| `--mode ask` write attempt | 2b | 2b-ask.out, 2b-ask.err, 2b-ask.fs, 2b-ask.meta.json |
| deny rules in cli.json | 2c | 2c-cli.json, 2c-deny.out, 2c-deny.err, 2c-deny.fs, 2c-deny.meta.json |
| unrestricted control | 2d | 2d-control.out, 2d-control.err, 2d-control.fs, 2d-control.meta.json |
| hooks, relative command path | 2e | 2e-hooks.json, 2e-hook.sh, 2e-hooks.log, 2e-hooks.out, 2e-hooks.err, 2e-hooks.fs, 2e-hooks.meta.json |
| hooks, absolute command path | 3-abs | 3-abs-hooks.json, 3-abs-hook.sh, 3-abs-hooks.log, 3-abs.out, 3-abs.err, 3-abs.fs, 3-abs.meta.json |
| hooks, user-level hooks.json | 3-user | 3-user-hooks.json, 3-user-hook.sh, 3-user-hooks.log, 3-user.out, 3-user.err, 3-user.fs, 3-user.meta.json |
| hooks, allow preToolUse / deny beforeShellExecution | 4 | 4-hooks.json, 4-hook.sh, 4-hooks.log, 4.out, 4.err, 4.fs, 4.meta.json |
| ACP transcript | 5 | 5-acp.json, 5-acp.meta.json |
| environment, inherited | 6 | 6.out, 6.err, 6.meta.json, 6-hook.sh, 6-hook-env.txt, 6-hook-env-names.txt |
| environment, clean | clean-6 | clean-6.out, clean-6.err, clean-6.meta.json, clean-6-hook.sh, clean-6-hook-env.txt, clean-6-hook-env-names.txt |
| interactive hooks, deny | 7-deny | 7-deny-hooks.json, 7-deny-hook.sh, 7-deny-hooks.log, 7-deny.tty.log, 7-deny.fs, 7-deny.meta.json |
| interactive hooks, stop followup | 7-stop | 7-stop-hooks.json, 7-stop-hook.sh, 7-stop-hooks.log, 7-stop.tty.log, 7-stop.fs, 7-stop.meta.json |
| stdin carries the instruction | 8a | 8a-stdin-codeword.out, 8a-stdin-codeword.err, 8a-stdin-codeword.meta.json |
| stdin plus argument | 8b | 8b-stdin-plus-arg.out, 8b-stdin-plus-arg.err, 8b-stdin-plus-arg.meta.json |

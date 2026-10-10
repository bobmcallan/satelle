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
- 10a, 11a and 12-*: the scratch directory (and its dash-encoded form) are
  replaced the same way, so `hooks.json` command paths and the sandbox `cwd`
  read `/SCRATCH/...`.
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
| preToolUse deny `{"permission":"deny","reason":…}` on a delete (blocks; reason hidden) | 10a | 10a-hooks.json, 10a-hook.sh, 10a-hooks.log, 10a.out, 10a.err, 10a.fs, 10a.meta.json |
| the same deny, second run | 11a | 11a-hooks.json, 11a-hook.sh, 11a-hooks.log, 11a.out, 11a.err, 11a.fs, 11a.meta.json |
| deny with snake_case `user_message`/`agent_message` (blocks; model sees `user_message`) | 12-snake | 12-snake-hooks.json, 12-snake-hook.sh, 12-snake.out, 12-snake.err, 12-snake.fs, 12-snake.meta.json |
| deny with camelCase `userMessage`/`agentMessage` (blocks; reason hidden) | 12-camel | 12-camel-hooks.json, 12-camel-hook.sh, 12-camel.out, 12-camel.err, 12-camel.fs, 12-camel.meta.json |
| deny with only `reason` (blocks; reason hidden) | 12-reason | 12-reason-hooks.json, 12-reason-hook.sh, 12-reason.out, 12-reason.err, 12-reason.fs, 12-reason.meta.json |
| claude's `hookSpecificOutput` deny (blocks; reason visible) | 12-claude | 12-claude-hooks.json, 12-claude-hook.sh, 12-claude.out, 12-claude.err, 12-claude.fs, 12-claude.meta.json |
| stderr reason with exit 2 (blocks; reason visible) | 12-exit2 | 12-exit2-hooks.json, 12-exit2-hook.sh, 12-exit2.out, 12-exit2.err, 12-exit2.fs, 12-exit2.meta.json |
| sessionStart `additional_context` reaches the model (print mode) | 9 | 9-hooks.json, 9-hook.sh, 9-hooks.log, 9-ctx-print.out, 9-ctx-print.meta.json |
| cursor runs a repo's `.claude/settings.json` hooks; Claude-format hook output (settings: Write\|Edit\|Bash matcher) | 10b | 10b-claude-settings.json, 10b-hooks.log, 10b.meta.json, 10b.fs |
| the same, Claude `permissionDecision` deny JSON | 11b | 11b-claude-settings.json, 11b-hooks.log, 11b.meta.json, 11b.fs |
| the same, blocking `exit 2` (the Delete tool escapes the matcher: `other.txt: absent`) | 11c | 11c-claude-settings.json, 11c-hook.sh, 11c-hooks.log, 11c.meta.json, 11c.fs |
| hook command forms cursor runs (PATH lookup, env prefix, absolute, `sh -c`) | 13 | 13-hooks.json, 13-forms.log, 13.meta.json |
| dogfood: installed sessionStart reaches the model (print mode) | 14-context | 14-context.out, 14-context.err, 14-context.meta.json |
| dogfood: installed stop hook's followup re-prompts the agent (interactive pty) | 14-stop | 14-stop.tty.txt, 14-stop.meta.json |
| dogfood: Write refused, no story engaged | 14-refuse-write | 14-refuse-write.out, 14-refuse-write.err, 14-refuse-write.meta.json |
| dogfood: Delete refused, no story engaged | 14-refuse-delete | 14-refuse-delete.out, 14-refuse-delete.err, 14-refuse-delete.meta.json |
| dogfood: Shell `rm` refused, no story engaged | 14-refuse-shell-rm | 14-refuse-shell-rm.out, 14-refuse-shell-rm.err, 14-refuse-shell-rm.meta.json |
| dogfood: Shell `git commit` refused, no story engaged | 14-refuse-git-commit | 14-refuse-git-commit.out, 14-refuse-git-commit.err, 14-refuse-git-commit.meta.json |
| dogfood: Write allowed, story engaged in an executor step | 14-engaged-write | 14-engaged-write.out, 14-engaged-write.err, 14-engaged-write.meta.json |
| dogfood run log | 14 | 14-transcript.md |

Probe 9, 10b, 11b, 11c and 13 (sty_7d098d50) were captured and redacted the same
way as the rest. Their hook logs are the hook script's first 300 (9) or 400 (the
rest) characters per payload, so a long `tool_input` ends mid-value; the tests
read `tool_name` and as much of `tool_input` as survived. The stream-json
`.out`/`.err` of 10b, 11b and 11c are not committed (the file-system result
`.fs` and the hook log carry what the tests pin); 11c's stream recorded a
successful `deleteToolCall` while its Write and Shell calls were refused.

Probe 14 (sty_7d098d50, AC13) is the dogfood of satelle's own cursor wiring, not a
probe with a hand-made `hooks.json`. Each run used a scratch git repo that had had
`satelle init` and `satelle agents install cursor`, so `.cursor/hooks.json` is the
real installed file (reproduced in 14-transcript.md, unmodified). Every child
process ran with Claude's environment removed (every `CLAUDE*` variable and
`AI_AGENT` dropped from cursor-agent's environment); no shim, HOME swap or
`~/.local/bin` edit was used. Before each cursor-agent run the repo held no other
harness wiring, and 14-transcript.md records `absent .claude/settings.json; absent
.grok/hooks/satelle.json; absent .pi/extensions/satelle.ts` ahead of every pass.
The engaged pass set the story and ran cursor-agent under one shared
`SATELLE_SESSION` (`dogfood-cursor-sty_7d098d50`), so cursor's hook processes saw
the seat the story set. Ordering: context, stop (pty), the four refusals, engaged.
Redaction as above (including cursor's own `Co-authored-by` trailer address in
14-refuse-git-commit.out, which cursor-agent appended to the `git commit` it ran);
the dogfood sandbox's `repo-1791624060` directory name is kept.

Deviation: 14-stop.tty.txt is a plain-text rendition of the raw pty log, not the
raw bytes (escape sequences, CRs, BEL and spinner redraws removed). It carries the
whole stop story: the agent's `HI`, the stop hook's `followup_message` queued as a
follow-up, its submission as the next message, and the agent's second turn
`NOTED satelle: STOP BLOCKED … these edits were made UNGATED: scratch.txt.`

Probe 12 (sty_be756616) asked the agent to create `written.txt` under a
preToolUse hook returning each deny variant; the `reason` the model saw is the
`rejected.reason` of the stream-json `tool_call` result. 10a and 11a asked it to
delete `other.txt`. `agentcli.PreToolUseDeny("cursor", …)` encodes the 12-snake
shape; `TestCursorDenyReasonVisible` pins it.

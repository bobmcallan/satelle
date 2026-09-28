# Antigravity (agy) hook payload fixtures (sty_9e88b82f)

**Provenance — these are NOT live captures.** They were written from two real
sources on the author's machine, because the coder grant could not run `agy`:

- agy's own hook contract, `~/.gemini/antigravity-cli/builtin/skills/agy-customizations/docs/hooks.md`
  (camelCase keys; `toolCall.{name,args}`, `conversationId`, `invocationNum`,
  `executionNum`, `terminationReason`, `fullyIdle`).
- real agy session transcripts (`~/.gemini/antigravity-cli/brain/*/.system_generated/logs/transcript.jsonl`),
  which show tool names `write_to_file` (arg `TargetFile`) and `run_command`
  (arg `CommandLine`), with each arg value **JSON-encoded as a string**
  (`"CommandLine":"\"ls -la\""`). The `*_encoded.json` fixtures carry that form;
  the others carry the plain form hooks.md shows. Both must resolve.

`replace_file_content` appears in agy's message logs but no local transcript
records its args; its `TargetFile` arg is taken from the story. Paths and ids
are sanitised.

Replace these with sanitised real hook-stdin captures (agy hook that tees stdin)
once one is taken — sibling story sty_68a24884 (detection) reuses them too.

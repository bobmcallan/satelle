#!/usr/bin/env bash
ev="$1"; in=$(cat)
printf '%s\t%s\n' "$ev" "$(printf '%s' "$in" | tr -d '\n' | head -c 600)" >> "/SCRATCH/out/3-user-hooks.log"
case "$ev" in
  beforeShellExecution) echo '{"permission":"deny","userMessage":"denied by probe","agentMessage":"denied by probe"}';;
  preToolUse) echo '{"permission":"deny","decision":"deny","reason":"denied by probe"}';;
  *) echo '{}';;
esac

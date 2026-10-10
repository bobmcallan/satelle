#!/usr/bin/env bash
ev="$1"; in=$(cat)
printf '%s\t%s\n' "$ev" "$(printf '%s' "$in" | tr -d '\n' | head -c 600)" >> "/SCRATCH/out/2e-hooks.log"
case "$ev" in
  beforeShellExecution|beforeMCPExecution) echo '{"permission":"deny","userMessage":"denied by probe","agentMessage":"denied by probe"}';;
  preToolUse|beforeFileEdit) echo '{"permission":"deny","decision":"deny","reason":"denied by probe"}';;
  stop) echo '{}';;
  *) echo '{}';;
esac

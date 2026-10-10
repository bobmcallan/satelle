#!/usr/bin/env bash
ev="$1"; in=$(cat)
printf '%s\t%s\n' "$ev" "$(printf '%s' "$in" | tr -d '\n' | head -c 700)" >> "/SCRATCH/out/4-hooks.log"
case "$ev" in
  beforeShellExecution) echo '{"permission":"deny","userMessage":"denied by probe","agentMessage":"denied by probe"}';;
  preToolUse) echo '{"permission":"allow"}';;
  stop) if [ ! -e "/SCRATCH/out/4-stop.flag" ]; then touch "/SCRATCH/out/4-stop.flag"; echo '{"followup_message":"Reply with the single word FOLLOWUP-SEEN."}'; else echo '{}'; fi;;
  *) echo '{}';;
esac

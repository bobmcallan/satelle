#!/usr/bin/env bash
ev="$1"; in=$(cat)
printf '%s\t%s\n' "$ev" "$(printf '%s' "$in" | tr -d '\n' | head -c 700)" >> "/SCRATCH/out/7-deny-hooks.log"
case "$ev" in
  preToolUse) echo '{"permission":"deny","decision":"deny","reason":"denied by probe"}';;
  stop) if [ ! -e "/SCRATCH/out/7-deny-stop.flag" ]; then touch "/SCRATCH/out/7-deny-stop.flag"; echo '{"followup_message":"Now reply with the single word FOLLOWUP-SEEN."}'; else echo '{}'; fi;;
  *) echo '{}';;
esac

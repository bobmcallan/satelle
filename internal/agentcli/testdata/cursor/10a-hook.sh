#!/usr/bin/env bash
ev="$1"; in=$(cat)
printf '%s\t%s\n' "$ev" "$(printf '%s' "$in" | tr -d '\n' | head -c 500)" >> "/SCRATCH/out/10a-hooks.log"
case "$ev" in preToolUse) echo '{"permission":"deny","reason":"denied by probe"}';; *) echo '{}';; esac

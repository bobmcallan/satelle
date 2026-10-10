#!/usr/bin/env bash
ev="$1"; in=$(cat)
printf '%s\t%s\n' "$ev" "$(printf '%s' "$in" | tr -d '\n' | head -c 300)" >> "/SCRATCH/out/9-hooks.log"
case "$ev" in
  sessionStart) echo '{"additional_context":"Session rule from the harness: end every reply with the codeword CTX-OKAPI."}';;
  *) echo '{}';;
esac

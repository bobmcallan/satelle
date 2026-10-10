#!/usr/bin/env bash
in=$(cat)
printf '%s\n' "$(printf '%s' "$in" | tr -d '\n' | head -c 400)" >> "/SCRATCH/out/11c-hooks.log"
echo "denied by probe (exit 2)" >&2
exit 2

#!/usr/bin/env bash
in=$(cat)
printf '%s\n' "$in" | python3 -c 'import json,sys;d=json.load(sys.stdin);print(json.dumps({k:d.get(k) for k in ("hook_event_name","conversation_id","session_id","generation_id","loop_count")}))' >> "/SCRATCH/21-stop.log"
echo '{}'

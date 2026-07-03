---
name: satelle-task-validate-before-review
scope: project
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: CODED entry gate for a task EXECUTION (backlog → in_progress). The skill carries a self-contained functional check (no agent run) that reads the transition payload on stdin and judges STRUCTURE deterministically — the run names a parent task header that exists and is structurally valid (frontmatter id, type task, status, a title heading — the same contract as structure.CheckTask / satelle task validate). Whether the work was DONE is not judged here: completion is the close gate's job (satelle-task-validate-after-review, LLM). Repo skill for the satelle executable-task machinery; pushes back when a run is not ready to begin.
---

# Task execution — validate-before (coded begin-run gate)

This is the **coded** entry gate on `backlog → in_progress` for a task
**execution** — one isolated RUN of a task (the task header is a stable authored
work-definition; the run is the item transitioning). The check below IS the
decision — deterministic shell, no LLM (see [[satelle-agent-model]]: mechanism
runs, the gate decides; the decision stays configuration per
[[satelle-constitution]]). It receives the `{story, from, to}` transition
payload on stdin, where `story` is the execution item carrying `parent_id` —
the `tsk_` header it runs.

What it judges — structure only, mirroring `structure.CheckTask`
(`satelle task validate`):

- the execution names a parent task (`parent_id`), and that header file exists
  under `.satelle/tasks/`;
- the header is structurally valid: YAML frontmatter with an `id`, `type: task`
  (OKF), a `status`, and a `# Title` heading in the body.

What it deliberately does NOT judge: whether the task's items were completed —
that is the close gate (`satelle-task-validate-after-review`), and the richness
of the work-definition prose is the author's business. A task may update
code/files; shipping those changes is the operator's, not this workflow's.

```check
# Coded structural entry gate. Reads {story, from, to} on stdin; exit 0
# accepts, non-zero rejects with the reason on stdout.
IN=$(cat)
rest=${IN##*\"to\":\"}; to=${rest%%\"*}
[ "$to" = "in_progress" ] || exit 0
case "$IN" in
  *'"parent_id":"tsk_'*) ;;
  *) echo "execution names no parent task header — create the run with: satelle execution create --parent <tsk_id> ..."; exit 1;;
esac
rest=${IN#*\"parent_id\":\"}; parent=${rest%%\"*}
F=".satelle/tasks/$parent.md"
[ -f "$F" ] || { echo "parent task header $F does not exist — a run executes an authored task"; exit 1; }
head -1 "$F" | grep -q '^---$'   || { echo "$F: missing YAML frontmatter"; exit 1; }
grep -q '^id:' "$F"              || { echo "$F: frontmatter missing id"; exit 1; }
grep -Eq '^type:[[:space:]]*task[[:space:]]*$' "$F" || { echo "$F: frontmatter must have 'type: task' (OKF)"; exit 1; }
grep -q '^status:' "$F"          || { echo "$F: frontmatter missing status"; exit 1; }
grep -q '^# ' "$F"               || { echo "$F: body missing a '# Title' heading"; exit 1; }
exit 0
```

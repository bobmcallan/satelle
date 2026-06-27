# Fixtures — satelle-plan-config-over-code-review

Two reference cases for the `satelle-plan-config-over-code-review` plan gate
(`open → planned`). Each is a `{story, from, to}` input the reviewer receives on
stdin, with the verdict the rubric must return. Verified live against the gate
(see the story ledger for the recorded transitions).

## Case A — ACCEPT (process lands as substrate)

Input:

```json
{
  "from": "open",
  "to": "planned",
  "story": {
    "title": "Author a deploy gate as a reviewer skill under .satelle/skills",
    "body": "Add a deploy-stage gate. Author it as satelle-story-deploy-review.md authored substrate; the binary only runs the gate and records the verdict.",
    "acceptance_criteria": "1. .satelle/skills/satelle-story-deploy-review.md exists.\n2. The binary runs it via the reviewer path; the decision lives in the rubric, not in code."
  }
}
```

Expected verdict:

```json
{"decision": "accept", "notes": ""}
```

Rationale: the gate's decision lands as authored substrate; the binary only
provides mechanism (running the reviewer). Honours configuration-over-code.

## Case B — REJECT (process baked into the binary)

Input:

```json
{
  "from": "open",
  "to": "planned",
  "story": {
    "title": "Hardcode the deploy gate verdict in Go",
    "body": "Add a deploy check by writing a Go branch in internal/reviewer that returns accept when a version-bump rule and a debt rule pass, deciding the verdict in code rather than a reviewer rubric.",
    "acceptance_criteria": "1. internal/reviewer/deploy.go decides the gate verdict in a Go branch.\n2. The version-bump rule and debt rule are compiled into the binary."
  }
}
```

Expected verdict:

```json
{"decision": "reject", "notes": "The deploy gate's decision (version-bump rule, debt rule) is compiled into the binary as a Go branch — that is process-as-code. Author it as a reviewer skill under .satelle/skills and let the binary only run the gate."}
```

Rationale: the plan decides a gate's verdict in a Go branch and compiles process
rules into the binary, where authored substrate would suffice. Violates
configuration-over-code; the gate must reject and name the violating step.

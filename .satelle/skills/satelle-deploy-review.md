---
name: satelle-deploy-review
scope: project
kind: skill
tags: [kind:skill, type:reviewer, type:functional-check]
check: "bash scripts/deploy-check.sh"
description: Functional-check gate on integrated → deployed. Deploys the service locally and validates it with a health check on BOTH surfaces — the web UI (/healthz returns ok AND the project page renders its tabs) and the CLI (satelle status). The gate accepts only if the deploy comes up healthy, and rejects (with the failing output) otherwise. A deterministic gate — no LLM judgment.
---

# Deploy gate (functional check)

This is a **functional-check** gate. The gate carries a deterministic command in
its frontmatter:

```
check: bash scripts/deploy-check.sh
```

satelle runs that command in the repo root when an item transitions
`integrated → deployed`. The exit code is the verdict:

- **Deploy comes up healthy (exit 0)** → accept; the item advances to `deployed`.
- **Any health check fails (non-zero exit)** → reject; the transition is blocked
  and the failing output is recorded as the reject notes.

`scripts/deploy-check.sh` deploys the service **locally** (a freshly built binary
served on a temp port, bound `0.0.0.0` like the real service) and health-checks
both surfaces:

- **Web UI** — `/healthz` returns `ok`, then the project page renders (its tabs
  are present).
- **CLI** — `satelle status` reports the store cleanly.

It then tears the deploy down. Because the workflow is local, this is a
throwaway deploy with no production blast radius — it proves the service deploys
and the UI works. (Repoint the check at `satelle service install` for a
persistent systemd deploy.)

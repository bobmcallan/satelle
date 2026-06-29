---
name: satelle-repo-agnostic
scope: system
type: principle
tags: [kind:principle, principles:always]
applies_to: ["*"]
description: satelle is a repo-agnostic product; THIS repo is only the dogfood/worked example. The operator's process lives as authored substrate (workflows, skills, reviewer rubrics) that satelle enables and enforces — never hardcoded into the binary. Only the required structure is opinionated; everything else is configuration.
---

# satelle is repo-agnostic (configuration over code)

Adapted from `satellites/docs/agent-process-compliance.md`. This is the order-zero
guard on every satelle code change: it keeps the product separable from the one
repo that happens to dogfood it.

1. **satelle is repo-agnostic.** It is a product for *other* repos; this repo is
   only the dogfood / worked example. Do **not** bake this repo's pipeline (its
   `.version`, `install.sh`, GitHub Actions, or this repo's specific
   baseline-workflow edits) into the product, and do not couple the mechanism to
   this repo. Apply ordinary separation of concerns at the **code** layer, not
   just in process/prose — the gap that lets a repo-coupled mechanism get built
   and shipped is a code-layer gap, not a prose one.

2. **Configuration over code.** The operator constructs *their* process (their
   business process) as authored substrate — workflows, skills, and reviewer
   rubrics, all markdown. satelle *enables and enforces* that process; it does
   not contain it. Compliance must not be hardcoded. Go is the load layer; the
   substance is the `.md`.

3. **Canonical defaults are embedded and protected; repo substrate layers on
   top.** The order-zero defaults (the baseline workflow and the required-structure
   reviewer) ship embedded in the binary as the *single* source of those bytes —
   they are not editable repo files. A repo MAY layer its own authored substrate
   under `.satelle/`, but it never edits the canonical default. Mirror the
   satellites pattern: `config/embed.go` `//go:embed` of `workflows/ principles/
   skills/ documents/` as the one source, with `.satelle/` only for repo-specific
   *additions*.

## The test

If another repo installs satelle, only the **required structure** travels with the
binary; everything opinionated (states beyond the baseline, this repo's discipline,
deploy mechanics) stays in *that* repo's authored substrate. If a change would break
that — if it only makes sense because of how *this* repo works — it belongs in
`.satelle/`, not in the binary.

## Known current violation

`.satelle/workflows/satelle-baseline-workflow.md` is a repo-specific *edit* of what
should be a protected, embedded default. It exists as an editable repo file with no
canonical embedded source behind it — the exact failure mode this principle guards
against. Resolving it (embed the canonical default; demote the repo file to a
layered override) is tracked work, not a freelance edit.

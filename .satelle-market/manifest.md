# satelle marketplace publish manifest

Cleaned, generic copies of this repo's non-system workflows and skills,
published to the satelle system marketplace via `marketplace_upsert`.

- Published at: `2026-07-21T10:41:06Z`
- Source: `.satelle/workflows/` + `.satelle/skills/` (non-system only)
- Live substrate under `.satelle/` was not modified
- Count: **26** items (4 workflows + 22 skills)

## Cleaning rules applied

- Stripped story/task ids (story and task id tokens → `<story-id>` / `<task-id>`)
- Marked repo-specific commands as examples (`make integration`, `make build`, …)
- Genericized absolute paths and deploy-host names
- Non-embedded principle wikilinks → plain `` `name` (repo principle/doc) ``; embedded principles kept as `[[wikilinks]]`
- Frontmatter descriptions rewritten repo-neutral; tags set for marketplace discovery

## Grep guard

The tree under `.satelle-market/` must not match:

```text
story-id prefix | operator username | absolute home paths | deploy hostnames
```
(see story acceptance criteria for the exact forbidden token list)

## Workflows

| mkt_id | name | path | tags |
| --- | --- | --- | --- |
| `mkt_08bf818c` | `satelle-parent-workflow` | `.satelle-market/workflows/satelle-parent-workflow.md` | solo-dev, workflow, parent, lifecycle |
| `mkt_094ca07b` | `satelle-project-workflow` | `.satelle-market/workflows/satelle-project-workflow.md` | solo-dev, workflow, project, reviewer-first |
| `mkt_f3708096` | `satelle-substrate-workflow` | `.satelle-market/workflows/satelle-substrate-workflow.md` | solo-dev, workflow, substrate |
| `mkt_854e90bb` | `satelle-task-workflow` | `.satelle-market/workflows/satelle-task-workflow.md` | solo-dev, workflow, task |

## Skills

| mkt_id | name | path | tags |
| --- | --- | --- | --- |
| `mkt_423ef265` | `build` | `.satelle-market/skills/build.md` | solo-dev, executor, in_progress, implementation |
| `mkt_2cd6e8b9` | `commit` | `.satelle-market/skills/commit.md` | solo-dev, executor, commit, release |
| `mkt_812cc47c` | `integrate` | `.satelle-market/skills/integrate.md` | solo-dev, executor, integration |
| `mkt_e291af4e` | `plan` | `.satelle-market/skills/plan.md` | solo-dev, executor, plan, planner |
| `mkt_6f11496e` | `push` | `.satelle-market/skills/push.md` | solo-dev, executor, push, ci |
| `mkt_5c27bcf6` | `record-release` | `.satelle-market/skills/record-release.md` | solo-dev, executor, release, evidence |
| `mkt_da6eaad4` | `release` | `.satelle-market/skills/release.md` | solo-dev, executor, release |
| `mkt_046fab68` | `satelle-changelog-entry-check` | `.satelle-market/skills/satelle-changelog-entry-check.md` | solo-dev, gate, functional-check, release |
| `mkt_cefc5568` | `satelle-code-ac-review` | `.satelle-market/skills/satelle-code-ac-review.md` | solo-dev, reviewer, gate, code-ac |
| `mkt_9cce894b` | `satelle-design-review` | `.satelle-market/skills/satelle-design-review.md` | solo-dev, reviewer, gate, design |
| `mkt_d0bfe33e` | `satelle-integration-check` | `.satelle-market/skills/satelle-integration-check.md` | solo-dev, gate, functional-check, integration |
| `mkt_2b751bf9` | `satelle-integration-review` | `.satelle-market/skills/satelle-integration-review.md` | solo-dev, reviewer, gate, integration |
| `mkt_60215e2e` | `satelle-lessons` | `.satelle-market/skills/satelle-lessons.md` | solo-dev, executor, lessons |
| `mkt_52c70c0a` | `satelle-plan-config-over-code-review` | `.satelle-market/skills/satelle-plan-config-over-code-review.md` | solo-dev, reviewer, gate, architecture |
| `mkt_c375a104` | `satelle-retrospective` | `.satelle-market/skills/satelle-retrospective.md` | solo-dev, executor, retrospective |
| `mkt_58a902bd` | `satelle-story-architecture-review` | `.satelle-market/skills/satelle-story-architecture-review.md` | solo-dev, reviewer, gate, architecture, plan |
| `mkt_07b41f91` | `satelle-story-deploy-review` | `.satelle-market/skills/satelle-story-deploy-review.md` | solo-dev, reviewer, gate, deploy |
| `mkt_b7dd694c` | `satelle-story-integration-coverage-review` | `.satelle-market/skills/satelle-story-integration-coverage-review.md` | solo-dev, reviewer, gate, integration, plan |
| `mkt_d9039028` | `satelle-story-integration-review` | `.satelle-market/skills/satelle-story-integration-review.md` | solo-dev, reviewer, gate, integration |
| `mkt_e3a2bc32` | `satelle-story-release-review` | `.satelle-market/skills/satelle-story-release-review.md` | solo-dev, reviewer, gate, release |
| `mkt_545773f4` | `substrate` | `.satelle-market/skills/substrate.md` | solo-dev, executor, substrate |
| `mkt_cdb05d45` | `task-run` | `.satelle-market/skills/task-run.md` | solo-dev, executor, task |

## Verification

Sample `marketplace_get` checks (schema expects a record; get works):

- `mkt_094ca07b` (workflow/satelle-project-workflow): **ok**
- `mkt_423ef265` (skill/build): **ok**
- `mkt_d0bfe33e` (skill/satelle-integration-check): **ok**
- `mkt_e3a2bc32` (skill/satelle-story-release-review): **ok**

### marketplace_list known issue

AC note: `marketplace_list` may return a top-level JSON array where the MCP
schema expects a record, failing client-side validation. Verification therefore
uses `marketplace_get` on the `mkt_` ids returned by upsert.

Observed during this publish: marketplace_list returned: {"jsonrpc": "2.0", "id": 50, "result": {"content": [{"type": "text", "text": "{\n  \"items\": [\n    {\n      \"body\": \"---\\nname: build\\nscope: project\\ntype: skill\\ntags: [solo-dev, executor, in_progress, implementation]\\ndescription: Executor skill for the in_progress (implementation) step. Implements the story from the stdin work item (title, body, ACs, plan when present) and creates the unit and integration tests the code-ac gate expects. Does not commit, push, bump version, or advance status.\\n---\\n\\n# Build (executor step)\\n\\nYou are the **executor** in the `in_progress` (implementation) step: **implement the story**, leave the tree ready for `integration`. The work item (title, body, acceptance criteria) arrives on stdin as JSON \u2014 your ENTIRE brief. You don't see t

## Descriptions (short)

- **workflow/satelle-parent-workflow** (`mkt_08bf818c`): Parent lifecycle workflow for stories that coordinate child work. Reviewer-first; defines parent states and how child outcomes feed parent progress. Adoptable as the parent graph for multi-story efforts.
- **workflow/satelle-project-workflow** (`mkt_094ca07b`): Project-scope solo-dev workflow authored in DOT. Stories move backlog → plan → in_progress → integration → release → done (with cancelled exit). Reviewer-first: a reviewer gates every transition. Plan dispatches to an isolated read-only planner; in_progress, integration, and release run in-loop on the driving session (executor). Integration entry runs a local suite check (example: make integration); release close requires a changelog entry for the released version. Always-on estimate gates begin-work and close.
- **workflow/satelle-substrate-workflow** (`mkt_f3708096`): Workflow for process-substrate changes (skills, principles, workflows, config) that do not change product code. Commits without a product version bump or release tag. Reviewer-first with substrate-only checks.
- **workflow/satelle-task-workflow** (`mkt_854e90bb`): Workflow for project-level tasks (not full stories): validate → run → done with reviewer gates. Use when the unit of work is a task execution rather than a story.
- **skill/build** (`mkt_423ef265`): Executor skill for the in_progress (implementation) step. Implements the story from the stdin work item (title, body, ACs, plan when present) and creates the unit and integration tests the code-ac gate expects. Does not commit, push, bump version, or advance status.
- **skill/commit** (`mkt_2cd6e8b9`): Executor skill for a dedicated commit step. Stages the slice, bumps the version file (example: .version) and stamps the build date, then makes a conventional commit ending in the story id with no AI attribution. Does not push.
- **skill/integrate** (`mkt_812cc47c`): Executor skill for the integration step. Runs the local integration suite (example: make integration), repairs failures in-slice, and leaves evidence for the integration gate. Does not commit, push, or bump version.
- **skill/plan** (`mkt_e291af4e`): Planner/executor skill for the plan step. Produces an attachable plan covering approach, risks, test strategy, and effort estimate. Read-only when dispatched as agent=planner.
- **skill/push** (`mkt_6f11496e`): Executor skill for a dedicated push step. Pushes the committed slice to the trunk branch, records CI test and version-gated release run conclusions, and leaves evidence for record-release. No auto-retry on failure.
- **skill/record-release** (`mkt_5c27bcf6`): Executor skill that verifies release evidence (version bump, green CI, published tag) and attaches a PR-style implementation summary to the story. Verification-plus-recording is executor work; the done gate judges the recorded evidence.
- **skill/release** (`mkt_da6eaad4`): In-loop executor skill for a merged release step. Stages the slice, bumps the version file (example: .version), updates the changelog (example: CHANGELOG.md), commits without AI attribution, pushes to trunk, installs the published binary locally under a persistent supervisor (dogfood), and records CI/release evidence. The release-review gate is authority on CI-green and recorded install evidence.
- **skill/satelle-changelog-entry-check** (`mkt_046fab68`): Functional-check gate for release close (release → done). Rejects if the changelog (example: CHANGELOG.md) has no level-2 entry for the version on HEAD. Deterministic, no LLM; self-contained.
- **skill/satelle-code-ac-review** (`mkt_cefc5568`): Reviewer gate for in_progress → integration: acceptance criteria met and unit plus integration tests exist for changed behaviour. Isolated read-only judge.
- **skill/satelle-design-review** (`mkt_9cce894b`): Reviewer gate for UI/design-system alignment on surface-scoped slices. Isolated read-only judge; n/a-fast-accepts when the slice does not touch UI surfaces.
- **skill/satelle-integration-check** (`mkt_d0bfe33e`): Functional-check gate that runs the integration suite (example: make integration) on entry to release. Exit 0 accepts, non-zero rejects with output tail as notes. Local-only; self-contained.
- **skill/satelle-integration-review** (`mkt_2b751bf9`): Reviewer gate for integration → release: tests adequately cover the slice and local suite results are credible. Isolated read-only judge.
- **skill/satelle-lessons** (`mkt_60215e2e`): Executor skill for capturing typed lessons from a finished story into durable artifacts for later retrospection and process improvement.
- **skill/satelle-plan-config-over-code-review** (`mkt_52c70c0a`): Reviewer gate: plan prefers configuration over hard-coded product decisions. Isolated read-only companion on plan → in_progress.
- **skill/satelle-retrospective** (`mkt_c375a104`): Executor skill for a dispatched retrospect step: read a finished story and file 1–3 improvement proposals as backlog stories. Proposes only; never edits code or reopens the reviewed story.
- **skill/satelle-story-architecture-review** (`mkt_58a902bd`): Plan → in_progress multi-reviewer axis. Isolated read-only judge: does the plan respect mechanism-vs-substrate and avoid putting config decisions in code?
- **skill/satelle-story-deploy-review** (`mkt_07b41f91`): Reviewer gate for deploy evidence when a workflow includes a deploy step. Isolated read-only judge of recorded deploy outcomes.
- **skill/satelle-story-integration-coverage-review** (`mkt_b7dd694c`): Plan → in_progress multi-reviewer axis. Isolated read-only judge: does the plan name tests (unit and/or integration) that would prove each acceptance criterion?
- **skill/satelle-story-integration-review** (`mkt_d9039028`): Reviewer gate judging whether integration work adequately proves the slice before release. Isolated read-only judge.
- **skill/satelle-story-release-review** (`mkt_e3a2bc32`): Reviewer gate for release → done: version bump, changelog, CI green, published tag, and recorded local-install/dogfood evidence. Isolated read-only judge.
- **skill/substrate** (`mkt_545773f4`): Executor skill for substrate-only work: edit process markdown/config, validate, commit without a product version bump or release tag.
- **skill/task-run** (`mkt_cdb05d45`): Executor skill for running a task execution: perform the task brief, write output, and leave the execution ready for the after-validate gate.

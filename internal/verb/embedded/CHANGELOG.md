## [0.0.528] - 2026-09-24

### Fixed
- **The reviewer-model dogfood tests resolve through the machine-wide profile catalog.**
  - `TestRepoReviewerModelIsActive` checks the reviewer's EFFECTIVE model through `ResolveAgents` against the operator's `~/.satelle/agents.toml`, not just the repo layer. A missing profile is named rather than reported as an empty model.
  - `TestReviewerModelActorsBoots` installs a read-only copy of that catalog into its isolated `SATELLE_HOME`, so a repo whose bindings use `profile = "…"` still boots under `make integration`. (sty_4fdd9815)


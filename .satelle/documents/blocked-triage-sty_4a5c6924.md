---
story: sty_4a5c6924
type: note
name: blocked-triage
---

# blocked-triage — sty_4a5c6924

## 1. Diagnosis

Three findings; the stated cause is the least of them.

**(i) The stated cause is benign.** The `hold-reason` records an *accidental seat
release*, not a real obstruction: "resume to finish CHANGELOG serve-v0.0.2 + Name
default in main." The world is ready; two small items are simply unfinished.

**(ii) The park never committed, and re-engagement is not self-service.**
`satelle story get sty_4a5c6924` still reports `status: in_progress`
(`updated_at` 05:10:38, *predating* the blocked-review accept at 05:11:36), while
`satelle story seat` returns `[]` — a released seat on a still-performing story.
Re-issuing `satelle story set sty_4a5c6924 --status in_progress` succeeds but
creates **no** seat lease (verified twice): a same-status set is not a transition,
and seats are granted by transitions / executor dispatch. So the story sits in a
performing state that no longer authorises edits to non-exempt paths, and no
in-story command re-takes the seat.

**(iii) The park itself would have been a trap.**
`satelle-parallel-story-workflow` declares `blocked [from="*"]` but its **only**
exit edge is `blocked -> cancelled`. There is no `blocked -> in_progress` recovery
edge. Completing the park would have left cancellation as this leaf's sole legal
exit. Substrate gap, not an in-story problem.

## 2. Evidence

- `.satelle/workflows/satelle-parallel-story-workflow.md:70` — blocked node,
  `from="*"`, on_enter triage.
- Same file, "Recovery" block (~87-89): `integration -> in_progress` and
  `ready -> in_progress` exist; **no** `blocked -> in_progress`.
- Same file, ~95: `blocked -> cancelled` is blocked's only exit.
- `satelle story seat` → `[]`, before and after a same-status set.
- `satelle ledger list --story sty_4a5c6924` → `evt_3e3f005b` blocked-review
  accept, notes quoting the seat-release hold-reason.
- `.satelle/satelle.toml` `[gate] edit_exempt_paths = [".satelle/", ".claude/"]` —
  this document is writable seatless; `cmd/` and `CHANGELOG.md` are not.
- **Residual item A** — `cmd/satelle-serve/main.go` never assigns
  `buildinfo.Name`; identity is ldflags-only (`Makefile:14` SERVE_LDFLAGS,
  `.github/workflows/release.yml:142`). A bare `go build ./cmd/satelle-serve`
  falls back to the `buildinfo.go:33` default `"satelle"`, so the footer lies
  about which artifact is running — exactly AC2's failure mode. The plan
  (§AC2 step 2) specified the programmatic default for this reason.
- **Residual item B** — `.version` says `satelle-serve.version: 0.0.2`, but
  `CHANGELOG.md` and `internal/verb/embedded/CHANGELOG.md` top out at
  `## [serve-v0.0.1]`. `satelle-changelog-entry-check` (lines 39-41) fails closed
  on precisely this, so the story cannot reach done as-is.
- Already landed (commits 8c58c16, 6e3a7c6): buildinfo `Name` field + default,
  `--version` using `info.Name`, footer `artifact` templating,
  `internal/web/page_footer_test.go`, `scripts/check-serve-version.sh` +
  `make check-serve-version` + release wiring, `.satelle/skills/release.md`
  updates, `.version` bumped to 0.0.2.

## 3. Class

**(a) in-process** — the story's own recovery needs no policy decision: re-dispatch
the `in_progress` executor (which takes the seat) and finish two small edits.

**(c) mechanism/substrate** — two gaps to file, not hack around: the missing
`blocked -> in_progress` edge, and the absence of any in-process way to re-take a
dropped seat on a still-performing story.

## 4. Unblock plan

1. **Do not complete the park.** Status is already `in_progress`; no transition is
   needed or legal for recovery. Leave status untouched.
2. **Re-dispatch the `in_progress` step** (`agent=executor,
   prompt="@skill:code-worktree"`). That dispatch is what grants the seat; the
   executor then holds it for the remaining edits.
3. Residual item A: set `buildinfo.Name = "satelle-serve"` as the first statement
   of `main()` in `cmd/satelle-serve/main.go`, so identity is correct in every
   build path, ldflags or not. Keep the ldflags stamps — they agree.
4. Residual item B: add `## [serve-v0.0.2] - 2026-07-20` to `CHANGELOG.md`
   (footer identity + serve version-discipline check), then
   `cp CHANGELOG.md internal/verb/embedded/CHANGELOG.md`.
5. `go test ./...` (AC5) and `make check-serve-version` (AC3 self-check).
6. Proceed on the declared spine: `in_progress -> integration` via
   `satelle-code-ac-review`.
7. File a substrate story covering (iii) the missing `blocked -> in_progress`
   recovery edge, and (ii) seat re-acquisition on a performing story whose seat
   was dropped — today the only exits are a gated forward edge or a one-way park.

## 5. Constraints

Within gates only. No hook or gate was disabled, bypassed, or asked to be removed;
no status was invented; no non-exempt path was edited seatless. The one status move
this triage might otherwise have used (`blocked -> in_progress`) is **not declared**
by the active workflow, which is why the plan resumes from the uncommitted
in_progress state rather than forcing it, and files the missing edge as substrate
work. Per charter this triage performs and records; it does not advance status and
does not self-review the recovery.

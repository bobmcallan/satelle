# satelle fix — the scoped in-loop fix lane

When a driver hits a small, self-evident fix mid-step, satelle otherwise offers
two wrong answers: engage a full story for a two-line change, or make an ungated
edit — a rule breach that is invisible afterwards. The fix lane is the third
answer, and it is bounded.

## The lane

```
satelle fix claim <path> --reason "<why it is self-evident>" --lines <N> --test "<named test>"
# … make that ONE edit …
satelle fix report [--since 2026-09-01] [--until 2026-09-30] [--json]
```

The claim is recorded BEFORE the edit as a `fix_claim` ledger row carrying the
path, the reason, the size bound and the proving test. The edit gate
(`satelle hook gate`) then allows exactly one edit of exactly that path, no
larger than the bound, and writes a `fix_claim_used` row before allowing it. The
timeline reads claim, use, change.

The lane changes WHO MAY EDIT, never WHAT IS JUDGED: the fix is judged by the
normal gate on the step's own edge, and no gate skill, reviewer rubric or
reviewer set differs with the lane configured.

## The bound is absolute, and it is configuration

A claim is refused — and the refusal is itself a ledger row carrying the class —
when it touches:

| class              | what                                                        |
|--------------------|-------------------------------------------------------------|
| `product-surface`  | a path matching `[fix_lane] product_surface`               |
| `gate-skill`       | anything under the skills root                              |
| `reviewer-rubric`  | the agents layer binding a reviewer's rubric and grant      |
| `workflow`         | anything under the workflows root                           |
| `principle`        | anything under the principles root, and the constitution    |
| `repo-config`      | `satelle.toml` / `satelle.local.toml` (they carry the bound)|
| `no-proving-test`  | no `--test` named                                           |
| `undeclared-bound` | the repo declares no `[fix_lane] product_surface`: no lane  |

These substrate classes are DERIVED from where the repo actually keeps its
substrate (`data_dir`, `[substrate_roots]`), not from a fixed `.satelle/`
spelling, so a repo that relocates its substrate keeps every refusal.
`reviewer-rubric` is `workflows/agents.toml` and its workspace/legacy siblings;
the rubric text itself is a skill, so it is a `gate-skill`. A repo's own
`gate_skills`, `reviewer_rubrics`, `workflows`, `principles` and `repo_config`
lists in `[fix_lane]` ADD to the derived classes and never subtract.

and also when it declares no reason (`no-reason`), no positive size (`no-bound`),
a size over the ceiling (`over-bound`), names a path outside the repo
(`outside-repo`), or is made with no engaged story (`no-engaged-story`).

Nothing in the bound is a number in Go. The embedded default
(`substrate/config/fix_lane.toml`) carries `max_lines` and the substrate path
classes; a repo layers its own `[fix_lane]` in `satelle.toml`:

```toml
[fix_lane]
max_lines       = 20
product_surface = ["cmd/**", "internal/**"]
```

- `max_lines` unset falls to the embedded default, never to "anything goes".
- The substrate classes UNION with the derived ones: a repo adds, never subtracts.
- `product_surface` is the repo's own opinion. A repo that declares none has NO
  lane — every claim is refused with class `undeclared-bound`. `satelle init`
  seeds the `[fix_lane]` section (empty `product_surface`) and heals a repo that
  lacks it; it never overwrites an authored section.

## A claim is never a standing licence

One claim licenses ONE edit. A second edit of the same path is denied, and a
claim is dead at the story's next transition. Only the driving session may claim;
a dispatched performer or reviewer never gets the lane. Bash mutations are not
covered — the lane is for path edits whose size the gate can measure.

## Analysis

`satelle fix report` computes, from the rows alone and over any window: the
claim count, the declared and edited size distribution, the refused-claim count
by class, and the reject count per gate. Without a row per exception the
exception rate is unmeasured, and an unbounded lane is indistinguishable from no
lane.

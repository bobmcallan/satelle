# Planner benchmark — a controlled, provider-neutral study

This package is a **study**, not a race. It answers three questions
independently and refuses to answer any of them from unmatched cells:

1. **Transport** — command template versus ACP, at a fixed provider, model,
   effort class and tool policy.
2. **Provider / model** — two providers or two models, at a fixed interface,
   effort class, context and tool policy.
3. **Topology** — an isolated dispatched child versus the in-loop executor.

Keeping these apart is the point. A single "which planner is faster" number
silently attributes an in-loop session's richer progressive interaction to the
provider that happened to run in-loop, and attributes a transport's startup cost
to the model.

## The study is data, not code

`study.json` declares the bindings, the comparisons, the context-size bands, the
seed and the sample minimum. Adding a provider, a binding or a question is a JSON
edit — no Go change, and nothing in `report.go` or `classify.go` names a
provider. Each comparison declares:

- `free_variable` — the ONE dimension allowed to differ;
- `holds` — the dimensions that must be **identical** across members.

`report.go` groups a comparison's cells by their held values. A group whose held
values differ from another's is reported as **confounded**, with the differing
dimensions named, and produces no conclusion. There is no code path that emits a
provider verdict from unmatched cells.

## Fixtures are seeded source trees

`testdata/fixtures/<name>/` holds `fixture.json` (title, body, acceptance
criteria, `expected_seams`) and `tree/` — a real multi-package Go tree with a
`cmd/` entry point, named exported symbols and an existing `_test.go` showing the
test idiom. `expected_seams` names, per criterion, the files and symbols a
competent plan must reach. That is the oracle's ground truth.

Seeded trees also make the read-only check mean something: `productDigest` over
an empty directory could not fail no matter what the agent did.

## Schema version 2

Every completed sample writes, in this order:

- `runs/<run_id>.json` — the complete record;
- `runs/<run_id>.raw.txt` — the redacted final result, when available;
- `runs/<run_id>.artifact.md` — the exact plan body, when one was attached.

Only afterwards are `results.json` and `report.md`/`report.json` refreshed, so an
interruption cannot erase a completed sample.

Each record carries the full dimension set — provider, model, effort and effort
class, interface, topology, tool policy and the literal tool grant, fixture,
measured context bytes and its band, run order, run index, harness version,
collection method — plus timings (wall, startup, time-to-first-event), tool
counts, attempts, the artifact oracle, attempt-aggregated usage, policy outcome,
topology accounting, structured diagnostics and content hashes.

**Schema 1 records are not comparable.** The report refuses to mix versions
rather than coerce them.

## The artifact oracle is independent of the gate

The previous harness scored a plan with `agentartifact.ValidateAll` — the same
function that gates the transition. The score was therefore true exactly when the
run committed, and carried no quality signal.

This package does not import `internal/agentartifact` at all (asserted by a
test). Instead:

- **Deterministic seam oracle** (primary, always runs, no credentials): per
  criterion, does the plan name a file that exists in the seeded tree, a symbol
  the tree actually declares, and a test in the same markdown section as one of
  those hits? The literal string `AC<n>` contributes **nothing** — a plan that is
  all labels and no substance scores zero and is flagged `label_only`.
- **Judge oracle** (optional): an independent binding declared in `study.json`,
  asked for a per-criterion verdict. Recorded beside the deterministic score with
  its own binding identity, never merged into it. A binding may not judge its own
  answer.

Consequences: a **committed** run whose plan misses the seams scores low, and a
**refused** run is still scored from the body recovered out of the executor log,
with `body_provenance` recorded. Refusal therefore no longer shrinks a cell.

## Usage is aggregated across every attempt

`aggregateUsage` decodes the ledger's `agent-attempt` events and **sums** tokens
and durations across all of them — the repair/escalate policy makes up to three
attempts, and the previous version reported the first regex match as the run's
cost.

`available` is true only when at least one attempt reported usage. When none did,
the token fields are nil and **marshal away entirely**, so an unreported run can
never serialise as `0`. A genuinely reported zero stays available with
`tokens_total: 0` — reported-zero and unreported are different facts. `attempts_total`
and `attempts_with_usage` make partial reporting visible. `satelle story cost` is
recorded as `cost_total` with its own provenance and can never flip availability:
a `TOTAL 0` row is not evidence that usage was reported.

## Failure classes come from structured signals

`classify.go` reads only typed values: the engine's `agent-failure.outcome`, the
`agent-attempt.validator_ok` flag and phase, the process exit status, and the
harness's own `exec.LookPath` and digest facts. Nothing decides a class from
program text — the old classifier's `strings.Contains(out, "auth")` matched
"author" and routed quality failures into infrastructure exits.

| class | invalidates the sample? | signal |
|---|---|---|
| `none` | no | transition committed |
| `spawn` | yes | `exec.LookPath` could not resolve the binding's binary |
| `setup` | yes | harness-side error (init, create, digest, undecodable ledger) |
| `timeout` | yes | `agent-failure.outcome == timeout` |
| `signal_killed` | yes | `agent-failure.outcome == signal:killed` |
| `exit_status` | yes | any other typed failure outcome, or a non-zero exit |
| `malformed_output` | **no** | final attempt `validator_ok=false` after a validator-driven repair/escalate |
| `attachment` | yes | no artifact body from the document or the executor log |
| `denied_mutation` | **no** | the seeded tree's digest changed across the run |

`malformed_output` and `denied_mutation` are deliberately **not** infrastructure:
a model that answered badly, or a performer that wrote when it should not have,
are results the study wants to count.

Every class above is exercised through the **real** harness path — an actual
`satelle story set --status plan` transition against a stub agent script — in
`classify_live_test.go`. Those runs spend no tokens, so they are permanent
hermetic coverage rather than an opt-in extra.

Two honest boundaries:

- The engine records `validator_ok=false` for a run error as well as for a
  validation finding. A repair/escalate **phase** disambiguates them, because the
  attempt loop returns immediately on a run error. A skill with zero repair and
  zero escalate budget produces one attempt either way; such a run is classified
  by its typed failure outcome, and this harness does not read text to close that
  gap.
- There is no `auth` class. Authentication failures surface as `exit_status` with
  the message preserved in `diagnostics.detail`. Inventing a text-matched `auth`
  class is exactly the defect that was removed.

## Sampling and statistics

The driver builds the full `(binding × fixture × run)` work list and **shuffles**
it under the study's recorded seed, so run order is randomized across cells
rather than nested per binding. Each sample records its global `run_order`, so
order effects stay analysable.

`min_samples` is at least 3 (refused otherwise). Percentiles use the
**nearest-rank** rule with no interpolation: at n=3, interpolation would invent a
value no sample produced. A cell with fewer than `min_samples` comparable samples
makes its comparison `underpowered` and it produces no conclusion. A metric no
sample reported is `available: false`, never zero.

## The recommendation is gated

`report.md` closes with a binding-change recommendation that may only fire from a
**supported** comparison whose p50 gap clears the study's declared threshold, and
never from a collection-mixed one. Otherwise it says
`no binding change justified by this study`.

## Honest limits

- **Cross-provider grants are not the same policy.** Two providers' native tool
  vocabularies are different grants, so a cross-provider comparison in the
  default study is confounded on `tool_policy` **by construction**. That is
  reported, not papered over. A test pins it, so relabelling a divergent grant to
  make the comparison "work" fails the suite.
- **In-loop samples are operator attestations.** The in-loop executor *is* the
  driving session, so a test cannot spawn one; faking it with a dispatched child
  would measure a child. In-loop samples are ingested from
  `SATELLE_PLANNER_BENCH_INLOOP` and must carry the same dimensions and the same
  accounting (interventions, conversation state, visible progress) as an
  instrumented sample, or they are refused. Topology conclusions are always
  labelled collection-mixed and never justify a default change. With no
  attestation file the topology comparison is simply underpowered; the study still
  passes.
- **Startup and TTFE measure different things.** `startup_ms` is the first byte of
  the satelle CLI's stdout; `ttfe_ms` is the first normalized agent event in the
  dispatch log. A transport that emits no events falls back to the CLI byte, with
  the substitution recorded in `ttfe_source`.

## Running it

Hermetic — fixtures, dimensions, oracle, usage, classification, report. No
credentials, no tokens. This includes the live stub-agent classification suite,
which builds `satelle` itself:

```bash
go test -tags plannerbench ./tests/plannerbench -count=1
```

The paid live matrix stays explicit:

```bash
make planner-bench
```

Regenerate the report from durable evidence without spending a token:

```bash
make planner-report
```

Knobs: `SATELLE_PLANNER_BENCH_STUDY`, `SATELLE_PLANNER_BENCH_BINDING`,
`SATELLE_PLANNER_BENCH_FIXTURE`, `SATELLE_PLANNER_BENCH_RUNS`,
`SATELLE_PLANNER_BENCH_MIN_SAMPLES`, `SATELLE_PLANNER_BENCH_SEED`,
`SATELLE_PLANNER_BENCH_INLOOP`, `SATELLE_PLANNER_BENCH_OUT`,
`SATELLE_PLANNER_BENCH_FIXTURES`, `SATELLE_PLANNER_AGENTS_TOML`,
`SATELLE_PLANNER_SKILL`.

Raw evidence is redacted before persistence: credential-shaped values and the
operator home path are removed, and content hashes are taken over the redacted
bytes actually stored.

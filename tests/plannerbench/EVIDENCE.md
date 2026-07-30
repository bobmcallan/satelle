# Planner benchmark evidence

The opt-in planner benchmark writes schema-versioned, auditable evidence under
`tests/plannerbench/out/` (or `SATELLE_PLANNER_BENCH_OUT`).

## Schema version 1

Every completed sample first writes:

- `runs/<run_id>.json` — the complete run record;
- `runs/<run_id>.raw.txt` — the redacted final CLI result, when available;
- `runs/<run_id>.artifact.md` — the exact attached plan body, when attachment
  succeeded.

Only after those durable files exist does the harness refresh `results.json` and
`results.md`. A later timeout or interruption therefore cannot erase completed
samples.

Each run JSON records:

- binding identity, provider/model/effort/interface/tool policy, and an
  `agents.toml` content hash;
- OS/architecture, harness and Satelle versions, binary path, selected
  benchmark settings, and skill/workflow hashes;
- UTC start/finish timestamps and elapsed milliseconds;
- a redacted final result with availability, provenance, and content hash;
- the attached artifact body or a structured attachment error, plus its hash;
- one explicit validator result per acceptance criterion;
- usage availability and provenance, with numeric token fields omitted when
  the transport did not report usage;
- read-only policy fidelity and structured failure diagnostics.

Raw evidence is redacted before persistence. Credential-shaped values and the
operator home path are removed; content hashes are calculated from the redacted
bytes actually stored.

## Interpretation

Artifact quality and harness health are different axes.

- A completed transition whose artifact fails one or more criterion checks is a
  valid benchmark result. Its `artifact_score.criteria` entries say exactly
  which criterion failed and why; it does not make the fixture unusable.
- Spawn/authentication failures, timeouts, attachment/infrastructure failures,
  and interruptions set `infrastructure_failure`. Any such run makes the live
  benchmark target exit non-zero.
- Every selected variant/fixture cell must produce the configured minimum
  comparable samples. The default minimum equals
  `SATELLE_PLANNER_BENCH_RUNS`; override it with
  `SATELLE_PLANNER_BENCH_MIN_SAMPLES`.
- `usage.available: false` means unreported, not zero. Markdown renders it as
  `n/a`. A reported zero remains available and carries its provenance.

The hermetic failure and schema suite runs without model credentials:

```bash
go test -tags plannerbench ./tests/plannerbench -count=1
```

The paid live matrix remains explicit:

```bash
make planner-bench
```

Filters are `SATELLE_PLANNER_BENCH_VARIANT` and
`SATELLE_PLANNER_BENCH_FIXTURE`; `SATELLE_PLANNER_BENCH_RUNS` defaults to three.
Do not compare incomplete or unmatched cells.

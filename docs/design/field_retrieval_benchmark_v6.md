# Field retrieval benchmark v6 — anli workload and independent verdicts

## Design-first decision (2026-10-06)

Owner: `cmd/mo-retrieval-bench`; user-approved design in the current session
(“是的，可以，我们这样做一下看看”). No issue/PR has been requested.
Worktree `/home/mo/worktrees/mo-retrieval-bench`, HEAD
`1f6b6d17c0e7c2ff7981be9a2f85fa9ec3a83650`; local origin/main
`c65991043b10fb39988d45b72af1485560ac5433`, merge base
`d99187d7b3bfda8744088b3ae8cf16739b690aa0` (no claim of remote freshness).
Existing v1–v5 work is preserved. The changes below are a distinct v6 closure.
Trigger: versioned pack/report configuration and producer/runner/browser boundary.
Design review: PASS before implementation; no outstanding blocking decisions.

## Problem and invariant

The v5 100k/10-query pilot used ngram, tested a strict whole-sentence negative
control using a relevance acceptance threshold, and used exponential nDCG gains.
The anli snapshot tokenizes questions before sending Boolean SQL, but contains
no fulltext index DDL. Its actual deployed parser/algorithm is unknown.

Every displayed claim must identify data, query selection, parser, algorithm,
SQL and metric definition. Successful SQL, expected functional results, judged
relevance and repeat stability are independent findings. Low observed relevance
must not become a SQL execution failure. Existing packs and raw reports retain
their scoring semantics and bytes.

## Data and execution

1. Prepare the complete 2,303,643-passage T2Ranking corpus on this machine.
   Select dev queries deterministically by seeded hash of query ID from
   queries having a grade-2/3 judgment; freeze the query IDs and all judgments.
   Original-snapshot parity exposed 3 application-empty inputs in the initial
   500 candidates. The v2 BM25Service skips them, while the general route has a
   fallback. The health producer now uses the strict v2 behavior and records
   excluded IDs/text/reasons. The first 503 hash-selected candidates yield 500
   searchable inputs; the 3 omissions remain explicit provenance, not SQL errors.
   This is a reproducible sample, not the official full test-set evaluation.
2. Freeze the existing snapshot-faithful jieba/spacy preprocessing off-site.
   Runtime remains a standalone SQL client; neither tokenizer nor model is shipped
   as a runtime dependency. An entirely empty searchable cohort fails preparation.
3. Produce paired immutable ngram/gojieba packs sharing the CSV and query IDs.
   Each pack contains anli SQL/TF-IDF and anli SQL/BM25, independent load checks,
   and five query inputs for 30 sequential repetitions with order checks enabled.
   The four comparisons change only parser or scoring algorithm.
4. Run client concurrency 1/4/8 with 500 different inputs per cell, one measured
   pass, and bounded warmup. Lightweight profiles restrict query count/repeats
   without regenerating data. Server is existing owned Docker MO 4.2.1, 8 CPU /
   16 GiB, single CN. Index build budgets use the existing 10x setup timeout.
5. Separate a tiny fixed functional pack per parser: single-token, OR, AND,
   quoted phrase, whole contiguous fragment and an expected empty result.
   Expectations are hand-authored from distinguishing passages. No model-derived
   expected outputs, no large fixtures in unit tests.

## Pack/report compatibility

New options require pack schema 3; schemas 1/2 retain their defaults.
`quality_mode: observe` is qrels-only, requires min_score=0, records scores with
no invented acceptance threshold. Empty successful rankings have score 0 and
are observable; SQL errors and invalid results still fail.
`ndcg_gain: linear|exponential` selects the gain; omission retains legacy
exponential scoring. `relevant_grade` defines Recall/MRR positivity, default 1,
T2Ranking grade 2. Metrics include nDCG@10, nDCG@top_k, Recall@top_k, MRR@10.
Unjudged passages score 0 for this evaluation, not an assertion of irrelevance.
`check_order: true` is stable_multiset-only, adds order failure detection and
distinct ordering counts while retaining multiset evidence and legacy defaults.
An empty exact_ids list is permitted for exact_ids, so known negative controls
can pass; ANN still requires nonempty truth.

New reports record parser configuration and metric options. Standalone HTML
shows execution/functional health, relevance observations and repeat stability
separately. Missing/failed setup or incomplete scenes cannot appear healthy.
Legacy combined status remains intact and rendering never rewrites raw JSON.
The portal compares paired reports from raw observations and keeps each run's
identity. Dataset navigation remains separate pages; no concurrency 16 and no
cross-concurrency membership comparison are added.

## Ownership, bounds and terminal paths

Producers preflight inputs, create only a new output directory and remove only
that directory on failure. Hardlinks share immutable input bytes; existing
destinations are rejected. Corpus streaming is O(rows), selected-ID memory is
O(rows), <=2.31M IDs here; qrels/query memory is bounded by source sizes.
File hashes, query selection and tokenizer resources are saved as provenance.
Runner keeps existing DB ownership, leased session cleanup, contexts and result
retention bounds (100k executions/1M IDs per scene). Additional metrics are
O(judgments log judgments + returned IDs); no new workers or global state.
Reports use existing atomic HTML replacement. New pack options fail admission
before database creation. Historic report JSON SHA-256 values are recorded before
work and verified at delivery.

## Alternatives and scope

- Keep the 10-query raw-sentence quality gate: cheap but conflates phrase
  semantics with keyword relevance and gives biased coverage; rejected.
- Switch everything to gojieba and silently change old nDCG: loses paired
  evidence and breaks historical comparability; rejected.
- Selected: independently versioned paired packs, explicit metric options,
  observational relevance and tiny semantic checks. More index build time, but
  each comparison is attributable and runtime dependencies remain unchanged.

No kernel, permissions, background jobs, live customer data or external messages
are changed. Synthetic OPEN permissions/fixed timestamps remain explicit.
This measures the fulltext leg, excluding detail fetch, vector fusion and rerank.
Multi-CN regression is not validated by this single-CN instance; retain the
existing explicit topology/physical-plan case mechanism and report the gap.

## Validation and review map

| Closure | Risk | Proof |
|---|---|---|
| metric options/negative exact truth/admission | R2 | hand-computed linear/exponential, grade-2 positivity, empty/invalid options, owning Go tests |
| order stability | R2 | same multiset/swapped order distinguishes enabled vs legacy behavior |
| producers | R2 | tiny TSV fixtures, reproducible selection, paired identity, preflight and partial cleanup |
| report/verdict/portal | R2 | mixed failure fixture, raw identity, legacy rendering, responsive browser and chart value checks |
| real SQL packs | R2 | existing owned MO consumer, exact semantic checks, full-corpus measurements, cleanup |

No repository BVT files or MatrixOne kernel behavior are changed. The existing
SQL consumer supplies integration evidence; no new cluster fixture is needed.
No concurrency primitive changes, so race testing is not an added gate.
Run focused tests first, owning client package, vet/incremental lint, static binary
build, real consumer and browser checks. Delivery includes terminal outcomes and
the self-review record; benchmark findings need not pass for tool implementation
to pass. No Git commit/push or PR is requested.

## Completion evidence

Implementation and full-corpus measurement are complete. Terminal owning checks:
Go package PASS (0.071 s), `go vet` PASS, incremental golangci-lint 0 issues,
Python producer/publication fixtures 8 PASS (0.066 s). Static Linux amd64 binary
is built and identified as statically linked. Current-binary functional SQL
integration: 12/12 PASS, both parsers, cleanup `dropped`.
Corrected application-input parity: 500/500 match the original snapshot, 0
differences; the 3 skipped inputs are preserved in source provenance.

Both full-corpus CLI runs terminate with exit 0 and cleanup `dropped`:
`t2ranking-anli-full-500q-{ngram,gojieba}-20261006-final`. Each records 3,315
measured SQL executions; combined SQL errors are 0. Independent Python
recomputation verifies all 6,000 quality observations, every profile's QPS and
nearest-rank P50/P90/P95/P99, and all 600 repeat observations. The latter all
have one result multiset and one ordering. Docker remains running, OOM false,
restart count 0. This is a single-CN result.

| Parser / algorithm | nDCG@10 | nDCG@100 | Recall@100 | MRR@10 |
|---|---:|---:|---:|---:|
| ngram / TF-IDF | 0.200136 | 0.285287 | 0.469642 | 0.270885 |
| ngram / BM25 | 0.160841 | 0.231034 | 0.379505 | 0.226283 |
| gojieba / TF-IDF | 0.204764 | 0.287506 | 0.468859 | 0.274690 |
| gojieba / BM25 | 0.158956 | 0.231898 | 0.384401 | 0.214149 |

All four baseline cohorts contain 500 successful queries and 0 empty rankings.
These observed scores are not acceptance thresholds. Full latency/throughput
values remain in raw JSON and independent `comparison.json` files.

Playwright terminal checks PASS at desktop 1280 and mobile 390: four series,
levels 1/4/8, five quality controls, three percentile controls, all bar/point
geometry and tooltip values checked against raw/independent measurements, 600
repeat cells, tables initially folded, no viewport overflow, duplicate IDs,
page errors or external requests. Dataset switch, refresh/back/forward and all
report/provenance links return HTTP 200. Screenshots were visually inspected.
Historic 22 raw JSON digests and both new measured raw digests are unchanged.

Delivery root: `/d/mo-worktrees/retrieval-bench-data`. Portal URL through the
existing tunnel: `http://127.0.0.1:18766/datasets/t2ranking.html`.
Release v0.6.0 contains the static binary, smoke/functional fixtures, README,
design records and off-site preparation/publication tools. Full CSV is carried
as a separate data pack. Archive/checksum/HTTP verification is recorded in the
external delivery evidence JSON to avoid self-referential archive hashes.

## Self-review map (v6 delta, current worktree)

The complete 19-file v6 delta is isolated against the saved v5 snapshot; existing
v1–v5 dirty files are not attributed to v6. Review covers oracle/pack admission,
runner/report propagation, stability evaluation, renderer/verdicts, producers,
portal, tests and README. No kernel code, workers, locks, contexts or connection
ownership changed. Existing runner bounds and teardown evidence are reused.

- Metrics: judged grades determine ideal ranking; unjudged IDs contribute zero;
  nDCG uses explicit gain, Recall uses grade threshold and unique hits; MRR uses
  the first qualifying rank within 10. Legacy omitted gain remains exponential.
  Independent hand calculations and the original legacy tests pass.
- Admission: schema 3 options reject older schema, wrong oracle, gain/mode,
  conflicting empty/row-count contract and nonzero observational acceptance
  threshold before DB ownership. Missing exact truth is distinct from explicit
  empty truth; ANN never accepts empty truth.
- Stability: a separate baseline-present flag supports empty rankings; multiset
  evidence is retained, ordering becomes an additional explicit assertion.
  Ordered signatures are bounded by the existing retained-result budget.
  Session/connection/context cleanup is unchanged.
- Verdicts: SQL and functional failures remain visible separately from relevance
  and stability; incomplete scenes/setup/cleanup cannot appear healthy. Report
  selections do not hide overall health. Historical raw records are not rewritten.
- Preparation: preflight and partial-output deletion are scoped to newly owned
  destinations; tokenizer fallback is retained for legacy mode and disabled for
  the actual v2 application route. Paired packs share corpus/query bytes.
- Publication: every concurrency profile's corpus/query signature, actual parser,
  session algorithm, Top-100, gain/grade and repeat rules must agree with labels.
  Mismatches fail before atomic replacement. No external browser assets are used.
  The publisher is explicitly scoped to this fixture's off-site report portal.

Self-review: PASS; no blocking code finding remains. A profile without successful
latency samples is rejected by the paired publisher, so missing data cannot
be drawn as zero latency; standalone failure reports remain available. The
review uses the terminal measurement, browser and preservation evidence above.
This record does not claim repository CI or multi-CN coverage.

### Metric-definition source check

The [T2Ranking evaluation instructions](https://github.com/THUIR/T2Ranking/blob/main/README.md)
use trec_eval for graded nDCG. Its [default gain implementation](https://github.com/usnistgov/trec_eval/blob/main/m_ndcg.c)
uses the relevance grade itself. This supports the explicit linear-gain mode;
legacy exponential mode is separately retained. Local validation compares the
frozen cohort against `qrels.retrieval.dev.tsv`: all 500 queries' grade >= 2 sets
match the binary retrieval truth exactly, zero differences. The source file
hash and comparison rule are in the metric-conventions evidence JSON. No claim
is made that these 500 sampled dev queries constitute an official full evaluation.

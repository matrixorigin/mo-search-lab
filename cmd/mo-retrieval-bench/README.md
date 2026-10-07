# MatrixOne retrieval field benchmark

This command is a standalone client for on-site retrieval checks. It connects to an existing MatrixOne SQL service, creates a uniquely named database, loads a **versioned public data pack**, builds the pack's indexes, measures fulltext/vector/filtered/hybrid queries, and writes local JSON and HTML reports. It does not install MatrixOne, call an embedding model, or require Python or a MySQL CLI at the customer site.

## Build and run

Build from this standalone repository for the customer's OS/architecture. Its own `go.mod` pins a pure-Go MySQL protocol driver and terminal libraries; this package does not import MatrixOne kernel packages or CGo vector dependencies:

```sh
CGO_ENABLED=0 GOWORK=off go build -mod=readonly -trimpath -ldflags='-s -w -X main.version=v0.8.2' -o mo-retrieval-bench ./cmd/mo-retrieval-bench
./mo-retrieval-bench validate --pack ./cmd/mo-retrieval-bench/testdata/smoke
export MO_BENCH_PASSWORD='...'
./mo-retrieval-bench run --host 127.0.0.1 --port 6001 --user root \
  --pack ./cmd/mo-retrieval-bench/testdata/smoke \
  --query-limit 2 --repeat 3 --concurrency 1
```

In the prototype release archive, the smoke pack is at `packs/smoke`; use that path with the included executable. Replace it with a separately prepared public data pack for a customer-scale check.

The default output directory has a timestamped name. The command prints its path and refuses to overwrite an existing report. `report.html` is self-contained and `report.json` contains the raw per-query observations and SQL. A failed run still writes both files after pack validation. The process exits nonzero if setup, quality checks, SQL execution, or cleanup fails. The test database is dropped on exit; `--keep-db` preserves it for investigation.

### Default health profile (v0.8.1)

`run --pack DIR` now runs all scenarios in that pack with this profile:

| Setting | Default |
| --- | --- |
| Ordinary queries | All queries, each measured once |
| Client concurrency levels | 1, 4, 8 |
| Unmeasured warmup | 5 queries per scenario/profile |
| Independent stability | Up to 5 queries, each repeated 30 times, concurrency 1 |
| Query/plan timeout | 3 minutes; preparation retains its 10x multiplier |

Provide the pack and connection details; profile flags are optional:

```sh
./mo-retrieval-bench run --pack /path/to/gist-health-pack \
  --host 127.0.0.1 --port 6001 --report-dir reports/gist
```

Explicit `--concurrency N` selects one level when `--concurrency-levels` is not
explicitly supplied. An explicit levels flag wins over the scalar regardless of
argument order; `--concurrency-levels=` falls back to the scalar. All other
explicit values retain their meaning: `--query-limit` reduces ordinary queries,
`--warmup 0` disables warmup, and zero stability limits opt into inheritance.
Paired SQL remains explicitly selected with `--mixed-scenarios ID1,ID2`; the CLI
does not infer pairings for arbitrary extension packs. Retention limits still
apply to the expanded profile before any database is created.

The HTML report starts with charts. A concurrency sweep overlays scenarios in consistent colors for QPS, switchable P90/P95/P99 latency and quality. Each metric uses a zero-based linear axis; percentile views label their own scale. Repeated stability appears as a query-by-repetition grid, with separate states for passed assertions, assertion failures, SQL errors, and missing executions. The grid shows up to 30 queries and 60 contiguous repetition bins, prioritizing errors within a bin.

Numeric tables, measurement metadata, and complete per-scenario charts are collapsed in native expandable details. Reports without concurrency profiles instead show per-query quality bars (up to 30 inputs) and P90/P95/P99 on a shared linear millisecond axis. Zero quality scores remain distinct from missing SQL observations. Exact P50/P90/P95/P99 values and all observations remain available in tables and JSON. Percentiles use successful measured SQL round trips, including quality failures and excluding SQL errors/warmups, with nearest-rank selection `ceil(p*N)`. The chart shows the sample count; for fewer than 100 samples, P99 is the sample maximum. It makes no external requests. Regenerate an older HTML report from its saved observations without rerunning MatrixOne:

```sh
./mo-retrieval-bench render --report-dir /path/to/existing-report
```

This command atomically replaces only `report.html`; `report.json` and its original measurement identity/hashes remain unchanged. The renderer reconstructs missing P90 from per-query observations and labels its own version separately. [The v3 report contract](../../docs/design/field_retrieval_benchmark_v3.md) describes the compatibility and chart semantics.

The bundled eight-row pack is **only a runner smoke fixture**. It now includes a repeated-result stability scenario and requires `--repeat 3` or higher. It cannot support a customer performance claim. The first customer-scale pack should use approximately 1–2.3 million public T2Ranking passages, frozen query/qrels files, and offline BGE-large-zh-v1.5 embeddings with pinned model revision and generation settings. Separate packs can cover LCQMC similar-question matching and GIST1M pure ANN behavior. The on-site host needs only this executable, the selected pack, network access to MatrixOne, and SQL permissions to create/drop a database and create tables/indexes. An optional Grafana integration is future work; unavailable resource metrics are explicit in the report.

## Interactive terminal report viewer

The same standalone binary includes a terminal dashboard. Open saved reports
directly over SSH; Python, a browser, an HTTP service and an active SQL connection
are not needed for viewing:

```sh
./mo-retrieval-bench ui --reports /path/to/reports
./mo-retrieval-bench ui --report-dir /path/to/one-run
./mo-retrieval-bench ui --reports /path/to/reports --dataset gist1m_filtered_v7
```

The catalogue groups runs by the recorded dataset ID, with the newest run first.
Opening a report does not rerun SQL or change any original file. The dashboard
has six pages: an initial concurrency-1 bar chart, concurrency latency/QPS,
recall and relevance quality, repeat stability, SQL/plans, and environment.
Metric definitions open with `?`. Overall assertion status stays visible under
profile selection; missing samples are shown as unavailable.

| Keys | Action |
| --- | --- |
| `d`, arrows, Enter, Esc | Choose dataset and historical run; cancel selection. |
| Left / Right | Switch dataset, opening its newest run. |
| `r` | Next historical run of the selected dataset. |
| Tab / Shift-Tab, `1`..`6` | Change report page. |
| `p` | Cycle P90 / P95 / P99. |
| `c` | Cycle all / measured client concurrency levels. |
| `n` / `b` | Next / previous SQL scenario and profile. |
| Up / Down, `j` / `k`, PgUp / PgDn, Home / End | Scroll. |
| `?`, `q`, Ctrl-C | Metric help, quit. |

Concurrency filtering applies to concurrency and quality; overview always shows
concurrency 1, stability retains its independent repeats, and SQL shows the
explicitly selected scenario. Optional `environment.json` and
`stability-tie-analysis.json` in the same run directory are supplementary saved
evidence. The raw overall status is never overridden by supplementary analysis.
A failed load after switching is visible and clears the old report.

For a script or copied text, select a page and print it without terminal controls:

```sh
./mo-retrieval-bench ui --report-dir /path/to/one-run \
  --plain --section concurrency --percentile 99 --concurrency 8
./mo-retrieval-bench ui --report-dir /path/to/one-run \
  --plain --section sql --scenario-id vector --concurrency 1
```

Non-terminal stdin/stdout and `TERM=dumb` automatically select plain output.
`--width` sets plain-output columns (40..240; default 100). Interactive views
adapt to terminal resizing; at least 40 columns and 12 rows are required.
Discovery scans four directory levels, at most 4096 entries / 128 reports.
A selected regular report file is limited to 64 MiB and the runner's retained
result budget. Symlinks are not followed. Unknown/malformed report headers are
listed as skipped; non-benchmark JSON formats such as the optional ES reference
are not opened as MO measurements. See the
[v8 design](../../docs/design/field_retrieval_benchmark_v8.md).

## Concurrency and result stability

Run a dataset once with multiple client concurrency limits. Each ordinary scenario
runs at each requested level on the same loaded data and index. Stability
scenarios run once at concurrency 1 with their own query/repetition budget:

```sh
./mo-retrieval-bench run --pack /path/to/gist-health-pack \
  --query-limit 100 --repeat 1 --warmup 10 \
  --concurrency-levels 1,4,8 \
  --stability-query-limit 5 --stability-repeat 30 \
  --report-dir /path/to/gist-health-report
```

`--concurrency-levels` takes up to eight unique values in 1..128 and overrides
`--concurrency` for ordinary scenarios. Its order is the execution order. The
shared pool supports the largest level, and workers/sessions finish before the
next profile starts. SQL workers have ready pinned connections before timing;
idle sessions are drained between profiles to reset session parameters. A sweep retains at most 100,000 executions and 1,000,000
result IDs in total; profiles are checked before creating a database. Zero
`--stability-repeat`/`--stability-query-limit` inherits `--repeat`/`--query-limit`.
Since v0.8.1, omitting these flags selects the default health profile above;
explicit single-concurrency and zero/inheritance flags preserve their meaning.

The report groups concurrency observations by scenario, showing QPS and
P90/P95/P99 curves plus exact values, sample counts, measured duration, quality,
SQL errors and assertion failures. SQL-success QPS is successes divided by
measured scenario wall time. The concurrency number is a client in-flight limit;
finite batches and sequential cache warming affect the observed performance.
For `hybrid_rrf`, a successful execution is one fused retrieval request whose
two SQL routes both completed; its QPS counts fused requests and its latency is
the whole request duration. The JSON retains the existing `sql_successes` name.
Endpoint/session counts do not establish CN counts. The stable-multiset section
shows each query's execution/success counts, distinct result sets, worst overlap,
changed IDs, failures and saved physical plans. Stable approximate results still
need the independent recall scenario for quality evidence. See the
[v4 design](../../docs/design/field_retrieval_benchmark_v4.md).

## Pack contract and extension

`manifest.json` lists the dataset, DDL, CSV load files with expected row counts, indexes, and scenario files. Every referenced file carries SHA-256. Paths must stay inside the pack directory. Scenarios are declarative JSON and contain one parameterized `SELECT` or two `SELECT` statements for `hybrid_rrf`, a query JSONL file, a result limit, and an oracle:

| Oracle | Meaning |
| --- | --- |
| `exact_ids` | Ordered result IDs must match the frozen answer exactly. |
| `ann_recall` | Fraction of exact top-K IDs retrieved by ANN. |
| `qrels` | nDCG@K from frozen relevance grades. |
| `nonempty` | Query must return at least one row. |
| `stable_multiset` (v2) | Repeated identical queries must return the same ID multiset; optional `exact_ids` also checks truth. |

Each query line gives an ID, named parameters, and oracle data (`exact_ids` or `relevance`). Add a customer issue as a new scenario and query file, then update the manifest with their hashes. A scenario can reproduce filtering, access restrictions, soft deletion, a specific SQL plan, ANN recall, hybrid rank fusion, or repeated-result drift. Hybrid scenarios use `candidate_k` per SQL route before fusing to `top_k` final IDs, so a CA-assistant-like pack can retrieve 600 candidates per route and report the final top 10. `--query-limit`, `--repeat`, `--concurrency`, and `--warmup` select a runtime profile without changing the pack. For a multi-CN stability test, add `--query-endpoints cn1:6001,cn2:6001`; the runner pins one session per endpoint, applies scenario `session_sql`, rotates identical queries across endpoints, and records `EXPLAIN PHYPLAN`. Use `plan_must_contain` in the scenario to require evidence of the intended distributed plan. Stability checks run sequentially and report their effective concurrency as 1. The result report records the actual count and profile. All pack SQL is trusted release content; review it before shipping and use a SQL account scoped to the test environment.

Before a large pack is used for customer reporting, freeze the public source revision/license, row ID mapping, embedding model revision and normalization, exact-search method, relevance judgments, checksums, and a successful reference run. Keep CSV and query inputs in the pack; do not embed multi-gigabyte data in the binary. `docs/design/field_retrieval_benchmark_v1.md` records the v1 boundaries and future extensions.

## Preparing public-data packs

`tools/prepare_public_pack.py` runs **before delivery**, using only the Python standard library. It streams source files into a versioned pack and records source SHA-256 digests in `source.json`; the runner records the generated CSV and scenario digests in its report. The on-site runner still needs no Python. Example:

```sh
python3 cmd/mo-retrieval-bench/tools/prepare_public_pack.py t2ranking \
  --source-dir /path/to/T2Ranking/data \
  --out /path/to/packs/t2ranking_100k --rows 100000 --queries 20
python3 cmd/mo-retrieval-bench/tools/prepare_public_pack.py gist \
  --source-dir /path/to/gist \
  --out /path/to/packs/gist_1m --rows 1000000 --queries 100 \
  --nprobes 20,100 --stability-queries 5 --top-k 100
```

The T2Ranking pack uses the first selected passages and only dev judgments for passages in that subset. Its nDCG is a pilot observation, not the official full-corpus benchmark. A GIST subset uses `nonempty` because the published nearest-neighbor truth was computed against all one million vectors; only a full GIST pack enables `ann_recall`. `--nprobes` adds scenarios with session-scoped `SET probe_limit` beside the server-default setting, allowing recall and latency to be compared on the same index. Add `--skip-default` to generate a pack containing only the explicit probe settings. SQL scenarios that use `session_sql` pin and configure one connection per query worker; the setting also applies to their EXPLAIN and warmup queries. Both generated packs should be validated with `mo-retrieval-bench validate --pack DIR` before a run. T2Ranking vector and hybrid scenarios still require separately generated, pinned embeddings and exact vector truth.

`passed` means the pack's explicit query/quality assertions passed in this run. SQL QPS, latency percentiles, and mean quality score include every query whose SQL completed, including queries that failed a quality assertion; SQL errors are counted as failures and excluded from those three metrics. Throughput and latency are observations with no universal pass line, and they exclude embedding generation, application reranking, and LLM work. A stable result is not necessarily a correct result; freeze `exact_ids` when the correct set is known. The eight-row smoke fixture cannot prove the multi-CN IVF fix. For cross-run comparisons, use the same pack digest, MatrixOne version, hardware/load conditions, and runtime profile. `docs/design/field_retrieval_benchmark_v2.md` records the stability contract and remaining customer-regression pack work; it is also included in the release archive.


For GIST, `--top-k` controls returned neighbors, the exact truth width, and
companion stability `expected_rows` together (default 10). The installed official
GIST truth has 100 neighbors per input, so a full pack prepared with `--top-k 100`
measures Recall@100 and repeats a 100-ID set. A shorter truth file is rejected;
results from different K values are separate measurement profiles.


The current GIST health profile uses client concurrency 1, 4 and 8. Project an
existing report onto these already measured levels without rerunning SQL:

```sh
./mo-retrieval-bench render --report-dir /path/to/gist-health-report \
  --concurrency-levels 1,4,8
```

All displayed plots, numeric tables and latency scales use the selected
ordinary profiles; independent stability scenes remain. The selector rejects
missing levels. Original JSON, identity, overall verdict and complete error
records remain unchanged; the HTML labels the selected scope. Omit this render
option to show the complete original run.

## anli-shaped T2Ranking pilot

The public dataset supplies text and relevance judgments; it does not require
submitting an entire question as one Boolean search fragment. To compare the
anli snapshot's input and SQL independently, prepare a new pack off site from
an existing T2Ranking pack:

```sh
# Preparation environment only: jieba==0.42.1, spacy==3.8.7,
# zh_core_web_sm==3.8.0. Customer runtime needs none of these.
python tools/prepare_anli_pack.py --base-pack /path/to/t2ranking-pilot \
  --tokenizer-dir /path/to/snapshot/rag_business/commons/utils/tokenizer \
  --top-k 100 --candidate-multiple 100 --stability-queries 5 \
  --out /path/to/t2ranking-anli
```

The preparer preserves original query IDs and subset judgments, freezes original
and tokenized inputs, verifies base file hashes, and records tokenizer/resource
versions. It creates four fulltext scenarios: original sentence/TF-IDF,
application tokens with the same SQL/TF-IDF, application SQL/TF-IDF, and the
same application SQL/BM25. All share one ngram index. The application shape uses
a nested 100x candidate limit, permission/deletion filters, score/time/ID
ordering and the application's post-limit score cutoff. OPEN permissions and
the constant timestamp are synthetic; this is not a selective permissions,
customer-data, hybrid-fusion or reranking measurement. Customer parser and
scoring configuration must be collected separately.

```sh
./mo-retrieval-bench validate --pack /path/to/t2ranking-anli
./mo-retrieval-bench run --host 127.0.0.1 --port 6001 --user root \
  --pack /path/to/t2ranking-anli --repeat 10 --warmup 8 \
  --concurrency-levels 1,4,8 --stability-query-limit 5 --stability-repeat 30 \
  --timeout 30s --report-dir ./t2ranking-anli-report
./mo-retrieval-bench render --report-dir ./t2ranking-anli-report \
  --scenario-ids raw_sentence_tfidf,anli_tokens_tfidf,anli_sql_tfidf,anli_sql_bm25
```

`render --scenario-ids` selects measured ordinary scenes for every displayed
plot/table and retains independent stability scenes. It rejects invalid,
duplicate or absent IDs before replacing HTML. Original JSON and overall verdict
remain intact; the HTML identifies its selection. Primary-key load checks remain
in the raw record and can be displayed by rendering without a selector. All
scoring algorithms are pinned per session; the application's BM25 class name
alone does not establish the customer's actual database setting. nDCG thresholds
are explicit pack checks, not official T2Ranking acceptance standards.

The scope and evidence are recorded in
[design v5](../../docs/design/field_retrieval_benchmark_v5.md).

## anli health matrix (schema 3)

The main workload uses frozen anli query preprocessing and application-shaped
fulltext SQL. Whole-sentence matching belongs to an independently judged
functional pack, alongside OR, AND, quoted phrase and expected empty results.
The application snapshot does not provide index DDL: ngram/gojieba and
TF-IDF/BM25 are explicit comparisons, not claims about the deployed customer.

Prepare off site (the customer runs only the binary and packs):

```sh
python3 tools/prepare_public_pack.py t2ranking \
  --source-dir /data/T2Ranking --out /data/packs/t2-full-500 \
  --rows 2303643 --queries 503 --query-seed 20261006 --relevant-grade 2
python3 tools/prepare_anli_pack.py --health --parser ngram \
  --base-pack /data/packs/t2-full-500 --tokenizer-dir /data/anli/tokenizer \
  --out /data/packs/anli-ngram --top-k 100 --stability-queries 5
python3 tools/prepare_anli_pack.py --health --parser gojieba \
  --base-pack /data/packs/t2-full-500 --tokenizer-dir /data/anli/tokenizer \
  --out /data/packs/anli-gojieba --top-k 100 --stability-queries 5
python3 tools/prepare_fulltext_functional.py --out /data/packs/fulltext-functional
```

`prepare_anli_pack.py` has the same pinned preparation dependencies documented
above; emitted queries need no tokenizer/model at runtime. Corpus CSV files are
hardlinked when possible. Sampling uses the lowest SHA-256 hashes of
`seed:query_id` over eligible dev queries and saves the selected IDs and source
hashes. The first 503 candidates yield 500 searchable queries; 3 empty-token inputs are skipped exactly as BM25Service does and recorded separately in source.json. Legacy general-route fallback is preserved only for non-health packs. A 500-query dev sample is not an official full-test-set evaluation.

Run each parser pack sequentially against the same service:

```sh
./mo-retrieval-bench run --pack /data/packs/anli-ngram \
  --host 127.0.0.1 --port 6001 --report-dir reports/anli-ngram \
  --concurrency-levels 1,4,8 --repeat 1 --warmup 5 \
  --stability-repeat 30 --stability-query-limit 5 --timeout 3m
./mo-retrieval-bench run --pack /data/packs/anli-gojieba \
  --host 127.0.0.1 --port 6001 --report-dir reports/anli-gojieba \
  --concurrency-levels 1,4,8 --repeat 1 --warmup 5 \
  --stability-repeat 30 --stability-query-limit 5 --timeout 3m
./mo-retrieval-bench run --pack /data/packs/fulltext-functional \
  --host 127.0.0.1 --port 6001 --report-dir reports/fulltext-functional
```

For a lighter profile add `--query-limit 50`; it changes selected queries but
still loads/builds the same corpus. Smaller corpus packs reduce preparation
cost, and their scores must carry the corpus scope. The timeout is a per-query
ceiling; import/index preparation gets 10 times that budget.

Schema 3 qrels options:

- `quality_mode: "observe"` requires `min_score: 0`; successful empty rankings
  score zero without becoming acceptance failures.
- `ndcg_gain: "linear"` matches the T2Ranking/trec_eval gain convention.
  Omission retains the historical `2^grade - 1` convention; historical reports
  are not silently recalculated.
- `relevant_grade: 2` counts grades 2/3 for Recall@top_k and MRR@10; graded
  nDCG@10 and nDCG@top_k still use all positive relevance levels.
- `check_order: true` adds ordering assertions to `stable_multiset`.
  `allow_empty: true` allows a consistently empty ranking, with
  `expected_rows: 0`; it does not claim relevance. Legacy defaults are unchanged.
- `exact_ids: []` explicitly declares a known negative functional result.
  Missing exact truth is rejected; ANN truth must be nonempty.

Standalone reports separate execution/functional health, relevance and repeat
stability, while preserving the original combined assertion status and CLI exit
code. Index SQL, metric options and per-execution quality metrics are saved in
raw JSON. Observed low scores alone do not fail health. Current anli packs use
synthetic OPEN permissions/fixed timestamps, candidate limit and post-Top-K
score cutoff 0.01; they measure the fulltext leg, not permission selectivity,
detail fetching, fusion or reranking.

`tools/render_t2_health_portal.py` publishes this fixture's paired chart page
from saved reports with matching corpus/query hashes and explicit parser,
algorithm, Top-K, metric and repeat settings. Copy each pack's `source.json`
and frozen qrels query JSONL (as `query-inputs.jsonl`) beside its saved report
before publishing. Its portal includes this fixture's environment, independent
metric verification, release and historical-report links; carry those artifacts
with the reports. This is an off-site publisher; runtime standalone HTML
needs no Python. Multi-CN sort regression requires a corresponding topology and
physical-plan evidence; a single-CN result does not validate it.

The T2Ranking portal starts with a separate concurrency-1 bar chart for
P90/P95/P99 SQL latency, using one zero-based scale across configurations.
To add the same chart to existing GIST dataset/home pages from their saved run:

```sh
python3 tools/render_single_concurrency.py --reports-root reports \
  --gist-report gist-1m-health-top100-20261006
```

This publisher updates only those GIST pages and can be repeated. It reads
saved measurements; no service or new benchmark run is required.

## Filtered GIST and concurrent database SQL (v0.7)

Offline `tools/prepare_filtered_gist.py` builds a full GIST pack with explicit
PRE/POST and 100/10/1% eligibility, and a separate GIST/T2 SQL workload pack.
Only the preparer requires NumPy/threadpoolctl. The customer runtime remains
the static binary and hashed data packs. Prefix visibility is synthetic;
filtered exact truth is recomputed within each eligible population in float64.
New query `allowed_id_ranges` rejects ineligible numeric IDs independently of
observational ANN recall. Schema 3 ordinary SQL may name `id_column` to read
all business result columns while preserving only IDs in reports.

```sh
mo-retrieval-bench run --pack /data/gist-filtered --report-dir reports/filters \
  --concurrency-levels 1,4,8 --stability-repeat 30 --stability-query-limit 5
mo-retrieval-bench run --pack /data/gist-t2-workload --report-dir reports/workload \
  --concurrency-levels 1,4,8 --mixed-scenarios vector,fulltext --warmup 5
```

The second command runs isolated SQLs first, then both simultaneously, on the
same unchanged data/indexes. At paired concurrency C, at most 2C SQLs execute.
Each route has its own initialized pinned sessions, returned IDs, SQL errors,
latencies, plans and quality. Each job waits for both SQLs before the worker
starts the next pair, so mixed QPS uses the common batch wall time and describes
paired workload completion, not an independent route's maximum capacity.
Oracle evaluation is outside timing for both isolated and mixed workload
profiles. No client fusion or post-filter is performed. Different GIST/text
tables represent general resource competition, not an anli same-table replay.
See `docs/design/field_retrieval_benchmark_v7.md` for bounds and evidence.

Preparation example (offline, NumPy/threadpoolctl installed):

```sh
python3 tools/prepare_filtered_gist.py --source sources/gist \
  --gist-pack packs/gist_1m_health_top100_v1 \
  --text-pack packs/t2ranking_anli_full_500q_ngram_v3 \
  --out packs/gist_1m_filtered_v7 --mixed-out packs/gist_t2_sql_workload_v7
```

The CLI generates a standalone local HTML report for either pack. Optional
offline tools independently verify saved observations and add interactive
charts to the existing dataset portal:

```sh
python3 tools/verify_database_runs.py --report reports/filters \
  --pack packs/gist_1m_filtered_v7 --binary ./mo-retrieval-bench
python3 tools/verify_database_runs.py --report reports/workload \
  --pack packs/gist_t2_sql_workload_v7 --binary ./mo-retrieval-bench
python3 tools/render_database_workloads.py --reports-root reports \
  --filtered-report filters --mixed-report workload
```

The independent verifier expects a completed full profile (100 queries,
1/4/8, five stability queries repeated 30 times for filters). Record terminal
process status as `run-status.json` with `complete: true` and `exit_code` before
publishing. Smaller field profiles still produce their own standalone CLI
report. Large CSV packs are delivered separately from the binary archive.

## Saved Elasticsearch reference measurements

`tools/benchmark_elasticsearch.py` is a standard-library experimental runner
for a loopback Elasticsearch fixture. It imports the same frozen T2Ranking
CSV and compares native CJK/BM25 `match` searches using anli tokens and raw
questions. It preserves returned IDs, timings, input hashes, ES settings and
repeat set/order observations. It creates an owned index and retains it for
follow-up measurement; it never overwrites a saved run. This experiment adds
no dependency to the standalone MO field binary.

```sh
python3 tools/benchmark_elasticsearch.py --endpoint http://127.0.0.1:19200 \
  --pack /data/packs/anli-ngram --output reports/es-first
# Reuse the complete index named in es-first/report.json for a longer run:
python3 tools/benchmark_elasticsearch.py --endpoint http://127.0.0.1:19200 \
  --pack /data/packs/anli-ngram --index mo_retrieval_bench_es_INDEX_ID \
  --repeat 20 --output reports/es-longer
```

`tools/render_es_reference.py` publishes `datasets/t2ranking-es.html` from
saved ES data, independently of MO reports or a live ES service. It validates
the query input hash, full corpus count, metric options, 1/4/8 profiles and
repeat completion. Put the recorded host CPU/core/thread counts, total memory,
OS/kernel and data storage in `host-configuration.json`, the Docker limits
in `environment.json`, and independent recomputation in `verification.json`
beside the ES raw record. The page contains only ES measurements and that
machine's configuration. The field MO report provides a collapsed reference
link when the saved page exists; customer-site health checks use their own
measurements and do not require an ES deployment or per-metric engine comparison.

```sh
python3 tools/render_es_reference.py --reports-root reports --es-report es-longer
```

See
[the experiment design](../../docs/design/field_retrieval_benchmark_es_v1.md).

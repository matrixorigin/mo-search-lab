# Elasticsearch reference records, revision 1.2

## Scope and design decision (2026-10-06)

Current user direction: retain ES measurements and this host's configuration
as a saved reference. Customer-site health reports evaluate their own service
and do not depend on a local ES deployment or per-metric engine comparison.
Implementation stays in the independent retrieval worktree. The delivered Go
field binary and its runtime dependencies are unchanged. The initial cross-engine
experiment's measurement/review evidence below is retained as history; the
reference-only publication contract in revision 1.2 supersedes its paired UI.

Design-first review: PASS before implementation. Trigger: a bounded concurrent
HTTP measurement client, external protocol, and customer-facing benchmark
claims. No issue/PR is requested. The existing v6 implementation is preserved;
only the ES runner, its contract tests, an ES comparison publisher, navigation,
and this experiment's documentation are in the incremental change scope.

## Contract

Same frozen documents (2,303,643), same 500 query IDs and relevance judgments,
same client concurrency 1/4/8, Top-100, linear nDCG@10/@100, grade >=2
Recall@100/MRR@10. Corpus and query SHA-256 values must match MO artifacts.
Every returned ID, response duration, error and ES `took` is retained. Zero
SQL/HTTP errors is an execution verdict, not a relevance acceptance claim.

Use the official pinned Elasticsearch 9.5.4 image, single node, one primary
shard, zero replicas, 8 CPU and 16 GiB container limit, explicit 8 GiB JVM heap.
Use built-in CJK analysis and default BM25 (k1=1.2, b=.75). Measure both frozen
anli token text and raw question text with `match`, operator OR. Search returns
IDs with `_source:false`, `track_total_hits:false`, `_score DESC,id ASC`,
`min_score:.01`, no highlighting/fetch/reranking. The original source is stored
at index time. Disable request cache explicitly. Preserve native caches/page
cache; this is a warmed, sequential experiment, not a cold-cache benchmark.

Important limits: CJK is not identical to MO ngram/gojieba, BM25 implementation
and score scales may differ, and ES native Top-100 doesn't reproduce MO's
candidate-layer LIMIT 10000 or Join/Sort plan. Constant OPEN/date/delete values
in MO have no selective filtering effect; ES omits these constants. Cross-engine
results compare practical retrieval routes and cannot isolate engine-only
costs. MO runs precede this ES run; shared-host/background/cache effects remain.
Report those facts in collapsed scope details and label each route clearly.

## Ownership, transitions, budgets

One runner owns one new index with an exclusive generated name and one new
output directory. It never overwrites a prior run or deletes an existing index.
Create -> stream bounded bulk -> refresh/count check -> wait for active merges
to finish with a bounded deadline -> measure -> stability -> save final record.
No retry masks bulk/search failure. Bulk concurrency <=4 with <=4 pending
chunks, each <=2000 documents or about 8 MiB plus one maximum-sized row.
Search workers <=8; one persistent HTTP connection per worker, closed in a
finally scope. Per-request timeout 180s, no unbounded accumulating future list.
Results are bounded by 2 routes x 500 x 3 profiles x20 repetitions plus
2 x5x30 stability. Default repeat remains 1. The final comparison uses repeat
20 because the initial repeat-1 4/8 profiles finished in under one second;
short bursts do not establish sustained throughput. Both runs are retained,
and the different MO/ES measurement counts are visible in the page.
Metrics are scored after latency stops and after measured profile wall time
stops. Worker queue wait and connection warmup are excluded from query latency.
All successful returned IDs are consumed before stopping the timer. QPS is
successful requests divided by profile wall time, not inverse percentile.

On error/interruption save a failed/partial report with the error; don't publish
it as a complete comparison. Preserve owned index for diagnosis and explicit
follow-up runs, recording its identity and count. Do not stop other services.
Bulk/index preparation, background merge settlement and input hashing are not
query performance. Docker container is loopback-only, benchmark-owned and has
no public credentials or outbound integration. Poll only known services; no
process argument dumps.

Stability uses five frozen queries, 30 repetitions per route, one client worker,
and records set/multiset AND ordered signatures, including valid empty results.
It does not establish multi-node/multi-CN stability.

## Alternatives and rollout

- Default standard analyzer: less suitable for Chinese phrase retrieval.
- Third-party IK/jieba plugin or offline tokenizing 2.3M documents: additional
  dependency/version/input transformation; defer until basic native baseline
  demonstrates the need for a token-identical experiment.
- ES adapter in the production Go binary: broader pack/transport lifecycle
  changes; defer. This exploratory standard-library runner is off-site only.

Keep measured raw records immutable. A separate T2Ranking ES reference page
contains only the two ES query routes, quality/QPS/P90/P95/P99, repeat results,
raw records, request configuration, and frozen host/container configuration.
The renderer consumes only saved ES artifacts and a shared static metric help
fragment; it does not read MO report data or call ES. The MO report offers a
collapsed optional reference link and shows its own measurements. Publish HTML
atomically after the saved query digest/cohort, indexed corpus count, profile
and repeat-completion checks. Existing field binary identity remains v0.6.0.

## Validation and review map

| Closure | Risk | Proof |
|---|---|---|
| ES HTTP worker/bulk ownership | R3, bounded state and lifecycle | fake HTTP negative/partial/timeout responses, worker terminal closure, real 2.3M import and all profile outcomes |
| metrics/stability | R2, public measurement claims | independent known-answer linear nDCG/Recall/MRR/percentile examples; ordered vs set counterexample; independent raw-run recomputation |
| comparison publication | R2, input/label integrity | reject mismatched digest, partial result, duplicate/missing profile; browser chart numeric checks, navigation/mobile overflow |
| docs/navigation | R0 | inspect incremental diff and links; preserve old raw report hashes |

No Go edits or kernel behavior changes: no Go/CGo/UT/BVT/kernel CI rerun.
Real measurements prove ES protocol/scale, not an MO kernel change. No runtime
speed threshold is asserted by unit tests.

## Primary references

- [Official Docker installation](https://www.elastic.co/docs/deploy-manage/deploy/self-managed/install-elasticsearch-docker-basic)
- [Language analyzers / CJK](https://www.elastic.co/docs/reference/text-analysis/analysis-lang-analyzer)
- [BM25 similarity](https://www.elastic.co/docs/reference/elasticsearch/index-settings/similarity)
- [Match query](https://www.elastic.co/docs/reference/query-languages/query-dsl/query-dsl-match-query)
- [Search API](https://www.elastic.co/docs/api/doc/elasticsearch/operation/operation-search)

## Terminal evidence

Design decision revision 1.1: PASS. Add a longer repeat-20 run because the first
repeat-1 4/8 batches finished in less than a second. Retain both records and
chart only the longer run for ES performance, with counts disclosed. Docker
disk allocation was initially blocked by default percentage watermarks on a
nearly full 2 TB host filesystem; the benchmark-owned ES cluster now uses
absolute free-space watermarks 15/10/5 GiB. No host or MO setting changed.

Range: HEAD `1f6b6d17c0e7c2ff7981be9a2f85fa9ec3a83650`, local origin/main
`c65991043b10fb39988d45b72af1485560ac5433`, merge-base
`d99187d7b3bfda8744088b3ae8cf16739b690aa0`. No remote PR/push/CI mutation;
remote freshness is not used for a merge claim. Prior v6 evidence is reused
for the unchanged Go field client and MO raw measurements.

Implementation review: PASS, no unresolved blocker. Incremental scope is the
ES runner, its tests, comparison publisher, conditional navigation in the MO
publisher, tools ignore rules, README and this design. Review covered bounded
bulk/search ownership, connection closure, cancellation/timeout, partial HTTP
failure, explicit input/topology/scoring identity, metric/gain definitions,
query cohort and repetition budgets, and publisher consumers. Index reuse
requires actual owner, corpus metadata, CJK/BM25 mapping and 1/0 shard topology.
The stronger reuse preflight was added after measurement; its predicates were
also verified against the actual measured index and saved in environment.json.
The measured runner snapshots remain byte-identical to their recorded SHA.

New evidence (2026-10-06):

- Seven ES/measurement/publication contract tests, plus eight unchanged pack
  contract tests: 15 PASS. No model, SQL service or Docker in these UTs.
- Real two-document ES protocol probe: both text routes and all 1/4/8 profiles,
  30-repeat checks passed; its owned temporary index was removed.
- Official image digest
  `sha256:82ac14f43fe701992e601f4cc81e1c0d7dbc5a2576d8cd736006452925df4026`.
  Running server reports ES 9.5.4 / Lucene 10.5.1, one node. MO and ES remain
  running, zero restart and OOM flags; CPU/memory limits match 8 / 16 GiB.
- `t2ranking-es-cjk-full-500q-20261006`: 2,303,643 rows imported and counted;
  3000 measured searches and 300 repeats, zero HTTP failures, exit 0.
- `t2ranking-es-cjk-full-500q-20r-20261006`: reuses that complete index, 60,000
  measured searches and 300 repeats, zero HTTP failures, exit 0. All ten
  stability query/route cells have one set and one order.
- Independent verifier recomputed every one of 63,000 quality observations,
  both runs' QPS and nearest-rank latency quantiles, and 600 repeat outcomes.
  Each verification.json records raw/input hashes and its verifier SHA.
- Longer-run raw JSON is retained, with a gzip download; its decompressed
  SHA-256 equals the verified raw JSON. Run-specific runner snapshots and the
  verifier are retained beside the data. Large records are downloads.
- Browser check: desktop 1280 and mobile 390, six labeled series verified
  against independent proofs, all four configuration filters, five quality
  metrics, three latency percentiles and linear/log scales; actual SVG values,
  300 repeat cells, local links, navigation/reload/back and overflow PASS.
  No page errors/external asset requests; screenshots inspected.
- All 27 earlier raw report hashes remain unchanged. GIST raw measurements
  and the standalone binary/archive are unchanged. Python validation and
  `git diff --check` pass. No Go/CGo/kernel UT/BVT/CI rerun is warranted by this
  incremental off-site experiment.

Primary report URL through the existing SSH tunnel:
`http://127.0.0.1:18766/datasets/t2ranking-es.html`.

## Revision 1.2: reference-only publication

User explicitly rejected per-metric customer-site engine comparison. The
publisher is now `render_es_reference.py`; it needs only a saved ES folder.
Remove MO series, matching-MO-resource requirements, engine filters, comparison
language and the prominent engine-comparison navigation. Keep the old page URL
for existing bookmarks. ES reference graphs contain two routes, not MO data.

Host configuration is recorded separately in `host-configuration.json`, with
collection time after the measurement: Intel i7-11700, 8 physical cores /
16 logical CPUs, 31.08 GiB OS-visible memory, Debian 13 / x86_64, NVMe
YMTC PC411-2048GB-B (2.05 TB device) with ext4 mounted at /d. ES container
limits stay 8 CPU / 16 GiB with 8 GiB heap, separately labeled from host totals.
No transient free-space/free-memory value is presented as hardware capacity.

Incremental scope: reference publisher and its consumer test, shared MO
publisher navigation, ignore entry, README, this design, generated HTML and new
host-configuration records. No runner, benchmark, raw-query or Go changes.
Design decision: narrower presentation/consumer maintenance; no new stateful
feature. Review covers independent saved-input validation, accurate host vs
container labels, preserved metric help, and mobile chart/configuration display.

Revision 1.2 terminal evidence (2026-10-06):

- All 15 Python contract tests pass, including publication from an ES-only
  fixture and rejection of incomplete/mismatched saved inputs.
- Desktop 1280 and mobile 390 browser checks pass: two ES-only chart series
  match independent verification, configuration panels and metric help fold,
  the MO reference link is optional and folded, 300 repeat cells and local
  downloads work, and there are no page errors or horizontal overflow.
- The owned ES test container is stopped; its data and image are retained.
  The MO test container remains running. The reference publisher succeeds
  with ES stopped, and both report pages return HTTP 200.
- All 27 historical raw reports and both ES raw reports retain their hashes.
  MO plotted data is unchanged. The main working directory remains clean.
  `git diff --check` passes. No new benchmark or Go/kernel tests are needed
  for this saved-record presentation change.

# Field retrieval benchmark v7: filtered ANN and concurrent SQL workloads

## Authorized scope and design decision

2026-10-06: the user approved implementing the smaller next step: reuse GIST1M
and T2Ranking, add effective database filters and concurrent SQL measurement.
Client fusion, client metadata filtering, reranking, text embedding generation,
ES deployment and kernel changes are outside this increment. Work remains in
the retrieval-bench worktree; historical reports and main checkout stay intact.

Design revision 1 review decision: PASS before implementation. The trigger is
CLI/report compatibility plus a concurrent client lifecycle. This document
records the local design review; no issue, PR, push or external review is owned
or authorized. Base/head/merge-base reuse the v6 recorded worktree identity.

## Data and filter contract

Prepare a new versioned schema-3 pack from the unchanged full GIST base. Store
visibility_rank=id as synthetic metadata. Cutoffs N, N/10, N/100 produce exact
100%/10%/1% visible populations. This is a reproducible prefix partition, not a
claim about customer role or cluster distributions. Compute exact L2 Top-100
separately within each eligible population from the original f32 vectors;
never filter the published unfiltered Top-100 to construct a new truth.
Freeze NumPy/tool version, source hashes, distance precision, tie rule and
generation settings. ID bounds in query JSON independently assert eligibility.

Use explicit `by rank with option 'mode=pre'` and `'mode=post'`, with the same
IVFFlat 1000 lists and probe_limit=100. Record normal and physical plans.
A real owned three-row probe on MO 4.2.1 confirmed PRE has the filtering
membership join feeding ivf_search; POST has a larger candidate limit followed
by the residual join; FORCE scans vectors exactly. Preserve the actual plans
rather than labeling a WHERE clause as PRE. Return counts and recall are
observations; SQL and eligibility failures are failures. Add repeated set/order
checks (5 inputs x 30 repeats); underfilled rankings remain visible.

## Concurrent SQL execution contract

Add `--mixed-scenarios id1,id2` for two existing ordinary SQL scenarios in the
same pack. Run isolated profiles first, then concurrent profiles on the same
loaded data/indexes. At concurrency C, C client workers each issue both SQLs
concurrently, wait for both, then admit their next job. Maximum SQL concurrency
is 2C. The two input sequences are paired by deterministic ordinal; they do not
need common query IDs or document IDs. This represents generic shared-resource
competition across GIST and text tables, not an anli same-table reproduction.

Each worker owns two pinned sessions, initializes the respective SET statements
once and closes every session after all its workers terminate. Preparation,
plans and warmup finish before timing starts. Per-route SQL latency ends after
rows are fully read. Oracle evaluation and aggregation run after the timed
batch. Both route QPS values use the shared batch wall time and count only that
route's completed SQLs; the report explains that request pairing limits each
route's throughput. No RRF or fused IDs are produced.

Raw reports add execution_mode and base_scenario_id for mixed scenario records;
mixed IDs use a reserved suffix and cannot collide with pack scenario IDs.
All planned results are present on preparation failure. Existing single-route
and hybrid packs keep their previous behavior. Result limits include the added
mixed profiles before any database is created. CLI selection is prevalidated.

## Ownership, termination and bounds

State: validate -> allocate result placeholders -> create/load/index owned DB ->
isolated measurement -> mixed sessions/setup -> plans/warmup -> workers ->
join -> evaluate/aggregate -> close sessions -> drop DB -> write report.

The mixed coordinator owns all sessions, results and the bounded jobs channel;
one worker owns each pair of sessions and each result index. Each job launches
two children and joins them before reusing sessions. The coordinator joins all
workers before closing sessions. Query/plan/setup calls have context deadlines;
partial acquisition/setup returns through deferred session cleanup. Parent
cancellation reaches SQL contexts. Buffered jobs are bounded by C and retained
results by existing 100k executions / 1M IDs limits including added profiles.
One branch failure remains a branch failure; successful sibling observations
are retained, and no retries conceal errors. There is no persistent background
worker, retry state or state shared between measurement generations.

## Change map and validation

| Closure | Risk | Required evidence |
|---|---|---|
| ID bounds, optional SQL ID column, observational ANN | R2 | invalid/schema/eligibility/quality tests, legacy package tests |
| mixed CLI/profile expansion/session lifecycle | R3 | deterministic overlap and session tests, branch/setup/cancel failures, race test |
| GIST metadata/exact truth/mixed pack preparer | R2 | small direct-distance control, immutable source/input hashes, real FORCE spot checks |
| static publishers and standalone HTML | R2 | isolated vs mixed identity, real browser/chart numbers, desktop/mobile, local downloads |
| static binary/release and live MO fixture | R2 | complete owning-package/static checks, static build, real full-scale terminal run, independent raw verification |

Alternatives: separate independent processes lack a common concurrency/profile
contract and traceable report; RRF reuse confounds database and client behavior.
The selected bounded paired SQL mode reuses existing pack SQL/oracles and keeps
each route observable. Schema 3 gains optional numeric allowed_id_ranges and
ID column for ordinary SQL; old packs need no migration. ANN observe remains
explicit and cannot silently waive eligibility or SQL failures.

Publish filter charts within the GIST dataset page and an optional concurrent
SQL section within the T2Ranking page. Preserve chart-first layout, Top-100,
1/4/8, the first concurrency-1 bars, separate dataset routes and the existing
saved ES reference. Large data packs remain separate from the static binary.

## Implementation and review record

Self-delivery review, 2026-10-06/07. Main checkout remains clean. This increment
is layered over the existing uncommitted v1-v6 work; no commit or kernel file is
changed. Head is `1f6b6d17c0e7c2ff7981be9a2f85fa9ec3a83650`, local base
`c65991043b10fb39988d45b72af1485560ac5433`, merge-base
`d99187d7b3bfda8744088b3ae8cf16739b690aa0`. A read-only remote verification
returned main `5669b1683cf2188652c4ea56390e6a77ab5d13d8`; that newer object was
not fetched or merged. This is an incremental client delivery review, not a
kernel PR review against remote main.

| Changed closure | Files / consumers | Reviewed contract and evidence |
|---|---|---|
| schema-3 pack options | pack.go -> oracle.go/stability.go; mixed_test.go | ordinary ID projection, ANN observe and eligibility independent of quality; two-integer ranges, sorted/nonoverlap/max256 and legacy schema rejection; owning UT |
| mixed profile admission | main.go -> mixed.go/run.go | exactly two ordinary SQL IDs, suffix collisions, equal query counts/repeats, retention includes both isolated and mixed results; negative controls |
| sessions, workers, timing | mixed.go and isolated run.go | 2C sessions, per-route SET, two children per paired job, bounded queue C, exclusive result indexes, join before close, successful sibling survives error; deterministic barriers, setup/branch/cancel tests, race |
| plan/result readers | run.go/stability.go | borrowed session ownership, statement deadline, deferred rows close, rows.Err, bounded 1MiB plans, read all projected columns; actual MO EXPLAIN/PHYPLAN |
| frozen public packs | tools/prepare_filtered_gist.py | source hashes, original query-vector identity, float64 direct-distance control, filtered population before ranking, ties by numeric ID; FORCE sampled truth |
| raw report -> HTML/portal | report.go/report_load.go/report_overview.go; render_database_workloads.py; render_t2_health_portal.py | mixed identities and paired QPS note, user's removed cards also removed from standalone CLI HTML; atomic publication, selected ratios/percentiles, input/plan links, retained T2 fragment |
| independent proof | tools/verify_database_runs.py | recompute every successful score, QPS, nearest-rank percentile, set/order statistic and ID eligibility from frozen inputs/raw IDs; recorded binary/pack identity |
| delivery | README, this document, static executable and archive | field runtime has no Python/native dependencies; large packs separate; saved prior raw reports preserved |

Applicable lenses: all rows cover functional/default/error and compatibility
contracts; mixed execution covers ownership/cancellation/wait/bounds and timing;
publishers cover actual browser consumers; delivery covers Linux amd64 static
build identity. SQL packs keep existing statement validation and owned database
boundaries. Persistence/restore/upgrade, MO tenant/auth changes, GPU, native
usearch, distributed protocol and kernel BVT are not changed. Existing v6
fulltext/tokenizer/raw-quality proofs remain valid for unchanged inputs.

Owning Go package tests, race tests and vet passed. Golangci-lint returned
0 issues. All 15 existing Python tests passed; the old portal-card expectation
was corrected to match the user's earlier card-removal request. The final card
and report-note edits do not change concurrent execution; its race evidence is
reused. Source/binary digests are captured for the delivered measurement.

### Filter measurement evidence

Saved run: `/d/mo-worktrees/retrieval-bench-data/reports/gist-1m-filtered-v7-20261006`.
24 profiles: 18 ANN profiles x 100 queries, plus six stability profiles x five
queries x 30 repetitions. All 2700 SQLs succeeded and every returned ID obeys
its eligibility bounds. Independent verification reproduced every metric.
The run exits 1: 73 strict-order assertions failed. These failures are retained;
all 900 repeat observations have unchanged result sets. A byte comparison of
the original fvecs confirmed every changed position swaps identical vectors.
The SQL orders by distance alone, so it does not specify a total ID order for
ties. This is not evidence of changing recall sets or a multi-CN sort bug.

Concurrency-1 Recall@100: PRE 100/10/1% = 0.9738 / 0.9389 / 0.8424;
POST = 0.9739 / 0.1454 / 0.0175. Mean returned counts: PRE 100 throughout;
POST 100 / 14.54 / 1.75. The saved POST plan requests 150 global candidates,
then applies the residual filter; the PRE plan has a membership join feeding
ivf_search. Both keep 1000 lists / 100 probes. The numbers describe this frozen
prefix partition and index, not every customer permission distribution.

Real MO4.2.1 `mode=force` scans for the first frozen query in all three visible
populations returned exactly the frozen Top100 sets. The retained owned
database was dropped after these checks; its original raw report still records
the requested keep-db state. `force-verification.json` records subsequent
cleanup. Source official full-population truth matches 97/100 frozen sets;
`oracle-comparison.json` preserves the tie/float precision boundary differences.
The old official truth and historical reports are unchanged.

Filter measurement executable SHA256:
`39b66403c01c3d55fbddc4c1533c4b95f61cf478e4945ace64f41c5c5887581b`.
Its immutable copy is `mo-retrieval-bench-linux-amd64-v0.7.0-filter-measurement`.
The final binary only changes mixed/isolated workload timing, bounded plan
reading/metadata validation and report presentation after that build; filtered
SQL/oracle semantics and input pack are unchanged. Regenerating its HTML does
not rewrite `report.json` or claim that the final binary made the older run.

Desktop/mobile browser checks confirmed every filter and percentile chart
value against independent proof, all 900 repeat cells including 73 failures,
local input/raw/proof links, first concurrency-1 bars, and no page overflow or
external requests. Profiles ran sequentially on a single-CN MO4.2.1 fixture,
8 CPU / 16GiB. CPU was not pinned; the host also ran the client and light
development checks. Cache/profile order can affect tails. Resource quotas are
saved configuration, not utilization samples. No Grafana was connected.

### Concurrent SQL measurement and delivery evidence

Saved run: `/d/mo-worktrees/retrieval-bench-data/reports/gist-t2-sql-workload-v7-20261006`.
1M vectors plus 2,303,643 text rows, both indexes in one owned database. Twelve
profiles: two routes, isolated/paired, 1/4/8; 100 fixed queries per profile.
All 1200 measured SQLs succeeded; independent quality/latency/QPS recomputation
passed. Exit 0, cleanup dropped, fixture restart count 0 and OOM flag false.

| Paired concurrency | Vector P95 isolated / paired (ms) | Fulltext P95 isolated / paired (ms) |
|---|---|---|
| 1 | 735.177 / 954.369 | 933.043 / 1349.330 |
| 4 | 3682.339 / 2879.846 | 2085.822 / 4691.417 |
| 8 | 7949.836 / 5473.457 | 3291.445 / 8844.580 |

Paired QPS per route is 1.06 / 1.31 / 1.29 (common batch duration). Isolated
vector QPS is 1.77 / 1.63 / 1.42; fulltext 1.84 / 3.56 / 3.58. Request pairing
changes admission/pacing: a faster route can wait on its sibling. Consequently
the lower vector tail under paired concurrency 4/8 is an observation under
that workload, not a standalone capacity improvement. The report displays
both latency and throughput, labels the 2C SQL ceiling and shared QPS window.

Delivered static Linux amd64 binary v0.7.0 SHA256:
`ec2f074c58d9163c1918851122f052f75135b76432b36cf564929a2785437cca`.
This is the measured paired executable. The final Go input digests are unchanged
since that build; the old filter executable is retained separately. Binary,
README, all necessary offline helper imports, design documents and small smoke
packs are delivered in the v0.7.0 archive; large frozen CSV packs stay in /d.

Browser consumers passed at 1280px and 390px: all new plotted numbers and all
selectors agree with independent verification; input/raw/config/proof links
resolve; existing dataset routes, first concurrency-1 bars, original four
fulltext series, folded metric explanations and saved ES reference remain.
The report contains no removed summary cards. Publication uses markers and
atomic replacement; repeated publication and T2 regeneration retain a single
workload panel/style. Historical raw report hashes remain unchanged.

Delivery review decision: PASS. The strict-order observations remain visible
with their independently verified same-vector tie explanation. This review
does not claim a multi-CN fix, same-table anli reproduction, universal quality
acceptance threshold or independently saturated mixed-route QPS. Further
customer cases use versioned SQL packs and their own explicit oracles.

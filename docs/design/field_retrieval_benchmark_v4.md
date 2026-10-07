# Field retrieval benchmark v4: concurrency and GIST stability

## Chart-first presentation refinement (2026-10-06)

Following the user's request to prioritize charts, the default report starts
with dataset-wide QPS, switchable P90/P95/P99 and quality plots. Keep consistent scenario colors and categorical client-concurrency axes.
Quality plots separate recall/nDCG/boolean-oracle metrics; means do not replace
per-query assertions. Empty or failed profiles have gaps, never invented zeros.
Repeated stability uses a query-by-repetition grid with explicit success,
assertion-failure, SQL-error and missing states. Bound grids to thirty queries
and sixty repetition bins; show exact ranges/counts for aggregated cells and
retain complete tables in expandable details. Metadata, stages, numeric tables
and per-scenario plots remain available below the primary charts. Inline SVG,
CSS and native controls only, with accessible labels and no external requests.

This is a renderer-only R2 observation/view change plus R1 layout change.
Validate derived plot values, missing samples and grid bins with focused tests,
inspect the actual GIST report in desktop/mobile browsers, and regenerate saved
HTML while checking every raw report JSON digest. No SQL or dataset rerun.

Status: accepted for the local prototype on 2026-10-06, following the user's
request to add concurrency and result-stability tests and reports. Local design
review: additive execution profiles within the existing pack/runner/report
boundary; no MatrixOne kernel change or new customer runtime dependency.

## Scope and invariant

Keep dataset → scenario → execution-profile → observation as the report model.
One run loads the dataset and builds indexes once. On this unchanged database,
run each ordinary scenario at the requested client concurrency levels, in the
specified order. Run each stability scenario once, sequentially, with an
independent repetition/query budget. CN topology is not client concurrency;
record SQL entrypoints and physical plans without inferring CN count from
endpoint or session count. The available trial environment is one CN.

Add `--concurrency-levels 1,4,8,16` (up to eight unique levels, each 1..128).
Without it, preserve `--concurrency`. Add `--stability-repeat` and
`--stability-query-limit`; zero inherits the existing corresponding flag.
Validate profiles and observation-retention limits before database creation.
Each sweep retains at most 100,000 executions and 1,000,000 result IDs in total;
single-profile runs retain their existing per-scenario caps.
The shared pool is sized for the largest requested concurrency. Workers and
pinned sessions remain owned by each existing scenario call and finish/close
before the next profile begins. No simultaneous scenarios or new scheduler.
Seed planned profiles with unexecuted metadata before opening connections, so
preparation failures publish explicit missing measurements instead of empty
or zero-valued performance claims. Socket read/write ceilings accommodate the
existing 10× preparation budget; SELECT/plan contexts retain the query budget.
The first full-data attempt proved the old shorter socket deadline truncated
CREATE INDEX at 30 seconds even though the stage allowed 300 seconds.
Design refinement before the full-data trial: pin a ready connection for every
ordinary SQL worker, including scenarios without SET statements, so connection
establishment is outside the measured batch. Drain idle pooled sessions before
each profile, after the prior call has joined workers and returned leases, to
prevent session SET values leaking into a default-parameter scenario. Hybrid
routes retain their pool-based two-route execution. Reusing one connection pool
with bounded, sequential generation resets avoids a new pool owner per profile.

Reports keep the pack's scenario ID/digest and record the effective client
concurrency, measured duration, successful SQL count, SQL error count, and
assertion failure count. Successful SQL with bad recall remains in latency/QPS
statistics. QPS is successful SQL executions divided by measured wall time;
for the existing hybrid route, the execution unit is one fused retrieval request
with both SQL routes completed, rather than each internal SQL. Label this unit
explicitly in its report. Retain the existing JSON counter names for compatibility.
warmup, plans, setup, and data import are excluded. Concurrency is a client
in-flight limit, not proof of a sustained arrival rate or peak server capacity.
Finite query batches and sequential cache warming are explicitly disclosed.

## GIST pack and stability

The off-site GIST pack preparer gains `--stability-queries N`. For each existing
probe scenario, create a companion `stable_multiset` scenario using the same
SQL, session settings and the first N query vectors/IDs. Require ten returned
rows and at least three repetitions; the trial uses thirty repetitions. The
companion omits `exact_ids`: approximate neighbors need not equal the official
exact top ten. The original `ann_recall` scenario retains official truth and
checks recall independently. Stability compares ID multisets, ignoring order.
Freeze data and indexes across the checks; a stable answer need not be correct.

Reuse the already prepared, immutable million-row CSV through a hard link when
curating the local extended pack; preserve original source/load hashes and
record the extension. No data duplication or recomputation of official truth.

## Report consumers

The HTML report adds a concurrency section, grouped by original scenario,
showing QPS versus concurrency and P90/P95/P99 versus concurrency on independent
axes with matching labeled levels. Keep an adjacent numerical table with
sample counts, recall/quality, SQL errors and assertion failures. The existing
latency chart labels each profile's concurrency. Stability gets a query-level
table showing repetition counts, successful executions, distinct result sets,
lowest overlap, changed IDs and pass/fail, plus endpoint/plan evidence. Show
errors and unexecuted checks explicitly. Inline SVG/CSS only; legacy reports
without the new fields remain renderable without fabricating load profiles.

## Change map and validation

| Closure | Risk and evidence |
| --- | --- |
| CLI → profile expansion → shared pool/scenario calls | R2 admission/planning; narrow R3 pool-generation boundary: validation/order/retention UT, ready-session and partial-setup cleanup UT, focused race, and public SQL session-reset regression. |
| Observations → SQL/error/quality counters → charts | R2: successful-SQL selection, duration/QPS and legacy rendering UT. |
| GIST preparer → companion scenarios → frozen pack | R2: minimum synthetic fixture checks query identity, omitted exact oracle, hashes and bounds; validate actual full pack. |
| Stability observations → per-query evidence table | R2: existing order/duplicate/drift/incorrect-truth UT retained; add failed/incomplete-run reporting checks. |
| HTML → desktop/mobile browser | R1: actual values/series, empty cases, escaping, no external requests or page overflow. |

Run focused and owning pure-Go package tests, incremental vet/lint, and race
validation for scenario execution. Exercise a small public-action smoke run
first, then one real GIST1M run on the existing local service. Existing
three-row-selection/multiset tests are reused; native/CGo kernel tests and
multi-CN bug-fix claims are outside this client-tool closure. Final self-review
checks all changes together with the preserved v3 latency/report work.

## Local delivery review: scope and ownership

The explicit incremental base is the prior prototype HEAD
`1f6b6d17c0e7c2ff7981be9a2f85fa9ec3a83650`; all changes are unstaged in the
independent worktree, including the retained v3 implementation and new v4
source/tests/design. This is a local delivery review, not a remote PR review.
No commit, push, kernel edit, or main-worktree mutation is part of delivery.
Every changed hunk and new source was read against this base.

Lifecycle invariant: a scenario owns its worker leases and all workers finish
before returning. The outer run owns the shared pool and can reset its idle
server sessions only after the previous scenario's return. Each result index
has exactly one worker writer; the aggregator reads after the wait group joins.

| State | Event / owner | Transition and terminal path |
| --- | --- | --- |
| Prepared database / idle pool | Outer run admits next validated profile | Close idle server sessions, restore bounded idle capacity, then acquire the new profile's leases. |
| Acquiring leases | Scenario obtains each connection | Ownership enters the scenario's deferred list immediately; partial failure returns all prior leases. |
| Ready leases | EXPLAIN and warmup complete | Start measured workers; warmup/plans/connection establishment are outside the ordinary SQL timer. Hybrid routes retain their existing pooled two-route path. |
| Measured workers | Finite job producer and worker group | Each job writes one preallocated result. Close jobs, join workers, aggregate, then return leases. SQL errors remain observations, not early returns that abandon jobs. |
| Returned scenario | Outer run starts next profile | No old worker or lease survives into the next generation; SET values cannot reach its default sessions. |
| Run terminates | Outer run / root connection | Close shared pool, drop owned test database unless explicitly retained, close root. Stage failure uses the same cleanup path and publishes unexecuted profiles. |

The Q1–Q3 audit follows the changed path into the existing SQL driver:

| Layer / check | Owner, wait or bound | Termination |
| --- | --- | --- |
| Runner Q1 | Root/shared pool are deferred by the run; leased ordinary connections by the scenario; stability pool+lease pairs by its existing scenario cleanup. | Partial session setup returns through the same cleanup owner. Ready-session UT and the SQL regression assert leases/cleanup. |
| Worker Q1/Q2 | Worker `Done` is deferred. Producer sends to a bounded jobs channel; consumers run finite queries, channel closes, `Wait` joins. Hybrid query joins two bounded SQL calls. | SQL contexts reach driver cancellation. No producer or callback belongs to the next pool generation. |
| Driver Q2 | mysql v1.9.3 `watchCancel` → watcher `ctx.Done` → `cancel` → `cleanup` → underlying socket `Close`. Cleanup is guarded by atomic closed state. | Cancellation interrupts network I/O independently of the worker; connect timeout and finite read/write ceilings also bound connection/stage operations. |
| Runner Q3 | At most 8 levels, 128 workers, twice the maximum concurrency in the shared pool; endpoint count ≤16; finite jobs and preallocated results. | Sweep admission caps 100,000 executions and 1,000,000 retained IDs before creating a database. No retry or new accumulating background task. |
| Stability Q3 | Existing physical-plan cap is 1 MiB; signatures are bounded by the retained per-query results. | Per-query maps are released after their summary; explicit SQL failure counts prevent a partial repetition check from appearing passed. |
| Renderer Q1/Q3 | One temporary HTML file, owned by render; report-view slices are copies of bounded observations. | Failed render closes/removes temporary output; successful render closes/renames it. Raw JSON is preserved. |

Correctness, consumers, lifecycle, liveness, scale and operational lenses map
to the execution/observation rows. Format compatibility, HTML escaping and
delivery map to the renderer, CLI and static archive. Pack preparation uses a
10-row deterministic Top-10 fixture and two query vectors; concurrent runner
UT uses two queries and four mock sessions. The public smoke check uses only
eight rows. The million-row run is a requested performance observation, not a
unit-test fixture. Index-plugin/kernel/ISCP and native/CGo lenses do not apply:
the change is an independent pure-Go SQL client, and pack DDL uses existing
server index functionality.

Two integration findings were closed before the final GIST trial:

- Reused sessions retained a prior `SET probe_limit=20`. The v0.3.0 negative
  fixture failed its following default-5 check; v0.4.0 passes both settings at
  concurrency 1 and 4 after draining idle sessions between profiles.
- The old 30-second socket limit truncated a CREATE INDEX stage that allowed
  300 seconds. The transport now accommodates that preparation budget while
  query/plan contexts retain 30 seconds. The original failed report remains
  preserved separately from the retry.

Accepted scope: the local service is a single CN. Endpoint/plan recording
supports a separately prepared multi-CN regression, but this run cannot prove
the recent distributed-sort fix. Ordinary sweeps use finite sequential batches,
and dedicated stability repetitions are sequential. A future scenario can add
repeated-input checks under controlled background load; resource monitoring also
requires its own integration. Neither is inferred from the current observations.

## Terminal validation and trial evidence (2026-10-06)

- Owning pure-Go package normal and race tests passed; `go vet` and incremental
  `golangci-lint --new-from-rev HEAD` passed (zero new issues). The full lint's
  three findings were reproduced on the unchanged baseline: one prealloc and
  two sqlclosecheck findings. `git diff --check` passed.
- Exact race selections were nonempty in the terminal JSON events:
  `TestConcurrentScenarioRetainsObservationsAndClosesSessions` and
  `TestCancelledSetupPublishesUnexecutedProfiles`, each measured 0.01 seconds.
  The 30-second adaptive budget therefore selected the capped 100 repetitions
  per exact test; both completed successfully. No repeated whole-package stress.
- GIST preparer unittest passed. The additional pure comparison test covers
  duplicate counts, reordered IDs, missing SQL, immutable observations and
  the 100-row display cap. Rendering tests cover escaped changed IDs and
  the fused-request throughput unit.
- Final public-action smoke run passed all nine profiles and dropped its
  database. The old/new session-reset regression separately proved the prior
  failure and the corrected default sessions.
- GIST1M completed fifteen profiles: twelve 100-query concurrency batches and
  three 5-query × 30-repeat stability batches, 1,650 measured executions total.
  Import took 66.405 seconds; index creation 44.469 seconds. SQL errors: zero;
  cleanup: dropped; Docker OOM flag false and restart count zero. Static limits
  were eight CPUs / 16 GiB; live resource metrics remain unavailable.
- All fifteen query/parameter repetition checks passed: one distinct set,
  overlap 1.0 and zero changed IDs across their 450 executions. At each load
  level, default-5 recall mean was 0.552 (33/100 at threshold), probe-20 mean
  0.836 (81/100), and probe-100 mean 0.985 (100/100). The overall benchmark
  status remains failed because the first two settings miss per-query 0.7
  assertions. That is a quality result, not a runner/test-suite failure.
- Comparing all 100 inputs across load levels reveals one ID change for
  `gist_62` at probe-100, concurrency 1 versus 4: `435270` → `296224`, overlap
  0.9. The original source fvec records are byte-identical (960 coordinates;
  SHA-256 `11a84c6c1e9be8315859f8e5444a5f6ccad614b12828d3a5a2b24a804bb7286c`).
  Their squared L2 distances are identical. The query orders solely by L2 and
  has no ID tie-break. `boundary-ties.json` records this evidence separately;
  neither that file nor the derived comparison rewrites the raw verdict.
- Chromium at desktop 1280×900 and mobile 390×844 verifies three load groups,
  three stability tables, numerical QPS/percentiles against the original JSON,
  the changed query/IDs, finite plotted points, uncut axis labels, expandable
  physical plans, no page overflow/errors, and zero external requests. Mobile
  tables/plots scroll inside their cards.

The measurement executable is preserved as the `v0.4.0-measured` binary,
SHA-256 `87ad1042ceca87a84326e7d9c751e8758a3bf242f82b34ab8cbcaa0a8ca5709a`.
The cross-profile comparison and final display refinements were added afterward
and rendered from the saved observations; execution semantics stayed unchanged.
Original report JSON SHA-256 remains
`91639d38f5883e8877a9a4d703504ff684ece7ba957698563d274aad69f01519`.
The final static Linux amd64 archive includes the binary, smoke pack, checksum,
license and design documents; no customer runtime dependency was added. New
source/tests, including the Python test rescued by a directory ignore exception,
are visible in the delivery worktree. Review decision: PASS for this client-tool
closure, with the single-CN and finite-batch scope stated above.


## Chart-first terminal validation (2026-10-06)

The v0.5.0 renderer places dataset-wide throughput, switchable P90/P95/P99,
quality and cross-profile change plots first. Native expandable details retain
all numerical tables, complete scenario plots and measurement metadata.
Reports without concurrency profiles show per-query quality bars, keeping
zero scores distinct from failed or missing SQL. Means include successful SQL
only. Quality displays retain thirty inputs; stability grids retain thirty
queries and at most sixty contiguous bins, with errors taking priority.

- Owning package tests, `go vet`, incremental `golangci-lint` (zero new issues)
  and `git diff --check` passed. Focused chart tests verify metric values,
  separated quality oracles, plot gaps, valid scores, distinct zero/no-sample
  states, immutable input, escaping and bounded error-priority grids.
- Chromium verified the report homepage, GIST report and T2Ranking report at
  1280×1000 and 390×844: QPS/percentile/quality/change points match the original
  observations; native percentile switches work; 450 matrix cells match the
  fifteen repeated-query outcomes; all numerical tables start collapsed;
  physical plans expand; SVG labels stay within their axes; there are no
  page overflows, script errors or external requests. Long dataset headings
  wrap on mobile, and wide quality/latency/grids scroll internally.
- All twenty saved reports were regenerated. Every original `report.json`
  digest is unchanged, including the GIST measurement digest recorded above.
  No SQL query, data import or runtime/synchronization change was made by this
  presentation refinement; existing runtime/race validation remains applicable.
- The chart-first homepage preserves the failed recall verdicts, the separate
  top-K tie evidence and the single-CN deployment scope. Historical reports
  and the v0.4 measurement binary remain available. The final v0.5 archive
  contains a static Linux amd64 executable and the original smoke pack.

Final v0.5 renderer executable SHA-256: `0d1cad386627ee9d12336379dd40b7bf12b58f7dfb9a463d5a2f8293d13fc661`.


## User-requested scope reduction (2026-10-06)

The v0.5.1 renderer removes cross-concurrency result-set changes: both the
summary chart and the expandable per-scenario comparison/counters/ID tables.
Delete the derived comparison helper and its exclusive tests. Existing
rendering tests retain independent quality, sample selection, gaps, escaping,
immutable raw measurements, concurrency plots and repeated stability oracles.
The current overview has three panels: QPS, switchable latency and quality.
Dataset pages keep their selector, separate routes and repeated-result grids.
Remove the portal's associated boundary-change paragraph; preserve all raw
observations and the previously saved evidence separately.

This is an R1 presentation/deletion refinement within the isolated client-tool
worktree. No runner, SQL, oracle, pack, topology or resource-lifecycle change;
BVT/race/scale reruns are unnecessary. Validate the owning rendering package,
incremental vet/lint, the desktop/mobile portal consumer and unchanged raw JSON
hashes. Earlier cross-profile evidence in this document is historical and is
no longer a feature offered by the current report.


Scope-reduction terminal evidence: the four focused rendering/stability tests
ran and passed, the owning pure-Go package passed with `-count=1`, and incremental
vet/lint passed (zero new issues). Twenty saved HTML reports were regenerated;
all twenty original JSON digests stayed unchanged. The separate dataset portal
passed Chromium checks at 1280×1000 and 390×844: exactly three GIST overview
panels, no comparison chart/table/text, all 450 repeated-stability cells retained,
working percentile and dataset switches, reload/back/forward selection and no
page overflow, browser errors or external requests. Saved raw tie evidence is
preserved outside the current UI. Local review decision: PASS for this scoped
renderer deletion; no runner/kernel contract changed.

Final v0.5.1 executable SHA-256: `e0accd3b8cc0ce2994eb1d690aaa80ac19f39282dd4a4ad497003d1a83874826`.


## GIST Top-100 pack revision (2026-10-06)

The user now requests one hundred returned vectors. The installed official GIST
truth file contains exactly one hundred neighbors per query, so the new pack
uses LIMIT/top_k/expected_rows=100 for both ordinary and repeated stability
scenarios, and official hundred-ID answers for Recall@100. The old Top-10 pack
and reports remain immutable history. Keep lists=1000, default/20/100 probes,
100 distinct quality queries, concurrency 1/4/8/16, and five inputs repeated
thirty times. The full sweep retains 165,000 result IDs, within the 1M bound.

Add an off-site --top-k option (default ten for backward compatibility) to the
GIST preparer. Reject nonpositive/excessive K, K larger than the selected base,
or official truth shorter than K; truncate ground truth to exactly K. Companion
SQL/top_k/expected_rows must agree and omit exact truth. Use the existing CSV
via hardlink in the curated revision, rebuilding only versioned scenario/query
files and digests. Freeze provenance of the prior manifest and selected truth.
The standalone runner already supports K=100; no runner/kernel/lifecycle change.

Risk: R2 frozen-pack correctness. Extend minimal preparer evidence for official
Top-100 ordering, companion cardinality, digests and insufficient truth. Validate
the full pack and exercise the actual requested single-CN measurement, publish
its new raw observations and keep the dataset portal chart-first with the new K
and quality labels. Live Grafana and multi-CN evidence remain outside this run.


Top-100 terminal evidence (2026-10-06):

- Both focused preparer cases passed: the original Top-10 fixture and a minimal
  hundred-row/one-query Top-100 fixture. Evidence covers complete official
  neighbor ordering, companion SQL/expected count, query identity, hashes,
  nonpositive/over-base/excessive K and insufficient truth. Ground truth is
  preflighted before streaming the large base CSV. The owning pure-Go package,
  vet and incremental lint also passed after the Top-K display refinement.
- The new full pack is immutable, uses the original million-row CSV inode,
  and has manifest digest
  `7463977b534b3ff7474dff8c0bbe7564499f0adf1a5a9ec7261130f66c9964dd`.
- The single-CN trial took 552.165 seconds, including 72.358 seconds of import
  and 59.757 seconds of index creation. All fifteen profiles completed: 1,650
  successful executions, each with exactly 100 returned IDs, zero SQL errors,
  165,000 retained IDs and cleanup dropped. Container OOM flag false/restarts 0.
- Official source answers independently reproduce every ordinary Recall@100
  score and mean. At concurrency 1, default/20/100 probes have means
  0.4375 / 0.7565 / 0.9735 and 9 / 61 / 100 queries meeting the 0.7 threshold.
  Probe 100 passes all hundred queries at every concurrency. The overall
  quality verdict remains failed because default and 20 miss per-query checks.
- All fifteen query/parameter repeated checks passed, 450 executions total:
  one distinct hundred-ID multiset, overlap 1 and zero changed IDs. This remains
  a five-input sample per parameter on a single CN.
- Chromium verifies separate dataset pages, Top-100 selection/labels,
  Recall@100, three overview panels, 450 passing cells, native percentile and
  dataset switching, reload/back/forward, no external requests/errors/overflow
  at 1280 and 390 widths. Full report plotted QPS, every percentile and quality
  point agree with the raw measurements. No removed comparison is reintroduced.
- All twenty prior JSON digests are unchanged. The measured v0.5.1 executable
  is separately preserved; the v0.5.2 renderer only adds visible stability Top-K
  labels and regenerates HTML, preserving this trial's JSON and measured identity.

Top-100 report JSON SHA-256: `a14bfc681498cca435fafe1a683737a88166ebf836019a5577d6d526069715ea`.
Final v0.5.2 renderer SHA-256: `c39f20f2da0c2287b312aba6864034922b562b8bfb2a822b63216d0b60a05b7c`.
Local review decision: PASS for the Top-100 frozen-pack/client-report revision.


## Requested concurrency profile: 1 / 4 / 8 (2026-10-06)

Use only client concurrency 1, 4 and 8 in the active GIST profile and documented
future run commands. Reuse the already measured observations rather than rerun
the million-row benchmark. Preserve its immutable original JSON and identity.

Add optional `render --concurrency-levels 1,4,8` as a projection of existing
ordinary scenarios. Retain independent repeated-stability scenes; reject
invalid, duplicate or absent requested levels before publishing HTML. Apply
selection to overview, detailed plots/tables and latency scales, so hidden
profiles cannot influence the displayed axes. Label selection explicitly and
retain the original overall verdict. Selected failures use typed scenario
counters; full original error records remain accessible in JSON. Rendering
without a selector keeps the existing full-report behavior.

Risk: R2 immutable view/selection and R1 presentation. Extend the existing saved
report test to cover retained stability, excluded profile effects, raw-byte
preservation, compatibility and atomic failure for absent/invalid selectors.
Use normal owning tests plus incremental vet/lint and an actual desktop/mobile
consumer check. No scheduler, SQL, oracle, data, lifecycle or topology change;
no additional scale/race rerun is required.


Three-level profile terminal evidence (2026-10-06):

- The saved-report projection test passes, including retained independent
  stability, unchanged input objects and JSON bytes, excluded-profile axis
  isolation, original verdict retention, legacy unfiltered rendering, and
  atomic preservation of HTML on absent/invalid selectors.
- The owning pure-Go package passes with `-count=1`; vet and incremental
  golangci-lint pass with zero new issues. The static client build succeeds.
- Chromium verifies separate dataset pages and the current full report at
  1280 and 390 widths. Plots and detailed tables contain only 1/4/8; every
  QPS, quality and P90/P95/P99 point matches its original observation.
  All 450 repeated-stability cells remain, percentile/dataset controls and
  reload/back/forward work, and no overflow or browser errors occur.
- The current HTML explicitly identifies its selected existing observations;
  no SQL or scale measurement was rerun. The original fifteen-profile,
  1,650-execution JSON and overall verdict remain intact. Future GIST run
  examples and the portal regeneration command use only 1/4/8.

Final v0.5.3 renderer SHA-256: `a1aa46881fe07e317cf93c7809dd3c79dc12ec7e7e5b6bf4cb78172d107164e7`.
Local review decision: PASS for the immutable three-level report projection.

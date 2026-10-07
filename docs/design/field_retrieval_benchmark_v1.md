# Field retrieval benchmark v1

Status: accepted for the local prototype on 2026-09-28. Owner: MatrixOne test tooling. This revision defines the contract for `cmd/mo-retrieval-bench`; changes to the pack schema or report semantics require a new revision.

## Purpose and boundary

The field tool connects to an existing MatrixOne SQL endpoint, creates one uniquely named test database, imports a versioned public data pack, runs retrieval scenarios, and writes a report that the customer can reproduce. It measures MatrixOne SQL retrieval, including indexed vector and fulltext routes. Application embedding inference, DashVector answer caching, external search, reranking, and LLM filtering are separate systems and are outside the reported SQL latency.

The first workload should use a frozen 1–2 million passage subset of T2Ranking (with the full collection as an optional longer profile), its queries and judgments, embeddings generated offline with the same pinned 1024-dimensional BGE model as the CA assistant, and offline exact nearest-neighbor ground truth. A small bundled pack exercises the runner; it is not a performance baseline. The current implementation can also consume packs for LCQMC question matching and GIST1M index checks without changing the executable.

## Invariants

1. Every run identifies the binary version, pack and scenario file digests, MatrixOne version, generated database, SQL, selected query IDs (which resolve to parameters in the frozen query file), repetitions, concurrency, and result samples. A report never presents an unlabeled sample run as a full benchmark.
2. The runner creates one new database, runs its pack SQL in that database, and drops only that generated name unless `--keep-db` is set. A collision fails; the runner never intentionally drops a pre-existing database. Packs are trusted release content: their SQL is reviewed before distribution, and field credentials should be scoped to a test environment. A failure still writes a report and attempts cleanup.
3. The tool verifies pack file hashes before any database mutation, checks loaded row counts, and fails closed on unknown schema versions, oracle types, malformed queries, missing truth, SQL errors, or timeouts. Missing Grafana data is recorded as unavailable and does not imply a healthy cluster.
4. Query values use database driver parameters. SQL and DDL come only from a local, trusted, versioned pack. The load driver allowlists only the pack's declared files.
5. Quality has separate meanings: exact ID assertions for deterministic cases; ANN recall against exact top-K for index accuracy; relevance judgments for semantic quality. Performance has no universal pass threshold. Any thresholds are explicit in the pack.

## Pack and extension contract

`manifest.json` has `schema_version: 1`, a dataset ID, DDL statements, loads with file paths/SHA-256/expected rows, index statements, and an explicit list of scenario JSON files. A scenario owns an ID, route (`sql` or `hybrid_rrf`), parameterized SQL, query JSONL, oracle, and Top-K. Hybrid scenarios can set `candidate_k` independently of the final Top-K to mirror the application's per-route retrieval depth. The query file owns inputs and its oracle data. The manifest digest and each referenced file digest are recorded in the report. Paths are relative to the pack root and may not escape it.

Adding a customer question means adding a scenario JSON plus a frozen query JSONL and listing them in the manifest. A `sql` route can test filtering, DDL-independent query shapes, or a new failure reproduction. `hybrid_rrf` runs vector and fulltext SQL, fuses by rank, and scores the fused IDs. A new oracle or mutation lifecycle needs a reviewed schema version and runner change. Every scenario has a stable ID so results can be compared across runs.

Profiles are runtime controls: query limit, warmup count, repetitions, concurrency, and statement timeout. A short profile samples the same frozen query IDs in deterministic order; longer profiles expand the count and repetitions. Reports must show the selected profile and actual query count. No automatic parameter tuning occurs during a measurement run.

## Flow, ownership, and resource bounds

The CLI owns the run ID, database, SQL pool, worker limit, context deadline, report directory, and cleanup. Preparation verifies files, JSONL, and SQL shape before creating the database. Setup creates the database, table, imports data using local infile, verifies counts, then builds indexes. Measurement warms each scenario, executes bounded jobs with at most `2 × concurrency` database connections (two per hybrid job), and records per-query outcome. Finalization drops the database unless retained; the caller then writes JSON and self-contained HTML. The report records setup, load, index, run, and cleanup errors separately.

The executable is a pure-Go build with the existing MySQL protocol driver and no runtime database CLI or Python dependency. Data preparation and embedding generation are offline; the on-site binary only consumes a pack. A full 2.3M × 1024 F32 embedding set is about 9.4 GB before CSV/index overhead, so packs are transferred separately from the executable. Query JSONL files are capped at 256 MiB and 100,000 rows at preflight; CLI selection controls measured jobs. Workers stream jobs through bounded channels. Retained results are capped at 100,000 executions and 1 million IDs per scenario. Raw results include Top-K IDs and timings, not full document bodies.

## Decisions and alternatives

### Concrete next extension: multi-CN IVF result stability

Issue [#28943](https://github.com/matrixorigin/matrixone/issues/28943) and fix [#28945](https://github.com/matrixorigin/matrixone/pull/28945) are a distinct regression class. The same fixed POST-mode IVF query on a two-CN cluster changed which 200-ID candidate group survived a global limit. The customer-observable failure is a changed **ID multiset**, not merely row order or lower ANN recall. The current v1 runner saves each repeated result, so the JSON report can show evidence, but it cannot automatically pass/fail this regression: it has no cross-run stability oracle, admits only one ID column and no duplicate IDs, rejects a leading `WITH`, does not initialize session settings on every pooled connection, and does not prove a two-CN physical plan.

A versioned extension should add a `stable_multiset` oracle evaluated across repeated identical queries. It must report distinct result multisets, changed ID counts, minimum overlap at K, and the iteration/endpoint for every divergence; it must ignore output order. The SQL reader must accept the original query shape, including CTEs, extra projected columns, and duplicate IDs from `UNION ALL`, while extracting a declared ID column. A pinned connection must run scenario session setup such as `SET probe_limit=5`; the runner must record direct CN endpoints or proxy route and capture/validate `EXPLAIN PHYPLAN` showing actual multi-CN dispatch and an ordered merge before candidate truncation. Require a minimum repetition count and fail a run whose required topology is absent rather than reporting a false pass.

This issue also needs a dedicated, seeded million-row `vecf32(1024)` regression pack with the exact filters, `lists=1024`, POST-mode `LIMIT 200` branches, fixed query vector, and independently established candidate-set oracle. The generic T2Ranking pack is not a substitute because its data distribution and query shape may never exercise this compiler boundary. Repeated-set equality detects drift; the independent oracle prevents a consistently wrong set from passing. The large fixture and two-CN integration run remain unimplemented.

* Reusing BVT/MOTR scripts gives strong functional coverage but assumes CI infrastructure, multiple runtimes, and test-specific data. Keep them as reference, not the field entrypoint.
* A Python package would shorten development but needs an interpreter and dependencies on customer machines. The single Go executable is the delivery boundary.
* A generic code plugin system would permit arbitrary scenario logic and enlarge the security/review surface. Versioned declarative scenario packs cover the known extension need with a smaller contract.

## Evidence and open work

Unit tests cover manifest/path/hash validation, oracles, fusion, percentiles, and report rendering. The eight-row smoke pack was run against a local MatrixOne v4.2.1 container on 2026-09-28: all four scenarios passed, EXPLAIN showed `ivf_search` for the vector route and the fulltext index route, and cleanup dropped the generated database. A second run with concurrency 4, repeat 3, and warmup 2 passed all 21 measured executions. The binary was built as a statically linked x86-64 executable; its SHA-256 matched the value in the report. A connection failure also produced a failed report. These checks validate the runner and do not establish performance at customer scale. The T2Ranking, LCQMC, and GIST1M large packs need their source revision, model/configuration, hashes, prepared vectors, exact ground truth, license notes, and reference results frozen before customer delivery. Grafana collection is an optional future source with its own versioned metric mapping; the report already has a place for unavailable resource metrics.

Design review record: the standalone test tool adds a versioned pack contract and executable. This document closes ownership, cleanup, data identity, result meanings, and extension rules for the local prototype. The open large-pack preparation, customer-scale integration run, and environment metrics support block customer delivery, not local implementation review.

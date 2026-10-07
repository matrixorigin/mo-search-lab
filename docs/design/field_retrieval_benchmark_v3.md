# Field retrieval benchmark v3: latency percentile charts

Status: accepted for the local prototype on 2026-10-06, following the request to
chart P90/P95/P99. Owner: MatrixOne test tooling. Design review: the extension
stays within the report/CLI component; it adds no database operation, dependency,
background task, or change to the pack contract.

## Contract and ownership

The scenario aggregator adds `p90_ms` using the existing nearest-rank percentile
definition: sort measured, successful SQL round trips and select ceil(p*N).
Include successful SQL whose quality assertion failed; exclude SQL execution
errors and unmeasured warmups. Preserve the existing P50/P95/P99 definitions.

The HTML renderer owns an inline SVG comparison of P90/P95/P99. All scenarios
share a zero-based linear millisecond axis, with numeric values, scenario/session
labels, and successful SQL sample counts. Empty scenarios have no percentile
bars. Explain that fewer than 100 samples make nearest-rank P99 the sample max.
Render escaped text, support narrow screens, and make no external requests.

`render --report-dir DIR` regenerates `report.html` from `report.json` without
querying MatrixOne. Old JSON without P90 remains readable: reconstruct latency
percentiles from its raw successful SQL observations. Preserve the raw JSON,
measurement identity, timings, pass/fail status, and hashes. Label the renderer
version separately from the measured executable version. Parse and render before
publishing HTML through a same-directory temporary file and atomic rename, so
invalid input or rendering failures preserve the previous report.

## Validation and delivery

Affected closure: `cmd/mo-retrieval-bench` only. Percentile selection and report
compatibility/I/O are R2; chart geometry is local R1. Focused tests check distinct
P90/P95/P99 ranks, inclusion/exclusion rules, old-report rendering, empty data,
escaped labels, and non-destructive render failures. Run the owning pure-Go
package tests and incremental static checks, then render the existing GIST/T2
reports and inspect desktop/mobile SVG output. Database BVT, CGo, topology, and
race runs are unnecessary because no SQL, worker, or synchronization path changes.

The chart is a view of the existing measurements. Sequential parameter scenarios
may have different cache states; plotting them does not create a controlled
performance comparison or an acceptance threshold.

## Local delivery review (2026-10-06)

Incremental scope: the working-tree delta from the prior prototype head
`1f6b6d17c0e7c2ff7981be9a2f85fa9ec3a83650`, including the new renderer and
this design document; no staged changes. The implementation remains in the
independent `mo-retrieval-bench` worktree. All changed hunks were reviewed.

| Closure | Risk and evidence |
| --- | --- |
| Aggregator → JSON → HTML | R2: distinct nearest-rank P90/P95/P99 and successful-SQL selection tests pass. Existing P50/P95/P99 semantics retained. |
| CLI → legacy JSON → atomic HTML | R2: raw measurements and identity stay unchanged; invalid JSON/status/latency retain prior HTML and leave no temporary file. Escaping tests pass. |
| Inline SVG → browser | R1: Chromium at 1280×900 and 390×844 verifies nine bars, shared scaling, actual values, no page overflow, no errors, and no external requests. Narrow charts scroll within their row. |
| README, binary, report index | R0/R1: version 0.3.0-prototype built as a static Linux amd64 executable; local report links and numeric P90/P99 entries updated. |

File ownership audit for the render command:

| Check | Terminal path |
| --- | --- |
| Q1 | `ReadFile` owns/closes its input. The render function owns its single temporary file: close/remove on failure, close/rename on success. Go's closed-file guard prevents a deferred second `Close` from closing a reused descriptor. |
| Q2 | Synchronous local file operations and finite template rendering; no added goroutine, lock, remote call, retry, or shared-handle wait. |
| Q3 | One input and temporary file per invocation; observation slices are bounded by the saved report and released at return. Only three bars per scenario, independent of query count. |

Validation: focused tests, the entire owning Go package, `go vet`, incremental
`golangci-lint --new-from-rev HEAD`, and `git diff --check` passed. Full-package
lint reports three pre-existing findings in unchanged connection/warmup code;
no changed-code findings. Fifteen saved reports were rendered successfully;
every original `report.json` SHA-256 remained identical. The served GIST report
returns HTTP 200. No database rerun was needed for this presentation change.
Review decision: PASS; no unresolved blockers in the incremental scope.

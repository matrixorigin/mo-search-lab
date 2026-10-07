# v8: Interactive terminal report viewer

## Design revision 1

Scope: add `ui` to the existing standalone executable in the isolated
`codex/mo-retrieval-bench` worktree. Base/head before work: `d23ab44425`.
The user requested an interactive CLI displaying information similar to the
browser report. This revision interprets that as a terminal report dashboard;
benchmark execution continues through the existing `run` command.

### Contract and architecture

```text
saved report directories -> bounded catalogue -> selected report.json
                                              -> existing report view/metrics
                                              -> terminal charts and details
optional local environment/tie evidence ------> labelled evidence section
keyboard -> dataset/run/section/profile selection -> render viewport
```

One immutable measurement source serves HTML and terminal consumers. Opening,
switching, and quitting the viewer never runs SQL or rewrites measurements.
The original overall status remains visible even when a profile is selected.
Missing samples are shown as unavailable, rather than zero-valued performance.
New declarative scenarios appear without adding a dataset-specific renderer.

`ui --reports DIR` discovers reports up to four directory levels beneath DIR.
`ui --report-dir DIR` opens one report. Dataset identity comes from `dataset`;
each dataset can have multiple historical runs, ordered newest first. The
default selection is the newest run. `--dataset ID` selects an exact identity.
`--section overview|concurrency|quality|stability|sql|environment|help` chooses
the initial section. `--plain` emits the complete selected section without ANSI
controls; it is also the default when stdin/stdout is not a terminal or TERM is
dumb. A plain file/pipe therefore never waits for keyboard input.

Keys: d opens the dataset/run chooser; left/right switch datasets; r switches
historical runs; tab and 1..6 select sections; p cycles P90/P95/P99; c selects
all or a measured concurrency level; n/b select a scenario for SQL and details;
up/down, j/k, PgUp/PgDn scroll; ? opens definitions; q/Ctrl-C exits.

Overview starts with concurrency-1 latency bars. Concurrency displays labelled
bars with exact QPS and latency values, including distinct isolated and mixed
route IDs. Quality shows Recall@K or qrels nDCG/Recall/MRR and returned counts.
Stability shows repeat matrices and distinct sets/orders, overlap and failures.
SQL includes saved session/index statements and logical/physical plans.
Environment contains measured versions, hashes, preparation stages, profile,
cleanup, errors, and optional saved machine configuration. Help explains the
metric definitions, client concurrency, mixed paired pacing and measurement
limits. No universal performance acceptance line is invented.

### Ownership, compatibility and bounds

The CLI owns catalogue discovery and selected report loading. The model owns
all navigation state; updates are sequential. A switch replaces the selected
report and resets scroll/profile state, including when loading fails. No stale
report is shown under another dataset's heading. File handles close inside each
read. There is no network access, watcher, reload loop or benchmark worker.

The existing Bubble Tea module handles terminal input, resize, alternate screen,
signals and restoration; it is already pinned in go.mod. Existing runewidth
handles Chinese display widths. No dependency or on-disk schema change is
needed. Unicode bars have plain text numeric values and labels. All externally
loaded text is stripped of terminal control characters; SQL wraps to the window
width. Tiny terminals receive a resize message and can still quit.

Discovery: at most 4096 visited entries and 128 reports; no symlink traversal.
Catalogue reads only bounded leading metadata (64 KiB), retaining paths/titles,
not all query results. Loading: one regular report file, at most 64 MiB, at most
256 scenarios / 100000 results / 1000000 returned IDs. Optional evidence files
are regular local files <=1 MiB; missing evidence is labelled unavailable;
malformed evidence is visible and cannot silently override raw status.
Switching synchronously loads a bounded local file; this avoids an asynchronous
loader and stale generation protocol. There is a brief pause for large files.
Rendering retains a single document/viewport. Existing measurement and oracle
code is reused unchanged. Removal is deleting the additive command/files;
v0.7 reports remain usable in both consumers.

### Alternatives and review

Status quo HTML requires browser access/tunnelling. A line-based menu has fewer
terminal lifecycle concerns but poor chart navigation/resize behavior. A new
web/terminal service or new TUI framework adds deployment/dependency costs.
Use the already pinned Bubble Tea library with a small model and plain-output
fallback. No kernel, distributed or SQL execution contract changes.

Design gate: new public CLI and terminal lifecycle, potentially >=500 production
lines. Revision 1 reviewed before implementation. Invariants, bounded ownership,
compatibility, error/quit handling and alternatives are closed. Decision: PASS.
Accepted limits: report browsing only; synchronous bounded loads; source dataset
IDs preserved; optional sidecars are labelled supplementary local evidence.

### Validation map

- UT: catalogue grouping/order, absent/malformed/oversized reports, switch failure,
  non-TTY plain mode, navigation/resize, unavailable versus zero, exact values,
  escaping/wrapping, stable sets versus changed orders and repeat matrices.
- Owning package tests, focused race, gofmt/vet/lint; no kernel/usearch dependency.
- Public client boundary: real binary plain output against existing GIST, T2 and
  mixed reports; PTY navigation and quit/Ctrl-C/SIGTERM restore terminal state.
- Saved report hashes remain unchanged, main checkout stays clean, static ELF
  release can open reports without Python/browser/SQL connectivity.
- No BVT or new database measurement: benchmark SQL and runner are unchanged;
  PTY and actual saved measurements prove the changed boundary directly.

## Delivery evidence

Self-delivery review against `d23ab44425`; all additions belong to the terminal
viewer and its public CLI entry, tests and documentation. No kernel/runner/oracle
code or module dependency changes. Revision 1 remains the approved design.

Change map: catalogue/loading and terminal lifecycle are R2/R3 respectively;
rendering/metric reuse is R2; README/design/release packaging is R0. The CLI owns
one document, the model serially owns selection/scroll state, and the existing
Bubble Tea program owns input/resize/signal handlers and terminal restoration.
File reads close on every result; failed switches clear stale data. Bounds apply
to discovery, file sizes, scenario/result/ID counts, iterations and repetitions.
Stored quality metrics are range checked. Text escaping removes terminal and
bidirectional controls. There are no new watchers, retries, queues or SQL calls.

Validation (Linux amd64, final binary SHA-256
`05393d2dd8ed3bd76740c28ca7a1e2a1113882e3615d76d8c86aedb2b874b2bc`):

- Four named terminal UTs, including negative-path subtests, passed; owning
  package `go test -count=1 -timeout 120s` passed after the final rendering edit.
- Focused `go test -race -run '^TestTerminal' -count=1` passed. Reused after the
  final decimal-precision-only edit; no state/lifecycle/synchronization change.
- gofmt, owning-package go vet and golangci-lint passed; lint reports 0 issues.
- Static ELF v0.8.0 built with CGO disabled (package has no usearch dependency).
- Real binary opened GIST filters (24 profiles), two-route SQL (12 profiles),
  and T2Ranking ngram (11 profiles) on all seven pages. Independently calculated
  nearest-rank percentiles, QPS and quality means agree with 306 plotted values
  within displayed precision. All 900 repeat cells match, including 73 original
  order assertion failures; supplementary tie evidence does not waive status.
- PTY with actual keyboard/resize: dataset/run chooser, historical run cycling,
  percentile/concurrency filtering, pages/help, 60x18 and 24x5 resize, recovery.
  Normal q, Ctrl-C and SIGTERM all exit 0, leave alternate screen and restore
  original termios state. Non-TTY output contains no ANSI and never waits for keys.
- Unmeasured concurrency 16 is rejected. Empty versus successful zero latency,
  failed SQL exclusion, bad files/sidecars, unsafe text and stale switch are
  separately checked. No benchmark database or monitor connection was needed.
- 41 saved measurement/environment/tie files retain their SHA-256 digests.
  Measurement corpus unchanged; main worktree remains clean.

Evidence artifacts: `/tmp/retrieval-terminal-v8-verification.json`,
`/tmp/verify-retrieval-terminal-v8.py`, captured terminal screen/ANSI in `/tmp`.
The verification script uses pyte only in a temporary development directory;
the customer binary has no Python dependency. No BVT or repeated database load
was needed because SQL execution behavior is unchanged. Self-review: PASS,
zero unresolved blockers. Delivery archive checks recorded alongside downloads.

## Revision 2: Full health profile by default (v0.8.1)

User request: the default run should produce the complete health measurement,
including multiple client concurrency levels. The former CLI defaults produced
one concurrency level and one repeat, which could not run stability packs.

Change the CLI defaults only: all ordinary queries, repeat 1, concurrency levels
1/4/8, warmup 5, stability query limit 5, stability repeats 30, query timeout 3m
(existing preparation multiplier remains 10). Connection defaults and report
location remain as documented. All pack scenarios are selected; no scenario
IDs or dataset-specific mixed pairing are inferred.

An explicitly supplied `--concurrency N` selects a single level when no explicit
`--concurrency-levels` is present. An explicit levels flag overrides the scalar
regardless of argument order. Explicit empty levels falls back to the scalar.
Explicit warmup/stability values including zero keep their existing meaning;
zero stability values opt into inheritance. Benchmark runner, measured report
schema, query/oracle semantics and retention admission remain unchanged.

The CLI flag registration is the single owner of defaults; tests parse those
actual flags and then exercise scenario planning. Existing saved measurements
remain valid; only new runs adopt the profile. v0.8.0 stays available as rollback.
Invalid override profiles are rejected before SQL/database creation. Resource
bounds are enforced after profile expansion, before setup. Larger default
loads are the explicit user-requested tradeoff.

Change classification: public CLI default/precedence contract (R2), no new
lifecycle or concurrency mechanism. Design revision 2 reviewed before code;
defaults, override precedence, compatibility and bounds are closed. PASS.
Proof: focused default/override/planning tests, owning package/static checks,
real binary help and a small full-profile smoke run, existing report hash checks.
No repeated million-row benchmark is needed for a CLI default change.

Revision 2 delivery review: PASS, zero unresolved blockers. New flag registration
and explicit scalar/sweep precedence are the full R2 change closure; runner,
oracle, retained result schema, terminal lifecycle and module dependencies are
unchanged. Focused `TestRunHealthDefaultsAndOverrides` and seven named override
subtests passed, including both argument orders, explicit zero inheritance and
default aggregate admission. Owning-package go test, go vet and golangci-lint
passed (0 issues). Previous race/PTY evidence is reused for the unchanged UI
lifecycle; this change adds no shared state or concurrency mechanism.

Static v0.8.1 binary SHA-256:
`a0cafc33de0614258e0418e36d45e5be7d485fd1b018c8318e4bddac0d42e35a`.
Real `run` against the owned MO 4.2.1 fixture, specifying only pack, SQL port and
report directory, produced 12 ordinary profiles (four scenarios x 1/4/8) and one
independent 30-repeat stability profile. All 51 SQL executions and assertions
passed. Saved profile confirms query-limit 0, repeat 1, warmup 5, 1/4/8,
stability limit 5/repeat 30 and timeout 3m. The database was dropped and absence
independently verified with SHOW DATABASES. Actual binary help shows defaults;
the terminal consumer reads the new report. All 41 earlier saved inputs retain
their hashes. This tiny fixture proves CLI/planning/cleanup, not performance.
Evidence: `/tmp/retrieval-default-health-v0.8.1-verification.json`.

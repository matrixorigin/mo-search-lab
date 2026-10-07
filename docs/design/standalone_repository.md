# Standalone repository extraction (v0.8.2)

This document records the v0.8.2 extraction under its original name. The project
is now **MO Search Lab**; see [current naming and compatibility](search_lab_name.md).

## Contract

Requested on 2026-10-07: maintain the customer retrieval health tool in its own
Git repository, with its own module, build, tests and delivery lifecycle.

The original source is the current `cmd/mo-retrieval-bench` worktree snapshot
of MatrixOne: base commit `d23ab44425`, including the subsequent v0.8 terminal
viewer and v0.8.1 default health profile. Copy the current files, including
untracked v0.8/v0.8.1 files and the formerly ignored public-pack preparer.
The source checksums and target mapping are recorded in
[migration-source.json](../migration-source.json).

The new module is `github.com/matrixorigin/mo-retrieval-bench`. This module path
names the project; it does not establish a published GitHub repository. Remote
creation/publication is separate from local extraction.

## Ownership and unchanged behavior

- This repository owns the runner, quality/stability calculations, raw report
  format, HTML/terminal viewers, offline preparers and publishers, tests,
  release script and dependency pins.
- All Go source and tests are copied byte for byte. The same Linux runtime
  module versions are pinned as v0.8.1, including transitive UI dependencies.
- SQL execution uses `database/sql` and `go-sql-driver/mysql`. There is no
  MatrixOne kernel import, module dependency, symlink or local-path replacement.
- `run`, `validate`, `render` and `ui` retain their flags and semantics. Version
  v0.8.2 and the module/build identity distinguish the extracted executable.
- Packs remain external, versioned inputs. Existing GIST/T2/mixed packs and
  historical reports require no regeneration. Preparation scripts move to the
  repository's root `tools/` directory; their local imports stay together.
- The original MatrixOne checkout/worktree and existing release artifacts are
  preserved. The new repository is a separate filesystem tree and Git root.

## Build and delivery

The independent `go.mod`/`go.sum` contain only the client's dependency closure.
Build/test commands use `GOWORK=off`. The customer executable builds with
`CGO_ENABLED=0`; no native vector libraries or MatrixOne thirdparties are needed.
Race instrumentation, if run by developers, uses the standard Go race toolchain.

`make check` runs formatting, vet, owning Go package tests, and all offline
Python contract tests without an SQL service or tokenizer/model installation.
`make release` builds Linux amd64 by default and packages the binary, license,
documentation, SHA-256 checksums and eight-row smoke pack. Public customer-size
packs are delivered separately or included in a complete field bundle, with
shared CSV hardlinks preserved. No public dataset is committed to Git.

The existing v0.8.1 complete field bundle remains usable. A freshly built v0.8.2
binary consumes those same packs and opens the same saved reports.

## Validation plan

1. Prove all copied Go/test/preparation files match the original checksums and
   source checkouts remain unchanged.
2. Prove dependency resolution/build use the new module, contain no MatrixOne
   dependency or replacement, and preserve runtime dependency versions.
3. Run `make check`, build and verify a release from this repository alone.
4. Extract the release elsewhere; check its SHA-256, version and smoke pack.
5. Run the extracted binary on the existing owned MO smoke fixture using default
   concurrency/stability flags. Verify report observations and database cleanup.
6. Open existing GIST/T2/mixed reports through the new binary, without rewriting
   historical measurements.

Large public performance runs and UI race/PTY proofs are reused from v0.8.1:
their runtime code and dependency inputs are unchanged. New evidence targets
the repository/build/dependency and executable consumer boundaries.

## Delivery evidence

Local checks passed on 2026-10-07:

- All 28 Go source/test files, all 13 Python preparation/publication/test files,
  smoke pack, copied design records and license match their source checksums.
  Only the runner README receives intentional build/module documentation edits.
- `make check` passed: Go package tests, vet, formatting and 15 Python tests.
  `go mod tidy -diff` is empty. The resolved module graph contains no MatrixOne
  module; there are no local-path replacements. All 19 Linux runtime dependency
  versions match the previous v0.8.1 binary.
- Independent release extraction verified every bundled file against
  SHA256SUMS, `version`, smoke-pack validation, the new module's build identity,
  and a static ELF with no interpreter/native runtime dependency.
- Actual MO 4.2.1 smoke execution passed: default 1/4/8 concurrency, 13 scenario
  profiles, 51 successful measured SQL executions, zero SQL/assertion failures.
  `SHOW DATABASES` independently confirmed removal of the generated test DB.
  The smoke report also opens through `ui --plain`.
- Existing GIST filtered, T2Ranking anli and paired SQL workload reports open
  through the extracted executable. All 41 recorded historical input hashes
  are unchanged. Original MatrixOne checkout/worktree statuses and all 64
  copied source-file hashes remain unchanged.

CI configuration is present for the independent repository; no GitHub run or
remote publication is claimed by this local verification.

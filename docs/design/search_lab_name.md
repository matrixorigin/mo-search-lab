# MO Search Lab (v0.8.3)

The user accepted `mo-search-lab` on 2026-10-07 as the name of the standalone
fulltext/vector benchmark and investigation tool.

## Current names

| Surface | Name |
| --- | --- |
| Local Git root | `/home/mo/mo-search-lab` |
| Go module | `github.com/matrixorigin/mo-search-lab` |
| Executable | `mo-search-lab` |
| Command sources | `cmd/mo-search-lab/` |
| Tool archive | `mo-search-lab-v0.8.3-linux-amd64.tar.gz` |
| Display name | MO Search Lab |

The module name identifies the project; no GitHub repository or publication is
implied. `run`, `validate`, `render`, `ui` and `version` retain their arguments.
The README, Makefile, release script, CLI usage, HTML title and terminal header
use the current name. The version changes to v0.8.3 for release identity.

## Compatibility

The pack schemas, dataset/scenario IDs, report JSON fields, metric definitions
and default execution profile are unchanged. Existing GIST, T2Ranking and paired
SQL workload packs are consumed directly. Saved reports remain readable.

Keep persisted ownership markers and namespaces (`mo_retrieval_bench_`, ES owner
`mo-retrieval-bench`, `mo_retrieval_bench_es_`) stable, so existing retained test
resources and reference indexes retain the same ownership checks. These are
internal resource identifiers, independent of the executable's public name.

Original design/verification records, release links and `migration-source.json`
are historical evidence. Their original names and source hashes are preserved.
The extraction record's `cmd/mo-retrieval-bench/` targets describe v0.8.2;
the current source directory is `cmd/mo-search-lab/`.

The old full field archive remains usable; its external packs also work with
the new executable. Public data and existing measurements are not regenerated
by this rename.

## Verification

Run the existing Go/Python contract suites, vet, formatting, static build and
release extraction checks. Verify the new module/name and unchanged runtime
dependency versions. Open each existing dataset report using the new binary;
check raw report hashes are preserved. A real smoke run against the owned MO
fixture proves the released executable's connection, default profile and
cleanup boundary. Reuse previous large-data performance evidence because the
benchmark runner and scoring logic are unchanged.

Local Go tests, vet, formatting and all 15 Python contract tests passed after
the rename. `go mod tidy -diff` is empty. The release and actual consumer checks
are recorded beside the downloadable v0.8.3 artifacts.

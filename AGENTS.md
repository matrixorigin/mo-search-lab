# Repository instructions

- MO Search Lab is a standalone fulltext/vector benchmark and investigation tool. Do not import MatrixOne kernel packages or add a MatrixOne module/local-path `replace` dependency.
- Keep command behavior, versioned pack schemas, raw measurements and report semantics compatible unless the task explicitly changes them.
- Large datasets, binaries and measured reports belong outside Git. Preserve frozen input hashes and shared data files.
- `make check` verifies this repository without an SQL service. `make release` produces the static customer binary and tool archive.
- Python scripts under `tools/` run during preparation or publication; customer execution must remain a single binary plus local data packs.
- Do not publish a remote repository or push without user authorization. Keep source provenance and license notices when moving code.

#!/usr/bin/env python3
"""Prepare reproducible public-data packs off site for mo-retrieval-bench.

Only the Python standard library is needed here. Customer sites receive the
generated pack and the standalone Go runner, not this preparation script.
"""
from __future__ import annotations

import argparse
import csv
import hashlib
import json
import math
import shutil
from pathlib import Path
import struct


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(8 * 1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def write_json(path: Path, value: object) -> None:
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def ref(path: Path) -> dict[str, str]:
    return {"path": path.name, "sha256": sha256(path)}


def write_jsonl(path: Path, rows: list[dict]) -> None:
    with path.open("w", encoding="utf-8", newline="\n") as output:
        for row in rows:
            output.write(json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n")


def add_scenario(output: Path, manifest: dict, name: str, value: dict, queries: list[dict]) -> None:
    query_path = output / f"{name}.jsonl"
    scenario_path = output / f"{name}.json"
    write_jsonl(query_path, queries)
    value["queries"] = ref(query_path)
    write_json(scenario_path, value)
    manifest["scenarios"].append(ref(scenario_path))


def prepare_t2ranking(source: Path, output: Path, rows: int, query_count: int,
                     query_seed: int | None = None, relevant_grade: int = 1) -> None:
    if relevant_grade < 1 or relevant_grade > 30:
        raise ValueError("relevant-grade must be 1..30")
    collection = source / "collection.tsv"
    queries_path = source / "queries.dev.tsv"
    qrels_path = source / "qrels.dev.tsv"
    csv.field_size_limit(64 * 1024 * 1024)

    selected_ids: set[str] = set()
    selected_first: list[str] = []
    documents = output / "documents.csv"
    with collection.open("r", encoding="utf-8", newline="") as input_file, \
            documents.open("w", encoding="utf-8", newline="") as output_file:
        reader = csv.reader(input_file, delimiter="\t")
        writer = csv.writer(output_file, lineterminator="\n")
        if next(reader, None) != ["pid", "text"]:
            raise ValueError("unexpected T2Ranking collection header")
        for record in reader:
            if len(record) != 2 or not record[0].isdigit():
                raise ValueError(f"invalid T2Ranking record after {len(selected_ids)} rows")
            identifier, body = record
            if identifier in selected_ids:
                raise ValueError(f"duplicate passage ID: {identifier}")
            # The SQL loader uses one CSV record per physical line.
            body = body.replace("\r", " ").replace("\n", " ").replace("\\", "\\\\")
            writer.writerow((identifier, body))
            selected_ids.add(identifier)
            if len(selected_first) < 5:
                selected_first.append(identifier)
            if len(selected_ids) == rows:
                break
    if len(selected_ids) != rows:
        raise ValueError(f"T2Ranking has only {len(selected_ids)} rows; requested {rows}")

    judgments: dict[str, dict[str, int]] = {}
    with qrels_path.open("r", encoding="utf-8", newline="") as input_file:
        reader = csv.reader(input_file, delimiter="\t")
        if next(reader, None) != ["qid", "-", "pid", "rel"]:
            raise ValueError("unexpected T2Ranking qrels header")
        for record in reader:
            if len(record) != 4:
                raise ValueError("invalid T2Ranking qrels record")
            query_id, _, passage_id, grade_text = record
            if passage_id in selected_ids:
                grade = int(grade_text)
                if grade < 0 or grade > 30:
                    raise ValueError(f"invalid relevance grade: {grade}")
                judgments.setdefault(query_id, {})[passage_id] = grade

    query_rows: list[dict] = []
    with queries_path.open("r", encoding="utf-8", newline="") as input_file:
        reader = csv.reader(input_file, delimiter="\t")
        if next(reader, None) != ["qid", "text"]:
            raise ValueError("unexpected T2Ranking query header")
        for record in reader:
            if len(record) != 2:
                raise ValueError("invalid T2Ranking query record")
            query_id, text = record
            relevance = judgments.get(query_id, {})
            text = text.replace("\r", " ").replace("\n", " ")
            if len(text) < 3 or not any(grade >= relevant_grade for grade in relevance.values()):
                continue
            query_rows.append({"id": query_id, "params": {"text": text}, "relevance": relevance})
            if query_seed is None and len(query_rows) == query_count:
                break
    if query_seed is not None:
        query_rows.sort(key=lambda q: hashlib.sha256(f"{query_seed}:{q['id']}".encode()).hexdigest())
        query_rows = query_rows[:query_count]
    if len(query_rows) != query_count:
        raise ValueError(f"only {len(query_rows)} usable T2Ranking queries; requested {query_count}")

    manifest = {
        "schema_version": 1,
        "dataset": f"t2ranking_subset{rows}_pilot",
        "ddl": ["CREATE TABLE documents (id bigint PRIMARY KEY, body text)"],
        "loads": [{"file": ref(documents), "table": "documents", "rows": rows}],
        "indexes": ["CREATE FULLTEXT INDEX idx_body ON documents(body) WITH PARSER ngram"],
        "scenarios": [],
    }
    add_scenario(output, manifest, "fulltext_qrels", {
        "id": "fulltext_qrels",
        "route": "sql",
        "sql": "SELECT id FROM documents WHERE MATCH(body) AGAINST (? IN BOOLEAN MODE) "
               "ORDER BY MATCH(body) AGAINST (? IN BOOLEAN MODE) DESC, id LIMIT 10",
        "args": ["text", "text"],
        "oracle": "qrels",
        "top_k": 10,
        "min_score": 0.01,
    }, query_rows)
    add_scenario(output, manifest, "load_exact", {
        "id": "load_exact",
        "route": "sql",
        "sql": "SELECT id FROM documents WHERE id = ? LIMIT 1",
        "args": ["id"],
        "oracle": "exact_ids",
        "top_k": 1,
    }, [{"id": f"doc_{identifier}", "params": {"id": identifier}, "exact_ids": [identifier]}
        for identifier in selected_first])
    write_json(output / "manifest.json", manifest)
    write_json(output / "source.json", {
        "source": "THUIR/T2Ranking",
        "selection": f"first {rows} collection records; {query_count} dev queries with in-corpus grade >= {relevant_grade}; "
                     + ("source order" if query_seed is None else f"SHA-256 seeded selection, seed={query_seed}"),
        "query_seed": query_seed,
        "relevant_grade": relevant_grade,
        "query_ids": [query["id"] for query in query_rows],
        "source_sha256": {path.name: sha256(path) for path in (collection, queries_path, qrels_path)},
        "note": "Pilot subset: relevance labels outside the subset are excluded; no customer-scale quality claim.",
    })


GIST_DIMENSION = 960
GIST_RECORD_BYTES = 4 + 4 * GIST_DIMENSION


def vector_text(raw: bytes) -> str:
    values = struct.unpack("<960f", raw)
    if not all(math.isfinite(value) for value in values):
        raise ValueError("GIST vector contains non-finite values")
    return "[" + ",".join(format(value, ".9g") for value in values) + "]"


def read_fvecs_record(input_file) -> str:
    header = input_file.read(4)
    if len(header) != 4 or struct.unpack("<I", header)[0] != GIST_DIMENSION:
        raise ValueError("invalid GIST fvecs dimension")
    raw = input_file.read(4 * GIST_DIMENSION)
    if len(raw) != 4 * GIST_DIMENSION:
        raise ValueError("truncated GIST fvecs vector")
    return vector_text(raw)


def add_gist_stability(output: Path, manifest: dict, name: str, scenario: dict,
                       queries: list[dict], count: int) -> None:
    if count < 1 or count > len(queries):
        raise ValueError("stability-queries must be between 1 and the number of GIST queries")
    manifest["schema_version"] = 2
    add_scenario(output, manifest, f"{name}_stability", {
        "id": f"{name}_stability",
        "route": "sql",
        "sql": scenario["sql"],
        "args": scenario["args"],
        **({"session_sql": scenario["session_sql"]} if "session_sql" in scenario else {}),
        "oracle": "stable_multiset",
        "id_column": "id",
        "top_k": scenario["top_k"],
        "expected_rows": scenario["top_k"],
        "min_repetitions": 3,
    }, [{"id": q["id"], "params": q["params"]} for q in queries[:count]])


def prepare_gist(source: Path, output: Path, rows: int, query_count: int, nprobes: list[int], skip_default: bool,
                 stability_queries: int = 0, top_k: int = 10) -> None:
    if top_k < 1 or top_k > 1000 or top_k > rows:
        raise ValueError("top-k must be between 1 and min(rows, 1000)")
    if stability_queries < 0 or stability_queries > query_count:
        raise ValueError("stability-queries must be between 0 and queries")
    base = source / "gist_base.fvecs"
    query_file = source / "gist_query.fvecs"
    truth_file = source / "gist_groundtruth.ivecs"
    if base.stat().st_size % GIST_RECORD_BYTES != 0:
        raise ValueError("invalid GIST base file length")
    total_rows = base.stat().st_size // GIST_RECORD_BYTES
    if rows > total_rows:
        raise ValueError(f"GIST has only {total_rows} base vectors; requested {rows}")
    if query_file.stat().st_size < query_count * GIST_RECORD_BYTES:
        raise ValueError("not enough GIST queries")
    full = rows == total_rows
    lists = min(1024, max(2, rows // 1000))
    if any(probe > lists for probe in nprobes):
        raise ValueError(f"nprobe cannot exceed index lists={lists}")
    query_rows: list[dict] = []
    with query_file.open("rb") as input_file, truth_file.open("rb") as truth:
        for number in range(query_count):
            vector = read_fvecs_record(input_file)
            count_raw = truth.read(4)
            if len(count_raw) != 4:
                raise ValueError("truncated GIST ground truth")
            count = struct.unpack("<I", count_raw)[0]
            if count < top_k or count > 1000:
                raise ValueError(f"invalid GIST truth width: {count}")
            raw_ids = truth.read(4 * count)
            if len(raw_ids) != 4 * count:
                raise ValueError("truncated GIST ground truth IDs")
            exact_ids = [str(identifier) for identifier in struct.unpack(f"<{top_k}I", raw_ids[:4 * top_k])]
            record = {"id": f"gist_{number}", "params": {"vector": vector}}
            if rows == total_rows:
                record["exact_ids"] = exact_ids
            query_rows.append(record)
    documents = output / "documents.csv"
    with base.open("rb") as input_file, documents.open("w", encoding="utf-8", newline="\n") as output_file:
        for identifier in range(rows):
            output_file.write(f'{identifier},"{read_fvecs_record(input_file)}"\n')

    manifest = {
        "schema_version": 2 if nprobes or stability_queries else 1,
        "dataset": f"gist1m_{'full' if full else f'subset{rows}_pilot'}",
        "ddl": [f"CREATE TABLE documents (id bigint PRIMARY KEY, embedding vecf32({GIST_DIMENSION}))"],
        "loads": [{"file": ref(documents), "table": "documents", "rows": rows}],
        "indexes": [f"CREATE INDEX idx_vec USING ivfflat ON documents(embedding) lists={lists} op_type 'vector_l2_ops'"],
        "scenarios": [],
    }
    base_sql = f"SELECT id FROM documents ORDER BY l2_distance(embedding, ?) LIMIT {top_k}"
    for probe in ([] if skip_default else [None]) + nprobes:
        name = "vector" if probe is None else f"vector_nprobe_{probe}"
        scenario = {
            "id": name,
            "route": "sql",
            "sql": base_sql,
            **({"session_sql": [f"SET probe_limit={probe}"]} if probe is not None else {}),
            "args": ["vector"],
            "oracle": "ann_recall" if full else "nonempty",
            "top_k": top_k,
            **({"min_score": 0.7} if full else {}),
        }
        add_scenario(output, manifest, name, scenario, query_rows)
        if stability_queries:
            add_gist_stability(output, manifest, name, scenario, query_rows, stability_queries)
    write_json(output / "manifest.json", manifest)
    write_json(output / "source.json", {
        "source": "INRIA TexMex GIST1M",
        "selection": f"first {rows} base vectors; first {query_count} official queries",
        "source_sha256": {path.name: sha256(path) for path in (base, query_file, truth_file)},
        "note": "Official exact Top-K truth is valid only when the full 1M base vectors are loaded."
                f" Explicit nprobe scenarios: {nprobes}; server-default included: {not skip_default}.",
        "stability_queries_per_scenario": stability_queries,
        "top_k": top_k,
    })


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("dataset", choices=("t2ranking", "gist"))
    parser.add_argument("--source-dir", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--rows", type=int, required=True)
    parser.add_argument("--queries", type=int, required=True)
    parser.add_argument("--nprobes", default="", help="comma-separated GIST nprobe values for additional SQL scenarios")
    parser.add_argument("--skip-default", action="store_true", help="omit the GIST server-default probe scenario")
    parser.add_argument("--stability-queries", type=int, default=0, help="add GIST repeated-result scenarios using the first N query vectors")
    parser.add_argument("--top-k", type=int, default=None, help="GIST returned neighbors and exact truth width (default 10)")
    parser.add_argument("--query-seed", type=int, default=None, help="T2Ranking: reproducible hash sample across eligible dev queries")
    parser.add_argument("--relevant-grade", type=int, default=1, help="T2Ranking: minimum grade required for eligible queries (official retrieval: 2)")
    args = parser.parse_args()
    if args.rows <= 0 or args.queries <= 0 or args.queries > 100000:
        parser.error("rows and queries must be positive; queries must not exceed 100000")
    if not args.source_dir.is_dir():
        parser.error("source-dir does not exist")
    if args.dataset == "t2ranking" and (args.nprobes or args.skip_default or args.stability_queries or args.top_k is not None):
        parser.error("--nprobes, --skip-default, --stability-queries and --top-k apply only to GIST")
    if args.dataset == "gist" and (args.query_seed is not None or args.relevant_grade != 1):
        parser.error("--query-seed and --relevant-grade apply only to T2Ranking")
    if not 1 <= args.relevant_grade <= 30:
        parser.error("--relevant-grade must be 1..30")
    if args.top_k is not None and (args.top_k < 1 or args.top_k > min(args.rows, 1000)):
        parser.error("--top-k must be between 1 and min(rows, 1000)")
    if args.stability_queries < 0 or args.stability_queries > args.queries:
        parser.error("--stability-queries must be between 0 and queries")
    try:
        nprobes = [int(value) for value in args.nprobes.split(",") if value]
    except ValueError:
        parser.error("--nprobes must contain comma-separated positive integers")
    if any(value <= 0 or value > 80000 for value in nprobes) or len(nprobes) != len(set(nprobes)):
        parser.error("--nprobes must contain unique values between 1 and 80000")
    if args.skip_default and not nprobes:
        parser.error("--skip-default requires --nprobes")
    args.out.mkdir(parents=True, exist_ok=False)
    try:
        if args.dataset == "t2ranking":
            prepare_t2ranking(args.source_dir, args.out, args.rows, args.queries, args.query_seed, args.relevant_grade)
        else:
            prepare_gist(args.source_dir, args.out, args.rows, args.queries, nprobes, args.skip_default, args.stability_queries, args.top_k if args.top_k is not None else 10)
    except BaseException:
        shutil.rmtree(args.out)
        raise
    print(f"pack: {args.out}")


if __name__ == "__main__":
    main()

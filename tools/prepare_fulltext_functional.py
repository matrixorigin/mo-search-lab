#!/usr/bin/env python3
"""Create a hand-judged ngram/gojieba functional pack, using only the standard library."""
import argparse
import csv
from pathlib import Path
import shutil

from prepare_public_pack import add_scenario, ref, write_json


def prepare(output: Path) -> None:
    output.mkdir(parents=True, exist_ok=False)
    try:
        documents = output / "documents.csv"
        with documents.open("w", newline="", encoding="utf-8") as stream:
            csv.writer(stream, lineterminator="\n").writerows([
                (1, "苹果 香蕉"), (2, "香蕉 苹果"), (3, "苹果"), (4, "香蕉"),
                (5, "大学怎么网上选宿舍"), (6, "大学网上宿舍"),
            ])
        cases = [
            ("single", "苹果", ["1", "2", "3"]),
            ("or", "苹果 香蕉", ["1", "2", "3", "4"]),
            ("and", "+苹果 +香蕉", ["1", "2"]),
            ("phrase", '"苹果 香蕉"', ["1"]),
            ("whole_sentence", "大学怎么网上选宿舍", ["5"]),
            ("expected_empty", "火星飞船", []),
        ]
        manifest = {"schema_version": 3, "dataset": "fulltext_functional_v1",
                    "ddl": [], "loads": [], "indexes": [], "scenarios": []}
        for parser in ("ngram", "gojieba"):
            table = f"docs_{parser}"
            manifest["ddl"].append(f"CREATE TABLE {table} (id bigint PRIMARY KEY, body text)")
            manifest["loads"].append({"file": ref(documents), "table": table, "rows": 6})
            manifest["indexes"].append(f"CREATE FULLTEXT INDEX idx_body ON {table}(body) WITH PARSER {parser}")
            name = f"semantic_{parser}"
            add_scenario(output, manifest, name, {
                "id": name, "route": "sql", "oracle": "exact_ids", "top_k": 10,
                "sql": f"SELECT id FROM {table} WHERE MATCH(body) AGAINST (? IN BOOLEAN MODE) ORDER BY id LIMIT 10",
                "args": ["text"], "session_sql": ["SET ft_relevancy_algorithm = 'TF-IDF'"],
            }, [{"id": name, "params": {"text": text}, "exact_ids": ids} for name, text, ids in cases])
        write_json(output / "manifest.json", manifest)
        write_json(output / "source.json", {"source": "hand-authored public synthetic passages",
                   "contract": "single term, OR, AND, quoted phrase, contiguous whole fragment, expected empty; both parsers",
                   "expected_results": "specified independently before executing MatrixOne; ORDER BY id"})
    except BaseException:
        shutil.rmtree(output)
        raise


if __name__ == "__main__":
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument("--out", type=Path, required=True)
    prepare(cli.parse_args().out)

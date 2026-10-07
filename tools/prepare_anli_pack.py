#!/usr/bin/env python3
"""Freeze anli-shaped queries over an existing T2Ranking pilot pack, off-site.

Preparation requires jieba==0.42.1, spacy==3.8.7 and zh_core_web_sm==3.8.0.
The resulting pack is consumed by the standalone binary without these packages.
"""
import argparse
import errno
import importlib.metadata
import json
import os
from pathlib import Path
import re
import shutil
import string

from prepare_public_pack import add_scenario, ref as file_ref, sha256, write_json


def verified_file(root: Path, reference: dict) -> Path:
    path = (root / reference["path"]).resolve()
    if not path.is_relative_to(root.resolve()) or not path.is_file():
        raise ValueError("input reference must be a file inside the base pack")
    if sha256(path) != reference["sha256"]:
        raise ValueError(f"input digest mismatch: {reference['path']}")
    return path


def make_tokenizer(resources: Path, fallback: bool = True):
    """Port the snapshot's HybridTokenizer and Boolean cleaning rules.

    Use a private jieba instance so preparing a pack cannot change another
    tokenizer's dictionary. English/numeric spans use the same spacy model as
    the snapshot, even though the first pilot is predominantly Chinese.
    """
    import jieba
    import spacy

    versions = {name: importlib.metadata.version(name) for name in ("jieba", "spacy")}
    if versions != {"jieba": "0.42.1", "spacy": "3.8.7"}:
        raise ValueError("preparation requires jieba==0.42.1 and spacy==3.8.7")
    nlp = spacy.load("zh_core_web_sm")
    if nlp.meta["version"] != "3.8.0":
        raise ValueError("preparation requires zh_core_web_sm==3.8.0")
    segmenter = jieba.Tokenizer()
    segmenter.load_userdict(str(resources / "custom_dict.txt"))
    stops = {line.strip() for line in (resources / "cn_stopwords.txt").read_text().splitlines() if line.strip()}
    punctuation = set(string.punctuation) | set('""\'\'（）【】《》…—！？，。；：、')
    punctuation -= {" ", "\u3000", "\u00a0", "\u2003"}

    def tokenize(text: str) -> str:
        clean = re.sub(r"[\s\u3000\u00a0\u2003\u200b]+", "", text)
        words = []
        for segment in re.findall(r"([\u4e00-\u9fff]+|[^\u4e00-\u9fff]+)", clean):
            if re.search(r"[\u4e00-\u9fff]+", segment):
                words.extend(word for word in segmenter.lcut(segment, cut_all=False)
                             if word not in stops and len(word) > 1)
            else:
                words.extend(token.text for token in nlp(segment))
        # The snapshot caps before punctuation filtering, then cleans each
        # retained token before joining it with spaces in the SQL builder.
        words = [word for word in words[:15] if word not in punctuation]
        cleaned = [re.sub(r"[^一-鿿\w]", "", word.replace("%", "")) for word in words]
        result = " ".join(word for word in cleaned if word)
        if fallback and not result.strip():
            result = " ".join(word for segment in text.split()
                              if (word := re.sub(r"[^一-鿿\w]", "", segment)))
        return result.replace("'", "")

    return tokenize, {**versions, "zh_core_web_sm": nlp.meta["version"],
                      "rules": "snapshot HybridTokenizer plus Boolean input cleaning; first 15 tokens",
                      "empty_input": "general-route fallback" if fallback else "BM25Service skips empty token input",
                      "resources_sha256": {name: sha256(resources / name)
                                           for name in ("tokenizer.py", "custom_dict.txt", "cn_stopwords.txt")}}


def canonical_sql(top_k: int) -> str:
    return ("SELECT id FROM documents WHERE MATCH(body) AGAINST (? IN BOOLEAN MODE) "
            "ORDER BY MATCH(body) AGAINST (? IN BOOLEAN MODE) DESC, id "
            f"LIMIT {top_k}")


def anli_sql(top_k: int, multiple: int) -> str:
    # Public passages stand in for embedding_text. Synthetic metadata keeps
    # the application SQL shape observable without inventing customer data or
    # claiming a selective permissions workload. The cutoff is AFTER Top-K,
    # matching BM25Service.search's pandas filter.
    return (
        "SELECT id FROM (SELECT id, vec_dist FROM (SELECT id, "
        "CAST('2026-01-01' AS DATETIME) AS business_time, 'OPEN' AS allow_access, "
        "'OPEN' AS allow_identities, CAST(NULL AS VARCHAR) AS delete_flag, "
        "MATCH(body) AGAINST (? IN BOOLEAN MODE) AS vec_dist "
        f"FROM documents LIMIT {top_k * multiple}) a "
        "WHERE (allow_access IS NULL OR LOCATE('PC', allow_access) > 0 "
        "OR LOCATE('OPEN', allow_access) > 0) AND delete_flag IS NULL "
        f"ORDER BY vec_dist DESC, business_time DESC, id LIMIT {top_k}) chosen "
        "WHERE vec_dist > 0.01 ORDER BY vec_dist DESC, id"
    )


def prepare_anli(base: Path, output: Path, tokenize, provenance: dict,
                 top_k: int = 100, multiple: int = 100, stability_queries: int = 5,
                 parser_name: str = "ngram", health: bool = False) -> None:
    if top_k < 1 or top_k > 1000 or multiple < 1 or multiple > 100:
        raise ValueError("top-k must be 1..1000 and candidate-multiple 1..100")
    if parser_name not in ("ngram", "gojieba"):
        raise ValueError("parser must be ngram or gojieba")
    base = base.resolve()
    manifest = json.loads((base / "manifest.json").read_text())
    if len(manifest["loads"]) != 1 or manifest["loads"][0]["table"] != "documents":
        raise ValueError("base must be a one-table T2Ranking pack")
    load = manifest["loads"][0]
    data = verified_file(base, load["file"])
    if top_k > load["rows"]:
        raise ValueError("top-k exceeds corpus rows")
    scenarios = {}
    for reference in manifest["scenarios"]:
        scene = json.loads(verified_file(base, reference).read_text())
        if scene["id"] in scenarios:
            raise ValueError("duplicate base scenario")
        scenarios[scene["id"]] = scene
    quality = scenarios["fulltext_qrels"]
    exact = scenarios["load_exact"]
    if quality["oracle"] != "qrels" or exact["oracle"] != "exact_ids":
        raise ValueError("base must supply qrels and independent load checks")
    inputs = [json.loads(line) for line in verified_file(base, quality["queries"]).read_text().splitlines()]
    load_checks = [json.loads(line) for line in verified_file(base, exact["queries"]).read_text().splitlines()]
    if not inputs or len(inputs) > 100000 or not 0 <= stability_queries <= len(inputs):
        raise ValueError("stability query count must fit the nonempty base query selection")
    prepared = []
    skipped = []
    seen = set()
    for query in inputs:
        original = query["params"]["text"]
        if not isinstance(original, str) or not original or not query["id"] or query["id"] in seen:
            raise ValueError("base queries require unique IDs and nonempty text")
        seen.add(query["id"])
        tokens = tokenize(original)
        if isinstance(tokens, str) and not tokens.strip() and health:
            skipped.append({"id": query["id"], "raw_text": original,
                            "reason": "anli BM25Service skips an empty tokenized query; no MO SQL"})
            continue
        if not isinstance(tokens, str) or not tokens.strip():
            raise ValueError(f"query {query['id']} tokenized to empty text")
        relevance = query["relevance"]
        if not relevance or not any(grade >= (2 if health else 1) for grade in relevance.values()):
            raise ValueError("base query must retain a positive subset judgment")
        prepared.append({"id": query["id"], "params": {"raw_text": original, "anli_text": tokens},
                         "relevance": relevance})
    if not prepared or stability_queries > len(prepared):
        raise ValueError("not enough searchable queries after application-side filtering")

    # All preflight precedes output ownership. A partial new pack is removed;
    # an existing destination is never overwritten or removed.
    output.mkdir(parents=True, exist_ok=False)
    try:
        copied = output / "documents.csv"
        try:
            os.link(data, copied)
        except OSError as error:
            if error.errno != errno.EXDEV:
                raise
            shutil.copyfile(data, copied)
        result = {"schema_version": 3 if health else 2,
                  "dataset": f"t2ranking_anli_{load['rows']}_{parser_name}_v3" if health else f"t2ranking_anli_{load['rows']}_v1",
                  "ddl": ["CREATE TABLE documents (id bigint PRIMARY KEY, body text)"],
                  "loads": [{"file": file_ref(copied), "table": "documents", "rows": load["rows"]}],
                  "indexes": [f"CREATE FULLTEXT INDEX idx_body ON documents(body) WITH PARSER {parser_name}"],
                  "scenarios": []}
        for name, sql, args, algorithm in (
            ("raw_sentence_tfidf", canonical_sql(top_k), ["raw_text", "raw_text"], "TF-IDF"),
            ("anli_tokens_tfidf", canonical_sql(top_k), ["anli_text", "anli_text"], "TF-IDF"),
            ("anli_sql_tfidf", anli_sql(top_k, multiple), ["anli_text"], "TF-IDF"),
            ("anli_sql_bm25", anli_sql(top_k, multiple), ["anli_text"], "BM25"),
        ):
            if health and not name.startswith("anli_sql_"):
                continue
            scene = {"id": name, "route": "sql", "sql": sql, "args": args,
                     "oracle": "qrels", "top_k": top_k, "min_score": 0.01,
                     "session_sql": [f"SET ft_relevancy_algorithm = '{algorithm}'"]}
            if health:
                scene.update(quality_mode="observe", ndcg_gain="linear", relevant_grade=2, min_score=0)
            add_scenario(output, result, name, scene, prepared)
            if name.startswith("anli_sql_") and stability_queries:
                repeated = [{"id": q["id"], "params": q["params"]}
                            for q in prepared[:stability_queries]]
                stable_scene = {
                    **scene, "id": name + "_stability", "oracle": "stable_multiset",
                    "min_score": 0, "id_column": "id", "expected_rows": 0,
                    "min_repetitions": 3,
                }
                for key in ("quality_mode", "ndcg_gain", "relevant_grade"):
                    stable_scene.pop(key, None)
                if health:
                    stable_scene.update(check_order=True, allow_empty=True)
                add_scenario(output, result, name + "_stability", stable_scene, repeated)
        add_scenario(output, result, "load_exact", {
            "id": "load_exact", "route": "sql", "sql": exact["sql"],
            "args": exact["args"], "oracle": "exact_ids", "top_k": 1,
        }, load_checks)
        write_json(output / "manifest.json", result)
        write_json(output / "source.json", {
            "source": "THUIR/T2Ranking", "base_manifest_sha256": sha256(base / "manifest.json"),
            "base_source": json.loads((base / "source.json").read_text()),
            "tokenizer": provenance, "top_k": top_k, "candidate_multiple": multiple,
            "query_ids": [query["id"] for query in prepared],
            "application_skipped_queries": skipped,
            "candidate_queries": len(inputs),
            "parser": parser_name,
            "evaluation": {"mode": "observe" if health else "assert", "ndcg_gain": "linear" if health else "exponential",
                           "relevant_grade": 2 if health else 1},
            "metadata": "Synthetic OPEN permission and constant timestamp projected in SQL; no customer records",
            "note": ("Frozen dev-query sample; observational relevance, no acceptance threshold. Unjudged passages receive zero evaluation gain. Corpus/selection is in base_source; not an official full test-set run."
                     if health else "Controlled pilot; subset qrels and custom 0.01 nDCG check are not an official benchmark or acceptance line.")
                    + " Variable hit counts are allowed; stability does not prove retrieval quality. Customer parser/algorithm is unconfirmed; OPEN permissions/timestamp are synthetic.",
        })
    except BaseException:
        shutil.rmtree(output)
        raise


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-pack", type=Path, required=True)
    parser.add_argument("--tokenizer-dir", type=Path, required=True,
                        help="snapshot directory with tokenizer.py, custom_dict.txt and cn_stopwords.txt")
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--top-k", type=int, default=100)
    parser.add_argument("--candidate-multiple", type=int, default=100)
    parser.add_argument("--stability-queries", type=int, default=5)
    parser.add_argument("--parser", choices=("ngram", "gojieba"), default="ngram")
    parser.add_argument("--health", action="store_true", help="schema 3: observe official linear-gain metrics; check repeat ordering")
    args = parser.parse_args()
    tokenize, provenance = make_tokenizer(args.tokenizer_dir, fallback=not args.health)
    prepare_anli(args.base_pack, args.out, tokenize, provenance,
                 args.top_k, args.candidate_multiple, args.stability_queries, args.parser, args.health)
    print(f"pack: {args.out}")


if __name__ == "__main__":
    main()

"""Small pack-contract evidence; no model installation or SQL service needed."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import prepare_anli_pack as anli
import prepare_public_pack as prep
import render_t2_health_portal as portal


class AnliPackTests(unittest.TestCase):
    def base_pack(self, root):
        base = root / "base"
        base.mkdir()
        (base / "documents.csv").write_text('1,"大学宿舍"\n2,"大学课程"\n')
        manifest = {"schema_version": 1, "dataset": "t2ranking_fixture", "ddl": [], "indexes": [],
                    "loads": [{"file": prep.ref(base / "documents.csv"), "table": "documents", "rows": 2}],
                    "scenarios": []}
        prep.add_scenario(base, manifest, "fulltext_qrels", {
            "id": "fulltext_qrels", "oracle": "qrels", "top_k": 2,
        }, [{"id": "q1", "params": {"text": "大学怎么选宿舍"}, "relevance": {"1": 3, "2": 0}}])
        prep.add_scenario(base, manifest, "load_exact", {
            "id": "load_exact", "oracle": "exact_ids", "top_k": 1,
            "sql": "SELECT id FROM documents WHERE id = ? LIMIT 1", "args": ["id"],
        }, [{"id": "doc1", "params": {"id": 1}, "exact_ids": ["1"]}])
        prep.write_json(base / "manifest.json", manifest)
        prep.write_json(base / "source.json", {"source": "fixture"})
        return base

    def test_paired_inputs_provenance_and_variable_count_stability(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            base = self.base_pack(root)
            before = {p.name: p.read_bytes() for p in base.iterdir()}
            output = root / "output"
            anli.prepare_anli(base, output, lambda text: "大学 宿舍", {"fixture": "tokenizer"},
                              top_k=2, multiple=10, stability_queries=1)
            manifest = json.loads((output / "manifest.json").read_text())
            self.assertEqual(manifest["schema_version"], 2)
            self.assertEqual(len(manifest["scenarios"]), 7)
            for reference in manifest["scenarios"]:
                scene = json.loads((output / reference["path"]).read_text())
                self.assertEqual(prep.sha256(output / reference["path"]), reference["sha256"])
                queries = output / scene["queries"]["path"]
                self.assertEqual(prep.sha256(queries), scene["queries"]["sha256"])
                query = json.loads(queries.read_text())
                if scene["id"] == "load_exact":
                    self.assertEqual(query["exact_ids"], ["1"])
                    continue
                self.assertEqual(query["id"], "q1")
                self.assertEqual(query["params"], {"raw_text": "大学怎么选宿舍", "anli_text": "大学 宿舍"})
                self.assertIn("LIMIT 2", scene["sql"])
                self.assertEqual(scene["top_k"], 2)
                if scene["oracle"] == "stable_multiset":
                    self.assertEqual(scene["expected_rows"], 0)
                    self.assertEqual(scene["min_repetitions"], 3)
                    self.assertEqual(scene["id_column"], "id")
                    self.assertNotIn("relevance", query)
                    self.assertNotIn("exact_ids", query)
                else:
                    self.assertEqual(query["relevance"], {"1": 3, "2": 0})
                    self.assertEqual(scene["min_score"], 0.01)
                    self.assertEqual(scene["oracle"], "qrels")
                if "anli_sql" in scene["id"]:
                    self.assertIn("LIMIT 20", scene["sql"])
                    self.assertIn("delete_flag IS NULL", scene["sql"])
                    self.assertIn("WHERE vec_dist > 0.01", scene["sql"])
            raw = json.loads((output / "raw_sentence_tfidf.json").read_text())
            token = json.loads((output / "anli_tokens_tfidf.json").read_text())
            self.assertEqual(raw["sql"], token["sql"])
            self.assertEqual(raw["session_sql"], token["session_sql"])
            self.assertEqual(raw["args"], ["raw_text", "raw_text"])
            self.assertEqual(token["args"], ["anli_text", "anli_text"])
            tf = json.loads((output / "anli_sql_tfidf.json").read_text())
            bm = json.loads((output / "anli_sql_bm25.json").read_text())
            self.assertEqual(tf["sql"], bm["sql"])
            self.assertEqual(bm["session_sql"], ["SET ft_relevancy_algorithm = 'BM25'"])
            source = json.loads((output / "source.json").read_text())
            self.assertEqual(source["base_manifest_sha256"], hashlib.sha256(before["manifest.json"]).hexdigest())
            self.assertEqual(source["query_ids"], ["q1"])
            self.assertEqual(source["tokenizer"], {"fixture": "tokenizer"})
            self.assertEqual({p.name: p.read_bytes() for p in base.iterdir()}, before)

    def test_preflight_and_failure_preserve_existing_inputs_and_outputs(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            base = self.base_pack(root)
            output = root / "output"
            for k, multiple, count in [(0, 1, 1), (3, 1, 1), (2, 0, 1), (2, 101, 1), (2, 1, 2)]:
                with self.assertRaises(ValueError):
                    anli.prepare_anli(base, output, lambda text: "大学", {}, k, multiple, count)
                self.assertFalse(output.exists())
            with self.assertRaisesRegex(ValueError, "empty"):
                anli.prepare_anli(base, output, lambda text: "", {}, 2, 1, 1)
            self.assertFalse(output.exists())
            with patch.object(anli, "add_scenario", side_effect=OSError("write failure")):
                with self.assertRaisesRegex(OSError, "write failure"):
                    anli.prepare_anli(base, output, lambda text: "大学", {}, 2, 1, 1)
            self.assertFalse(output.exists())
            output.mkdir()
            sentinel = output / "existing"
            sentinel.write_text("keep")
            with self.assertRaises(FileExistsError):
                anli.prepare_anli(base, output, lambda text: "大学", {}, 2, 1, 1)
            self.assertEqual(sentinel.read_text(), "keep")
            (base / "documents.csv").write_text("corrupt")
            with self.assertRaisesRegex(ValueError, "digest mismatch"):
                anli.prepare_anli(base, root / "another", lambda text: "大学", {}, 2, 1, 1)
            self.assertFalse((root / "another").exists())

    def test_health_pair_preserves_inputs_and_has_no_quality_acceptance_line(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            base = self.base_pack(root)
            frozen = []
            for parser in ("ngram", "gojieba"):
                output = root / parser
                anli.prepare_anli(base, output, lambda text: "大学 宿舍", {}, 2, 10, 1, parser, True)
                manifest = json.loads((output / "manifest.json").read_text())
                self.assertEqual(manifest["schema_version"], 3)
                self.assertEqual(len(manifest["scenarios"]), 5)
                self.assertIn(f"WITH PARSER {parser}", manifest["indexes"][0])
                frozen.append((output / "anli_sql_tfidf.jsonl").read_bytes())
                for reference in manifest["scenarios"]:
                    scene = json.loads((output / reference["path"]).read_text())
                    if scene["oracle"] == "qrels":
                        self.assertEqual(scene["min_score"], 0)
                        self.assertEqual(scene["quality_mode"], "observe")
                        self.assertEqual(scene["ndcg_gain"], "linear")
                        self.assertEqual(scene["relevant_grade"], 2)
                    elif scene["oracle"] == "stable_multiset":
                        self.assertTrue(scene["check_order"])
                        self.assertTrue(scene["allow_empty"])
                        self.assertNotIn("quality_mode", scene)
            self.assertEqual(*frozen)

    def test_portal_requires_same_measured_inputs_and_metric_conventions(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            function = root / "functional"
            function.mkdir()
            prep.write_json(function / "report.json", {"status": "passed", "scenarios": [{"executions": 12, "failures": 0}]})
            for parser in ("ngram", "gojieba"):
                folder = root / parser
                folder.mkdir()
                scenes = []
                for algorithm in ("tfidf", "bm25"):
                    for level in (1, 4, 8):
                        scenes.append({"id": f"anli_sql_{algorithm}", "oracle": "qrels", "effective_concurrency": level,
                            "top_k": 100, "ndcg_gain": "linear", "relevant_grade": 2, "quality_mode": "observe", "queries_sha256": "q"*64,
                            "session_sql": [f"SET ft_relevancy_algorithm = '{'TF-IDF' if algorithm == 'tfidf' else 'BM25'}'"],
                            "qps": 10, "p90_ms": 1, "p95_ms": 2, "p99_ms": 3, "sql_successes": 1, "sql_failures": 0,
                            "results": [{"id": "q", "sql_succeeded": True, "pass": True, "ids": ["a"],
                                         "quality": {"ndcg": .5, "ndcg_at_10": .25, "recall": .75, "mrr_at_10": 1}}]})
                    scenes.append({"id": f"anli_sql_{algorithm}_stability", "oracle": "stable_multiset",
                        "check_order": True, "allow_empty": True, "selected_queries": 1, "repetitions": 3,
                        "results": [{"sql_succeeded": True, "pass": True}]})
                prep.write_json(folder / "source.json", {"candidate_queries": 1, "application_skipped_queries": [], "query_ids": ["q"]})
                prep.write_json(folder / "report.json", {"inputs": [{"sha256": "d"*64, "rows": 100}],
                    "index_sql": [f"CREATE FULLTEXT INDEX idx ON documents(body) WITH PARSER {parser}"],
                    "cleanup": "dropped", "stages": [], "scenarios": scenes})
                (folder / "report.html").write_text('<style></style><section id="stability">real measured stability</section>')
            body = portal.render(root, "ngram", "gojieba", "functional")
            self.assertNotIn('id="findings"', body)
            self.assertIn("0.2500", body)
            self.assertIn('id="quality-metric-help"', body)
            path = root / "gojieba/report.json"
            report = json.loads(path.read_text())
            report["scenarios"][0]["queries_sha256"] = "different"
            prep.write_json(path, report)
            with self.assertRaisesRegex(ValueError, "digests differ"):
                portal.render(root, "ngram", "gojieba", "functional")

            report["scenarios"][1]["queries_sha256"] = "q"*64
            report["scenarios"][1]["sql_successes"] = 0
            prep.write_json(path, report)
            with self.assertRaisesRegex(ValueError, "no successful latency"):
                portal.render(root, "ngram", "gojieba", "functional")
            report["scenarios"][1]["sql_successes"] = 1
            report["scenarios"][0]["queries_sha256"] = "q"*64
            report["scenarios"][0]["ndcg_gain"] = "exponential"
            prep.write_json(path, report)
            with self.assertRaisesRegex(ValueError, "explicit observational"):
                portal.render(root, "ngram", "gojieba", "functional")
            report["scenarios"][0]["ndcg_gain"] = "linear"
            report["scenarios"][1]["queries_sha256"] = "different at concurrency 4"
            prep.write_json(path, report)
            with self.assertRaisesRegex(ValueError, "digests differ"):
                portal.render(root, "ngram", "gojieba", "functional")

    def test_health_records_application_empty_input_without_fallback_sql(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            base = self.base_pack(root)
            scene_path = base / "fulltext_qrels.json"
            scene = json.loads(scene_path.read_text())
            queries = base / scene["queries"]["path"]
            with queries.open("a") as stream:
                stream.write(json.dumps({"id": "skip", "params": {"text": "啥也不是"}, "relevance": {"1": 3}}) + "\n")
            scene["queries"] = prep.ref(queries)
            prep.write_json(scene_path, scene)
            manifest = json.loads((base / "manifest.json").read_text())
            manifest["scenarios"][0] = prep.ref(scene_path)
            prep.write_json(base / "manifest.json", manifest)
            output = root / "health"
            anli.prepare_anli(base, output, lambda text: "大学 宿舍" if "大学" in text else "", {}, 2, 10, 1, "ngram", True)
            self.assertEqual(len((output / "anli_sql_tfidf.jsonl").read_text().splitlines()), 1)
            source = json.loads((output / "source.json").read_text())
            self.assertEqual(source["candidate_queries"], 2)
            self.assertEqual(source["application_skipped_queries"][0]["id"], "skip")


if __name__ == "__main__":
    unittest.main()

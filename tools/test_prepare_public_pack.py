"""Small deterministic contract tests for off-site GIST pack preparation."""
import hashlib
import json
from pathlib import Path
import struct
import tempfile
import unittest

import prepare_public_pack as prep


class GistStabilityPackTests(unittest.TestCase):
    def test_seeded_t2_selection_uses_positive_grade_two_and_is_reproducible(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "source"
            source.mkdir()
            (source / "collection.tsv").write_text("pid\ttext\n1\t大学宿舍\n2\t大学课程\n")
            (source / "queries.dev.tsv").write_text("qid\ttext\n" + "".join(f"{i}\t大学问题{i}\n" for i in range(12)))
            (source / "qrels.dev.tsv").write_text("qid\t-\tpid\trel\n" + "".join(f"{i}\t0\t1\t{1 if i == 0 else 3}\n" for i in range(12)))
            selections = []
            for seed in (20261006, 20261006, 42):
                output = root / f"pack{len(selections)}"
                output.mkdir()
                prep.prepare_t2ranking(source, output, 2, 3, query_seed=seed, relevant_grade=2)
                queries = [json.loads(line) for line in (output / "fulltext_qrels.jsonl").read_text().splitlines()]
                selections.append([query["id"] for query in queries])
                self.assertEqual(len(queries), 3)
                self.assertNotIn("0", selections[-1])
                self.assertTrue(all(query["relevance"] == {"1": 3} for query in queries))
            self.assertEqual(selections[0], selections[1])
            self.assertNotEqual(selections[0], selections[2])

    def test_companions_keep_inputs_and_approximate_oracles_separate(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source, output = root / "source", root / "pack"
            source.mkdir()
            output.mkdir()

            def vector(value):
                return struct.pack("<I960f", 960, *([value] * 960))

            # Ten rows are the minimum that supports a Top-10 answer.
            (source / "gist_base.fvecs").write_bytes(b"".join(vector(i) for i in range(10)))
            (source / "gist_query.fvecs").write_bytes(vector(0.5) + vector(7.5))
            truth = b"".join(struct.pack("<I10I", 10, *sorted(range(10), key=lambda i: (abs(i - q), i))) for q in (0.5, 7.5))
            (source / "gist_groundtruth.ivecs").write_bytes(truth)
            prep.prepare_gist(source, output, 10, 2, [1], False, 1)
            manifest = json.loads((output / "manifest.json").read_text())
            self.assertEqual(manifest["schema_version"], 2)
            self.assertEqual(len(manifest["scenarios"]), 4)
            for reference in manifest["scenarios"]:
                path = output / reference["path"]
                self.assertEqual(hashlib.sha256(path.read_bytes()).hexdigest(), reference["sha256"])
            for name in ("vector", "vector_nprobe_1"):
                ordinary = json.loads((output / f"{name}.json").read_text())
                stable = json.loads((output / f"{name}_stability.json").read_text())
                queries = [json.loads(line) for line in (output / ordinary["queries"]["path"]).read_text().splitlines()]
                repeats = [json.loads(line) for line in (output / stable["queries"]["path"]).read_text().splitlines()]
                self.assertEqual(stable["sql"], ordinary["sql"])
                self.assertEqual(stable.get("session_sql"), ordinary.get("session_sql"))
                self.assertEqual(stable["oracle"], "stable_multiset")
                self.assertEqual(stable["id_column"], "id")
                self.assertEqual(ordinary["oracle"], "ann_recall")
                self.assertEqual(stable["expected_rows"], 10)
                self.assertEqual(stable["min_repetitions"], 3)
                self.assertEqual(repeats, [{"id": queries[0]["id"], "params": queries[0]["params"]}])
                self.assertIn("exact_ids", queries[0])
                self.assertEqual(hashlib.sha256((output / stable["queries"]["path"]).read_bytes()).hexdigest(), stable["queries"]["sha256"])
            with self.assertRaises(ValueError):
                prep.prepare_gist(source, output, 10, 2, [], False, 3)

    def test_top100_uses_complete_official_truth_and_matching_stability_limit(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source, output = root / "source", root / "pack"
            source.mkdir()
            output.mkdir()
            def vector(value):
                return struct.pack("<I960f", 960, *([value] * 960))
            (source / "gist_base.fvecs").write_bytes(b"".join(vector(i) for i in range(100)))
            (source / "gist_query.fvecs").write_bytes(vector(99.5))
            ids = list(reversed(range(100)))
            truth_path = source / "gist_groundtruth.ivecs"
            truth_path.write_bytes(struct.pack("<I100I", 100, *ids))
            prep.prepare_gist(source, output, 100, 1, [], False, 1, 100)
            manifest = json.loads((output / "manifest.json").read_text())
            ordinary = json.loads((output / "vector.json").read_text())
            stable = json.loads((output / "vector_stability.json").read_text())
            query = json.loads((output / "vector.jsonl").read_text())
            repeated = json.loads((output / "vector_stability.jsonl").read_text())
            self.assertEqual(query["exact_ids"], [str(i) for i in ids])
            self.assertEqual(ordinary["sql"], "SELECT id FROM documents ORDER BY l2_distance(embedding, ?) LIMIT 100")
            self.assertEqual(ordinary["top_k"], 100)
            self.assertEqual(ordinary["oracle"], "ann_recall")
            self.assertEqual(stable["sql"], ordinary["sql"])
            self.assertEqual(stable["top_k"], 100)
            self.assertEqual(stable["expected_rows"], 100)
            self.assertEqual(repeated, {"id": query["id"], "params": query["params"]})
            for reference in manifest["scenarios"]:
                path = output / reference["path"]
                self.assertEqual(hashlib.sha256(path.read_bytes()).hexdigest(), reference["sha256"])
            for k in (0, 101, 1001):
                with self.assertRaisesRegex(ValueError, "top-k"):
                    prep.prepare_gist(source, output, 100, 1, [], False, 1, k)
            truth_path.write_bytes(struct.pack("<I10I", 10, *range(10)))
            with self.assertRaisesRegex(ValueError, "truth width"):
                prep.prepare_gist(source, output, 100, 1, [], False, 1, 100)


if __name__ == "__main__":
    unittest.main()

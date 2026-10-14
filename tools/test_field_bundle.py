"""Exercise the field package and installer without MO, public network access or real datasets."""
import functools
import hashlib
import http.server
import json
import os
import random
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import threading
import unittest

import package_field_bundle as bundle


class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


class FieldBundleTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.fixture = tempfile.TemporaryDirectory()
        cls.root = Path(cls.fixture.name)
        name = "mo-search-lab-v0.14.0-linux-amd64"
        tool = cls.root / "tool" / name
        (tool / "packs" / "smoke").mkdir(parents=True)
        binary = tool / "mo-search-lab"
        binary.write_text('#!/bin/sh\ncase "$1" in version) echo v0.14.0;; validate) exit 0;; *) exit 1;; esac\n')
        binary.chmod(0o755)
        (tool / "packs" / "smoke" / "manifest.json").write_text('{}\n')
        tool_archive = cls.root / "tool.tar.gz"
        with tarfile.open(tool_archive, "w:gz") as archive:
            archive.add(tool, arcname=name)
        packs = cls.root / "packs"
        rng = random.Random(71)
        original = cls.root / "original.csv"
        shared = cls.root / "shared.csv"
        text = cls.root / "texts.csv"
        vectors = [f'{i},"[{",".join(str(rng.random()) for _ in range(16))}]"\n'
                   for i in range(100)]
        original.write_text("".join(vectors))
        shared.write_text("".join(line.split(",", 1)[0] + "," + line for line in vectors))
        text.write_text("".join(f'{i},"{rng.randbytes(128).hex()}"\n' for i in range(100)))
        cls.source_files = {}
        for index, source in enumerate(bundle.PACKS.values()):
            target = packs / source
            target.mkdir(parents=True)
            files = ({"documents.csv": original} if index == 0 else
                     {"vectors.csv": shared} if index == 1 else
                     {"documents.csv": text} if index == 2 else
                     {"vectors.csv": shared, "texts.csv": text})
            loads = []
            for filename, csv in files.items():
                os.link(csv, target / filename)
                loads.append({"file": {"path": filename, "sha256": bundle.sha256(csv)},
                              "table": "documents", "rows": 100})
            (target / "manifest.json").write_text(json.dumps({"dataset": f"public{index}", "loads": loads,
                                                              "schema_version": 3}))
            (target / "source.json").write_text('{}\n')
            for path in target.iterdir():
                cls.source_files[path] = bundle.sha256(path)
        cls.output = cls.root / "release"
        cls.summary = bundle.package(tool_archive, packs, cls.output, "v0.14.0", part_bytes=4096)
        cls.name = name

    @classmethod
    def tearDownClass(cls):
        cls.fixture.cleanup()

    def install(self, parent, cache, offline=True, url=None):
        args = ["bash", str(self.output / "setup.sh"), str(parent), "--download-dir", str(cache)]
        if offline:
            args.append("--offline")
        if url:
            args += ["--base-url", url]
        return subprocess.run(args, text=True, capture_output=True, timeout=15)

    def test_complete_offline_install_preserves_shared_data_and_reruns(self):
        with tempfile.TemporaryDirectory() as directory:
            parent = Path(directory) / "install"
            result = self.install(parent, self.output)
            self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
            target = parent / self.name
            for alias in bundle.PACKS:
                self.assertTrue((target / "packs" / alias / "manifest.json").is_file())
            self.assertEqual((target / "packs/vector/documents.csv").stat().st_ino,
                             (target / "packs/dual-query/vectors.csv").stat().st_ino)
            self.assertEqual((target / "packs/filter/vectors.csv").stat().st_ino,
                             (target / "packs/dual-query/vectors.csv").stat().st_ino)
            self.assertEqual((target / "packs/fulltext/documents.csv").stat().st_ino,
                             (target / "packs/dual-query/texts.csv").stat().st_ino)
            reports = target / "reports"
            reports.mkdir()
            (reports / "keep.json").write_text("original")
            rerun = self.install(parent, self.output)
            self.assertEqual(rerun.returncode, 0, rerun.stderr)
            self.assertIn("已安装", rerun.stdout)
            self.assertEqual((reports / "keep.json").read_text(), "original")

    def test_corrupt_offline_part_never_installs(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            cache = base / "cache"
            shutil.copytree(self.output, cache)
            part = cache / self.summary["parts"][0]["name"]
            part.write_bytes(b"corrupted")
            result = self.install(base / "install", cache)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((base / "install" / self.name).exists())

    def test_download_resumes_or_restarts_and_complete_partial_is_reused(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            cache = base / "cache"
            cache.mkdir()
            first = self.summary["parts"][0]["name"]
            (cache / (first + ".partial")).write_bytes((self.output / first).read_bytes())
            second = self.summary["parts"][1]["name"]
            (cache / (second + ".partial")).write_bytes((self.output / second).read_bytes()[:100])
            handler = functools.partial(QuietHandler, directory=str(self.output))
            server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                result = self.install(base / "install", cache, offline=False,
                                      url=f"http://127.0.0.1:{server.server_port}")
                self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
                self.assertFalse((cache / (first + ".partial")).exists())
                self.assertEqual(bundle.sha256(cache / first), self.summary["parts"][0]["sha256"])
            finally:
                server.shutdown()
                server.server_close()
                thread.join()

    def test_existing_directory_and_invalid_manifest_are_not_overwritten(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            target = base / "install" / self.name
            target.mkdir(parents=True)
            (target / "report.json").write_text("keep")
            result = self.install(base / "install", self.output)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual((target / "report.json").read_text(), "keep")
            cache = base / "bad-manifest"
            cache.mkdir()
            (cache / "SHA256SUMS").write_text("0" * 64 + "  ../../outside\n")
            result = self.install(base / "new", cache)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((base / "new" / self.name).exists())

    def test_parts_are_a_single_archive_and_sources_remain_frozen(self):
        digest = hashlib.sha256()
        for part in self.summary["parts"]:
            payload = (self.output / part["name"]).read_bytes()
            self.assertLessEqual(len(payload), 4096)
            digest.update(payload)
        self.assertEqual(digest.hexdigest(), self.summary["archive_sha256"])
        with tarfile.open(self.summary["archive"]) as archive:
            source = archive.extractfile(self.name + "/packs/vector/manifest.json")
            manifest = json.loads(source.read())
            self.assertEqual(manifest["schema_version"], 4)
            self.assertEqual(manifest["loads"][0]["columns"], ["id", "", "embedding"])
            shared_files = [entry for entry in archive.getmembers()
                            if entry.name.endswith(".csv") and entry.isfile()]
            self.assertEqual(len(shared_files), 2)
        for path, digest in self.source_files.items():
            self.assertEqual(bundle.sha256(path), digest)

    def test_shared_projection_rejects_different_vectors(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            shutil.copytree(self.root / "packs" / bundle.PACKS["vector"], root / "packs/vector")
            shutil.copytree(self.root / "packs" / bundle.PACKS["filter"], root / "packs/filter")
            (root / "packs/vector/documents.csv").unlink()
            csv = root / "packs/filter/vectors.csv"
            lines = csv.read_text().splitlines(keepends=True)
            lines[0] = lines[0].replace("[", "[9,", 1)
            csv.write_text("".join(lines))
            with self.assertRaisesRegex(ValueError, "projection does not match"):
                bundle.compact_vector_pack(root)
            self.assertFalse((root / "packs/vector/documents.csv").exists())

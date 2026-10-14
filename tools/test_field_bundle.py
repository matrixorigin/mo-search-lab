"""Exercise the field package and installer without MO, public network access or real datasets."""
import functools
import hashlib
import http.server
import json
import os
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
        shared = cls.root / "shared.csv"
        shared.write_bytes(os.urandom(16000))
        for index, source in enumerate(bundle.PACKS.values()):
            target = packs / source
            target.mkdir(parents=True)
            (target / "manifest.json").write_text(json.dumps({"dataset": f"public{index}"}))
            (target / "source.json").write_text('{}\n')
            os.link(shared, target / "data.csv")
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
            self.assertEqual((target / "packs/vector/data.csv").stat().st_ino,
                             (target / "packs/dual-query/data.csv").stat().st_ino)
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

    def test_parts_are_a_single_archive_and_all_frozen_files_are_preserved(self):
        digest = hashlib.sha256()
        for part in self.summary["parts"]:
            payload = (self.output / part["name"]).read_bytes()
            self.assertLessEqual(len(payload), 4096)
            digest.update(payload)
        self.assertEqual(digest.hexdigest(), self.summary["archive_sha256"])
        with tarfile.open(self.summary["archive"]) as archive:
            source = archive.extractfile(self.name + "/packs/vector/manifest.json")
            self.assertEqual(source.read(), json.dumps({"dataset": "public0"}).encode())

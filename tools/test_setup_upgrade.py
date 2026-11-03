"""Exercise installation and upgrades against a local release server."""
import functools
import http.server
import json
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import threading
import unittest

import test_field_bundle as field
import package_field_bundle as bundle


class SetupUpgradeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        field.FieldBundleTests.setUpClass()
        cls.data = field.FieldBundleTests
        cls.temp = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temp.name)
        cls.release = cls.root / "release"
        cls.release.mkdir()
        cls.name = "mo-search-lab-v0.15.0-linux-amd64"
        payload = cls.root / cls.name
        (payload / "docs").mkdir(parents=True)
        (payload / "packs/smoke").mkdir(parents=True)
        binary = payload / "mo-search-lab"
        binary.write_text('#!/bin/sh\necho v0.15.0\n')
        binary.chmod(0o755)
        for name in ["README.md", "LICENSE", "NOTICE", "docs/handbook.md"]:
            (payload / name).write_text("new " + name)
        (payload / "TOOL.json").write_text(json.dumps({"version": "v0.15.0"}))
        files = sorted(p for p in payload.rglob("*") if p.is_file())
        (payload / "SHA256SUMS").write_text("".join(
            f"{bundle.sha256(p)}  ./{p.relative_to(payload)}\n" for p in files))
        cls.archive = cls.release / (cls.name + ".tar.gz")
        with tarfile.open(cls.archive, "w:gz") as archive:
            archive.add(payload, arcname=cls.name)
        script = (Path(__file__).resolve().parents[1] / "scripts/setup.sh").read_text()
        script = script.replace("data_version='v0.14.1'", "data_version='v0.14.0'")
        script = script.replace("b897ae948e592b76eae89676c9c0c9eb2c00eb22650f66ef54bc66ddf027ba37",
                                bundle.sha256(cls.data.output / "setup.sh"))
        (cls.release / "setup.sh").write_text(script)
        (cls.release / "SHA256SUMS").write_text(
            f"{bundle.sha256(cls.archive)}  {cls.archive.name}\n" +
            f"{bundle.sha256(cls.release / 'setup.sh')}  setup.sh\n")

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()
        field.FieldBundleTests.tearDownClass()

    def setup(self, target, cache=None, offline=True, extra=(), env=None):
        args = ["bash", str(self.release / "setup.sh"), str(target), "--download-dir", str(cache or self.release)]
        if offline:
            args.append("--offline")
        return subprocess.run(args + list(extra), text=True, capture_output=True, timeout=20, env=env)

    def old_install(self, parent):
        result = self.data.install(self.data, parent, self.data.output)
        self.assertEqual(result.returncode, 0, result.stderr)
        target = parent / self.data.name
        checksum = target / "SHA256SUMS"
        checksum.write_text("".join(line.replace("  ", "  ./", 1) + "\n"
                                    for line in checksum.read_text().splitlines()))
        (target / "docs").mkdir(exist_ok=True)
        (target / "docs/old.md").write_text("old docs")
        (target / "reports").mkdir()
        (target / "reports/keep.json").write_text("keep report")
        return target

    def test_offline_upgrade_preserves_data_reports_identity_and_checksums(self):
        with tempfile.TemporaryDirectory() as directory:
            target = self.old_install(Path(directory))
            csv = target / "packs/vector/documents.csv"
            original = (csv.stat().st_ino, bundle.sha256(csv), (target / "BUNDLE.json").read_bytes())
            for _ in range(2):
                result = self.setup(target)
                self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
                self.assertEqual(subprocess.check_output([str(target / "mo-search-lab"), "version"], text=True).strip(), "v0.15.0")
                self.assertEqual(original, (csv.stat().st_ino, bundle.sha256(csv), (target / "BUNDLE.json").read_bytes()))
                self.assertEqual((target / "reports/keep.json").read_text(), "keep report")
                self.assertEqual(subprocess.run(["sha256sum", "--check", "--status", "SHA256SUMS"], cwd=target).returncode, 0)
                self.assertEqual(json.loads((target / "TOOL.json").read_text())["version"], "v0.15.0")

    def test_first_install_downloads_fixed_data_and_reuses_downloads(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            # Serve tool and data from one isolated tree without copying CSVs.
            served = root / "served"
            served.mkdir()
            (served / "tool").symlink_to(self.release, target_is_directory=True)
            (served / "data").symlink_to(self.data.output, target_is_directory=True)
            handler = functools.partial(field.QuietHandler, directory=str(served))
            server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            url = f"http://127.0.0.1:{server.server_port}"
            try:
                target = root / "new-install"
                result = self.setup(target, root / "cache", offline=False,
                                    extra=("--base-url", url + "/tool", "--data-base-url", url + "/data"))
                self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
                self.assertEqual(json.loads((target / "TOOL.json").read_text())["version"], "v0.15.0")
                self.assertEqual((target / "packs/vector/documents.csv").stat().st_ino,
                                 (target / "packs/dual-query/vectors.csv").stat().st_ino)
                self.assertEqual(subprocess.run(["sha256sum", "--check", "--status", "SHA256SUMS"], cwd=target).returncode, 0)
                # Existing installation must not request data on subsequent upgrades.
                result = self.setup(target, root / "cache", offline=False,
                                    extra=("--base-url", url + "/tool", "--data-base-url", url + "/missing-data"))
                self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
            finally:
                server.shutdown()
                server.server_close()
                thread.join()

    def test_invalid_archive_or_target_never_changes_existing_installation(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            target = self.old_install(root)
            before = (target / "mo-search-lab").read_bytes()
            cache = root / "corrupt"
            shutil.copytree(self.release, cache)
            (cache / self.archive.name).write_bytes(b"corrupt")
            result = self.setup(target, cache)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual((target / "mo-search-lab").read_bytes(), before)
            self.assertEqual((target / "reports/keep.json").read_text(), "keep report")
            unrelated = root / "unrelated"
            unrelated.mkdir()
            (unrelated / "keep").write_text("keep")
            self.assertNotEqual(self.setup(unrelated).returncode, 0)
            self.assertEqual((unrelated / "keep").read_text(), "keep")

    def test_failed_update_restores_original_program_and_docs(self):
        import os
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            target = self.old_install(root)
            before = {p.relative_to(target): p.read_bytes() for p in target.rglob("*") if p.is_file()}
            bin_dir = root / "bin"
            bin_dir.mkdir()
            wrapper = bin_dir / "mv"
            # Fail exactly once at binary publication, after docs were replaced.
            wrapper.write_text('#!/usr/bin/env bash\nset -e\n'
                'if [[ "${*: -1}" == "$FAIL_TARGET/mo-search-lab" && ! -e "$FAIL_MARKER" ]]; then\n'
                ' touch "$FAIL_MARKER"; exit 1\nfi\nexec /bin/mv "$@"\n')
            wrapper.chmod(0o755)
            env = dict(os.environ, PATH=str(bin_dir) + os.pathsep + os.environ["PATH"],
                       FAIL_TARGET=str(target), FAIL_MARKER=str(root / "failed"))
            result = self.setup(target, env=env)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(before, {p.relative_to(target): p.read_bytes() for p in target.rglob("*") if p.is_file()})
            self.assertFalse(list(root.glob(".mo-search-lab-update-*")))

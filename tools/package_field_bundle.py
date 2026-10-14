#!/usr/bin/env python3
"""Build the complete field distribution off site; no Python is needed on site."""
from __future__ import annotations
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile

PACKS = {
    "vector": "gist_1m_health_top100_v1",
    "filter": "gist_1m_filtered_v7",
    "fulltext": "t2ranking_anli_full_500q_ngram_v3",
    "dual-query": "gist_t2_sql_workload_v7",
}


def sha256(path: Path) -> str:
    result = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(8 << 20), b""):
            result.update(chunk)
    return result.hexdigest()


def link_or_copy(source: str, destination: str) -> str:
    if Path(source).suffix != ".csv":
        return shutil.copy2(source, destination)
    try:
        os.link(source, destination)
    except OSError:
        shutil.copy2(source, destination)
    return destination


def compact_vector_pack(root: Path) -> None:
    """Reuse the filtered CSV after proving its projected bytes match the source."""
    vector = root / "packs" / "vector"
    filtered = root / "packs" / "filter"
    manifest = json.loads((vector / "manifest.json").read_text())
    shared = json.loads((filtered / "manifest.json").read_text())["loads"][0]
    if len(manifest["loads"]) != 1 or manifest["loads"][0]["rows"] != shared["rows"]:
        raise ValueError("vector and filtered packs must contain the same rows")
    load = manifest["loads"][0]
    original_hash = load["file"]["sha256"]
    original_manifest_hash = sha256(vector / "manifest.json")
    csv = filtered / shared["file"]["path"]
    projected = hashlib.sha256()
    rows = 0
    with csv.open("rb") as stream:
        for line in stream:
            identifier, visibility, embedding = line.split(b",", 2)
            if identifier != visibility:
                raise ValueError("filtered GIST visibility must equal its vector ID")
            projected.update(identifier + b"," + embedding)
            rows += 1
    if rows != load["rows"] or projected.hexdigest() != original_hash:
        raise ValueError("shared CSV projection does not match frozen vector data")
    target = vector / load["file"]["path"]
    os.link(csv, target)
    load["file"]["sha256"] = shared["file"]["sha256"]
    load["columns"] = ["id", "", "embedding"]
    manifest["schema_version"] = 4
    (vector / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    source_path = vector / "source.json"
    source = json.loads(source_path.read_text())
    source["distribution"] = {
        "source_manifest_sha256": original_manifest_hash,
        "original_csv_sha256": original_hash,
        "shared_csv_sha256": shared["file"]["sha256"],
        "columns": load["columns"],
        "projection_verified_rows": rows,
    }
    source_path.write_text(json.dumps(source, ensure_ascii=False, indent=2) + "\n")


def share_csv_files(root: Path) -> int:
    """Deduplicate validated load files even when source packs were copied across disks."""
    canonical = {}
    for alias in PACKS:
        directory = root / "packs" / alias
        manifest = json.loads((directory / "manifest.json").read_text())
        for load in manifest["loads"]:
            path = directory / load["file"]["path"]
            digest = load["file"]["sha256"]
            if digest not in canonical:
                canonical[digest] = path
            elif not path.samefile(canonical[digest]):
                path.unlink()
                os.link(canonical[digest], path)
    return sum(path.stat().st_size for path in canonical.values())


def package(tool_archive: Path, packs_root: Path, output: Path, version: str,
            part_bytes: int = 1_900_000_000) -> dict:
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", version):
        raise ValueError("version must be vMAJOR.MINOR.PATCH")
    if part_bytes < 1 or part_bytes >= 2**31:
        raise ValueError("parts must be smaller than GitHub's 2 GiB asset limit")
    output.mkdir(parents=True, exist_ok=False)
    repo = Path(__file__).resolve().parents[1]
    name = f"mo-search-lab-{version}-linux-amd64"
    archive_name = f"{name}-full.tar.gz"
    with tempfile.TemporaryDirectory(prefix=".field-stage-", dir=output) as temporary:
        stage = Path(temporary)
        with tarfile.open(tool_archive) as archive:
            archive.extractall(stage, filter="data")
        root = stage / name
        if not (root / "mo-search-lab").is_file():
            raise ValueError("tool archive name/structure does not match requested version")
        measured = subprocess.check_output([str(root / "mo-search-lab"), "version"], text=True).strip()
        if measured != version:
            raise ValueError(f"tool version is {measured}, expected {version}")
        provenance = {"version": version, "source_commit": subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip(), "packs": []}
        for alias, source_name in PACKS.items():
            source = packs_root / source_name
            if not (source / "manifest.json").is_file():
                raise ValueError(f"missing required pack: {source}")
            print(f"Validate and include {alias}: {source}", flush=True)
            subprocess.run([str(root / "mo-search-lab"), "validate", "--pack", str(source)], check=True)
            # The vector pack will use the exact filtered CSV, with one skipped field.
            ignore = shutil.ignore_patterns("*.csv") if alias == "vector" else None
            shutil.copytree(source, root / "packs" / alias, copy_function=link_or_copy, ignore=ignore)
        print("Verify shared GIST data and prepare column mapping", flush=True)
        compact_vector_pack(root)
        csv_unique_bytes = share_csv_files(root)
        for alias, source_name in PACKS.items():
            source = packs_root / source_name
            directory = root / "packs" / alias
            subprocess.run([str(root / "mo-search-lab"), "validate", "--pack", str(directory)], check=True)
            manifest = json.loads((directory / "manifest.json").read_text())
            provenance["packs"].append({"directory": f"packs/{alias}", "dataset": manifest["dataset"],
                "manifest_sha256": sha256(directory / "manifest.json"),
                "source_manifest_sha256": sha256(source / "manifest.json")})
        (root / "BUNDLE.json").write_text(json.dumps(provenance, ensure_ascii=False, indent=2) + "\n")
        (root / "DATASETS.md").write_text('''# Public dataset sources

GIST1M: INRIA/IRISA TexMex, http://corpus-texmex.irisa.fr/ .
Vector, filter and paired SQL tests share one CSV. The vector load skips the
visibility field. Its projected ID/vector bytes are verified against the frozen
source CSV SHA-256. Original vector/query/neighbor hashes and the verified mapping
are retained in packs/vector/source.json and packs/filter/source.json.
Filtering visibility is synthetic.

T2Ranking: https://github.com/THUIR/T2Ranking and
https://huggingface.co/datasets/THUIR/T2Ranking . The publisher identifies Apache-2.0;
the license text is in LICENSES/T2Ranking-Apache-2.0.txt. Collection/query identities,
judgments, frozen preprocessing and source hashes are retained in packs/fulltext/source.json.
Quality metrics describe the selected cohort of 500 frozen development queries.

The paired SQL pack shares the exact filter/fulltext CSV files and runs the two
SQL routes concurrently. Input identities and preparation details are retained
in each pack's source.json.
''')
        (root / "LICENSES").mkdir(exist_ok=True)
        shutil.copy2(repo / "LICENSE", root / "LICENSES" / "T2Ranking-Apache-2.0.txt")
        # The archive/parts protect CSV bytes; packs verify their frozen CSV hashes
        # again before SQL. The installer verifies small files without another CSV scan.
        small = sorted(path for path in root.rglob("*") if path.is_file()
                       and path.name != "SHA256SUMS" and path.suffix != ".csv")
        (root / "SHA256SUMS").write_text("".join(f"{sha256(p)}  {p.relative_to(root)}\n" for p in small))
        archive_path = output / archive_name
        print("Compress complete bundle (shared CSV hard links retained)", flush=True)
        with archive_path.open("wb") as destination:
            tar = subprocess.Popen(["tar", "--sort=name", "--owner=0", "--group=0", "--numeric-owner",
                                    "-C", str(stage), "-cf", "-", name], stdout=subprocess.PIPE)
            try:
                compressed = subprocess.run(["gzip", "-6", "-n"], stdin=tar.stdout, stdout=destination)
            finally:
                if tar.stdout:
                    tar.stdout.close()
            tar_code = tar.wait()
            if tar_code or compressed.returncode:
                raise RuntimeError(f"compression failed: tar={tar_code}, gzip={compressed.returncode}")
        archive_hash = sha256(archive_path)
        (output / "archive.sha256").write_text(f"{archive_hash}  {archive_name}\n")
        parts = []
        with archive_path.open("rb") as source:
            number = 1
            while True:
                chunk = source.read(min(part_bytes, 8 << 20))
                if not chunk:
                    break
                part = output / f"{archive_name}.part{number:02d}"
                remaining = part_bytes
                with part.open("wb") as stream:
                    stream.write(chunk)
                    remaining -= len(chunk)
                    while remaining > 0:
                        chunk = source.read(min(remaining, 8 << 20))
                        if not chunk:
                            break
                        stream.write(chunk)
                        remaining -= len(chunk)
                parts.append(part)
                number += 1
        (output / "SHA256SUMS").write_text("".join(f"{sha256(p)}  {p.name}\n"
            for p in [output / "archive.sha256", *parts]))
        setup, replaced = re.subn(r"^version='v[0-9]+\.[0-9]+\.[0-9]+'$", f"version='{version}'",
                                 (repo / "scripts" / "setup.sh").read_text(), count=1, flags=re.MULTILINE)
        if replaced != 1:
            raise ValueError("installer version marker is missing")
        (output / "setup.sh").write_text(setup)
        (output / "setup.sh").chmod(0o755)
        result = {"archive": str(archive_path), "archive_sha256": archive_hash,
                  "compressed_bytes": archive_path.stat().st_size,
                  "csv_unique_bytes": csv_unique_bytes, "compression": "gzip -6",
                  "parts": [{"name": p.name, "bytes": p.stat().st_size, "sha256": sha256(p)} for p in parts],
                  "provenance": provenance}
        (output / "verification.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
        print(json.dumps({"compressed_bytes": result["compressed_bytes"], "parts": len(parts)}, ensure_ascii=False), flush=True)
        return result


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tool-archive", type=Path, required=True)
    parser.add_argument("--packs-root", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--version", default="v0.14.1")
    args = parser.parse_args()
    package(args.tool_archive.resolve(), args.packs_root.resolve(), args.output.resolve(), args.version)


if __name__ == "__main__":
    main()

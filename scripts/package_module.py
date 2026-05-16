#!/usr/bin/env python3
from __future__ import annotations

import argparse
import os
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo


FIXED_DATE = (2024, 1, 1, 0, 0, 0)


def mode_for(rel: str, path: Path) -> int:
    if path.is_dir():
        return 0o755
    name = path.name
    if rel.startswith("bin/") or name in {"update-binary"} or name.endswith(".sh"):
        return 0o755
    return 0o644


def should_skip(path: Path) -> bool:
    name = path.name
    if name in {".DS_Store", "Thumbs.db"}:
        return True
    if name.endswith((".bak", ".log", ".tmp", ".map")):
        return True
    return any(part == "__pycache__" for part in path.parts)


def add_entry(zipf: ZipFile, root: Path, path: Path) -> None:
    rel = path.relative_to(root).as_posix()
    if path.is_dir():
        rel += "/"
    info = ZipInfo(rel, FIXED_DATE)
    info.create_system = 3
    info.external_attr = (mode_for(rel, path) & 0xFFFF) << 16
    if path.is_dir():
        zipf.writestr(info, b"")
    else:
        info.compress_type = ZIP_DEFLATED
        zipf.writestr(info, path.read_bytes())


def package(module_dir: Path, output: Path) -> None:
    module_dir = module_dir.resolve()
    output = output.resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    if output.exists():
        output.unlink()

    paths = sorted(p for p in module_dir.rglob("*") if not should_skip(p))
    with ZipFile(output, "w", compression=ZIP_DEFLATED, compresslevel=9) as zipf:
        dirs = sorted({p for p in paths if p.is_dir()})
        files = sorted(p for p in paths if p.is_file())
        for path in dirs + files:
            add_entry(zipf, module_dir, path)


def main() -> None:
    parser = argparse.ArgumentParser(description="Package SSHCustom-VPNChain flashable module zip")
    parser.add_argument("module_dir", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    package(args.module_dir, args.output)
    print(args.output)


if __name__ == "__main__":
    main()

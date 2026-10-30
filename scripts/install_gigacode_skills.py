#!/usr/bin/env python3
"""Install SEAI-only instructions in a selected project without replacing user files."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys

SOURCE = Path(__file__).resolve().parents[1]


def install(project, binary, data, layout):
    project = Path(project).resolve(strict=True)
    root = subprocess.check_output(
        ["git", "-C", str(project), "rev-parse", "--show-toplevel"], text=True
    ).strip()
    if Path(root).resolve() != project:
        raise ValueError("--project must be the Git repository root")
    binary = Path(binary).resolve(strict=True)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise ValueError("--seai must point to an executable SEAI binary")
    data = Path(data).resolve()
    if data == project or project in data.parents:
        raise ValueError("runtime must be outside the project")
    prefix = ".gigacode" if layout == "cli" else ".gigacode_vsc"
    planned = {}
    for src in sorted((SOURCE / ".gigacode/skills").glob("seai-*/SKILL.md")):
        planned[project / prefix / "skills" / src.parent.name / src.name] = src.read_bytes()
    planned[project / ".gigacode/seai/POLICY.md"] = (SOURCE / ".gigacode/seai/POLICY.md").read_bytes()
    session = {"seai_bin": str(binary), "data_dir": str(data), "project_root": str(project)}
    planned[project / ".gigacode/seai/session.json"] = (json.dumps(session, indent=2) + "\n").encode()
    block = (SOURCE / ".gigacode/seai/GIGACODE.block.md").read_text()
    memory = project / "GIGACODE.md"
    if memory.is_symlink():
        raise ValueError("symlink GIGACODE.md is unsupported")
    old = memory.read_text() if memory.exists() else ""
    begin, end = "<!-- SEAI-ONLY:BEGIN -->", "<!-- SEAI-ONLY:END -->"
    if begin in old or end in old:
        if old.count(begin) != 1 or old.count(end) != 1:
            raise ValueError("malformed SEAI policy markers in GIGACODE.md")
        a, b = old.index(begin), old.index(end) + len(end)
        if a > b or old[a:b] != block.strip():
            raise ValueError("existing SEAI policy differs; merge GIGACODE.md manually")
    else:
        planned[memory] = (old + ("\n\n" if old else "") + block).encode()
    ignore = project / ".gitignore"
    if ignore.is_symlink():
        raise ValueError("symlink .gitignore is unsupported")
    previous_ignore = ignore.read_text() if ignore.exists() else ""
    entry = "/.gigacode/seai/session.json"
    if entry not in previous_ignore.splitlines():
        planned[ignore] = (previous_ignore.rstrip("\n") + "\n" + entry + "\n").encode()
    # Check every destination before writing anything; user changes are never replaced.
    for dest, body in planned.items():
        if dest.is_symlink() or any(p.is_symlink() for p in dest.parents if p != project and project in p.parents):
            raise ValueError("symlink destination is unsupported: " + str(dest))
        if dest.exists() and dest not in (memory, ignore) and dest.read_bytes() != body:
            raise ValueError("destination differs; merge manually: " + str(dest))
    written = []
    for dest, body in planned.items():
        if dest.exists() and dest.read_bytes() == body:
            continue
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_bytes(body)
        if dest.name == "session.json":
            dest.chmod(0o600)
        written.append(str(dest.relative_to(project)))
    return {"project": str(project), "layout": layout, "written": written,
            "note": "Skills are instructions, not tool access enforcement; restart GigaCode and check /skills and /memory."}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--project", required=True)
    p.add_argument("--seai", required=True)
    p.add_argument("--data", required=True)
    p.add_argument("--layout", choices=["cli", "vscode"], default="cli")
    a = p.parse_args()
    try:
        print(json.dumps(install(a.project, a.seai, a.data, a.layout), indent=2))
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print("SEAI skills installation failed: " + str(error), file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())

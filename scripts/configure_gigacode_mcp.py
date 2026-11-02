#!/usr/bin/env python3
"""Merge project-scoped SEAI MCP entries; never relax global shell permissions."""
import argparse
import json
import os
from pathlib import Path
import tempfile


def configure(workspace, project, binary, data, trust_readonly=False,
              candidate_writes=False, trust_candidate_writes=False):
    workspace = Path(workspace).resolve(strict=True)
    project = Path(project).resolve(strict=True)
    binary = Path(binary).resolve(strict=True)
    data = Path(data).resolve(strict=True)
    if workspace != project and workspace not in project.parents:
        raise ValueError("workspace must contain project")
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise ValueError("--seai must be an executable")
    if data == project or project in data.parents:
        raise ValueError("runtime must be outside project")
    if trust_candidate_writes and not candidate_writes:
        raise ValueError("--trust-candidate-writes requires --candidate-writes")
    folder = workspace / ".gigacode"
    path = folder / "settings.json"
    if folder.is_symlink() or path.is_symlink():
        raise ValueError("symlink settings destination is unsupported")
    original = path.read_bytes() if path.exists() else None
    settings = json.loads(original) if original else {}
    if not isinstance(settings, dict) or not isinstance(settings.get("mcpServers", {}), dict):
        raise ValueError("settings and mcpServers must be objects")
    servers = settings.setdefault("mcpServers", {})
    base = {"command": str(binary), "args": ["--data", str(data), "mcp", "--repo", str(project)],
            "cwd": str(project), "timeout": 600000}
    planned = {"seai": {**base, "trust": trust_readonly, "includeTools": ["seai_connect", "seai_read", "seai_contract"]}}
    if candidate_writes:
        planned["seai-candidate"] = {**base, "args": base["args"] + ["--candidate-writes"],
                                     "trust": trust_candidate_writes, "includeTools": ["seai_candidate"]}
    for name, value in planned.items():
        if name in servers and servers[name] != value:
            raise ValueError(f"existing MCP entry {name!r} differs; merge settings manually (no files changed)")
    servers.update(planned)
    body = (json.dumps(settings, ensure_ascii=False, indent=2) + "\n").encode()
    if original != body:
        folder.mkdir(parents=True, exist_ok=True)
        fd, temp = tempfile.mkstemp(prefix=".seai-settings-", dir=folder)
        try:
            with os.fdopen(fd, "wb") as stream:
                stream.write(body)
            os.chmod(temp, path.stat().st_mode & 0o777 if path.exists() else 0o600)
            os.replace(temp, path)
        finally:
            if os.path.exists(temp):
                os.unlink(temp)
    return {"settings": str(path), "servers": list(planned), "trust_readonly": trust_readonly,
            "trust_candidate_writes": trust_candidate_writes,
            "next": "Restart GigaCode in workspace; /mcp must show connected; call seai_connect. This does not prove client integration or enforce SEAI-only source access."}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    for name in ("workspace", "project", "seai", "data"):
        p.add_argument("--" + name, required=True)
    p.add_argument("--trust-readonly", action="store_true", help="skip confirmations only for the bounded read-only SEAI server")
    p.add_argument("--candidate-writes", action="store_true", help="add separate candidate server; confirmations remain by default")
    p.add_argument("--trust-candidate-writes", action="store_true", help="explicitly trust candidate workflow calls too; human behavior approval is still required")
    a = p.parse_args()
    try:
        print(json.dumps(configure(a.workspace, a.project, a.seai, a.data, a.trust_readonly,
                                   a.candidate_writes, a.trust_candidate_writes), ensure_ascii=False, indent=2))
    except (ValueError, OSError) as e:
        p.exit(2, str(e) + "\n")

if __name__ == "__main__":
    main()

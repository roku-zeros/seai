#!/usr/bin/env python3
"""Install SEAI-only instructions in a selected project without replacing user files."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys

SOURCE = Path(__file__).resolve().parents[1]


def install(project, binary, data, layout, workspace=None):
    project = Path(project).resolve(strict=True)
    root = subprocess.check_output(
        ["git", "-C", str(project), "rev-parse", "--show-toplevel"], text=True
    ).strip()
    if Path(root).resolve() != project:
        raise ValueError("--project must be the Git repository root")
    workspace = Path(workspace).resolve(strict=True) if workspace is not None else project
    if not workspace.is_dir() or (workspace != project and workspace not in project.parents):
        raise ValueError("--workspace must be the opened directory containing --project")
    binary = Path(binary).resolve(strict=True)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise ValueError("--seai must point to an executable SEAI binary")
    data = Path(data).resolve()
    if data == project or project in data.parents:
        raise ValueError("runtime must be outside the project")
    prefix = ".gigacode" if layout == "cli" else ".gigacode_vsc"
    planned = {}
    skill_sources = sorted((SOURCE / ".gigacode/skills").glob("seai-*/SKILL.md"))
    if not skill_sources:
        raise ValueError("SEAI source skill bundle is missing")
    for src in skill_sources:
        planned[workspace / prefix / "skills" / src.parent.name / src.name] = src.read_bytes()
    planned[workspace / ".gigacode/seai/POLICY.md"] = (SOURCE / ".gigacode/seai/POLICY.md").read_bytes()
    session = {"seai_bin": str(binary), "data_dir": str(data), "project_root": str(project)}
    planned[workspace / ".gigacode/seai/session.json"] = (json.dumps(session, indent=2) + "\n").encode()
    block = (SOURCE / ".gigacode/seai/GIGACODE.block.md").read_text()
    memory = workspace / "GIGACODE.md"
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
    ignore = workspace / ".gitignore"
    if ignore.is_symlink():
        raise ValueError("symlink .gitignore is unsupported")
    previous_ignore = ignore.read_text() if ignore.exists() else ""
    entry = "/.gigacode/seai/session.json"
    if entry not in previous_ignore.splitlines():
        planned[ignore] = (previous_ignore.rstrip("\n") + "\n" + entry + "\n").encode()
    # Check every destination before writing anything; user changes are never replaced.
    for dest, body in planned.items():
        if dest.is_symlink() or any(p.is_symlink() for p in dest.parents if p != workspace and workspace in p.parents):
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
        written.append(str(dest.relative_to(workspace)))
    return {"project": str(project), "workspace": str(workspace), "layout": layout, "written": written,
            "note": "Start GigaCode from workspace, restart the session, then check /memory and /skills. Installed files do not prove that the client loaded them."}


def check(workspace, project=None, probe=False, layout="cli"):
    workspace = Path(workspace).resolve(strict=True)
    if not workspace.is_dir():
        raise ValueError("--workspace must be a directory")
    issues = []
    memory = workspace / "GIGACODE.md"
    policy = workspace / ".gigacode/seai/POLICY.md"
    config = workspace / ".gigacode/seai/session.json"
    for path in [memory, policy, config]:
        if path.is_symlink():
            issues.append({"code": "SYMLINK_STARTUP_FILE", "path": str(path)})
        elif not path.is_file():
            issues.append({"code": "MISSING_STARTUP_FILE", "path": str(path)})
    if memory.is_file() and not memory.is_symlink():
        content = memory.read_text()
        if content.count("<!-- SEAI-ONLY:BEGIN -->") != 1 or content.count("<!-- SEAI-ONLY:END -->") != 1:
            issues.append({"code": "MISSING_OR_AMBIGUOUS_STARTUP_RULE", "path": str(memory)})
    session = None
    if config.is_file() and not config.is_symlink():
        try:
            session = json.loads(config.read_text())
            if not isinstance(session, dict) or any(not isinstance(session.get(key), str) or not Path(session[key]).is_absolute() for key in ["seai_bin", "data_dir", "project_root"]):
                raise ValueError("session paths must be absolute strings")
            configured = Path(session["project_root"]).resolve(strict=True)
            if project is not None and configured != Path(project).resolve(strict=True):
                issues.append({"code": "PROJECT_SCOPE_MISMATCH", "configured_project": str(configured)})
            if configured != workspace and workspace not in configured.parents:
                issues.append({"code": "PROJECT_OUTSIDE_WORKSPACE"})
        except (ValueError, OSError, TypeError) as error:
            issues.append({"code": "INVALID_SESSION_CONFIG", "message": str(error)})
            session = None
    layouts = {}
    expected = [p.parent.name for p in (SOURCE / ".gigacode/skills").glob("seai-*/SKILL.md")]
    if not expected:
        raise ValueError("SEAI source skill bundle is missing")
    for name, prefix in [("cli", ".gigacode"), ("vscode", ".gigacode_vsc")]:
        layouts[name] = [skill for skill in sorted(expected) if (workspace / prefix / "skills" / skill / "SKILL.md").is_file()]
    if len(layouts[layout]) != len(expected):
        issues.append({"code": "INCOMPLETE_SKILL_SET", "expected_count": len(expected), "layout": layout})
    nested = []
    candidates = [SOURCE]
    if project is not None:
        candidates.append(Path(project).resolve(strict=True))
    for candidate in candidates:
        if candidate != workspace and workspace in candidate.parents and (candidate / "GIGACODE.md").is_file():
            nested.append(str(candidate / "GIGACODE.md"))
    if issues and nested:
        issues.append({"code": "NESTED_RULE_IS_NOT_WORKSPACE_RULE", "nested_rules": sorted(set(nested)), "action": "Install with --workspace equal to the directory from which GigaCode is started."})
    probes = {}
    if probe and session is not None:
        for label, command in [("doctor", ["doctor"]), ("project", ["project", "status"])]:
            try:
                result = subprocess.run([session["seai_bin"], "--data", session["data_dir"], "--json", *command], capture_output=True, text=True, timeout=15)
                response = json.loads(result.stdout)
                if not isinstance(response, dict) or response.get("schema_version") != "seai.api/v1" or not isinstance(response.get("data", {}), dict):
                    raise ValueError("unexpected SEAI envelope")
                if result.returncode or response.get("error") or response.get("status") == "error":
                    issues.append({"code": "SEAI_PROBE_FAILED", "command": label, "exit_code": result.returncode})
                else:
                    probes[label] = response.get("data", {})
            except (OSError, subprocess.TimeoutExpired, ValueError) as error:
                issues.append({"code": "SEAI_PROBE_FAILED", "command": label, "message": str(error)})
        if "project" in probes:
            actual = probes["project"].get("root")
            if not isinstance(actual, str) or Path(actual).resolve() != Path(session["project_root"]).resolve():
                issues.append({"code": "DAEMON_PROJECT_MISMATCH", "actual_root": actual})
    return {"status": "issues_found" if issues else "files_configured", "workspace": str(workspace), "layout": layout, "project": session["project_root"] if session else None, "skills": layouts, "issues": issues, "probe": probes,
            "client_loaded_verified": False, "next_steps": ["Start a new GigaCode CLI session in this exact workspace.", "Check /memory for its GIGACODE.md and /skills for SEAI skills.", "Ask an ordinary code question without mentioning SEAI and inspect the actual tool calls."]}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--project")
    p.add_argument("--workspace", help="directory opened in GigaCode; defaults to project")
    p.add_argument("--check", action="store_true", help="diagnose startup files without modifying them")
    p.add_argument("--probe", action="store_true", help="with --check, call configured SEAI doctor/project status")
    p.add_argument("--seai")
    p.add_argument("--data")
    p.add_argument("--layout", choices=["cli", "vscode"], default="cli")
    a = p.parse_args()
    try:
        if a.check:
            if not a.workspace and not a.project:
                p.error("--check requires --workspace or --project")
            result = check(a.workspace or a.project, a.project, a.probe, a.layout)
            print(json.dumps(result, indent=2))
            return 0 if result["status"] == "files_configured" else 3
        if not a.project or not a.seai or not a.data:
            p.error("installation requires --project, --seai and --data")
        if a.probe:
            p.error("--probe requires --check")
        print(json.dumps(install(a.project, a.seai, a.data, a.layout, a.workspace), indent=2))
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print("SEAI skills installation failed: " + str(error), file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""CLI -> Unix JSON-RPC -> daemon -> Git/CAS -> index; no external services."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time

parser = argparse.ArgumentParser()
parser.add_argument("--bin", required=True, type=Path)
args = parser.parse_args()
binaries = args.bin.resolve()
source = Path(__file__).resolve().parents[1]
# Optional development-only validation. The normal smoke requires only Python stdlib.
validators = {}
if os.environ.get("SEAI_VALIDATE_SCHEMAS") == "1":
    import jsonschema
    schemas = {p.name: json.loads(p.read_text()) for p in (source / "schemas").glob("*.json")}
    from referencing import Registry, Resource
    registry = Registry().with_resources((schema["$id"], Resource.from_contents(schema)) for schema in schemas.values())
    for name, schema in schemas.items():
        jsonschema.Draft202012Validator.check_schema(schema)
        validators[name] = jsonschema.Draft202012Validator(schema, registry=registry)


def run(argv, **kw):
    return subprocess.run([str(v) for v in argv], check=True, capture_output=True, text=True, **kw).stdout

with tempfile.TemporaryDirectory(prefix="seai-smoke-") as tmp:
    root = Path(tmp)
    repo = root / "repo"
    data = root / "data"
    shutil.copytree(source / "fixtures/strategy", repo)
    for cmd in [["init", "-q"], ["config", "user.name", "SEAI fixture"], ["config", "user.email", "fixture@example.invalid"], ["add", "."], ["commit", "-qm", "fixture"]]:
        run(["git", "-C", repo, *cmd])
    initial = run(["git", "-C", repo, "rev-parse", "HEAD"])
    # Darwin resolves /var -> /private/var; keep socket path short with /tmp alias.
    socket_dir = Path(tempfile.mkdtemp(prefix="seai-", dir="/tmp"))
    os.chmod(socket_dir, 0o700)
    proc = None
    log = open(root / "daemon.log", "w+")
    try:
        def start():
            p = subprocess.Popen([str(binaries / "seaid"), "--repo", str(repo), "--data", str(socket_dir), "--project", "smoke"], stdout=log, stderr=log)
            for _ in range(100):
                if p.poll() is not None:
                    log.seek(0)
                    raise RuntimeError(log.read())
                if (socket_dir / "seaid.sock").exists():
                    return p
                time.sleep(0.05)
            p.terminate()
            p.wait(timeout=20)
            raise RuntimeError("daemon startup timeout")

        def cli(*cmd):
            response = json.loads(run([binaries / "seai", "--data", socket_dir, "--json", *cmd]))
            name = None
            if cmd[0] == "search": name = "text-search-v1.schema.json" if "--text" in cmd else "search-result-v1.schema.json"
            elif cmd[0] == "files": name = "file-list-v1.schema.json"
            elif cmd[0] == "read": name = "file-read-v1.schema.json"
            elif cmd[0] == "symbol": name = "symbol-v1.schema.json"
            elif cmd[0] in ["references", "calls", "tests"]: name = "references-v1.schema.json"
            elif cmd[0] == "task": name = "task-diff-v1.schema.json" if cmd[1] in ["diff", "export"] else "task-v1.schema.json"
            elif cmd[0] == "patch": name = "task-v1.schema.json"
            elif cmd[0] in ["index", "job"]: name = "job-v1.schema.json"
            if name in validators: validators[name].validate(response["data"])
            return response

        proc = start()
        assert cli("doctor")["data"]["pilot_ready"] is False
        job = cli("index", "build", "--ref", "HEAD", "--operation-id", "smoke-build", "--epoch", "0", "--wait")
        assert job["data"]["state"] == "SUCCEEDED", job
        result = cli("search", "Eligible")
        assert result["data"]["hits"][0]["name"] == "Eligible", result
        generation = result["data"]["index_generation_id"]
        snapshot = result["data"]["source_snapshot_id"]
        symbol = result["data"]["hits"][0]
        assert cli("files")["data"]["files"]
        assert cli("symbol", symbol["symbol_id"])["data"]["text"]
        assert cli("references", symbol["symbol_id"])["data"]["references"]
        assert cli("tests", "related", symbol["symbol_id"])["data"]["references"]
        assert cli("search", "below minimum age", "--text")["data"]["hits"]
        source_file = cli("read", "rule.go", "--limit", "65536")["data"]
        original_text = source_file["text"]
        spec = {
            "schema_version": "seai.change/v1", "change_id": "SMOKE-REFACTOR", "revision": 1,
            "kind": "refactor", "objective": "Rename threshold constant preserving all behavior",
            "source_requirements": [], "scope": {"files": ["rule.go"]}, "expected_behavior": [],
            "preserved_behavior": [{"id": "INV1", "statement": "Eligibility and reason outputs remain equivalent"}],
            "acceptance_criteria": [{"id": "AC1", "behavior_ids": ["INV1"], "expected": "17 false; 18 true; 19 true; same reasons", "assertion_profile": "equivalence-v1"}],
            "unresolved": []}
        spec_path = root / "spec.json"
        spec_path.write_text(json.dumps(spec))
        task = cli("task", "create", "--spec", str(spec_path))["data"]
        plan = {"schema_version": "seai.plan/v1", "steps": [{"id": "S1", "description": "Rename declaration and use", "files": ["rule.go"], "criterion_ids": ["AC1"], "evidence_ids": [source_file["file_evidence_id"]]}], "required_checks": ["compile", "targeted", "regression", "version-comparison"]}
        plan_path = root / "plan.json"
        plan_path.write_text(json.dumps(plan))
        task = cli("task", "plan", task["task_id"], "--plan", str(plan_path))["data"]
        patch = {"schema_version": "seai.patch/v1", "base_snapshot_id": task["candidate_snapshot_id"], "spec_digest": task["spec_digest"], "files": [{"path": "rule.go", "before_digest": source_file["blob_digest"], "before_mode": source_file["mode"], "content": original_text.replace("MinimumAge", "EligibilityMinimumAge"), "after_digest": hashlib.sha256(original_text.replace("MinimumAge", "EligibilityMinimumAge").encode()).hexdigest(), "mode": source_file["mode"]}]}
        patch_path = root / "patch.json"
        patch_path.write_text(json.dumps(patch))
        task = cli("patch", "apply", task["task_id"], "--patch", str(patch_path))["data"]
        assert task["candidate_snapshot_id"] != snapshot
        assert "EligibilityMinimumAge" in cli("read", "rule.go", "--snapshot", task["candidate_snapshot_id"], "--limit", "65536")["data"]["text"]
        assert cli("read", "rule.go", "--limit", "65536")["data"]["text"] == original_text
        assert cli("task", "diff", task["task_id"])["data"]["patch"]
        output = root / "change.patch"
        cli("task", "export", task["task_id"], "--allow-incomplete", "--output", str(output))
        manifest = json.loads(Path(str(output) + ".manifest.json").read_text())
        assert manifest["release_readiness"] == "NOT_REVIEW_READY"
        run(["git", "-C", repo, "apply", "--check", output])
        cancelled = cli("task", "cancel", task["task_id"])["data"]
        assert cancelled["state"] == "CANCELLED"
        assert cli("task", "resume", task["task_id"])["data"]["candidate_snapshot_id"] == task["candidate_snapshot_id"]
        (repo / "rule.go").write_text("invalid changed live file\n")
        stale = subprocess.run([str(binaries / "seai"), "--data", str(socket_dir), "search", "Eligible"], capture_output=True, text=True)
        assert stale.returncode == 3 and json.loads(stale.stdout)["error"]["code"] == "STALE_WORKSPACE"
        assert cli("search", "Eligible", "--generation", generation)["data"]["index_generation_id"] == generation
        proc.terminate()
        proc.wait(timeout=20)
        proc = start()
        assert cli("search", "Eligible", "--generation", generation)["data"]["hits"][0]["name"] == "Eligible"
        assert cli("task", "status", task["task_id"])["data"]["candidate_snapshot_id"] == task["candidate_snapshot_id"]
        repeat = cli("index", "build", "--ref", "HEAD", "--operation-id", "smoke-build", "--epoch", "0", "--wait")
        assert repeat["data"]["result"]["index_generation_id"] == generation
        assert run(["git", "-C", repo, "rev-parse", "HEAD"]) == initial
        assert (repo / "rule.go").read_text() == "invalid changed live file\n"
        print(json.dumps({"status": "passed", "go_version": run(["go", "version"]).strip(), "checks": ["daemon startup", "CLI/RPC", "immutable evidence", "restart", "idempotent retry", "user source preserved", "snapshot reads and graph", "contract and plan", "isolated candidate", "git-applicable diagnostic export", "task cancel/resume/restart", "stale guard"], "generation": generation}, indent=2))
    finally:
        if proc is not None and proc.poll() is None:
            proc.terminate()
            proc.wait(timeout=20)
        log.close()
        shutil.rmtree(socket_dir)

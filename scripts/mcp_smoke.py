#!/usr/bin/env python3
"""Real stdio MCP -> Unix RPC -> disposable daemon/index/workflow, no external services."""
import argparse
import json
import os
from pathlib import Path
import select
import shutil
import subprocess
import tempfile
import time
import threading
from concurrent.futures import ThreadPoolExecutor
from smoke_support import wait_for_run

p = argparse.ArgumentParser(description=__doc__)
p.add_argument("--bin", required=True, type=Path)
a = p.parse_args()
binaries = a.bin.resolve()
source = Path(__file__).resolve().parents[1]

with tempfile.TemporaryDirectory(prefix="seai-mcp-") as temp:
    root = Path(temp)
    repo = root / "repo"
    shutil.copytree(source / "fixtures/strategy", repo)
    for args in [["init", "-q"], ["config", "user.name", "SEAI fixture"], ["config", "user.email", "fixture@example.invalid"], ["add", "."], ["commit", "-qm", "fixture"]]:
        subprocess.run(["git", "-C", repo, *args], check=True, capture_output=True)
    runtime = Path(tempfile.mkdtemp(prefix="seai-mcp-", dir="/tmp"))
    os.chmod(runtime, 0o700)
    children = []
    daemon = None
    with open(root / "daemon.log", "w+") as log:
        try:
            def bridge(writes=False, research=False):
                proc = subprocess.Popen([str(binaries / "seai"), "--data", str(runtime), "mcp", "--repo", str(repo), *(["--candidate-writes"] if writes else []), *(["--research"] if research else [])], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=log, text=True, bufsize=1)
                children.append(proc)
                return proc

            seq = 0
            sequence_lock = threading.Lock()
            def rpc(proc, method, params=None, notification=False):
                global seq
                with sequence_lock:
                    seq += 1
                    request_id = seq
                message = {"jsonrpc": "2.0", "method": method, "params": params or {}}
                if not notification:
                    message["id"] = request_id
                proc.stdin.write(json.dumps(message) + "\n")
                proc.stdin.flush()
                if notification:
                    return
                if not select.select([proc.stdout], [], [], 20)[0]:
                    raise RuntimeError("MCP response timeout: " + method)
                response = json.loads(proc.stdout.readline())
                assert response["id"] == request_id and "error" not in response, response
                return response["result"]

            def initialize(proc):
                result = rpc(proc, "initialize", {"protocolVersion": "2025-03-26", "capabilities": {}, "clientInfo": {"name": "fixture", "version": "1"}})
                assert result["serverInfo"]["name"] == "seai"
                rpc(proc, "notifications/initialized", notification=True)
                tools = rpc(proc, "tools/list")["tools"]
                if os.environ.get("SEAI_VALIDATE_SCHEMAS") == "1":
                    import jsonschema
                    for tool in tools:
                        jsonschema.Draft202012Validator.check_schema(tool["inputSchema"])
                return tools

            read = bridge()
            started = time.monotonic()
            tools = initialize(read)  # daemon deliberately absent
            assert {t["name"] for t in tools} == {"seai_connect", "seai_read", "seai_contract"}
            discovery_seconds = time.monotonic() - started
            schema = rpc(read, "tools/call", {"name": "seai_contract", "arguments": {"name": "change"}})
            assert not schema["isError"]
            daemon = subprocess.Popen([str(binaries / "seaid"), "--repo", str(repo), "--data", str(runtime), "--project", "mcp-fixture", "--freshness-budget", "5m"], stdout=log, stderr=log)
            for _ in range(200):
                if (runtime / "seaid.sock").exists():
                    break
                if daemon.poll() is not None:
                    raise RuntimeError("daemon exited")
                time.sleep(0.05)
            else:
                raise RuntimeError("daemon startup timeout")
            def cli(*args):
                result = subprocess.run([str(binaries / "seai"), "--data", str(runtime), *args], check=True, capture_output=True, text=True, timeout=120)
                return json.loads(result.stdout)["data"]
            assert cli("index", "build", "--wait")["state"] == "SUCCEEDED"
            def tool(proc, name, arguments, fail=False):
                result = rpc(proc, "tools/call", {"name": name, "arguments": arguments})
                assert result["isError"] == fail, result
                return json.loads(result["content"][0]["text"])
            connected = tool(read, "seai_connect", {})
            assert connected["capabilities"]["data"]["freshness_budget_seconds"] == 300
            generation = connected["pinned_generation"]
            before = tool(read, "seai_read", {"method": "file.read", "params": {"path": "rule.go"}})["data"]
            assert "text" in before and "content_base64" not in before
            original = (repo / "rule.go").read_bytes()
            (repo / "rule.go").write_text("package changed\n")
            historical = tool(read, "seai_read", {"method": "file.read", "params": {"path": "rule.go"}})["data"]
            assert historical["text"] == before["text"] and historical["index_generation_id"] == generation
            (repo / "rule.go").write_bytes(original)
            tool(read, "seai_read", {"method": "human.approve", "params": {}}, fail=True)
            writer = bridge(True)
            assert "seai_candidate" in {t["name"] for t in initialize(writer)}
            spec = {"schema_version": "seai.change/v1", "change_id": "mcp-refactor", "revision": 1, "kind": "refactor", "objective": "Rename without behavior change", "source_requirements": [], "scope": {"files": ["rule.go"]}, "expected_behavior": [], "preserved_behavior": [{"id": "INV1", "statement": "Preserve eligibility"}], "acceptance_criteria": [{"id": "AC1", "behavior_ids": ["INV1"], "expected": "Equivalent eligibility", "assertion_profile": "fixture-v1"}], "unresolved": []}
            epoch = connected["project"]["data"]["epoch"]
            params = {"operation_id": "mcp-create", "expected_project_epoch": epoch, "expected_revision": 0, "spec": spec}
            arguments = {"method": "task.create", "params": params}
            accepted = tool(writer, "seai_candidate", arguments)["data"]
            run = wait_for_run(lambda rid: tool(read, "seai_read", {"method": "runs.get", "params": {"id": rid}})["data"], accepted["run_id"], 60)
            assert run["state"] == "SUCCEEDED", run
            assert run["elapsed_ms"] >= 0 and run["finished_at"]
            task = run["result"]
            assert tool(writer, "seai_candidate", arguments)["data"] == accepted
            stored = tool(read, "seai_read", {"method": "task.get", "params": {"task_id": task["task_id"]}})["data"]
            assert stored["freshness"] == "UNKNOWN"
            assert cli("task", "status", task["task_id"], "--stored")["freshness"] == "UNKNOWN"
            diff = tool(read, "seai_read", {"method": "task.diff", "params": {"task_id": task["task_id"]}})["data"]
            assert isinstance(diff["checks"], dict)
            coordinator = bridge(research=True)
            assert "seai_research_start" in {t["name"] for t in initialize(coordinator)}
            start_args = {"objective": "Explain eligibility, dependencies and tests", "topics": ["rules", "impact", "tests"], "index_generation_id": generation, "expected_project_epoch": epoch, "operation_id": "research-create"}
            accepted = tool(coordinator, "seai_research_start", start_args)["data"]
            research_id = accepted["run_id"]
            for _ in range(200):
                board = tool(read, "seai_read", {"method": "research.get", "params": {"id": research_id}})["data"]
                if not board["initializing"]:
                    break
                time.sleep(0.05)
            else:
                raise RuntimeError("research board startup timeout")
            assert board["result"]["max_workers"] == 2
            idle = tool(read, "seai_read", {"method": "research.get", "params": {"id": research_id, "cursor": board["cursor"], "wait_ms": 1}})["data"]
            assert idle["unchanged"] is True and "jobs" not in idle

            workers = [bridge(research=True), bridge(research=True)]
            for worker in workers:
                initialize(worker)
            barrier = threading.Barrier(2)
            def investigate(index):
                worker = workers[index]
                claim = tool(worker, "seai_research_claim", {"run_id": research_id, "job_id": board["jobs"][index]["job_id"], "operation_id": "claim-" + str(index)})["data"]
                assert claim["input"]["index_generation_id"] == generation
                file = tool(worker, "seai_read", {"method": "file.read", "params": {"path": "rule.go", "index_generation_id": claim["input"]["index_generation_id"]}})["data"]
                barrier.wait(timeout=15)  # both workers must overlap before either reports
                report = {"summary": "Pinned source inspected", "findings": [{"statement": "rule.go exists in the shared snapshot", "evidence_ids": [file["file_evidence_id"]]}], "gaps": []}
                return tool(worker, "seai_research_report", {"run_id": research_id, "job_id": claim["job_id"], "lease_epoch": claim["lease_epoch"], "operation_id": "report-" + str(index), "report": report})
            with ThreadPoolExecutor(max_workers=2) as pool:
                reports = list(pool.map(investigate, [0, 1]))
            assert len(reports) == 2
            assert cli("research", "status", research_id)["state"] == "RUNNING"
            third = cli("research", "claim", research_id, "--operation-id", "claim-last")
            tool(coordinator, "seai_research_report", {"run_id": research_id, "job_id": third["job_id"], "lease_epoch": third["lease_epoch"], "operation_id": "report-last", "report": {"summary": "Shared file inspected", "findings": [{"statement": "Source belongs to pinned snapshot", "evidence_ids": [before["file_evidence_id"]]}], "gaps": []}})
            research_result = wait_for_run(lambda rid: tool(read, "seai_read", {"method": "runs.get", "params": {"id": rid}})["data"], research_id, 60)
            assert research_result["result"]["review_required"] is True and len(research_result["result"]["reports"]) == 3
            assert tool(coordinator, "seai_research_start", start_args)["data"] == accepted
            cli_session = cli("research", "start", "--generation", generation, "--objective", "CLI cancellation", "--topic", "rules", "--topic", "tests", "--operation-id", "cli-research", "--epoch", str(epoch))
            assert cli("research", "cancel", cli_session["run_id"])["state"] == "CANCELLED"

            assert (repo / "rule.go").read_bytes() == original
            print(json.dumps({"status": "passed", "offline_discovery_seconds": round(discovery_seconds, 3), "checks": ["offline MCP discovery", "bundled contracts", "runtime budget override", "pinned immutable reads", "no duplicate base64", "human approval boundary", "async create/replay", "run timing", "stored task/diff", "unchanged product working tree", "two concurrent research clients", "three collected evidence reports without provider", "research replay", "CLI research start/status/claim/cancel"]}))
        finally:
            for child in children:
                if child.poll() is None:
                    child.stdin.close()
                    child.wait(timeout=20)
            if daemon is not None and daemon.poll() is None:
                daemon.terminate()
                daemon.wait(timeout=20)
            shutil.rmtree(runtime)

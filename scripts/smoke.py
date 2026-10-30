#!/usr/bin/env python3
"""CLI -> Unix JSON-RPC -> daemon -> Git/CAS -> index; no external services."""
import argparse
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
            return json.loads(run([binaries / "seai", "--data", socket_dir, "--json", *cmd]))

        proc = start()
        assert cli("doctor")["data"]["pilot_ready"] is False
        job = cli("index", "build", "--ref", "HEAD", "--operation-id", "smoke-build", "--epoch", "0", "--wait")
        assert job["data"]["state"] == "SUCCEEDED", job
        result = cli("search", "Eligible")
        assert result["data"]["hits"][0]["name"] == "Eligible", result
        generation = result["data"]["index_generation_id"]
        (repo / "rule.go").write_text("invalid changed live file\n")
        assert cli("search", "Eligible")["data"]["index_generation_id"] == generation
        proc.terminate()
        proc.wait(timeout=20)
        proc = start()
        assert cli("search", "Eligible")["data"]["hits"][0]["name"] == "Eligible"
        repeat = cli("index", "build", "--ref", "HEAD", "--operation-id", "smoke-build", "--epoch", "0", "--wait")
        assert repeat["data"]["result"]["index_generation_id"] == generation
        assert run(["git", "-C", repo, "rev-parse", "HEAD"]) == initial
        assert (repo / "rule.go").read_text() == "invalid changed live file\n"
        print(json.dumps({"status": "passed", "go_version": run(["go", "version"]).strip(), "checks": ["daemon startup", "CLI/RPC", "immutable evidence", "restart", "idempotent retry", "user source preserved"], "generation": generation}, indent=2))
    finally:
        if proc is not None and proc.poll() is None:
            proc.terminate()
            proc.wait(timeout=20)
        log.close()
        shutil.rmtree(socket_dir)

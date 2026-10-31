#!/usr/bin/env python3
"""Generate operator-approved LOCAL profiles for the synthetic strategy fixture only."""
import argparse
import json
from pathlib import Path
import shutil

p = argparse.ArgumentParser(description=__doc__)
p.add_argument("--output", required=True, type=Path)
p.add_argument("--go", default=shutil.which("go"))
p.add_argument("--approve-local-execution", action="store_true", required=True,
               help="Acknowledge execution of fixture code without an OS sandbox")
a = p.parse_args()
if not a.go or not Path(a.go).is_file():
    p.error("Go executable required")
common = dict(program=str(Path(a.go).resolve()), isolation="local-approved",
              acknowledge_local_execution=True, timeout_seconds=120,
              max_output_bytes=262144, memory_mb=0, cpus=0)
profiles = [dict(common, id="compile", kind="compile", args=["build", "./..."]),
            dict(common, id="tests", kind="tests", args=["test", "-json", "-count=1", "./..."],
                 test_names=["TestEligibleBoundary"])]
fixtures = []
for age in [17, 18, 19]:
    eligible = age >= 18
    fixtures.append(dict(id=f"age-{age}", input=json.dumps(dict(age=age)),
                         expected=dict(eligible=eligible, reason="eligible" if eligible else "below minimum age"),
                         criterion_ids=["AC1"], allowed_paths=[]))
profiles.append(dict(common, id="comparison", kind="comparison", args=["run", "./cmd/probe"],
                     assertion_profile="eligibility-v1", fixtures=fixtures))
config = dict(schema_version="seai.runtime/v1", checks=profiles,
              max_workers=2, max_node_attempts=1, max_run_seconds=1800)
with a.output.open("x", encoding="utf-8") as f:
    json.dump(config, f, ensure_ascii=False, indent=2)
    f.write("\n")
print(a.output.resolve())

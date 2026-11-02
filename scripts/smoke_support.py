"""Monotonic, configurable async wait shared by SEAI CLI smoke scenarios."""
import json
import math
import time


def wait_for_run(fetch, run_id, timeout_seconds, *, clock=time.monotonic, sleep=time.sleep):
    if not math.isfinite(timeout_seconds) or timeout_seconds <= 0:
        raise ValueError("run timeout must be a finite positive number")
    deadline = clock() + timeout_seconds
    last = None
    while clock() < deadline:
        last = fetch(run_id)
        state = last.get("state")
        if state == "SUCCEEDED":
            return last
        if state not in ("QUEUED", "RUNNING"):
            raise RuntimeError("run {} ended without success: {}".format(run_id, json.dumps(last)))
        remaining = deadline - clock()
        if remaining > 0:
            sleep(min(.2, remaining))
    raise TimeoutError("smoke wait budget {}s exceeded for run {}; last state: {}. "
                       "The wait timeout is not a test verdict; inspect run status/logs.".format(
                           timeout_seconds, run_id, last))

#!/usr/bin/env python3
"""Launch frozen test artifacts on 103 with explicit per-lane cwd and JSON status."""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import re
import shlex
import subprocess


REMOTE = r'''
import base64, datetime, hashlib, json, os, pathlib, re, signal, socket, subprocess, sys, time, uuid

config = json.loads(base64.b64decode(sys.argv[1]))
bundle = pathlib.Path(config["bundle_root"]).resolve(strict=True)
base = pathlib.Path("/data2/sat20wallet-sdk-tests").resolve(strict=True)
bundle.relative_to(base)
raw = (bundle / "manifest.json").read_bytes()
assert hashlib.sha256(raw).hexdigest() == config["manifest_sha256"], "bundle manifest changed"
manifest = json.loads(raw)
for name, expected in manifest["files"].items():
    relative = pathlib.PurePosixPath(name)
    assert not relative.is_absolute() and ".." not in relative.parts
    assert hashlib.sha256((bundle / name).read_bytes()).hexdigest() == expected, name
processes = subprocess.check_output(["ps", "-eo", "pid=,comm="], text=True)
assert not any(line.split()[-1] in {"e2e.test", "wallet.test", "satoshinet.test", "test2json"}
               for line in processes.splitlines() if line.split()), "another SDK test is active"
for port in list(range(31450, 31455)) + list(range(31460, 31465)):
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", port))
stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
runs = bundle / "runs"
runs.mkdir(mode=0o700, exist_ok=True)
assert runs.is_dir() and not runs.is_symlink(), "runs parent is not a private directory"
run = runs / (config["name"] + "-" + stamp + "-" + uuid.uuid4().hex[:8])
run.mkdir(mode=0o700)
state = {"run_root": str(run), "mode": config["mode"], "status": "PREPARING",
         "manifest_sha256": config["manifest_sha256"], "controller_sha256": config["controller_sha256"],
         "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "lanes": {},
         "fail_fast": True,
         "list_only": config["list_only"],
         "coverage_status": "NOT_APPLICABLE_LIST" if config["list_only"] else "NOT_APPLICABLE_PROBE" if config["mode"] == "probe" else "PENDING_VALIDATION"}

def save():
    path = run / "run-status.json"
    temp = run / "run-status.json.tmp"
    with temp.open("w") as stream:
        json.dump(state, stream, indent=2)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temp, path)
    assert json.loads(path.read_text())["run_root"] == str(run)

def protected_root_metadata():
    p = pathlib.Path("/root/review-evidence/core-modules-e2e-latest.json")
    if not p.exists():
        return None
    s = p.stat()
    return {"path": str(p), "inode": s.st_ino, "size": s.st_size, "mtime_ns": s.st_mtime_ns}

state["protected_root_report_before"] = protected_root_metadata()
(run / "services-before.txt").write_text(processes)
stopped = [None]
def stopping(signum, frame):
    stopped[0] = signum
for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
    signal.signal(sig, stopping)
jobs = {}
streams = []
readers = {}

def clean_group(pid):
    try:
        os.killpg(pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    time.sleep(0.2)
    try:
        os.killpg(pid, signal.SIGKILL)
    except ProcessLookupError:
        pass

def first_failure():
    for name, reader in readers.items():
        while True:
            position = reader.tell()
            line = reader.readline()
            if not line:
                break
            if not line.endswith("\n"):
                reader.seek(position)
                break
            try:
                event = json.loads(line)
            except ValueError:
                continue
            if event.get("Action") == "fail":
                return {"lane": name, "test": event.get("Test"), "event_time": event.get("Time")}
    for name, process in jobs.items():
        code = process.poll()
        if code is not None and code != 0:
            return {"lane": name, "returncode": code}
    return None

started = time.monotonic()
save()
print(json.dumps({"run_root": str(run), "status": "PREPARING"}), flush=True)
try:
    for lane in config["lanes"]:
        name = lane["name"]
        directory = run / name
        directory.mkdir(mode=0o700)
        for part in ("work", "tmp", "home"):
            (directory / part).mkdir(mode=0o700)
        work = (directory / "work").resolve(strict=True)
        work.relative_to(run)
        assert work != pathlib.Path("/root") and not list(work.iterdir())
        env = {"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": str(directory / "home"),
               "TMPDIR": str(directory / "tmp"), "GOMAXPROCS": "4", "GOTELEMETRY": "off",
               "TZ": "UTC", "LC_ALL": "C", "SAT20_E2E_REQUIRE_BITCOIND": "1",
               "SAT20_REAL_SATOSHINET_PORT_BASE": "31450", "SAT20WALLET_E2E_ARTIFACTS": str(bundle)}
        if name == "e2e":
            env["SAT20WALLET_CORE_E2E_CHILD_RUN"] = config["core_selector"]
        # A real child witnesses cwd using the same cwd/env API as the test.
        witness = subprocess.check_output([sys.executable, "-c", "import os;print(os.getcwd())"],
                                          cwd=str(work), env=env, text=True).strip()
        assert pathlib.Path(witness).resolve() == work, "cwd witness differs"
        (directory / "cwd-witness.json").write_text(json.dumps({"expected": str(work), "actual": witness}) + "\n")
        stdout = (directory / "results.jsonl").open("wb")
        stderr = (directory / "runner.stderr").open("wb")
        streams.extend([stdout, stderr])
        readers[name] = (directory / "results.jsonl").open()
        command = ["/usr/bin/time", "-v", "-o", str(directory / "time.txt"),
                   "/usr/bin/nice", "-n", "10", "/usr/bin/ionice", "-c2", "-n7",
                   str(bundle / "test2json"), "-t", "-p", lane["package"],
                   str(bundle / lane["binary"]), "-test.v=test2json", "-test.run=" + lane["selector"],
                   "-test.skip=" + config["skip_selector"], "-test.count=1", "-test.parallel=1",
                   "-test.failfast", "-test.timeout=" + str(config["go_timeout_minutes"]) + "m"]
        if config["list_only"]:
            command.append("-test.list=" + lane["selector"])
        # cwd is a Popen argument, never an optional shell cd with fallback.
        process = subprocess.Popen(command, cwd=str(work), env=env, stdin=subprocess.DEVNULL,
                                   stdout=stdout, stderr=stderr, start_new_session=True)
        assert os.getpgid(process.pid) == process.pid, "process group is not private"
        state["lanes"][name] = {"pid": process.pid, "pgid": process.pid, "cwd": str(work),
                                 "cwd_witness": witness, "command": command, "returncode": None,
                                 "core_report": str(work / "review-evidence/core-modules-e2e-latest.json") if name == "e2e" else None}
        jobs[name] = process
        save()
    state["status"] = "RUNNING"
    save()
    print(json.dumps({"run_root": str(run), "lanes": state["lanes"]}), flush=True)
    while any(p.poll() is None for p in jobs.values()):
        if stopped[0] is not None or time.monotonic() - started > config["wall_timeout_minutes"] * 60:
            state["status"] = "ABORTING"
            state["stop_signal"] = stopped[0]
            for p in jobs.values():
                clean_group(p.pid)
            break
        if "first_failure" not in state:
            failure = first_failure()
            if failure is not None:
                state["first_failure"] = failure
                state["status"] = "FAIL_FAST_STOPPING"
                state["stopped_sibling_lanes"] = []
                save()
                for name, p in jobs.items():
                    if name != failure["lane"] and p.poll() is None:
                        state["stopped_sibling_lanes"].append(name)
                        clean_group(p.pid)
        for name, p in jobs.items():
            if p.poll() is not None:
                state["lanes"][name]["returncode"] = p.returncode
        state["elapsed_seconds"] = time.monotonic() - started
        save()
        time.sleep(0.2 if config["mode"] == "probe" else 2)
    for name, p in jobs.items():
        state["lanes"][name]["returncode"] = p.wait()
        clean_group(p.pid)
    if "first_failure" not in state:
        failure = first_failure()
        if failure is not None:
            state["first_failure"] = failure
    state["status"] = ("ABORTED" if stopped[0] is not None or state["status"] == "ABORTING"
                       else "FAILED" if "first_failure" in state else "EXECUTION_FINISHED")
except BaseException as error:
    state["status"] = "LAUNCH_ERROR"
    state["error"] = str(error)
    for p in jobs.values():
        clean_group(p.pid)
        p.wait()
finally:
    for stream in streams:
        stream.close()
    for reader in readers.values():
        reader.close()
    state["ended_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
    state["elapsed_seconds"] = time.monotonic() - started
    state["protected_root_report_after"] = protected_root_metadata()
    state["protected_root_report_unchanged"] = state["protected_root_report_before"] == state["protected_root_report_after"]
    (run / "services-after.txt").write_text(subprocess.check_output(["ps", "-eo", "pid=,comm="], text=True))
    success = state["status"] == "EXECUTION_FINISHED" and all(p.returncode == 0 for p in jobs.values())
    if (config["mode"] == "probe" or config["list_only"]) and success:
        try:
            for name in jobs:
                events = [json.loads(line) for line in (run / name / "results.jsonl").read_text().splitlines() if line]
                if any(e.get("Action") == "run" and e.get("Test") for e in events):
                    raise ValueError("probe ran a business test")
                if config["list_only"]:
                    listed = sorted({e.get("Output", "").strip() for e in events
                                     if re.fullmatch(r"Test\w+", e.get("Output", "").strip())})
                    assert listed == config["expected_roots"][name] and listed, "listed roots mismatch: " + name
                    state.setdefault("listed_roots", {})[name] = listed
            success = success and state["protected_root_report_unchanged"]
        except Exception as error:
            success = False
            state["error"] = str(error)
        state["status"] = (("LIST_VERIFIED" if success else "LIST_FAILED") if config["list_only"]
                           else "PROBE_PASSED" if success else "PROBE_FAILED")
    state["execution_exit_code"] = 0 if success else 1
    save()
    print(json.dumps({"run_root": str(run), "status": state["status"], "execution_exit_code": state["execution_exit_code"]}), flush=True)
sys.exit(state["execution_exit_code"])
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan", type=Path, required=True)
    parser.add_argument("--mode", choices=("probe", "full", "target", "direct"), required=True)
    parser.add_argument("--name", default="guarded-run")
    parser.add_argument("--selector", help="Target E2E selector; full and probe use the frozen plan.")
    parser.add_argument("--core-selector", help="Explicit native Core selector for a target run.")
    parser.add_argument("--list-only", action="store_true", help="List exact selected roots without running business tests.")
    args = parser.parse_args()
    if not args.name.replace("-", "").isalnum():
        parser.error("name must contain only letters, numbers and hyphens")
    plan = json.loads(args.plan.read_text())
    names = (("direct",) if args.mode == "direct" else
             ("e2e",) if args.mode == "target" else ("wallet", "e2e"))
    if args.mode in ("target", "direct") and not args.selector:
        parser.error("target requires --selector")
    if args.list_only and args.mode == "probe":
        parser.error("list-only requires full, target or direct mode")
    config = {"bundle_root": plan["bundle_root"], "manifest_sha256": plan["manifest_sha256"],
              "mode": args.mode, "name": args.name, "skip_selector": plan["skip_selector"],
              "core_selector": args.core_selector or plan["core_child_selector"],
              "go_timeout_minutes": 1 if args.mode == "probe" else (30 if args.mode in ("target", "direct") else 150),
              "wall_timeout_minutes": 3 if args.mode == "probe" else (35 if args.mode in ("target", "direct") else 160),
              "controller_sha256": hashlib.sha256(REMOTE.encode()).hexdigest(),
              "list_only": args.list_only,
              "lanes": [{"name": n,
                         "binary": "satoshinet.test" if n == "direct" else n + ".test",
                         "package": "github.com/sat20-labs/satoshinet" if n == "direct" else "github.com/sat20-labs/sat20wallet/sdk/" + n,
                         "selector": "^$" if args.mode == "probe" else
                         (args.selector if args.mode in ("target", "direct") else plan["run_selectors"][n])} for n in names]}
    config["expected_roots"] = {lane["name"]: sorted(root for root in
        (plan.get("direct_roots", []) if lane["name"] == "direct" else plan["selected_roots"][lane["name"]])
        if re.search(lane["selector"], root)) for lane in config["lanes"]}
    encoded = base64.b64encode(json.dumps(config).encode()).decode()
    command = ["ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=8",
               "103", "python3 - " + shlex.quote(encoded)]
    result = subprocess.run(command, input=REMOTE, text=True)
    raise SystemExit(result.returncode)


if __name__ == "__main__":
    main()

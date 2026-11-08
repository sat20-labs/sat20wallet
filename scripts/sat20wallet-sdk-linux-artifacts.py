#!/usr/bin/env python3
"""Build Linux E2E artifacts from the current Mac worktrees, without source copies."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import time
from datetime import datetime, timezone


REPO = Path(__file__).resolve().parents[1]
WORKSPACE = REPO.parent
GO = Path("/usr/local/go/bin/go")
ZIG = WORKSPACE / ".test-tools/zig-official-0.17.1/zig-x86_64-macos-0.17.1/zig"
GO_VERSION = "go1.24.13"


def digest(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest() if hasattr(hashlib, "file_digest") else hashlib.sha256(source.read()).hexdigest()


def source_snapshot():
    records = {}
    revisions = {}
    for name, prefix in (("sat20wallet", "sdk"), ("satoshinet", ""),
                         ("indexer", ""), ("transcend", ""), ("xaplib", "chainindex")):
        root = WORKSPACE / name
        revisions[name] = subprocess.check_output(
            ["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
        command = ["git", "-C", str(root), "ls-files", "-z", "--cached", "--others", "--exclude-standard"]
        if prefix:
            command += ["--", prefix]
        for relative in subprocess.check_output(command).decode().split("\0"):
            if not relative:
                continue
            path = root / relative
            if not path.is_file():
                continue
            if path.suffix not in {".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".s", ".S", ".syso"} and path.name not in {"go.mod", "go.sum"}:
                continue
            records[name + "/" + relative] = digest(path)
    # Include the current local go:embed inputs as well as Go/C source files.
    for relative in ("indexer/rpcserver/ord/static", "indexer/rpcserver/ord/templates",
                     "transcend/rpc/web/manage.html"):
        root = WORKSPACE / relative
        paths = root.rglob("*") if root.is_dir() else [root]
        for path in paths:
            if path.is_file():
                records[str(path.relative_to(WORKSPACE))] = digest(path)
    contracts = REPO / "sdk/e2e/testdata/contracts"
    for path in sorted(contracts.rglob("*")):
        if path.is_file() and path.suffix in {".sol", ".json"}:
            records["sat20wallet/" + str(path.relative_to(REPO))] = digest(path)
    return {"revisions": revisions, "files": records}


def main():
    started = time.perf_counter()
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    output = (args.output or REPO / ".cache/sdk-linux-artifacts" / stamp).resolve()
    output.relative_to((REPO / ".cache").resolve())
    if output.exists():
        raise SystemExit("Output already exists; use a new build directory.")
    os.umask(0o077)
    output.mkdir(parents=True)
    version = subprocess.check_output([str(GO), "version"], text=True).split()[2]
    if version != GO_VERSION:
        raise SystemExit("Expected " + GO_VERSION + ", got " + version)
    if subprocess.check_output([str(ZIG), "version"], text=True).strip() != "0.17.1":
        raise SystemExit("Expected the verified official Zig 0.17.1.")
    env = dict(os.environ)
    for key in ("GOFLAGS", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS"):
        env.pop(key, None)
    env.update(GOOS="linux", GOARCH="amd64", GOAMD64="v1", GOTOOLCHAIN="local",
               GOPROXY="off", GOSUMDB="off", GOMAXPROCS="2", CGO_ENABLED="1",
               CC=str(ZIG) + " cc -target x86_64-linux-gnu.2.35 -mcpu=baseline",
               CXX=str(ZIG) + " c++ -target x86_64-linux-gnu.2.35 -mcpu=baseline")
    before = source_snapshot()
    (output / "source-inputs-before.json").write_text(json.dumps(before, indent=2) + "\n")
    with (output / "build.log").open("w") as log:
        def build(name, directory, arguments, cgo="1"):
            print("Building " + name, flush=True)
            command = [str(GO), "build", "-mod=readonly", "-p=2", "-o", str(output / name)] + arguments
            if name.endswith(".test"):
                command = [str(GO), "test", "-mod=readonly", "-p=2", "-c", "-o", str(output / name)] + arguments
            log.write("\n" + " ".join(command) + "\n")
            log.flush()
            subprocess.run(command, cwd=directory, env=dict(env, CGO_ENABLED=cgo), stdout=log, stderr=subprocess.STDOUT, check=True)

        sdk = REPO / "sdk"
        # Preserve the original native runner's 18-package inventory and full
        # production build gate; internal module tests are not selected here.
        module = "github.com/sat20-labs/sat20wallet/sdk"
        suffixes = ("", "/account", "/account/fuzzy", "/account/internal/shamir",
                    "/common", "/config", "/e2e", "/e2e/cmd/solidity-compile",
                    "/plugin", "/testutil/fakeindexer", "/wallet", "/wallet/dkvs",
                    "/wallet/lightnode", "/wallet/rgb11", "/wallet/supernode",
                    "/wallet/utils", "/wasm", "/wire")
        production_started = time.perf_counter()
        print("Verifying 18-package inventory and production build", flush=True)
        packages = subprocess.check_output([str(GO), "list", "-mod=readonly", "./..."],
                                           cwd=sdk, env=env, stderr=log, text=True).splitlines()
        if sorted(packages) != sorted(module + suffix for suffix in suffixes):
            raise SystemExit("Package inventory differs from the original 18-package gate: " + repr(packages))
        (output / "production-packages.txt").write_text("\n".join(packages) + "\n")
        production_command = [str(GO), "build", "-mod=readonly", "-p=2", "./..."]
        log.write("\n" + " ".join(production_command) + "\n")
        log.flush()
        subprocess.run(production_command, cwd=sdk, env=env, stdout=log,
                       stderr=subprocess.STDOUT, check=True)
        production_seconds = time.perf_counter() - production_started
        build("wallet.so", sdk / "plugin", ["-buildmode=plugin", "main.go"])
        build("satoshinet-miner-rpctest", WORKSPACE / "satoshinet", ["-tags=rpctest,wallet_plugin", "github.com/sat20-labs/satoshinet"])
        build("stpd-normal.so", WORKSPACE / "transcend/plugin", ["-buildmode=plugin", "main.go"])
        # Both variants must resolve the same current local SDK/node/indexer inputs.
        for name in ("satoshinet", "indexer", "sat20wallet/sdk"):
            actual = subprocess.check_output([str(GO), "list", "-mod=readonly", "-m", "-f", "{{.Dir}}", "github.com/sat20-labs/" + name], cwd=WORKSPACE / "transcend", env=env, text=True).strip()
            if Path(actual).resolve() != (WORKSPACE / name).resolve():
                raise SystemExit("Unexpected plugin replacement: " + name + " => " + actual)
        build("stpd-dkvs.so", WORKSPACE / "transcend/plugin", ["-buildmode=plugin", "main.go"])
        build("satoshinet-core-rpctest", WORKSPACE / "satoshinet", ["-tags=rpctest,stp_plugin", "github.com/sat20-labs/satoshinet"])
        build("e2e.test", sdk, ["./e2e"])
        build("wallet.test", sdk, ["./wallet"])
        build("test2json", sdk, ["cmd/test2json"], "0")

    source_hashes = {}
    contract_root = sdk / "e2e/testdata/contracts"
    for source in sorted(contract_root.glob("*.sol")):
        source_hashes["testdata/contracts/" + source.name] = digest(source)
    artifacts = output / "solidity"
    artifacts.mkdir()
    for artifact in sorted((contract_root / "artifacts").glob("*.json")):
        data = json.loads(artifact.read_text())
        source_name = "testdata/contracts/" + data["sourceName"]
        if data["sourceSHA256"] != source_hashes[source_name]:
            raise SystemExit("Stale Solidity artifact: " + artifact.name)
        if data["evmVersion"] != "paris" or not data["optimizerEnabled"] or data["optimizerRuns"] != 200:
            raise SystemExit("Unexpected Solidity compiler profile: " + artifact.name)
        shutil.copyfile(artifact, artifacts / artifact.name)
    after = source_snapshot()
    (output / "source-inputs-after.json").write_text(json.dumps(after, indent=2) + "\n")
    if before != after:
        raise SystemExit("Source inputs changed during the build; do not run these artifacts.")
    names = ["wallet.so", "satoshinet-miner-rpctest", "stpd-normal.so", "stpd-dkvs.so",
             "satoshinet-core-rpctest", "e2e.test", "wallet.test", "test2json"]
    names += ["solidity/" + path.name for path in sorted(artifacts.glob("*.json"))]
    manifest = {"go_version": GO_VERSION, "goos": "linux", "goarch": "amd64",
                "test_cgo_enabled": "1", "node_plugin_cgo_enabled": "1", "test2json_cgo_enabled": "0",
                "files": {name: digest(output / name) for name in names},
                "solidity_sources": source_hashes}
    (output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    (output / "build-summary.json").write_text(json.dumps({
        "production_package_count": len(packages), "production_build_passed": True,
        "production_gate_wall_seconds": production_seconds,
        "mac_build_wall_seconds": time.perf_counter() - started,
        "go_version": GO_VERSION, "target": "linux/amd64", "node_cgo_enabled": True,
        "source_inputs_unchanged": True,
    }, indent=2) + "\n")
    print("Verified artifact bundle: " + str(output), flush=True)


if __name__ == "__main__":
    main()
